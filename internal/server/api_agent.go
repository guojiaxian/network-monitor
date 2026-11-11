package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"netwatch/internal/proto"
)

// AgentAPI 探针侧 API：注册、配置下发、指标接收、心跳。
type AgentAPI struct {
	cfg    Config
	store  *Store
	eng    *Engine
	sched  *SpeedSched
	egress *EgressMonitor
}

func NewAgentAPI(cfg Config, store *Store, eng *Engine, sched *SpeedSched, egress *EgressMonitor) *AgentAPI {
	return &AgentAPI{cfg: cfg, store: store, eng: eng, sched: sched, egress: egress}
}

func (a *AgentAPI) Routes(r chi.Router) {
	r.Post("/register", a.register)
	r.Group(func(r chi.Router) {
		r.Use(a.auth)
		r.Get("/config", a.config)
		r.Post("/metrics", a.metrics)
		r.Post("/heartbeat", a.heartbeat)
		r.Get("/download/{id}", a.download)
		r.Get("/relay/alerts", a.relayAlerts) // probe-1 中继：中心专线断时代发告警
	})
}

func (a *AgentAPI) enrollToken(ctx context.Context) string {
	var tok string
	_ = a.store.GetSetting(ctx, "agent.enroll_token", &tok)
	if tok != "" {
		return tok
	}
	return a.cfg.EnrollToken
}

// register 探针首次注册：校验注册令牌，建档并返回身份与初始配置。
func (a *AgentAPI) register(w http.ResponseWriter, r *http.Request) {
	var req proto.RegisterReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	if req.EnrollToken != a.enrollToken(r.Context()) {
		httpError(w, 403, "invalid enroll token")
		return
	}
	id, secret, err := newCredentials()
	if err != nil {
		httpError(w, 500, "internal")
		return
	}
	if err := a.store.UpsertProbe(r.Context(), &req, id, secret); err != nil {
		httpError(w, 500, "db: "+err.Error())
		return
	}
	var cfg proto.ProbeConfig
	_ = a.store.GetSetting(r.Context(), "agent_config", &cfg)
	if a.sched != nil {
		cfg.SpeedTask = a.sched.TaskFor(id)
	}
	writeJSON(w, proto.RegisterResp{AgentID: id, Secret: secret, Config: cfg})
}

// auth 校验 X-NW-Agent / X-NW-Secret
func (a *AgentAPI) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-NW-Agent")
		sec := r.Header.Get("X-NW-Secret")
		if id == "" || sec == "" || !a.store.AuthProbe(r.Context(), id, sec) {
			httpError(w, 401, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// configFor 下发探测配置（全部探针统一使用 agent_config）
func configFor(ctx context.Context, store *Store) proto.ProbeConfig {
	var cfg proto.ProbeConfig
	if err := store.GetSetting(ctx, "agent_config", &cfg); err != nil {
		return DefaultProbeConfig()
	}
	return cfg
}

// config 下发探测配置（探针按版本号热更新；测速/升级任务由调度器附加）
func (a *AgentAPI) config(w http.ResponseWriter, r *http.Request) {
	agentID := r.Header.Get("X-NW-Agent")
	cfg := configFor(r.Context(), a.store)
	if a.sched != nil {
		cfg.SpeedTask = a.sched.TaskFor(agentID)
	}
	if pv := a.store.GetProbePendingVersion(r.Context(), agentID); pv != "" {
		if rel, err := a.store.GetReleaseByVersion(r.Context(), pv); err == nil {
			cfg.Upgrade = &proto.UpgradeTask{ReleaseID: rel.ID, Version: rel.Version, SHA256: rel.SHA256}
		}
	}
	writeJSON(w, cfg)
}

// metrics 接收批量探测结果：入时序库 + 喂告警引擎
func (a *AgentAPI) metrics(w http.ResponseWriter, r *http.Request) {
	var batch proto.MetricsBatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&batch); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	agentID := r.Header.Get("X-NW-Agent")
	a.eng.Ingest(r.Context(), agentID, batch.Samples)
	writeJSON(w, map[string]any{"accepted": len(batch.Samples)})
}

// heartbeat 心跳：刷新探针在线时间，驱动盲区判定；升级完成后清除待升级标记
func (a *AgentAPI) heartbeat(w http.ResponseWriter, r *http.Request) {
	var hb proto.Heartbeat
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&hb); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	agentID := r.Header.Get("X-NW-Agent")
	if err := a.store.TouchProbe(r.Context(), agentID, hb.Version); err != nil {
		httpError(w, 500, "db")
		return
	}
	if hb.Version != "" {
		a.store.ClearProbePendingIfMatch(r.Context(), agentID, hb.Version)
	}
	if len(hb.SysInfo) > 0 {
		a.store.UpdateProbeSysInfo(r.Context(), agentID, hb.SysInfo)
	}
	a.eng.OnHeartbeat(agentID, hb)
	resp := map[string]any{"ok": true}
	if a.eng.PopEgressRefresh(agentID) {
		resp["refresh_egress"] = true // UI"立即刷新"：让 agent 马上重探出口并回报
	}
	writeJSON(w, resp)
}

// relayAlerts 供 probe-1 中继读取：中心公网出口状态 + 当前 firing 告警摘要。
// 使用探针身份认证，只返回标题/级别/开始时间，不返回邮件正文与凭据。
func (a *AgentAPI) relayAlerts(w http.ResponseWriter, r *http.Request) {
	rows, total, err := a.store.ListAlerts(r.Context(), "firing", "", "", time.Time{}, 50, 0)
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	type item struct {
		ID       int64     `json:"id"`
		Severity string    `json:"severity"`
		Kind     string    `json:"kind"`
		Title    string    `json:"title"`
		StartsAt time.Time `json:"starts_at"`
	}
	out := make([]item, 0, len(rows))
	for _, x := range rows {
		out = append(out, item{ID: x.ID, Severity: x.Severity, Kind: x.Kind, Title: x.Title, StartsAt: x.StartsAt})
	}
	egressOK := true
	var checkedAt int64
	if a.egress != nil {
		egressOK = a.egress.OK()
		if t := a.egress.CheckedAt(); !t.IsZero() {
			checkedAt = t.Unix()
		}
	}
	writeJSON(w, map[string]any{
		"egress_ok":         egressOK,
		"egress_checked_at": checkedAt,
		"alerts":            out,
		"total":             total,
		"time":              time.Now().Format(time.RFC3339),
	})
}

// download 探针鉴权后下载指定版本二进制（自更新用）
func (a *AgentAPI) download(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpError(w, 400, "bad id")
		return
	}
	rel, err := a.store.GetRelease(r.Context(), id)
	if err != nil {
		httpError(w, 404, "release not found")
		return
	}
	path := filepath.Join(a.cfg.DataDir, "agent-releases", rel.Version, rel.Filename)
	log.Printf("download: enter probe=%s path=%s", r.Header.Get("X-NW-Agent"), path)
	f, err := os.Open(path)
	if err != nil {
		log.Printf("download: open failed: %v", err)
		httpError(w, 404, "binary missing")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		log.Printf("download: stat failed: %v", err)
		httpError(w, 404, "binary missing")
		return
	}
	if st.IsDir() {
		log.Printf("download: path is a dir")
		httpError(w, 404, "binary missing")
		return
	}
	log.Printf("download: probe=%s release=%d file=%s size=%d", r.Header.Get("X-NW-Agent"), rel.ID, path, st.Size())
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-SHA256", rel.SHA256)
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.WriteHeader(200)
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("download: probe=%s copy error: %v", r.Header.Get("X-NW-Agent"), err)
	}
}

func newCredentials() (id, secret string, err error) {
	id, err = randHex(8)
	if err != nil {
		return
	}
	secret, err = randHex(24)
	return
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ---------- 通用响应工具 ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var errNotFound = errors.New("not found")
