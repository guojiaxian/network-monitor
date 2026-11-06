package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// AdminAPI 管理端 API：登录、首次初始化向导、楼层/线路/探针/设置/告警管理。
// 角色：admin 可读写；viewer 只读（非 GET 请求返回 403）。
type AdminAPI struct {
	cfg      Config
	store    *Store
	eng      *Engine
	sched    *SpeedSched
	rep      *Reporter
	mon      *MonitorRunner
	ind      *IndustryRunner
	tsdb     *TSDB
	sessMu   sync.Mutex
	sessions map[string]nwSession
	provMu   sync.Mutex
	provJobs map[string]*ProvisionJob // 零接触开通任务（内存态）
}

type nwSession struct {
	user string
	role string
	exp  time.Time
}

type ctxKey int

const ctxUserKey ctxKey = 1

func NewAdminAPI(cfg Config, store *Store, eng *Engine, sched *SpeedSched, rep *Reporter, mon *MonitorRunner, ind *IndustryRunner, tsdb *TSDB) *AdminAPI {
	return &AdminAPI{cfg: cfg, store: store, eng: eng, sched: sched, rep: rep, mon: mon, ind: ind, tsdb: tsdb,
		sessions: map[string]nwSession{}, provJobs: map[string]*ProvisionJob{}}
}

func (a *AdminAPI) Routes(r chi.Router) {
	r.Get("/bootstrap", a.bootstrap) // 是否已初始化（登录页判断）
	r.Post("/setup", a.setup)        // 首次初始化：创建管理员
	r.Post("/login", a.login)
	r.Group(func(r chi.Router) {
		r.Use(a.requireSession, a.auditAndGuard)
		r.Post("/logout", a.logout)
		r.Get("/me", a.me) // 当前会话用户（顶栏显示）

		r.Get("/overview", a.overview)

		r.Get("/floors", a.listFloors)
		r.Post("/floors", a.upsertFloor)
		r.Post("/floors/{id}/scan", a.scanFloor)
		r.Delete("/floors/{id}", a.deleteFloor)

		r.Get("/lines", a.listLines)
		r.Post("/lines", a.upsertLine)
		r.Delete("/lines/{id}", a.deleteLine)
		r.Post("/lines/import", a.importLinesCSV)

		r.Get("/probes", a.listProbes)
		r.Post("/probes/{id}/assign", a.assignProbe)
		r.Delete("/probes/{id}", a.deleteProbe)
		r.Put("/probes/{id}/meta", a.updateProbeMeta)
		r.Post("/probes/{id}/ssh/test", a.sshTest)
		r.Post("/probes/{id}/ssh/restart-agent", a.sshRestartAgent)
		r.Post("/probes/{id}/ssh/exec", a.sshExec)
		r.Get("/probes/{id}/ssh/terminal", a.sshTerminal)
		r.Post("/probes/ssh/import", a.sshImport)
		r.Post("/probes/provision", a.provisionNew)          // 零接触开通：免密+装机+注册+线路自动匹配绑定
		r.Get("/probes/provision/{job}", a.provisionStatus)  // 开通任务进度轮询
		r.Post("/probes/{id}/group", a.setProbeGroup)
		r.Post("/speedtest/run", a.runSpeedTest)

		r.Get("/alerts", a.listAlerts)
		r.Get("/settings", a.getSettings)
		r.Put("/settings", a.putSettings)
		r.Post("/settings/smtp/test", a.testSMTP)
		r.Post("/settings/notify/test", a.testNotify)

		r.Get("/metrics/query_range", a.metricsRange)
		r.Get("/history", a.history)

		r.Get("/users", a.listUsers)
		r.Post("/users", a.addUser)
		r.Delete("/users/{id}", a.deleteUser)

		r.Get("/silences", a.listSilences)
		r.Post("/silences", a.createSilence)
		r.Delete("/silences/{id}", a.deleteSilence)

		r.Get("/reports", a.listReports)
		r.Get("/reports/{id}", a.getReport)
		r.Get("/reports/{id}/export.csv", a.exportReport)
		r.Post("/reports/generate", a.genReport)
		r.Get("/audit", a.listAudit)

		r.Get("/agent-releases", a.listReleases)
		r.Post("/agent-releases", a.uploadRelease)
		r.Post("/probes/upgrade", a.upgradeProbes)
		r.Get("/snmp/ifaces", a.snmpIfaces)
	})
}

// ---------- 会话 ----------

const sessionTTL = 12 * time.Hour

// me 当前登录用户（顶栏显示用；GET 不审计）
func (a *AdminAPI) me(w http.ResponseWriter, r *http.Request) {
	sess, _ := r.Context().Value(ctxUserKey).(nwSession)
	writeJSON(w, map[string]string{"username": sess.user, "role": sess.role})
}

func (a *AdminAPI) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("nw_session")
		var sess nwSession
		valid := false
		if err == nil {
			a.sessMu.Lock()
			s, ok := a.sessions[c.Value]
			if ok && time.Now().Before(s.exp) {
				sess = s
				valid = true
			}
			a.sessMu.Unlock()
		}
		if !valid {
			httpError(w, 401, "请先登录")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey, sess)))
	})
}

// auditAndGuard 观察者禁写 + 变更操作审计
func (a *AdminAPI) auditAndGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, _ := r.Context().Value(ctxUserKey).(nwSession)
		if r.Method != http.MethodGet && sess.role != "admin" {
			httpError(w, 403, "观察者账号无写权限")
			return
		}
		if r.Method != http.MethodGet {
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)
			go a.store.WriteAudit(context.Background(), sess.user,
				r.Method+" "+r.URL.Path, fmt.Sprintf("status=%d", rec.status))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder 记录响应码用于审计
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (a *AdminAPI) newSession(w http.ResponseWriter, user, role string) {
	tok := randToken()
	a.sessMu.Lock()
	// 顺手清理过期会话
	for k, s := range a.sessions {
		if time.Now().After(s.exp) {
			delete(a.sessions, k)
		}
	}
	a.sessions[tok] = nwSession{user: user, role: role, exp: time.Now().Add(sessionTTL)}
	a.sessMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: "nw_session", Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
}

func randToken() string { id, _ := randHex(24); return id }

// ---------- 初始化与登录 ----------

type bootstrapResp struct {
	Initialized bool `json:"initialized"`
}

func (a *AdminAPI) bootstrap(w http.ResponseWriter, r *http.Request) {
	n, err := a.store.CountUsers(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, bootstrapResp{Initialized: n > 0})
}

type setupReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setup 首次启动向导第一步：仅当无任何用户时可用
func (a *AdminAPI) setup(w http.ResponseWriter, r *http.Request) {
	n, err := a.store.CountUsers(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	if n > 0 {
		httpError(w, 403, "系统已初始化")
		return
	}
	var req setupReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	if utf8.RuneCountInString(req.Username) < 2 || len(req.Password) < 8 {
		httpError(w, 400, "用户名至少 2 个字符，密码至少 8 位")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, 500, "internal")
		return
	}
	if err := a.store.CreateUser(r.Context(), req.Username, string(hash)); err != nil {
		httpError(w, 500, "db")
		return
	}
	a.newSession(w, req.Username, "admin")
	writeJSON(w, map[string]any{"ok": true})
}

func (a *AdminAPI) login(w http.ResponseWriter, r *http.Request) {
	var req setupReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	id, hash, err := a.store.GetUser(r.Context(), req.Username)
	if err != nil {
		httpError(w, 401, "用户名或密码错误")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		httpError(w, 401, "用户名或密码错误")
		return
	}
	_ = id
	a.newSession(w, req.Username, a.store.GetUserRole(r.Context(), req.Username))
	writeJSON(w, map[string]any{"ok": true})
}

func (a *AdminAPI) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("nw_session"); err == nil {
		a.sessMu.Lock()
		delete(a.sessions, c.Value)
		a.sessMu.Unlock()
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 总览 ----------

type overviewLine struct {
	LineRow
	ProbeIDs []string      `json:"probe_ids"`
	Statuses []ProbeStatus `json:"statuses"`
	Status   string        `json:"status"` // 汇总状态（取最差）
}

var statusRank = map[string]int{
	"unknown": 0, "online": 1, "degraded": 2, "speed_low": 2, "conflict": 3, "down": 3, "floor_down": 4, "blind": 5,
}

// statusOrder 同级别的展示名取确定性顺序（map 遍历无序，避免 degraded/speed_low 随机跳变）
var statusOrder = []string{"blind", "floor_down", "down", "conflict", "degraded", "speed_low", "online", "unknown"}

// overviewData 总览数据构建（管理端与大屏共用）。mon/ind 为中心侧监测引擎（可为 nil）。
func overviewData(ctx context.Context, store *Store, eng *Engine, mon *MonitorRunner, ind *IndustryRunner) (map[string]any, error) {
	lines, err := store.ListLines(ctx)
	if err != nil {
		return nil, err
	}
	probes, err := store.ListProbes(ctx)
	if err != nil {
		return nil, err
	}
	snap := eng.Snapshot()

	byLine := map[int][]string{}
	unbound := []map[string]string{}
	for _, p := range probes {
		if p.LineID != nil {
			byLine[*p.LineID] = append(byLine[*p.LineID], p.ID)
		} else if p.Group != "external" {
			// 外网组探针本就不绑线路（在外网区块展示），不计入未绑定警告
			name := p.Name
			if name == "" {
				name = p.Hostname
			}
			unbound = append(unbound, map[string]string{"id": p.ID, "name": name, "hostname": p.Hostname})
		}
	}

	ovs := make([]overviewLine, 0, len(lines))
	for _, l := range lines {
		ol := overviewLine{LineRow: l, ProbeIDs: byLine[l.ID]}
		worst := 0
		haveDown, haveUp := false, false
		for _, pid := range ol.ProbeIDs {
			st, ok := snap[pid]
			if !ok {
				st = ProbeStatus{Status: "unknown"}
			}
			ol.Statuses = append(ol.Statuses, st)
			if statusRank[st.Status] > worst {
				worst = statusRank[st.Status]
			}
			// 交叉验证：同线路部分探针报中断、部分正常 → 数据冲突
			if st.Status == "down" {
				haveDown = true
			}
			if st.Status == "online" || st.Status == "degraded" {
				haveUp = true
			}
		}
		ol.Status = "unknown"
		for _, k := range statusOrder {
			if statusRank[k] == worst {
				ol.Status = k
				break
			}
		}
		if haveDown && haveUp {
			ol.Status = "conflict"
		}
		ovs = append(ovs, ol)
	}

	// 外网组探针（阿里云等）：状态代表"官网从外网是否可达"
	type extProbe struct {
		ID       string      `json:"id"`
		Name     string      `json:"name"`
		Hostname string      `json:"hostname"`
		Status   ProbeStatus `json:"status"`
	}
	extProbes := []extProbe{}
	worstExt, haveExtUp := 0, false
	for _, p := range probes {
		if p.Group != "external" {
			continue
		}
		st, ok := snap[p.ID]
		if !ok {
			st = ProbeStatus{Status: "unknown"}
		}
		name := p.Name
		if name == "" {
			name = p.Hostname
		}
		extProbes = append(extProbes, extProbe{ID: p.ID, Name: name, Hostname: p.Hostname, Status: st})
		if statusRank[st.Status] > worstExt {
			worstExt = statusRank[st.Status]
		}
		if st.Status == "online" || st.Status == "degraded" {
			haveExtUp = true
		}
	}
	extStatus := "unknown"
	for _, k := range statusOrder {
		if statusRank[k] == worstExt {
			extStatus = k
			break
		}
	}
	if len(extProbes) > 0 && worstExt >= statusRank["down"] && haveExtUp {
		extStatus = "conflict"
	}

	// 内→内：中心服务器内置监控（Uptime Kuma 模式）
	monSnapshot := []MonitorStatus{}
	if mon != nil {
		monSnapshot = mon.Snapshot()
	}

	// 行业网监测（中心对行业网 IP 仅 ICMP ping）
	indSnapshot := []IndustryStatus{}
	if ind != nil {
		indSnapshot = ind.Snapshot()
	}

	return map[string]any{"lines": ovs, "unbound_probes": unbound,
		"external_probes": extProbes, "external_status": extStatus,
		"internal_monitors": monSnapshot, "industry_monitors": indSnapshot}, nil
}
func (a *AdminAPI) overview(w http.ResponseWriter, r *http.Request) {
	out, err := overviewData(r.Context(), a.store, a.eng, a.mon, a.ind)
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, out)
}

// ---------- 楼层 ----------

func (a *AdminAPI) listFloors(w http.ResponseWriter, r *http.Request) {
	fs, err := a.store.ListFloors(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, fs)
}

func (a *AdminAPI) upsertFloor(w http.ResponseWriter, r *http.Request) {
	var fr FloorRow
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&fr); err != nil || fr.Name == "" {
		httpError(w, 400, "参数错误")
		return
	}
	if !validDisplayName(fr.Name) {
		httpError(w, 400, "楼层名含非法字符（禁止 \" \\ 反引号，且不能为空白）")
		return
	}
	var id *int
	if fr.ID != 0 {
		id = &fr.ID
	}
	nid, err := a.store.UpsertFloor(r.Context(), id, fr.Name, fr.CIDR)
	if err != nil {
		if strings.Contains(err.Error(), "已存在") {
			httpError(w, 400, err.Error())
			return
		}
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"id": nid})
}

func (a *AdminAPI) deleteFloor(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		httpError(w, 400, "bad id")
		return
	}
	if err := a.store.DeleteFloor(r.Context(), id); err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 线路 ----------

func (a *AdminAPI) listLines(w http.ResponseWriter, r *http.Request) {
	ls, err := a.store.ListLines(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, ls)
}

func (a *AdminAPI) upsertLine(w http.ResponseWriter, r *http.Request) {
	var l LineRow
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&l); err != nil || l.Name == "" {
		httpError(w, 400, "参数错误")
		return
	}
	if !validDisplayName(l.Name) {
		httpError(w, 400, "线路名含非法字符（禁止 \" \\ 反引号，且不能为空白）")
		return
	}
	nid, err := a.store.UpsertLine(r.Context(), &l)
	if err != nil {
		if strings.Contains(err.Error(), "已存在") {
			httpError(w, 400, err.Error())
			return
		}
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"id": nid})
}

func (a *AdminAPI) deleteLine(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		httpError(w, 400, "bad id")
		return
	}
	if err := a.store.DeleteLine(r.Context(), id); err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// importLinesCSV CSV 批量导入。列：楼层名,子网CIDR,线路名,公网IP,下行Mbps,上行Mbps
// 首行若含"楼层"视为表头跳过。
func (a *AdminAPI) importLinesCSV(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		httpError(w, 400, "read body")
		return
	}
	floors, _ := a.store.ListFloors(r.Context())
	floorByName := map[string]int{}
	for _, f := range floors {
		floorByName[f.Name] = f.ID
	}
	n := 0
	skipped := []string{}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) < 3 {
			continue
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		if n == 0 && (cols[0] == "楼层" || cols[0] == "楼层名") {
			continue // 标准表头
		}
		floorName, cidr, lineName := cols[0], cols[1], cols[2]
		publicIP := ""
		down, up := 0, 0
		if len(cols) > 3 {
			publicIP = cols[3]
		}
		if len(cols) > 4 {
			down = atoi(cols[4])
		}
		if len(cols) > 5 {
			up = atoi(cols[5])
		}
		fid, ok := floorByName[floorName]
		if !ok {
			nid, err := a.store.UpsertFloor(r.Context(), nil, floorName, cidr)
			if err != nil {
				continue
			}
			fid = nid
			floorByName[floorName] = nid
		}
		l := LineRow{Name: lineName, ISP: "联通", PublicIP: publicIP,
			ContractDownMbps: down, ContractUpMbps: up, FloorID: &fid}
		if _, err := a.store.UpsertLine(r.Context(), &l); err != nil {
			skipped = append(skipped, lineName+"（"+err.Error()+"）")
			continue
		}
		n++
	}
	writeJSON(w, map[string]any{"imported": n, "skipped": skipped})
}

// ---------- 探针 ----------

func (a *AdminAPI) listProbes(w http.ResponseWriter, r *http.Request) {
	ps, err := a.store.ListProbes(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	snap := a.eng.Snapshot()
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		m := map[string]any{"probe": p, "status": snap[p.ID]}
		out = append(out, m)
	}
	writeJSON(w, out)
}

type assignReq struct {
	LineID *int `json:"line_id"` // null 解绑
}

// runSpeedTest 立即测速：POST /api/admin/speedtest/run {line_id}
func (a *AdminAPI) runSpeedTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LineID int `json:"line_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.LineID <= 0 {
		httpError(w, 400, "参数错误：需要 line_id")
		return
	}
	if a.sched == nil {
		httpError(w, 500, "调度器未就绪")
		return
	}
	probes, err := a.store.ListProbes(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	for _, p := range probes {
		if p.LineID != nil && *p.LineID == req.LineID {
			a.sched.RunNow(p.ID)
			writeJSON(w, map[string]any{"ok": true, "probe_id": p.ID})
			return
		}
	}
	httpError(w, 400, "该线路未绑定探针")
}

func (a *AdminAPI) assignProbe(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req assignReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	if req.LineID != nil {
		lines, err := a.store.ListLines(r.Context())
		if err != nil {
			httpError(w, 500, "db")
			return
		}
		found := false
		for _, l := range lines {
			if l.ID == *req.LineID {
				found = true
			}
		}
		if !found {
			httpError(w, 400, "线路不存在")
			return
		}
	}
	if err := a.store.AssignProbeLine(r.Context(), id, req.LineID); err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *AdminAPI) deleteProbe(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteProbe(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 告警 ----------

func (a *AdminAPI) listAlerts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := atoi(r.URL.Query().Get("limit"))
	als, err := a.store.ListAlerts(r.Context(), status, limit)
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, als)
}

// ---------- 设置 ----------

func (a *AdminAPI) getSettings(w http.ResponseWriter, r *http.Request) {
	m, err := a.store.ListSettings(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	// SMTP 密码不明文回传：已配置时返回占位符，PUT 收到占位符/空则保留原值
	if raw, ok := m["smtp"]; ok {
		var sm SMTPSettings
		if json.Unmarshal(raw, &sm) == nil && sm.Password != "" {
			sm.Password = "__SET__"
			if b, err := json.Marshal(sm); err == nil {
				m["smtp"] = b
			}
		}
	}
	writeJSON(w, m)
}

// putSettings 整体保存某几个设置键（值必须是合法 JSON 对象）
func (a *AdminAPI) putSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	allowed := map[string]bool{"agent_config": true, "smtp": true, "alert_settings": true,
		"agent.enroll_token": true, "speed_settings": true, "report_settings": true,
		"notify_channels": true, "snmp_settings": true, "agent_config_external": true,
		"internal_monitors": true, "industry_monitors": true}
	for k, v := range body {
		if !allowed[k] {
			continue
		}
		if k == "internal_monitors" {
			var im MonitorSettings
			if json.Unmarshal(v, &im) != nil {
				httpError(w, 400, "internal_monitors 格式错误")
				return
			}
			seen := map[string]bool{}
			for _, it := range im.Items {
				it.Name = strings.TrimSpace(it.Name)
				it.Target = strings.TrimSpace(it.Target)
				if it.Name == "" || it.Target == "" {
					httpError(w, 400, "监控项 name/target 不能为空")
					return
				}
				if it.Type != "tcp" && it.Type != "http" && it.Type != "icmp" {
					httpError(w, 400, "监控项 type 必须为 tcp/http/icmp")
					return
				}
				if seen[it.Name] {
					httpError(w, 400, "监控项名称重复: "+it.Name)
					return
				}
				seen[it.Name] = true
			}
		}
		if k == "industry_monitors" {
			var im IndustrySettings
			if json.Unmarshal(v, &im) != nil {
				httpError(w, 400, "industry_monitors 格式错误")
				return
			}
			for i := range im.Items {
				im.Items[i].Name = strings.TrimSpace(im.Items[i].Name)
				im.Items[i].Target = strings.TrimSpace(im.Items[i].Target)
			}
			if err := ValidateIndustrySettings(&im); err != nil {
				httpError(w, 400, err.Error())
				return
			}
		}
		var check any
		if json.Unmarshal(v, &check) != nil {
			httpError(w, 400, "invalid json for "+k)
			return
		}
		if k == "smtp" {
			var incoming SMTPSettings
			if json.Unmarshal(v, &incoming) == nil && (incoming.Password == "" || incoming.Password == "__SET__") {
				var cur SMTPSettings
				if a.store.GetSetting(r.Context(), "smtp", &cur) == nil {
					incoming.Password = cur.Password
				}
				if nb, err := json.Marshal(incoming); err == nil {
					v = nb
				}
			}
		}
		if err := a.store.PutSetting(r.Context(), k, json.RawMessage(v)); err != nil {
			httpError(w, 500, "db")
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

type testSMTPReq struct {
	To string `json:"to"`
}

// testSMTP 发送测试邮件，验证 SMTP 配置
func (a *AdminAPI) testSMTP(w http.ResponseWriter, r *http.Request) {
	var cur SMTPSettings
	if err := a.store.GetSetting(r.Context(), "smtp", &cur); err != nil {
		httpError(w, 500, "smtp settings missing")
		return
	}
	var req testSMTPReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.To != "" {
		cur.To = []string{req.To}
	}
	if !cur.Valid() {
		httpError(w, 400, "SMTP 配置不完整（host/port/from/收件人）")
		return
	}
	html := `<p>这是一封来自 NetWatch 网络监控系统的测试邮件。</p><p>收到本邮件说明 SMTP 配置正确，告警邮件可以正常送达。</p>`
	if err := SendMail(cur, "[NetWatch] 测试邮件", html); err != nil {
		httpError(w, 500, "发送失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

type testNotifyReq struct {
	Channel string `json:"channel"` // dingtalk / wecom
}

// testNotify 向指定 IM 渠道发送测试消息
func (a *AdminAPI) testNotify(w http.ResponseWriter, r *http.Request) {
	var req testNotifyReq
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req)
	var ch NotifyChannels
	if err := a.store.GetSetting(r.Context(), "notify_channels", &ch); err != nil {
		httpError(w, 500, "notify settings missing")
		return
	}
	md := "✅ NetWatch 测试消息\n\n通道连通正常，告警将推送到本群。\n\n—— NetWatch 网络监控系统"
	var err error
	switch req.Channel {
	case "dingtalk":
		err = SendDingtalk(ch.DingtalkWebhook, ch.DingtalkSecret, "[NetWatch] 测试消息", md)
	case "wecom":
		err = SendWecom(ch.WecomWebhook, md)
	default:
		httpError(w, 400, "channel 必须为 dingtalk 或 wecom")
		return
	}
	if err != nil {
		httpError(w, 500, "发送失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func atoi(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// metricsRange 代理 VictoriaMetrics 的 /api/v1/query_range（管理端已认证，仅转发）。
// 参数：query、start、end、step。仅允许查询 nw_ 前缀指标，防滥用。
func (a *AdminAPI) metricsRange(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("query")
	if !strings.Contains(query, "nw_") {
		httpError(w, 400, "仅允许查询 nw_ 前缀指标")
		return
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", q.Get("start"))
	params.Set("end", q.Get("end"))
	params.Set("step", q.Get("step"))
	if params.Get("start") == "" {
		params.Set("start", fmt.Sprintf("%d", time.Now().Add(-24*time.Hour).Unix()))
	}
	if params.Get("end") == "" {
		params.Set("end", fmt.Sprintf("%d", time.Now().Unix()))
	}
	if params.Get("step") == "" {
		params.Set("step", "60")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		a.cfg.VMURL+"/api/v1/query_range?"+params.Encode(), nil)
	if err != nil {
		httpError(w, 500, "internal")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		httpError(w, 502, "tsdb unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ---------- 用户管理 ----------

func (a *AdminAPI) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := a.store.ListUsers(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, us)
}

type addUserReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"` // admin / viewer
}

func (a *AdminAPI) addUser(w http.ResponseWriter, r *http.Request) {
	var req addUserReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	if utf8.RuneCountInString(req.Username) < 2 || len(req.Password) < 8 {
		httpError(w, 400, "用户名至少 2 个字符，密码至少 8 位")
		return
	}
	if req.Role != "admin" && req.Role != "viewer" {
		req.Role = "viewer"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, 500, "internal")
		return
	}
	if err := a.store.CreateUser(r.Context(), req.Username, string(hash)); err != nil {
		httpError(w, 400, "用户名已存在")
		return
	}
	if err := a.store.SetUserRole(r.Context(), req.Username, req.Role); err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *AdminAPI) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		httpError(w, 400, "bad id")
		return
	}
	us, err := a.store.ListUsers(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	var target UserRow
	found := false
	for _, u := range us {
		if u.ID == id {
			target = u
			found = true
		}
	}
	if !found {
		httpError(w, 404, "用户不存在")
		return
	}
	if target.Role == "admin" {
		n, _ := a.store.CountAdmins(r.Context())
		if n <= 1 {
			httpError(w, 400, "不能删除最后一个管理员")
			return
		}
	}
	if err := a.store.DeleteUser(r.Context(), id); err != nil {
		httpError(w, 500, "db")
		return
	}
	a.sessMu.Lock()
	for k, sess := range a.sessions {
		if sess.user == target.Username {
			delete(a.sessions, k)
		}
	}
	a.sessMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

// safeName 白名单字符校验（防路径穿越）
// validDisplayName 线路/楼层/探针显示名：拒绝会破坏 PromQL 查询与 HTML 的字符
func validDisplayName(s string) bool {
	if len([]rune(s)) > 64 {
		return false
	}
	for _, c := range s {
		if c == '"' || c == '\\' || c == '`' || c == '\n' || c == '\r' {
			return false
		}
	}
	return strings.TrimSpace(s) != ""
}

func safeName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ---------- 静默窗口 ----------

func (a *AdminAPI) listSilences(w http.ResponseWriter, r *http.Request) {
	ss, err := a.store.ListSilences(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, ss)
}

type silenceReq struct {
	LineID *int    `json:"line_id"` // null = 全部线路
	Hours  float64 `json:"hours"`   // 持续时长
	Reason string  `json:"reason"`
}

func (a *AdminAPI) createSilence(w http.ResponseWriter, r *http.Request) {
	var req silenceReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.Hours <= 0 {
		httpError(w, 400, "参数错误：需要 hours>0")
		return
	}
	if req.Hours > 24*30 {
		httpError(w, 400, "静默时长过长（最长 30 天）")
		return
	}
	if req.LineID != nil {
		lines, err := a.store.ListLines(r.Context())
		if err != nil {
			httpError(w, 500, "db")
			return
		}
		found := false
		for _, l := range lines {
			if l.ID == *req.LineID {
				found = true
			}
		}
		if !found {
			httpError(w, 400, "线路不存在")
			return
		}
	}
	sess, _ := r.Context().Value(ctxUserKey).(nwSession)
	now := time.Now()
	id, err := a.store.CreateSilence(r.Context(), req.LineID,
		now, now.Add(time.Duration(req.Hours*float64(time.Hour))), req.Reason, sess.user)
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (a *AdminAPI) deleteSilence(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpError(w, 400, "bad id")
		return
	}
	if err := a.store.DeleteSilence(r.Context(), id); err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 报表 ----------

func (a *AdminAPI) listReports(w http.ResponseWriter, r *http.Request) {
	rs, err := a.store.ListReports(r.Context(), r.URL.Query().Get("kind"), atoi(r.URL.Query().Get("limit")))
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, rs)
}

func (a *AdminAPI) getReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpError(w, 400, "bad id")
		return
	}
	_, _, _, data, err := a.store.GetReport(r.Context(), id)
	if err != nil {
		httpError(w, 404, "报表不存在")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

func (a *AdminAPI) exportReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpError(w, 400, "bad id")
		return
	}
	kind, start, _, data, err := a.store.GetReport(r.Context(), id)
	if err != nil {
		httpError(w, 404, "报表不存在")
		return
	}
	var d reportData
	if json.Unmarshal(data, &d) != nil {
		httpError(w, 500, "报表数据损坏")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=netwatch-%s-%s.csv", kind, start.Format("20060102")))
	_, _ = w.Write(ReportCSV(&d))
}

type genReportReq struct {
	Kind string `json:"kind"` // day / week
}

func (a *AdminAPI) genReport(w http.ResponseWriter, r *http.Request) {
	var req genReportReq
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req)
	if a.rep == nil {
		httpError(w, 500, "报表模块未就绪")
		return
	}
	id, err := a.rep.Generate(r.Context(), req.Kind)
	if err != nil {
		httpError(w, 500, "生成失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (a *AdminAPI) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListAudit(r.Context(), atoi(r.URL.Query().Get("limit")))
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, rows)
}

// ---------- 探针版本发布与升级 ----------

func (a *AdminAPI) listReleases(w http.ResponseWriter, r *http.Request) {
	rs, err := a.store.ListReleases(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, rs)
}

// uploadRelease 上传探针二进制（multipart: file, version, arch）
func (a *AdminAPI) uploadRelease(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		httpError(w, 400, "上传失败: "+err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpError(w, 400, "缺少 file 字段")
		return
	}
	defer file.Close()
	version := strings.TrimSpace(r.FormValue("version"))
	if version == "" {
		httpError(w, 400, "缺少 version")
		return
	}
	if !safeName(version) || !safeName(hdr.Filename) {
		httpError(w, 400, "version/文件名含非法字符（仅允许字母数字._-）")
		return
	}
	arch := r.FormValue("arch")
	if arch == "" {
		arch = "amd64"
	}

	dir := filepath.Join(a.cfg.DataDir, "agent-releases", version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpError(w, 500, "存储失败")
		return
	}
	dst, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		httpError(w, 500, "存储失败")
		return
	}
	defer os.Remove(dst.Name()) // 成功后 rename 使其消失，失败时清理
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, h), file)
	dst.Close()
	if err != nil {
		httpError(w, 500, "写入失败")
		return
	}
	filename := hdr.Filename
	if err := os.Rename(dst.Name(), filepath.Join(dir, filename)); err != nil {
		httpError(w, 500, "存储失败")
		return
	}
	rel := &ReleaseRow{Version: version, OS: "linux", Arch: arch,
		SHA256: hex.EncodeToString(h.Sum(nil)), Filename: filename, SizeBytes: size}
	if err := a.store.CreateRelease(r.Context(), rel); err != nil {
		httpError(w, 500, "db: "+err.Error())
		return
	}
	writeJSON(w, rel)
}

type upgradeReq struct {
	ProbeIDs  []string `json:"probe_ids"`
	ReleaseID int64    `json:"release_id"`
}

// snmpIfaces 拉取防火墙接口列表（按当前 SNMP 设置连接设备）
func (a *AdminAPI) snmpIfaces(w http.ResponseWriter, r *http.Request) {
	var st SnmpSettings
	if err := a.store.GetSetting(r.Context(), "snmp_settings", &st); err != nil {
		httpError(w, 500, "snmp settings missing")
		return
	}
	if st.Device == "" || st.Community == "" {
		httpError(w, 400, "请先填写设备 IP 与只读团体字并保存")
		return
	}
	if st.Port <= 0 || st.Port > 65535 {
		st.Port = 161
	}
	ifaces, err := Ifaces(st)
	if err != nil {
		httpError(w, 502, "SNMP 连接失败: "+err.Error())
		return
	}
	writeJSON(w, ifaces)
}

func (a *AdminAPI) upgradeProbes(w http.ResponseWriter, r *http.Request) {
	var req upgradeReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || len(req.ProbeIDs) == 0 {
		httpError(w, 400, "参数错误：需要 probe_ids")
		return
	}
	rel, err := a.store.GetRelease(r.Context(), req.ReleaseID)
	if err != nil {
		httpError(w, 404, "版本不存在")
		return
	}
	for _, pid := range req.ProbeIDs {
		if err := a.store.SetProbePendingVersion(r.Context(), pid, rel.Version); err != nil {
			httpError(w, 500, "db")
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "count": len(req.ProbeIDs), "version": rel.Version})
}

// ---------- 探针远程运维（名称/SSH/远程操作） ----------

type probeMetaReq struct {
	Name    *string `json:"name"`
	SshHost *string `json:"ssh_host"`
	SshPort *int    `json:"ssh_port"`
	SshUser *string `json:"ssh_user"`
	SshPass *string `json:"ssh_pass"` // 明文入参，落库前加密；空指针=不改
}

// updateProbeMeta 修改探针名称与 SSH 运维信息
func (a *AdminAPI) updateProbeMeta(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req probeMetaReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	if req.SshPort != nil && (*req.SshPort <= 0 || *req.SshPort > 65535) {
		v := 22
		req.SshPort = &v
	}
	var passEnc *string
	if req.SshPass != nil {
		key, err := a.sshMasterKey()
		if err != nil {
			httpError(w, 500, "密钥初始化失败: "+err.Error())
			return
		}
		enc, err := encryptPass(key, *req.SshPass)
		if err != nil {
			httpError(w, 500, "加密失败")
			return
		}
		passEnc = &enc
	}
	if err := a.store.UpdateProbeMeta(r.Context(), id, req.Name, req.SshHost, req.SshPort, req.SshUser, passEnc); err != nil {
		httpError(w, 500, "db: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// sshTest 测试到探针的 SSH 连通性
func (a *AdminAPI) sshTest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, port, user, pass, err := a.probeSSH(r.Context(), id)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	out, err := sshRun(host, port, user, pass, "echo netwatch-ssh-ok", 15*time.Second)
	if err != nil {
		httpError(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "output": out})
}

// sshRestartAgent 远程重启探针服务（agent 宕机时的救援通道）
func (a *AdminAPI) sshRestartAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, port, user, pass, err := a.probeSSH(r.Context(), id)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	out, err := sshRun(host, port, user, pass,
		"systemctl restart netwatch-agent 2>/dev/null || service netwatch-agent restart 2>/dev/null || (pkill netwatch-agent; echo 'systemd 不可用，已杀进程等待守护拉起')",
		30*time.Second)
	if err != nil {
		httpError(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "output": out})
}

type sshExecReq struct {
	Command string `json:"command"`
}

// sshExec 在探针上远程执行命令（仅管理员；审计日志自动记录）
func (a *AdminAPI) sshExec(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req sshExecReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.Command == "" {
		httpError(w, 400, "参数错误：需要 command")
		return
	}
	sess, _ := r.Context().Value(ctxUserKey).(nwSession)
	a.store.WriteAudit(r.Context(), sess.user, "ssh-exec:"+id[:min(8, len(id))], req.Command)

	host, port, user, pass, err := a.probeSSH(r.Context(), id)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	out, err := sshRun(host, port, user, pass, req.Command, 60*time.Second)
	if err != nil {
		httpError(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "output": out})
}

// sshImport 批量导入探针 SSH 凭据。CSV：匹配键,SSH主机,用户,密码[,端口]
// 匹配键 = 探针 ID 前缀或注册主机名。
func (a *AdminAPI) sshImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		httpError(w, 400, "read body")
		return
	}
	probes, err := a.store.ListProbes(r.Context())
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	key, err := a.sshMasterKey()
	if err != nil {
		httpError(w, 500, "密钥初始化失败")
		return
	}

	find := func(k string) *ProbeRow {
		for i := range probes {
			p := &probes[i]
			if p.ID == k || strings.HasPrefix(p.ID, k) || p.Hostname == k {
				return p
			}
		}
		return nil
	}

	n, skipped := 0, []string{}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) < 4 {
			skipped = append(skipped, line+"（列数不足）")
			continue
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		p := find(cols[0])
		if p == nil {
			skipped = append(skipped, cols[0]+"（未匹配到探针）")
			continue
		}
		port := 22
		if len(cols) > 4 && cols[4] != "" {
			port = atoi(cols[4])
		}
		user := cols[2]
		if user == "" {
			user = "root"
		}
		enc, err := encryptPass(key, cols[3])
		if err != nil {
			skipped = append(skipped, cols[0]+"（加密失败）")
			continue
		}
		if err := a.store.UpdateProbeMeta(r.Context(), p.ID, nil, &cols[1], &port, &user, &enc); err != nil {
			skipped = append(skipped, cols[0]+"（写入失败）")
			continue
		}
		n++
	}
	writeJSON(w, map[string]any{"imported": n, "skipped": skipped})
}

type groupReq struct {
	Group string `json:"group"` // internal / external
}

// setProbeGroup 探针切换分组：internal=楼层内网组（绑线路）；external=外网服务器组（探测官网）
func (a *AdminAPI) setProbeGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req groupReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil ||
		(req.Group != "internal" && req.Group != "external") {
		httpError(w, 400, "group 必须为 internal 或 external")
		return
	}
	if err := a.store.SetProbeGroup(r.Context(), id, req.Group); err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 探测历史（PRTG 风格原始记录查询） ----------

// HistoryRecord 单条探测历史记录
type HistoryRecord struct {
	TS     int64   `json:"ts_ms"`
	Target string  `json:"target"`
	Class  string  `json:"class"` // ping: gw/public；其他为空
	OK     bool    `json:"ok"`
	RTTms  float64 `json:"rtt_ms"`
	Code   float64 `json:"code"`      // http 状态码
	Cert   float64 `json:"cert_days"` // http 证书剩余天数
}

// history 探测历史查询：GET /api/admin/history?type=&probe=&target=&start=&end=&limit=
// type: ping/tcp/dns/http/monitor/speed。合并 ok 与 rtt 序列，按时间倒序返回。
// nameKey 历史记录的名称键：常规序列取 target 标签；中心侧 monitor/industry
// 序列无 target，名称分别在 monitor/industry 标签里。
func nameKey(m map[string]string) string {
	if v := m["target"]; v != "" {
		return v
	}
	if v := m["monitor"]; v != "" {
		return v
	}
	return m["industry"]
}

func (a *AdminAPI) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ := q.Get("type")
	probe := q.Get("probe")
	target := q.Get("target")
	limit := atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 2000
	}
	if limit > 20000 {
		limit = 20000
	}
	start := atoi64(q.Get("start"))
	end := atoi64(q.Get("end"))
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 || start >= end {
		start = end - 3600
	}

	// PromQL 标签值转义：\ " 破坏匹配串（注入面收口）
	esc := func(v string) string {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		return v
	}
	probe = esc(probe)
	target = esc(target)

	var okMetric, rttMetric string
	switch typ {
	case "ping":
		okMetric, rttMetric = "nw_ping_ok", "nw_ping_rtt_ms"
	case "tcp":
		okMetric, rttMetric = "nw_tcp_ok", "nw_tcp_rtt_ms"
	case "dns":
		okMetric, rttMetric = "nw_dns_ok", "nw_dns_rtt_ms"
	case "http":
		okMetric, rttMetric = "nw_http_ok", "nw_http_ttfb_ms"
	case "monitor":
		okMetric, rttMetric = "nw_monitor_ok", "nw_monitor_rtt_ms"
	case "industry":
		okMetric, rttMetric = "nw_industry_ok", "nw_industry_rtt_ms"
	case "speed":
		okMetric, rttMetric = "nw_speed_down_mbps", ""
	default:
		httpError(w, 400, "type 必须为 ping/tcp/dns/http/monitor/industry/speed")
		return
	}

	// monitor/industry 类型无 probe 标签，按名称过滤（target=名称）；其余按探针过滤
	var sel string
	if typ == "monitor" {
		sel = `{monitor!=""}`
		if target != "" {
			sel = `{monitor="` + target + `"}`
		}
	} else if typ == "industry" {
		sel = `{industry!=""}`
		if target != "" {
			sel = `{industry="` + target + `"}`
		}
	} else {
		sel = `{probe="` + probe + `"`
		if target != "" {
			sel += `,target="` + target + `"`
		}
		sel += `}`
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	okSeries, err := a.tsdb.Export(ctx, okMetric+sel, start, end)
	if err != nil {
		httpError(w, 502, "tsdb: "+err.Error())
		return
	}
	var rttSeries []ExportSeries
	if rttMetric != "" {
		rttSeries, _ = a.tsdb.Export(ctx, rttMetric+sel, start, end)
	}

	// ok 序列：每条 = 一次探测记录；rtt/code/证书按 (target,ts) 关联。
	// monitor/industry 序列无 target 标签，名称在 monitor/industry 标签里——
	// 关联键统一用 nameKey（target→monitor→industry 依次取），否则 RTT 恒为 0。
	records := []HistoryRecord{}
	targets := map[string]bool{}
	rttAt := map[string]float64{}
	for _, s := range rttSeries {
		for i, v := range s.Values {
			if i < len(s.Timestamps) {
				rttAt[nameKey(s.Metric)+"|"+strconv.FormatInt(s.Timestamps[i], 10)] = v
			}
		}
	}
	codeAt := map[string]float64{}
	certAt := map[string]float64{}
	if typ == "http" {
		codeSers, _ := a.tsdb.Export(ctx, "nw_http_code"+sel, start, end)
		for _, s := range codeSers {
			for i, v := range s.Values {
				if i < len(s.Timestamps) {
					codeAt[s.Metric["target"]+"|"+strconv.FormatInt(s.Timestamps[i], 10)] = v
				}
			}
		}
		certSers, _ := a.tsdb.Export(ctx, "nw_http_cert_days"+sel, start, end)
		for _, s := range certSers {
			for i, v := range s.Values {
				if i < len(s.Timestamps) {
					certAt[s.Metric["target"]+"|"+strconv.FormatInt(s.Timestamps[i], 10)] = v
				}
			}
		}
	}
	for _, s := range okSeries {
		tgt := nameKey(s.Metric)
		targets[tgt] = true
		for i, v := range s.Values {
			if i >= len(s.Timestamps) {
				continue
			}
			ts := s.Timestamps[i]
			key := tgt + "|" + strconv.FormatInt(ts, 10)
			rec := HistoryRecord{TS: ts, Target: tgt,
				Class: s.Metric["class"], OK: v > 0.5,
				RTTms: rttAt[key], Code: codeAt[key], Cert: certAt[key]}
			records = append(records, rec)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].TS > records[j].TS })
	total := len(records)
	if len(records) > limit {
		records = records[:limit]
	}
	tgtList := []string{}
	for t := range targets {
		tgtList = append(tgtList, t)
	}
	sort.Strings(tgtList)
	writeJSON(w, map[string]any{"records": records, "targets": tgtList, "limit": limit, "total": total})
}

// okSeriesExportExtras 附加指标（code/cert）序列拉取的小包装
func okSeriesExportExtras(ctx context.Context, a *AdminAPI, match string, start, end int64) []ExportSeries {
	s, err := a.tsdb.Export(ctx, match, start, end)
	if err != nil {
		return nil
	}
	return s
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
