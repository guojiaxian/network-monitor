package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
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

	"netwatch/internal/proto"

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
	scan     *ScanRunner              // IP 扫描执行器（手动/定时共用，内含"同一时刻只跑一批"的闸）
}

type nwSession struct {
	user string
	role string
	exp  time.Time
}

type ctxKey int

const ctxUserKey ctxKey = 1

func NewAdminAPI(cfg Config, store *Store, eng *Engine, sched *SpeedSched, rep *Reporter, mon *MonitorRunner, ind *IndustryRunner, tsdb *TSDB, scan *ScanRunner) *AdminAPI {
	return &AdminAPI{cfg: cfg, store: store, eng: eng, sched: sched, rep: rep, mon: mon, ind: ind, tsdb: tsdb, scan: scan,
		sessions: map[string]nwSession{}, provJobs: map[string]*ProvisionJob{}}
}

func (a *AdminAPI) Routes(r chi.Router) {
	r.Get("/bootstrap", a.bootstrap) // 是否已初始化（登录页判断）
	r.Post("/setup", a.setup)        // 首次初始化：创建管理员
	r.Post("/login", a.login)
	r.Group(func(r chi.Router) {
		r.Use(a.requireSession, a.auditAndGuard)
		r.Post("/logout", a.logout)
		r.Get("/me", a.me)                      // 当前会话用户（顶栏显示）
		r.Put("/me/password", a.changePassword) // 修改自己密码（auditAndGuard 中放行，观察者亦可）

		r.Get("/overview", a.overview)
		r.Get("/floor-status", a.floorStatus) // 楼层状态：以楼层为单位聚合关联线路状态与最近一次 IP 扫描
		r.Get("/monitor-doc", a.monitorDoc)   // 监测说明：探测/监测机制与周期超时实时数据（monitordoc.go 维护）

		r.Get("/floors", a.listFloors)
		r.Post("/floors", a.upsertFloor)
		r.Post("/floors/{id}/scan", a.scanFloor) // 单楼层同步扫描（旧接口保留）
		r.Delete("/floors/{id}", a.deleteFloor)

		r.Post("/scan", a.startScan)           // 多楼层 IP 扫描（异步任务，记录入库，前端轮询进度）
		r.Get("/scan/runs", a.listScanRuns)    // 扫描记录列表（不含逐 IP 明细）
		r.Get("/scan/runs/{id}", a.getScanRun) // 扫描记录明细（含逐 IP 结果）
		r.Get("/scan/summary", a.scanSummary)  // 扫描批次聚合（探测历史 IP 扫描页签）

		r.Get("/lines", a.listLines)
		r.Post("/lines", a.upsertLine)
		r.Get("/lines/{id}/ip-history", a.lineIPHistory) // 线路出口 IP 变化轨迹（详情页）
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
		r.Post("/probes/refresh", a.refreshProbes)          // 立即刷新探针信息（心跳应答夹带重探出口指令）
		r.Post("/probes/provision", a.provisionNew)         // 零接触开通：免密+装机+注册+线路自动匹配绑定
		r.Get("/probes/provision/{job}", a.provisionStatus) // 开通任务进度轮询
		r.Post("/speedtest/run", a.runSpeedTest)

		r.Get("/alerts", a.listAlerts)
		r.Get("/settings", a.getSettings)
		r.Put("/settings", a.putSettings)
		r.Post("/settings/smtp/test", a.testSMTP)
		r.Post("/settings/notify/test", a.testNotify)
		r.Post("/settings/syslog/test", a.testSyslog) // 日志外发测试：发一条样例日志到审计服务器

		r.Get("/metrics/query_range", a.metricsRange)
		r.Get("/history", a.history)
		r.Get("/history/labels", a.historyLabels) // 目标/监控项下拉枚举（VM 标签值代理）

		r.Get("/users", a.listUsers)
		r.Post("/users", a.addUser)
		r.Delete("/users/{id}", a.deleteUser)

		r.Get("/silences", a.listSilences)
		r.Post("/silences", a.createSilence)
		r.Delete("/silences/{id}", a.deleteSilence)

		r.Get("/reports", a.listReports)
		r.Get("/reports/template-default", a.repTemplateDefault) // 内置默认模板+变量/表格速查（前端"恢复默认"用）
		r.Get("/reports/{id}", a.getReport)
		r.Get("/reports/{id}/export.csv", a.exportReport)
		r.Get("/reports/{id}/export.docx", a.exportReportDocx) // 按模板一键导出 Word 运维报告
		r.Post("/reports/generate", a.genReport)
		r.Get("/audit", a.listAudit)

		r.Get("/agent-releases", a.listReleases)
		r.Post("/agent-releases", a.uploadRelease)
		r.Post("/probes/upgrade", a.upgradeProbes)
		r.Get("/snmp/ifaces", a.snmpIfaces)
	})
}

// ---------- 会话 ----------

// 会话 30 天（Cookie Max-Age 与 PG sessions.exp 一致；持久化到库，server 重启不失效）
const sessionTTL = 30 * 24 * time.Hour

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
			// 内存未命中（server 重启后会话内存丢失）→ 回源 PG 恢复
			if !valid {
				if u, rl, ok2 := a.store.GetSession(r.Context(), c.Value); ok2 {
					a.sessMu.Lock()
					sess = nwSession{user: u, role: rl, exp: time.Now().Add(sessionTTL)}
					a.sessions[c.Value] = sess
					a.sessMu.Unlock()
					valid = true
				}
			}
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
		// 个人操作放行：改自己密码、退出登录——任何已登录用户均可（仍照常审计）。
		// 登出若被观察者 403，会话不删除，"退出登录"形同虚设
		personal := (r.Method == http.MethodPut && r.URL.Path == "/api/admin/me/password") ||
			(r.Method == http.MethodPost && r.URL.Path == "/api/admin/logout")
		if r.Method != http.MethodGet && !personal && sess.role != "admin" {
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

func (a *AdminAPI) newSession(w http.ResponseWriter, r *http.Request, user, role string) {
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
	// 持久化（重启后会话保留）并顺手清理过期行
	exp := time.Now().Add(sessionTTL)
	if err := a.store.CreateSession(r.Context(), tok, user, role, exp); err != nil {
		log.Printf("persist session: %v", err)
	}
	a.store.CleanExpiredSessions(r.Context())
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
	a.newSession(w, r, req.Username, "admin")
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
		a.store.WriteAudit(r.Context(), req.Username, "login", "失败")
		httpError(w, 401, "用户名或密码错误")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		a.store.WriteAudit(r.Context(), req.Username, "login", "失败")
		httpError(w, 401, "用户名或密码错误")
		return
	}
	_ = id
	a.store.WriteAudit(r.Context(), req.Username, "login", "成功")
	a.newSession(w, r, req.Username, a.store.GetUserRole(r.Context(), req.Username))
	writeJSON(w, map[string]any{"ok": true})
}

func (a *AdminAPI) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("nw_session"); err == nil {
		a.sessMu.Lock()
		delete(a.sessions, c.Value)
		a.sessMu.Unlock()
		_ = a.store.DeleteSession(r.Context(), c.Value) // 同步删持久化行
	}
	writeJSON(w, map[string]any{"ok": true})
}

// changePassword 修改自己密码：校验旧密码 → 更新哈希 → 踢掉该用户其他会话（保留当前会话）
func (a *AdminAPI) changePassword(w http.ResponseWriter, r *http.Request) {
	sess, _ := r.Context().Value(ctxUserKey).(nwSession)
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	if len(req.NewPassword) < 8 {
		httpError(w, 400, "新密码至少 8 位")
		return
	}
	_, hash, err := a.store.GetUser(r.Context(), sess.user)
	if err != nil {
		httpError(w, 401, "用户不存在")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)) != nil {
		httpError(w, 400, "旧密码错误")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, 500, "internal")
		return
	}
	if err := a.store.SetUserPassword(r.Context(), sess.user, string(newHash)); err != nil {
		httpError(w, 500, "db")
		return
	}
	// 踢掉其他设备的会话（保留当前会话，避免自己被登出）；旧密码若已泄露，改密后即刻全端失效。
	// requireSession 已校验过 cookie，此处必能取到当前 token
	cur, _ := r.Cookie("nw_session")
	a.sessMu.Lock()
	exp := time.Now().Add(sessionTTL)
	for k, s := range a.sessions {
		if s.user == sess.user && k != cur.Value {
			delete(a.sessions, k)
		} else if k == cur.Value {
			s.exp = exp // 内存与 PG 有效期对齐
			a.sessions[k] = s
		}
	}
	a.sessMu.Unlock()
	_ = a.store.DeleteSessionsByUser(r.Context(), sess.user) // PG 行按用户全删
	if err := a.store.CreateSession(r.Context(), cur.Value, sess.user, sess.role, exp); err != nil {
		log.Printf("改密后重建当前会话行失败: %v", err) // 失败仅影响重启后需重登，当前登录态仍在
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 总览 ----------

type overviewLine struct {
	LineRow
	ProbeIDs []string      `json:"probe_ids"`
	Statuses []ProbeStatus `json:"statuses"`
	Status   string        `json:"status"`  // 汇总状态（取最差）
	Minutes  []MinStat     `json:"minutes"` // 方格心跳图：各探针逐分钟样本并集（时间升序，稀疏）
}

var statusRank = map[string]int{
	"unknown": 0, "online": 1, "degraded": 2, "speed_low": 2, "conflict": 3, "down": 3, "floor_down": 4, "blind": 5,
}

// statusOrder 同级别的展示名取确定性顺序（map 遍历无序，避免 degraded/speed_low 随机跳变）
var statusOrder = []string{"blind", "floor_down", "down", "conflict", "degraded", "speed_low", "online", "unknown"}

// aggregateLineOverviews 把探针实时状态聚合到线路（总览与楼层状态共用；纯函数便于回归）。
// 每条线路取最差探针状态，部分探针报中断、部分正常视为数据冲突；未绑定线路的探针单独返回。
func aggregateLineOverviews(lines []LineRow, probes []ProbeRow, snap map[string]ProbeStatus) ([]overviewLine, []map[string]string) {
	byLine := map[int][]string{}
	unbound := []map[string]string{}
	for _, p := range probes {
		if p.LineID != nil {
			byLine[*p.LineID] = append(byLine[*p.LineID], p.ID)
		} else {
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
			if len(st.Minutes) > 0 {
				ol.Minutes = poolMinStats(ol.Minutes, st.Minutes)
			}
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
	return ovs, unbound
}

// minutesParam 心跳分钟数裁剪参数：缺省 def；非法/越界回落 def；上限 max（与 3h 内存环一致）。
func minutesParam(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("minutes"))
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// trimMinStats 只保留最后 n 个分钟桶（时间升序切片）；n<=0 不裁剪。
func trimMinStats(ms []MinStat, n int) []MinStat {
	if n > 0 && len(ms) > n {
		return ms[len(ms)-n:]
	}
	return ms
}

// overviewData 总览数据构建（管理端与大屏共用）。mon/ind 为中心侧监测引擎（可为 nil）。
// minutes 控制线路/楼层心跳分钟数：状态页/大屏 60，详情页 180（内存环 3h）。
func overviewData(ctx context.Context, store *Store, eng *Engine, mon *MonitorRunner, ind *IndustryRunner, minutes int) (map[string]any, error) {
	lines, err := store.ListLines(ctx)
	if err != nil {
		return nil, err
	}
	probes, err := store.ListProbes(ctx)
	if err != nil {
		return nil, err
	}
	snap := eng.Snapshot()
	ovs, unbound := aggregateLineOverviews(lines, probes, snap)
	// 线路心跳按 minutes 裁剪；探针 statuses 里的 minutes 固定 60（总览/大屏/探针页用，
	// 详情页用的是线路级 ovs[].minutes，可请求 180）。
	for i := range ovs {
		ovs[i].Minutes = trimMinStats(ovs[i].Minutes, minutes)
		for j := range ovs[i].Statuses {
			ovs[i].Statuses[j].Minutes = trimMinStats(ovs[i].Statuses[j].Minutes, 60)
		}
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
		"internal_monitors": monSnapshot, "industry_monitors": indSnapshot,
		"loss_thresh": lossThreshOf(ctx, store)}, nil
}
func (a *AdminAPI) overview(w http.ResponseWriter, r *http.Request) {
	out, err := overviewData(r.Context(), a.store, a.eng, a.mon, a.ind,
		minutesParam(r, 60, int(ringHistory.Minutes())))
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, out)
}

// ---------- 楼层状态 ----------

// FloorStatusLine 楼层卡片内单条关联线路及其实时状态。
type FloorStatusLine struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	PublicIP string `json:"public_ip"`
	Status   string `json:"status"`
}

// overviewFloor 楼层状态页单卡片数据：以楼层为单位聚合关联线路的探针状态与最近一次 IP 扫描。
type overviewFloor struct {
	FloorRow
	Status     string            `json:"status"` // 楼层网络状况 = 关联线路取最差（无关联线路=unknown）
	RTTms      float64           `json:"rtt_ms"` // 关联线路探针时延/丢包取最大，TCP/HTTP 全部成功才算成功
	LossPct    float64           `json:"loss_pct"`
	TCPOk      bool              `json:"tcp_ok"`
	HTTPOk     bool              `json:"http_ok"`
	Minutes    []MinStat         `json:"minutes"`    // 方格心跳图：关联线路各探针逐分钟样本并集（时间升序，稀疏）
	LineStats  []FloorStatusLine `json:"line_stats"` // 关联线路及各自状态（多线路楼层逐行展示）
	Online     *int              `json:"online"`     // 本楼层最近一次 IP 扫描在线数（nil=尚未扫描）
	Total      *int              `json:"total"`
	ScannedAt  *time.Time        `json:"scanned_at"`
	ProbeNames []string          `json:"probe_names"` // 关联探针名（页脚展示）
}

func floorStatusData(ctx context.Context, store *Store, eng *Engine, minutes int) ([]overviewFloor, error) {
	floors, err := store.ListFloors(ctx)
	if err != nil {
		return nil, err
	}
	lines, err := store.ListLines(ctx)
	if err != nil {
		return nil, err
	}
	probes, err := store.ListProbes(ctx)
	if err != nil {
		return nil, err
	}
	ovs, _ := aggregateLineOverviews(lines, probes, eng.Snapshot())
	scans, err := store.LatestFloorScans(ctx)
	if err != nil {
		return nil, err
	}
	probeNames := map[string]string{}
	for _, p := range probes {
		n := p.Name
		if n == "" {
			n = p.Hostname
		}
		probeNames[p.ID] = n
	}
	out := aggregateFloorStatus(floors, ovs, scans, probeNames)
	for i := range out {
		out[i].Minutes = trimMinStats(out[i].Minutes, minutes)
	}
	return out, nil
}

// aggregateFloorStatus 把线路状态与楼层扫描摘要聚合到楼层（纯函数，供回归用例覆盖）。
// 楼层状态 = 关联线路取最差（statusOrder 从差到好，首个命中即最差）；心跳条跨线路逐位与。
func aggregateFloorStatus(floors []FloorRow, ovs []overviewLine, scans map[int]FloorScanSummary, probeNames map[string]string) []overviewFloor {
	byLine := map[int]overviewLine{}
	for _, ov := range ovs {
		byLine[ov.ID] = ov
	}
	out := make([]overviewFloor, 0, len(floors))
	for _, f := range floors {
		fl := overviewFloor{FloorRow: f}
		var allStatuses []ProbeStatus
		seenProbes := map[string]bool{}
		for _, brief := range f.Lines {
			ov, ok := byLine[brief.ID]
			if !ok {
				continue
			}
			fl.LineStats = append(fl.LineStats, FloorStatusLine{ID: ov.ID, Name: ov.Name, PublicIP: ov.PublicIP, Status: ov.Status})
			allStatuses = append(allStatuses, ov.Statuses...)
			if len(ov.Minutes) > 0 {
				fl.Minutes = poolMinStats(fl.Minutes, ov.Minutes)
			}
			for _, pid := range ov.ProbeIDs {
				if !seenProbes[pid] {
					seenProbes[pid] = true
					fl.ProbeNames = append(fl.ProbeNames, probeNames[pid])
				}
			}
		}
		fl.Status = "unknown"
		for _, k := range statusOrder {
			for _, ls := range fl.LineStats {
				if ls.Status == k {
					fl.Status = k
					break
				}
			}
			if fl.Status != "unknown" {
				break
			}
		}
		fl.TCPOk, fl.HTTPOk = len(allStatuses) > 0, len(allStatuses) > 0
		for _, st := range allStatuses {
			if st.RTTms > fl.RTTms {
				fl.RTTms = st.RTTms
			}
			if st.LossPct > fl.LossPct {
				fl.LossPct = st.LossPct
			}
			if !st.TCPOk {
				fl.TCPOk = false
			}
			if !st.HTTPOk {
				fl.HTTPOk = false
			}
		}
		if sm, ok := scans[f.ID]; ok {
			up, total, at := sm.Up, sm.Total, sm.At
			fl.Online, fl.Total, fl.ScannedAt = &up, &total, &at
		}
		out = append(out, fl)
	}
	return out
}

// poolMinStats 多组逐分钟样本并集汇总（同分钟 n/f 累加，升序输出）。
// 方格心跳图的交叉验证语义：任一探针/线路的成功样本都计入（与"有一个目标成功
// 即视为联通"同构——全部样本失败才失败）。
// M（降级中位数）取各输入的最大值：与状态聚合"取最差探针/线路"同构——
// 线路/楼层心跳黄 ⇔ 最差探针按告警口径处于降级（黄格与降质徽章同源）。
func poolMinStats(a, b []MinStat) []MinStat {
	if len(a) == 0 {
		return append([]MinStat(nil), b...)
	}
	if len(b) == 0 {
		return append([]MinStat(nil), a...)
	}
	m := map[int64]*MinStat{}
	for _, g := range [][]MinStat{a, b} {
		for _, s := range g {
			p := m[s.T]
			if p == nil {
				p = &MinStat{T: s.T}
				m[s.T] = p
			}
			p.N += s.N
			p.F += s.F
			if s.M > p.M {
				p.M = s.M
			}
		}
	}
	out := make([]MinStat, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// lossThreshOf 降质丢包阈值（%）：读告警设置，缺省/非法回落 10。
// 随 overview/floor-status/history 接口下发，前端心跳黄格与 P2 降质告警同口径。
func lossThreshOf(ctx context.Context, store *Store) float64 {
	var als AlertSettings
	if err := store.GetSetting(ctx, "alert_settings", &als); err == nil && als.LossPctThresh > 0 {
		return als.LossPctThresh
	}
	return 10
}

func (a *AdminAPI) floorStatus(w http.ResponseWriter, r *http.Request) {
	out, err := floorStatusData(r.Context(), a.store, a.eng,
		minutesParam(r, 60, int(ringHistory.Minutes())))
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"floors": out, "loss_thresh": lossThreshOf(r.Context(), a.store)})
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

// normalizeFloorCIDR 校验并规范化楼层子网（空串=允许，仅展示不扫描）。
// 扫描器只支持 IPv4 且上限 /22；非法 CIDR 会在定时扫描时反复产生失败记录。
func normalizeFloorCIDR(raw string) (string, string) {
	cidr := strings.TrimSpace(raw)
	if cidr == "" {
		return "", ""
	}
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return "", "子网 CIDR 格式错误（示例：10.8.14.0/24）"
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones < 22 {
		return "", "子网 CIDR 仅支持 IPv4 且范围不超过 /22（如 10.8.14.0/24）"
	}
	return ipnet.String(), ""
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
	// CIDR 先校验再入库：非法格式会在定时扫描时反复产生失败记录，且 /23 以上宽网段
	// 扫描接口本身会拒绝（scanMaxHosts=/22）。允许留空（仅展示用）；非空统一规范化为网络地址。
	cidr, msg := normalizeFloorCIDR(fr.CIDR)
	if msg != "" {
		httpError(w, 400, msg)
		return
	}
	fr.CIDR = cidr
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
		if errors.Is(err, ErrNotFound) {
			httpError(w, 404, "楼层不存在")
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
	// 手工修改出口 IP 同样入变化轨迹（old_ip 取修改前值；UpsertLine 空值不覆盖语义不变）
	var oldIP string
	if l.ID != 0 {
		if lines, err := a.store.ListLines(r.Context()); err == nil {
			for _, ol := range lines {
				if ol.ID == l.ID {
					oldIP = ol.PublicIP
				}
			}
		}
	}
	sess, _ := r.Context().Value(ctxUserKey).(nwSession)
	nid, err := a.store.UpsertLine(r.Context(), &l, l.FloorIDs)
	if err != nil {
		if strings.Contains(err.Error(), "已存在") {
			httpError(w, 400, err.Error())
			return
		}
		if errors.Is(err, ErrNotFound) {
			httpError(w, 404, "线路不存在")
			return
		}
		httpError(w, 500, "db")
		return
	}
	if l.ID != 0 && l.PublicIP != "" && l.PublicIP != oldIP {
		if err := a.store.RecordLineIPChange(r.Context(), l.ID, oldIP, l.PublicIP, "", sess.user, "manual"); err != nil {
			log.Printf("线路 %d 手工改出口 IP 留痕失败: %v", l.ID, err)
		}
	}
	writeJSON(w, map[string]any{"id": nid})
}

// lineIPHistory 线路出口 IP 变化轨迹（线路详情页展示）
func (a *AdminAPI) lineIPHistory(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		httpError(w, 400, "bad id")
		return
	}
	rows, err := a.store.ListLineIPHistory(r.Context(), id, 100)
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	if rows == nil {
		rows = []LineIPChange{}
	}
	writeJSON(w, rows)
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
	first := true
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) < 3 {
			skipped = append(skipped, "格式错误（列数不足）："+line)
			continue
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		if first {
			first = false
			if cols[0] == "楼层" || cols[0] == "楼层名" {
				continue // 标准表头
			}
		}
		floorName, cidr, lineName := cols[0], cols[1], cols[2]
		if !validDisplayName(floorName) || !validDisplayName(lineName) {
			skipped = append(skipped, fmt.Sprintf("楼层/线路名非法：%q / %q", floorName, lineName))
			continue
		}
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
			ncidr, msg := normalizeFloorCIDR(cidr)
			if msg != "" {
				skipped = append(skipped, floorName+"（"+msg+"）")
				continue
			}
			nid, err := a.store.UpsertFloor(r.Context(), nil, floorName, ncidr)
			if err != nil {
				skipped = append(skipped, floorName+"（"+err.Error()+"）")
				continue
			}
			fid = nid
			floorByName[floorName] = nid
		}
		l := LineRow{Name: lineName, ISP: "联通", PublicIP: publicIP,
			ContractDownMbps: down, ContractUpMbps: up}
		if _, err := a.store.UpsertLine(r.Context(), &l, []int{fid}); err != nil {
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
		st := snap[p.ID]
		st.Minutes = trimMinStats(st.Minutes, 60) // 探针状态页/总览默认 60 格
		m := map[string]any{"probe": p, "status": st}
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

// listAlerts 告警中心列表：status/severity/kind/hours/limit/offset 过滤分页。
// 时间窗以告警开始时间为准，缺省近 24 小时（hours=0 表示全部时间）。
func (a *AdminAPI) listAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hours := 24
	if raw := q.Get("hours"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			hours = v
		}
	}
	var since time.Time
	if hours > 0 {
		since = time.Now().Add(-time.Duration(hours) * time.Hour)
	}
	rows, total, err := a.store.ListAlerts(r.Context(), q.Get("status"), q.Get("severity"), q.Get("kind"),
		since, atoi(q.Get("limit")), atoi(q.Get("offset")))
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	writeJSON(w, map[string]any{"total": total, "rows": rows})
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
	// 报告模板未配置时回填内置默认（管理员打开编辑器即见全文，可直接修改保存）
	if _, ok := m["report_templates"]; !ok {
		t := DefaultRepTemplate()
		if b, err := json.Marshal(map[string]string{"day": t, "week": t, "month": t}); err == nil {
			m["report_templates"] = b
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
		"notify_channels": true, "snmp_settings": true,
		"internal_monitors": true, "industry_monitors": true, "scan_settings": true,
		"report_templates": true, "syslog_settings": true}
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
			for i := range im.Items {
				it := &im.Items[i]
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
				if it.IntervalS < 10 {
					it.IntervalS = 30
				}
				if it.TimeoutS <= 0 || it.TimeoutS > 60 {
					it.TimeoutS = 5
				}
			}
			if nb, err := json.Marshal(im); err == nil {
				v = nb
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
			if nb, err := json.Marshal(im); err == nil {
				v = nb
			}
		}
		if k == "scan_settings" {
			var ss ScanSettings
			if json.Unmarshal(v, &ss) != nil {
				httpError(w, 400, "scan_settings 格式错误")
				return
			}
			if ss.IntervalMin < scanIntervalMinMin || ss.IntervalMin > scanIntervalMaxMin {
				httpError(w, 400, fmt.Sprintf("扫描间隔需在 %d~%d 分钟之间", scanIntervalMinMin, scanIntervalMaxMin))
				return
			}
			if ss.TimeoutMs < scanTimeoutMsMin || ss.TimeoutMs > scanTimeoutMsMax {
				httpError(w, 400, fmt.Sprintf("单 IP 超时需在 %d~%d 毫秒之间", scanTimeoutMsMin, scanTimeoutMsMax))
				return
			}
			ss.normalize()
			if nb, err := json.Marshal(ss); err == nil {
				v = nb
			}
		}
		if k == "alert_settings" {
			var als AlertSettings
			if json.Unmarshal(v, &als) != nil {
				httpError(w, 400, "alert_settings 格式错误")
				return
			}
			// 阈值必须服务端兜底：这些值直接决定误报/漏报（blind=0 会让所有探针
			// 立刻变盲区、line_down=0 立刻判中断、loss_window=0 使降质判定恒样本不足）。
			// 越界回落默认值，语义与 agent_config/scan_settings 的钳位一致。
			if als.BlindSecs < 30 || als.BlindSecs > 3600 {
				als.BlindSecs = 60
			}
			if als.LineDownSecs < 5 || als.LineDownSecs > 3600 {
				als.LineDownSecs = 30
			}
			if als.LossPctThresh <= 0 || als.LossPctThresh > 100 {
				als.LossPctThresh = 10
			}
			if als.LossWindowMin < 1 || als.LossWindowMin > 60 {
				als.LossWindowMin = 5
			}
			if als.EvalIntervalS < 2 || als.EvalIntervalS > 60 {
				als.EvalIntervalS = 5
			}
			if als.TypeFailWindowS < 30 || als.TypeFailWindowS > 600 {
				als.TypeFailWindowS = 60
			}
			if nb, err := json.Marshal(als); err == nil {
				v = nb
			}
		}
		if k == "snmp_settings" {
			var sn SnmpSettings
			if json.Unmarshal(v, &sn) != nil {
				httpError(w, 400, "snmp_settings 格式错误")
				return
			}
			if sn.IntervalS < 10 {
				sn.IntervalS = 30
			}
			if sn.TimeoutS <= 0 || sn.TimeoutS > 30 {
				sn.TimeoutS = 5
			}
			if nb, err := json.Marshal(sn); err == nil {
				v = nb
			}
		}
		if k == "agent_config" {
			var pc proto.ProbeConfig
			if json.Unmarshal(v, &pc) != nil {
				httpError(w, 400, "agent_config 格式错误")
				return
			}
			// 周期/超时/重试字段的钳位（越界回落默认；<=0 视为未配置，同样回默认）。
			// 探测周期过小会让 12 台探针整体高频打目标（1s 间隔=每秒数十个探测），
			// 超时过大则会让单轮探测长时间占用 goroutine，故服务端统一兜底。
			if pc.IntervalPingS < 5 || pc.IntervalPingS > 3600 {
				pc.IntervalPingS = 10
			}
			if pc.IntervalTCPS < 5 || pc.IntervalTCPS > 3600 {
				pc.IntervalTCPS = 30
			}
			if pc.IntervalDNSS < 5 || pc.IntervalDNSS > 3600 {
				pc.IntervalDNSS = 60
			}
			if pc.IntervalHTTPS < 5 || pc.IntervalHTTPS > 3600 {
				pc.IntervalHTTPS = 60
			}
			if pc.TimeoutS < 1 || pc.TimeoutS > 10 {
				pc.TimeoutS = 3
			}
			if pc.RetryN < 0 || pc.RetryN > 3 {
				pc.RetryN = 1
			}
			if pc.RetryPauseMs < 100 || pc.RetryPauseMs > 2000 {
				pc.RetryPauseMs = 300
			}
			if pc.HeartbeatSec < 5 || pc.HeartbeatSec > 60 {
				pc.HeartbeatSec = 15
			}
			if pc.EgressMin < 1 || pc.EgressMin > 60 {
				pc.EgressMin = 5
			}
			if nb, err := json.Marshal(pc); err == nil {
				v = nb
			}
		}
		if k == "syslog_settings" {
			var sl SyslogSettings
			if json.Unmarshal(v, &sl) != nil {
				httpError(w, 400, "syslog_settings 格式错误")
				return
			}
			sl.normalize()
			if sl.Enabled && sl.Host == "" {
				httpError(w, 400, "启用日志外发必须填写审计服务器地址")
				return
			}
			if nb, err := json.Marshal(sl); err == nil {
				v = nb
			}
		}
		if k == "report_settings" {
			var rs ReportSettings
			if json.Unmarshal(v, &rs) != nil {
				httpError(w, 400, "report_settings 格式错误")
				return
			}
			if rs.MonthlyDay < 1 || rs.MonthlyDay > 28 {
				httpError(w, 400, "月报生成日期需在 1~28 之间")
				return
			}
			for _, t := range []string{rs.DailyAt, rs.WeeklyAt, rs.MonthlyAt} {
				if strings.TrimSpace(t) != "" {
					if _, err := time.Parse("15:04", strings.TrimSpace(t)); err != nil {
						httpError(w, 400, "报表时间格式应为 HH:MM（如 08:00）")
						return
					}
				}
			}
		}
		if k == "report_templates" {
			var tm map[string]string
			if json.Unmarshal(v, &tm) != nil {
				httpError(w, 400, "report_templates 格式错误")
				return
			}
			for kk := range tm {
				if kk != "day" && kk != "week" && kk != "month" {
					httpError(w, 400, "report_templates 键须为 day/week/month: "+kk)
					return
				}
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
	html := `<p>这是一封来自物探中心网络监控系统（NetWatch）的测试邮件。</p><p>收到本邮件说明 SMTP 配置正确，告警邮件可以正常送达。</p>`
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
	md := "✅ 物探中心网络监控系统 测试消息\n\n通道连通正常，告警将推送到本群。\n\n—— 物探中心网络监控系统（NetWatch）"
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

// testSyslog 日志外发测试：按已保存配置向审计服务器同步发送一条样例日志（错误返回给界面）。
func (a *AdminAPI) testSyslog(w http.ResponseWriter, r *http.Request) {
	var st SyslogSettings
	if err := a.store.GetSetting(r.Context(), "syslog_settings", &st); err != nil {
		httpError(w, 500, "db")
		return
	}
	if strings.TrimSpace(st.Host) == "" {
		httpError(w, 400, "请先填写并保存审计服务器地址")
		return
	}
	if err := SendTestLog(a.cfg, st); err != nil {
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
	// 同步删 PG 会话行：只清内存不够——server 重启后内存回源 PG，已删用户会复活登录
	if err := a.store.DeleteSessionsByUser(r.Context(), target.Username); err != nil {
		log.Printf("删除用户 %q 的持久化会话失败: %v", target.Username, err)
	}
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

// exportReportDocx 按模板渲染 Word 运维报告（模板取 settings 键 report_templates[kind]，缺省用内置默认）
func (a *AdminAPI) exportReportDocx(w http.ResponseWriter, r *http.Request) {
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
	tmpls := map[string]string{}
	_ = a.store.GetSetting(r.Context(), "report_templates", &tmpls)
	tmpl := tmpls[kind]
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultRepTemplate()
	}
	var rs ReportSettings
	_ = a.store.GetSetting(r.Context(), "report_settings", &rs)
	doc, err := RenderReportDocx(tmpl, &d, rs.OrgName)
	if err != nil {
		httpError(w, 500, "生成 Word 失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=netwatch-%s-%s.docx", kind, start.Format("20060102")))
	_, _ = w.Write(doc)
}

// repTemplateDefault 返回内置默认模板与变量/表格速查（管理员恢复默认、编辑器速查数据源）
func (a *AdminAPI) repTemplateDefault(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "day" && kind != "week" && kind != "month" {
		kind = "day"
	}
	writeJSON(w, map[string]any{"kind": kind, "text": DefaultRepTemplate(),
		"vars": repVarDocs, "tables": repTableDocs})
}

type genReportReq struct {
	Kind string `json:"kind"` // day / week / month
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
	// 显示名与楼层/线路同口径校验（空串=清空，回退主机名/ID）；引号反斜杠会污染
	// 页面展示、告警标题与日志外发 JSON。
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n != "" && !validDisplayName(n) {
			httpError(w, 400, "探针名含非法字符（禁止 \" \\ 反引号/换行，最长 64 字符）")
			return
		}
		req.Name = &n
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

// ---------- 探测历史（PRTG 风格原始记录查询） ----------

// HistoryRecord 单条探测历史记录
type HistoryRecord struct {
	TS     int64   `json:"ts_ms"`
	Target string  `json:"target"`
	Probe  string  `json:"probe,omitempty"` // 探针 ID（中心侧 monitor/industry 无）
	Class  string  `json:"class"`           // ping: gw/public；其他为空
	Type   string  `json:"type"`            // ping/tcp/dns/http/monitor/industry/speed
	OK     bool    `json:"ok"`
	RTTms  float64 `json:"rtt_ms"`    // 时延 ms；speed 类型=下载速率 Mbps
	UpMbps float64 `json:"up_mbps"`   // speed 上传速率 Mbps
	Code   float64 `json:"code"`      // http 状态码
	Cert   float64 `json:"cert_days"` // http 证书剩余天数
}

// HistoryStats 查询窗口全量统计（status 过滤前口径）
type HistoryStats struct {
	Total  int     `json:"total"`
	OK     int     `json:"ok"`
	Fail   int     `json:"fail"`
	AvgRTT float64 `json:"avg_rtt_ms"` // 平均时延 ms；speed 类型=平均下载 Mbps
}

// HistoryBucket 趋势分桶（≤240 桶，前端方格条/折线图数据源，渲染量恒定不卡顿）
type HistoryBucket struct {
	TS     int64   `json:"ts"`         // 桶起始（unix 秒）
	N      int     `json:"n"`          // 探测次数
	OKN    int     `json:"ok_n"`       // 成功次数
	AvgRTT float64 `json:"avg_rtt_ms"` // 平均时延/速率
	// PN/PL：ping 样本数与桶内"按目标丢包率下中位数"（%，lossMedian 同 helper）——
	// 降级黄格判定与 P2 告警同口径；type=all 桶仅统计 ping 部分。样本不足时 PL 不产出。
	PN int     `json:"pn,omitempty"`
	PL float64 `json:"pl,omitempty"`
}

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

var histTypes = map[string]bool{"ping": true, "tcp": true, "dns": true, "http": true,
	"monitor": true, "industry": true, "speed": true, "all": true}

// histMetric 探测类型 → VM 指标族（ok 主序列 / 关联值序列 / 附加序列）。
// speed：ok 序列值即下载速率（无独立 rtt 序列）。
func histMetric(typ string) (okMetric, rttMetric string, extras []string) {
	switch typ {
	case "ping":
		return "nw_ping_ok", "nw_ping_rtt_ms", nil
	case "tcp":
		return "nw_tcp_ok", "nw_tcp_rtt_ms", nil
	case "dns":
		return "nw_dns_ok", "nw_dns_rtt_ms", nil
	case "http":
		return "nw_http_ok", "nw_http_ttfb_ms", []string{"nw_http_code", "nw_http_cert_days"}
	case "monitor":
		return "nw_monitor_ok", "nw_monitor_rtt_ms", nil
	case "industry":
		return "nw_industry_ok", "nw_industry_rtt_ms", nil
	case "speed":
		return "nw_speed_down_mbps", "", []string{"nw_speed_up_mbps"}
	}
	return "", "", nil
}

// histSelAll 无目标过滤选择器（该探针/中心侧全部目标）
func histSelAll(typ, probe string) string {
	if typ == "monitor" {
		return `{monitor!=""}`
	}
	if typ == "industry" {
		return `{industry!=""}`
	}
	return `{probe="` + probe + `"}`
}

// histSelOne 单目标精确选择器（= 匹配）。多目标不改用正则：VM export 对
// match[] 里的反斜杠转义一律 400（[\d]、\. 实测皆拒），逐目标精确导出最稳。
func histSelOne(typ, probe, target string) string {
	t := strings.ReplaceAll(strings.ReplaceAll(target, `\`, `\\`), `"`, `\"`)
	if typ == "monitor" {
		return `{monitor="` + t + `"}`
	}
	if typ == "industry" {
		return `{industry="` + t + `"}`
	}
	return `{probe="` + probe + `",target="` + t + `"}`
}

// histClampWindow 窗口钳位：单次查询最长 31 天（原始点逐条导出，窗口过长会拖垮
// 服务端内存，且单序列 NDJSON 行超出 export 解析行上限会被静默跳过）
func histClampWindow(start, end int64) (int64, int64) {
	const maxWin = int64(31 * 86400)
	if end-start > maxWin {
		start = end - maxWin
	}
	return start, end
}

// histFamily 导出并组装单个类型族的记录（history 单类型/全类型复用）。
// 多探针（定时测速对比）×多目标：逐组合精确导出后合并（空=一次拉全部）；
// rtt/code/cert/up 等关联序列按探针全量拉取，按 (名称键, 探针, ts) 关联，
// 导出失败不阻塞主记录（缺失按 0 展示）。
func (a *AdminAPI) histFamily(ctx context.Context, typ string, probes []string, targets []string, start, end int64) ([]HistoryRecord, map[string]bool, error) {
	okMetric, rttMetric, extras := histMetric(typ)
	sels := make([]string, 0, len(probes)*(len(targets)+1))
	allSels := make([]string, 0, len(probes)) // 各探针的全量选择器（关联序列用）
	for _, p := range probes {
		allSels = append(allSels, histSelAll(typ, p))
		if len(targets) == 0 {
			sels = append(sels, histSelAll(typ, p))
		} else {
			for _, t := range targets {
				sels = append(sels, histSelOne(typ, p, t))
			}
		}
	}
	var okSeries []ExportSeries
	for _, sel := range sels {
		ss, err := a.tsdb.Export(ctx, okMetric+sel, start, end)
		if err != nil {
			return nil, nil, err
		}
		okSeries = append(okSeries, ss...)
	}
	joins := []string{}
	if rttMetric != "" {
		joins = append(joins, rttMetric)
	}
	joins = append(joins, extras...)
	joinAt := make(map[string]map[string]float64, len(joins)) // 指标名 → (名称键|探针|ts) → 值
	for _, m := range joins {
		at := map[string]float64{}
		for _, as := range allSels {
			sers, err := a.tsdb.Export(ctx, m+as, start, end)
			if err != nil {
				continue
			}
			for _, s := range sers {
				for i, v := range s.Values {
					if i < len(s.Timestamps) {
						at[nameKey(s.Metric)+"\x00"+s.Metric["probe"]+"|"+strconv.FormatInt(s.Timestamps[i], 10)] = v
					}
				}
			}
		}
		joinAt[m] = at
	}
	records := []HistoryRecord{}
	tgtSet := map[string]bool{}
	for _, s := range okSeries {
		tgt := nameKey(s.Metric)
		prb := s.Metric["probe"]
		tgtSet[tgt] = true
		for i, v := range s.Values {
			if i >= len(s.Timestamps) {
				continue
			}
			ts := s.Timestamps[i]
			key := tgt + "\x00" + prb + "|" + strconv.FormatInt(ts, 10)
			rec := HistoryRecord{TS: ts, Target: tgt, Probe: prb, Class: s.Metric["class"], Type: typ,
				OK: v > 0.5, RTTms: joinAt[rttMetric][key]}
			if typ == "speed" {
				rec.RTTms = v // 下载速率即数值通道
				rec.UpMbps = joinAt["nw_speed_up_mbps"][key]
			}
			if typ == "http" {
				rec.Code = joinAt["nw_http_code"][key]
				rec.Cert = joinAt["nw_http_cert_days"][key]
			}
			records = append(records, rec)
		}
	}
	return records, tgtSet, nil
}

// histExpectedIntv 类型的期望探测间隔（秒）：读自后台各类型配置（改配置即生效），读不到用默认值。
// 趋势分桶以它为自适应下限（桶跨度 ≥ 期望间隔，低频类型一格一个探测周期）——
// 探测周期比格子宽的类型（如 1 小时一轮的测速）不再把"正常探测间隙"显示成灰色空格，
// 空格只剩该周期内确实无样本的情况（漏探/暂停/离线/未启用）。
func (a *AdminAPI) histExpectedIntv(ctx context.Context, typ string) int64 {
	switch typ {
	case "all", "ping", "tcp", "dns", "http":
		var c struct {
			IntervalPingS int `json:"interval_ping_s"`
			IntervalTCPS  int `json:"interval_tcp_s"`
			IntervalDNSS  int `json:"interval_dns_s"`
			IntervalHTTPS int `json:"interval_http_s"`
		}
		if a.store.GetSetting(ctx, "agent_config", &c) == nil {
			v := map[string]int{"ping": c.IntervalPingS, "tcp": c.IntervalTCPS,
				"dns": c.IntervalDNSS, "http": c.IntervalHTTPS}
			if typ == "all" {
				m := 0
				for _, x := range v { // 全类型取最短间隔（ping 恒在，锚定格子密度）
					if x > 0 && (m == 0 || x < m) {
						m = x
					}
				}
				if m > 0 {
					return int64(m)
				}
				return 10
			}
			if v[typ] > 0 {
				return int64(v[typ])
			}
		}
		if typ == "all" {
			return 10
		}
		return map[string]int64{"ping": 10, "tcp": 30, "dns": 60, "http": 60}[typ]
	case "monitor", "industry":
		key := "internal_monitors"
		if typ == "industry" {
			key = "industry_monitors"
		}
		var c struct {
			Items []struct {
				IntervalS int `json:"interval_s"`
			} `json:"items"`
		}
		if a.store.GetSetting(ctx, key, &c) == nil {
			m := 0
			for _, it := range c.Items {
				s := it.IntervalS
				if s < 10 {
					s = 30 // 与 monitor/industry 运行时钳位一致
				}
				if s > 0 && (m == 0 || s < m) {
					m = s
				}
			}
			if m > 0 {
				return int64(m)
			}
		}
		return 60
	case "speed":
		var c struct {
			IntervalMin int `json:"interval_min"`
		}
		if a.store.GetSetting(ctx, "speed_settings", &c) == nil && c.IntervalMin > 0 {
			return int64(c.IntervalMin) * 60
		}
		return 3600
	}
	return 60
}

// histBucketize 把一组记录聚合到固定时间桶（跨度/桶数由调用方定，含网格对齐钳位）。
// includeSpeedRTT：speed 下载速率计入均值通道（图表纵轴=Mbps）。
// withLoss：聚合 ping 记录的按目标丢包计数并计算桶内下中位数 PL（降级黄格判定，
// 与 P2 告警 medianLoss 同 helper 同口径；系列折线不需要，传 false 省 payload）。
func histBucketize(recs []HistoryRecord, start, span int64, n int, includeSpeedRTT bool, withLoss bool) []HistoryBucket {
	buckets := make([]HistoryBucket, n)
	for i := range buckets {
		buckets[i].TS = start + int64(i)*span
	}
	rttSum := make([]float64, n)
	rttN := make([]int, n)
	var bucketTargets map[int]map[string][2]int // 桶 → 目标 → [总数, 失败数]（ping，PL 计算用）
	if withLoss {
		bucketTargets = map[int]map[string][2]int{}
	}
	for _, rc := range recs {
		idx := int((rc.TS/1000 - start) / span)
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		b := &buckets[idx]
		b.N++
		if rc.OK {
			b.OKN++
		}
		if (includeSpeedRTT || rc.Type != "speed") && rc.RTTms > 0 {
			rttSum[idx] += rc.RTTms
			rttN[idx]++
		}
		if withLoss && rc.Type == "ping" {
			b.PN++
			pt := bucketTargets[idx]
			if pt == nil {
				pt = map[string][2]int{}
				bucketTargets[idx] = pt
			}
			p := pt[rc.Target]
			p[0]++
			if !rc.OK {
				p[1]++
			}
			pt[rc.Target] = p
		}
	}
	for i := range buckets {
		if rttN[i] > 0 {
			buckets[i].AvgRTT = rttSum[i] / float64(rttN[i])
		}
		if withLoss && buckets[i].PN > 0 {
			if v, ok := lossMedian(bucketTargets[i]); ok {
				buckets[i].PL = v
			}
		}
	}
	return buckets
}

// histStatsBuckets 全窗口统计与趋势分桶（status 过滤前口径）。
// minSpan：桶跨度下限（=期望探测间隔），低频类型一格一个探测周期；
// maxBuckets：桶数上限（默认 240；前端缩放优化会请求更密的基线桶，钳位 ≤960）。
func histStatsBuckets(recs []HistoryRecord, start, end int64, includeSpeedRTT bool, minSpan int64, maxBuckets int) (HistoryStats, []HistoryBucket, int64) {
	if maxBuckets < 1 {
		maxBuckets = 240
	}
	span := (end - start) / int64(maxBuckets)
	if span < 1 {
		span = 1
	}
	if span < minSpan {
		span = minSpan
	}
	n := int((end-1-start)/span) + 1
	if n > maxBuckets {
		n = maxBuckets
	}
	buckets := histBucketize(recs, start, span, n, includeSpeedRTT, true)
	st := HistoryStats{}
	var sum float64
	var cnt int
	for _, rc := range recs {
		st.Total++
		if rc.OK {
			st.OK++
		} else {
			st.Fail++
		}
		if (includeSpeedRTT || rc.Type != "speed") && rc.RTTms > 0 {
			sum += rc.RTTms
			cnt++
		}
	}
	if cnt > 0 {
		st.AvgRTT = sum / float64(cnt)
	}
	return st, buckets, span
}

// histSeries 按"类型×探针×目标"分组的趋势桶（折线图多指标同图数据源）
type histSeries struct {
	Type    string          `json:"type"`
	Probe   string          `json:"probe,omitempty"`
	Target  string          `json:"target"`
	Buckets []HistoryBucket `json:"buckets"`
}

var histTypeOrder = map[string]int{"ping": 0, "tcp": 1, "dns": 2, "http": 3, "monitor": 4, "industry": 5, "speed": 6}

// histSeriesBuckets 同跨度按 (type,probe,target) 分组聚合，系列按类型顺序+探针+目标名稳定排序
// （同名 target 不混线：如 223.5.5.5 的 ping 与 dns、多探针的定时测速对比）。
// maxBuckets 与 histStatsBuckets 保持一致，保证系列与合并桶同网格同长度。
func histSeriesBuckets(recs []HistoryRecord, start, end, span int64, maxBuckets int, includeSpeedRTT bool) []histSeries {
	if maxBuckets < 1 {
		maxBuckets = 240
	}
	n := int((end-1-start)/span) + 1
	if n > maxBuckets {
		n = maxBuckets
	}
	groups := map[string][]HistoryRecord{}
	for _, rc := range recs {
		k := rc.Type + "\x00" + rc.Probe + "\x00" + rc.Target
		groups[k] = append(groups[k], rc)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a := strings.SplitN(keys[i], "\x00", 3)
		b := strings.SplitN(keys[j], "\x00", 3)
		if histTypeOrder[a[0]] != histTypeOrder[b[0]] {
			return histTypeOrder[a[0]] < histTypeOrder[b[0]]
		}
		if a[1] != b[1] {
			return a[1] < b[1]
		}
		return a[2] < b[2]
	})
	out := make([]histSeries, 0, len(keys))
	for _, k := range keys {
		parts := strings.SplitN(k, "\x00", 3)
		out = append(out, histSeries{Type: parts[0], Probe: parts[1], Target: parts[2],
			Buckets: histBucketize(groups[k], start, span, n, includeSpeedRTT, false)})
	}
	return out
}

// histFilterStatus 结果筛选（status=ok/fail），原地过滤返回保留切片
func histFilterStatus(recs []HistoryRecord, status string) []HistoryRecord {
	switch status {
	case "ok":
		kept := recs[:0]
		for _, r := range recs {
			if r.OK {
				kept = append(kept, r)
			}
		}
		return kept
	case "fail":
		kept := recs[:0]
		for _, r := range recs {
			if !r.OK {
				kept = append(kept, r)
			}
		}
		return kept
	}
	return recs
}

// history 探测历史查询：GET /api/admin/history?type=&probe=&targets=(&n 分隔多选)&status=&start=&end=&limit=&offset=&by_target=
// type: all/ping/tcp/dns/http/monitor/industry/speed。服务端分页（offset/limit），
// 返回当页 records + 全窗口统计 stats + 趋势分桶 buckets——前端渲染量恒定；
// by_target=1 时额外返回按（类型×目标）分组的 series[]（折线图多指标同图数据源）。
func (a *AdminAPI) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ := q.Get("type")
	probe := q.Get("probe")
	status := q.Get("status")
	byTarget := q.Get("by_target") == "1"
	limit := atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 2000
	}
	if limit > 20000 {
		limit = 20000
	}
	offset := atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	start := atoi64(q.Get("start"))
	end := atoi64(q.Get("end"))
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 || start >= end {
		start = end - 3600
	}
	start, end = histClampWindow(start, end)

	// 多选目标：换行分隔（目标名可能含逗号）；多选探针（定时测速对比）同理。
	// 未传 probes 时回退单值 probe 参数（历史行为兼容）。
	sel := []string{}
	for _, t := range strings.Split(q.Get("targets"), "\n") {
		if t = strings.TrimSpace(t); t != "" {
			sel = append(sel, t)
		}
	}
	if len(sel) > 16 {
		sel = sel[:16]
	}
	esc1 := func(v string) string {
		return strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`)
	}
	probes := []string{}
	for _, s := range strings.Split(q.Get("probes"), "\n") {
		if s = strings.TrimSpace(s); s != "" {
			probes = append(probes, esc1(s))
		}
	}
	if len(probes) > 16 {
		probes = probes[:16]
	}

	// PromQL 标签值转义：\ " 破坏匹配串（注入面收口）
	probe = esc1(probe)
	if len(probes) == 0 {
		probes = []string{probe}
	}
	if !histTypes[typ] {
		httpError(w, 400, "type 必须为 all/ping/tcp/dns/http/monitor/industry/speed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	fams := []string{typ}
	if typ == "all" {
		fams = []string{"ping", "tcp", "dns", "http", "monitor", "industry", "speed"}
	}
	var records []HistoryRecord
	tgtSet := map[string]bool{}
	warnings := []string{}
	if len(fams) == 1 {
		recs, tg, err := a.histFamily(ctx, fams[0], probes, sel, start, end)
		if err != nil {
			httpError(w, 502, "tsdb: "+err.Error())
			return
		}
		records, tgtSet = recs, tg
	} else {
		// 全类型：各族并发导出（单请求墙钟≈最慢单族），部分失败进 warnings 不静默
		var mu sync.Mutex
		var wg sync.WaitGroup
		var okFams int
		for _, ft := range fams {
			wg.Add(1)
			go func(ft string) {
				defer wg.Done()
				recs, tg, err := a.histFamily(ctx, ft, probes, sel, start, end)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					warnings = append(warnings, ft+": "+err.Error())
					return
				}
				okFams++
				records = append(records, recs...)
				for t := range tg {
					tgtSet[t] = true
				}
			}(ft)
		}
		wg.Wait()
		if okFams == 0 && len(warnings) > 0 {
			httpError(w, 502, "tsdb: "+strings.Join(warnings, "; "))
			return
		}
	}

	// 全窗口统计与趋势分桶（status 过滤前口径）；桶跨度按类型期望间隔自适应：
	// 低频类型一格一个探测周期（span ≥ 期望间隔），正常间隙不落空格。
	// buckets 参数：前端缩放优化请求更密基线桶（默认 240，钳 ≤960）
	nB := atoi(q.Get("buckets"))
	if nB < 240 {
		nB = 240
	}
	if nB > 960 {
		nB = 960
	}
	intv := a.histExpectedIntv(ctx, typ)
	st, buckets, span := histStatsBuckets(records, start, end, typ == "speed", intv, nB)
	var series []histSeries
	if byTarget {
		// 系列按 (type,probe,target) 独立分桶：all 型测速系列也携带自身 Mbps 值
		//（前端画右侧副轴）；合计桶/统计仍排除 speed，ms 均值不受污染
		series = histSeriesBuckets(records, start, end, span, nB, typ == "speed" || typ == "all")
	}

	// 结果筛选 → 时间倒序 → offset/limit 切片（total=过滤后总数）
	records = histFilterStatus(records, status)
	sort.Slice(records, func(i, j int) bool { return records[i].TS > records[j].TS })
	total := len(records)
	if offset >= total {
		records = []HistoryRecord{}
	} else {
		records = records[offset:]
		if len(records) > limit {
			records = records[:limit]
		}
	}
	tgtList := []string{}
	for t := range tgtSet {
		tgtList = append(tgtList, t)
	}
	sort.Strings(tgtList)
	writeJSON(w, map[string]any{"records": records, "targets": tgtList, "total": total,
		"limit": limit, "offset": offset, "stats": st, "buckets": buckets, "bucket_span": span,
		"expected_intv": intv, "series": series, "warnings": warnings,
		"loss_thresh": lossThreshOf(r.Context(), a.store)})
}

// historyLabels 枚举 VM 中某指标已有的标签值（探测历史页目标/监控项下拉数据源）。
// GET /api/admin/history/labels?metric=nw_ping_ok&label=target&probe=<探针ID>
// 解决"目标下拉依赖查询响应回填、选中后只剩一个目标"的鸡生蛋问题。
var histLabelMetrics = map[string]bool{"nw_ping_ok": true, "nw_tcp_ok": true, "nw_dns_ok": true,
	"nw_http_ok": true, "nw_monitor_ok": true, "nw_industry_ok": true, "nw_speed_down_mbps": true}
var histLabelNames = map[string]bool{"target": true, "monitor": true, "industry": true}

func (a *AdminAPI) historyLabels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metric, label := q.Get("metric"), q.Get("label")
	if !histLabelMetrics[metric] || !histLabelNames[label] {
		httpError(w, 400, "metric/label 非法")
		return
	}
	esc := func(v string) string {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		return v
	}
	matches := []string{}
	if p := esc(q.Get("probe")); p != "" && label == "target" {
		matches = append(matches, metric+`{probe="`+p+`"}`)
	} else {
		matches = append(matches, metric)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	vals, err := a.tsdb.LabelValues(ctx, label, matches)
	if err != nil {
		httpError(w, 502, "tsdb: "+err.Error())
		return
	}
	if vals == nil {
		vals = []string{}
	}
	writeJSON(w, map[string]any{"values": vals})
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
