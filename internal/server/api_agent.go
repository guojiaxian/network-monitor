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
	cfg   Config
	store *Store
	eng   *Engine
	sched *SpeedSched
}

func NewAgentAPI(cfg Config, store *Store, eng *Engine, sched *SpeedSched) *AgentAPI {
	return &AgentAPI{cfg: cfg, store: store, eng: eng, sched: sched}
}

func (a *AgentAPI) Routes(r chi.Router) {
	r.Post("/register", a.register)
	r.Group(func(r chi.Router) {
		r.Use(a.auth)
		r.Get("/config", a.config)
		r.Post("/metrics", a.metrics)
		r.Post("/heartbeat", a.heartbeat)
		r.Get("/download/{id}", a.download)
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

// configFor 按探针分组取配置：internal=楼层宽带内网组 / external=外网服务器组
func configFor(ctx context.Context, store *Store, probeID string) proto.ProbeConfig {
	key := "agent_config"
	var grp string
	_ = store.pool.QueryRow(ctx, `SELECT COALESCE(grp,'internal') FROM probes WHERE id=$1`, probeID).Scan(&grp)
	if grp == "external" {
		key = "agent_config_external"
	}
	var cfg proto.ProbeConfig
	if err := store.GetSetting(ctx, key, &cfg); err != nil {
		if grp == "external" {
			return DefaultExternalProbeConfig()
		}
		return DefaultProbeConfig()
	}
	return cfg
}

// config 下发探测配置（探针按版本号热更新；测速/升级任务由调度器附加）
func (a *AgentAPI) config(w http.ResponseWriter, r *http.Request) {
	agentID := r.Header.Get("X-NW-Agent")
	cfg := configFor(r.Context(), a.store, agentID)
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
	a.eng.OnHeartbeat(agentID, hb.Version, hb.PublicIP)
	writeJSON(w, map[string]any{"ok": true})
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

func timePtr(t time.Time) *time.Time { return &t }
