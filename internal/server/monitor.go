package server

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"netwatch/internal/agent"
)

// 内→内 内置监控（Uptime Kuma 模式）：中心服务器自己作为探针，
// 探测内网服务（OA/业务系统/服务器/交换机）。复用楼层探针同一套探测原语。

// MonitorItem 单个内网监控目标
type MonitorItem struct {
	Name      string `json:"name"`
	Type      string `json:"type"`   // tcp / http / icmp
	Target    string `json:"target"` // tcp: host:port；http: URL；icmp: IP
	IntervalS int    `json:"interval_s"`
}

type MonitorSettings struct {
	Enabled  bool          `json:"enabled"`
	Items    []MonitorItem `json:"items"`
}

func DefaultMonitorSettings() MonitorSettings { return MonitorSettings{} }

// MonitorStatus 单目标实时状态（总览 API）
type MonitorStatus struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Target  string  `json:"target"`
	Up      bool    `json:"up"`
	Known   bool    `json:"known"`  // 是否已有探测结果
	RTTms   float64 `json:"rtt_ms"`
	Code    int     `json:"code"`   // http 状态码
	LastTS  string  `json:"last_ts"`
}

// MonitorRunner 内置探测器
type MonitorRunner struct {
	store   *Store
	tsdb    *TSDB
	eng     *Engine
	log     *log.Logger

	mu      sync.Mutex
	status  map[string]*MonitorStatus
	failCnt map[string]int
	version int // 配置代次（热更新用）
}

func NewMonitorRunner(store *Store, tsdb *TSDB, eng *Engine) *MonitorRunner {
	return &MonitorRunner{store: store, tsdb: tsdb, eng: eng,
		log:     log.New(os.Stderr, "monitor ", log.LstdFlags|log.Lmsgprefix),
		status:  map[string]*MonitorStatus{},
		failCnt: map[string]int{}}
}

func (m *MonitorRunner) Start(ctx context.Context) {
	go m.reloadLoop(ctx)
}

// reloadLoop 每 30s 重读设置，代次变化时重启全部目标循环（启动时立即加载一次），
// 并清理已删除目标的运行状态与进行中告警。
func (m *MonitorRunner) reloadLoop(ctx context.Context) {
	var cancel context.CancelFunc
	run := func() {
		st := m.settings(ctx)
		m.mu.Lock()
		m.version++
		v := m.version
		// 清理已删除目标的遗留状态与告警
		live := map[string]bool{}
		for _, it := range st.Items {
			live[it.Name] = true
		}
		for name := range m.status {
			if !live[name] {
				delete(m.status, name)
				delete(m.failCnt, name)
			}
		}
		m.mu.Unlock()
		// 已删除目标的进行中告警交由 Engine 清理
		m.eng.CleanMonitors(live)

		if !st.Enabled {
			if cancel != nil {
				cancel()
				cancel = nil
			}
			return
		}
		if cancel != nil {
			cancel()
		}
		var cctx context.Context
		cctx, cancel = context.WithCancel(ctx)
		go m.runAll(cctx, st, v)
	}
	run()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if cancel != nil {
				cancel()
			}
			return
		case <-t.C:
			run()
		}
	}
}

func (m *MonitorRunner) settings(ctx context.Context) MonitorSettings {
	var st MonitorSettings
	if err := m.store.GetSetting(ctx, "internal_monitors", &st); err != nil {
		return DefaultMonitorSettings()
	}
	for i := range st.Items {
		if st.Items[i].IntervalS < 10 {
			st.Items[i].IntervalS = 30
		}
	}
	return st
}

func (m *MonitorRunner) runAll(ctx context.Context, st MonitorSettings, version int) {
	for _, it := range st.Items {
		go m.runOne(ctx, it, version)
	}
}

func (m *MonitorRunner) runOne(ctx context.Context, it MonitorItem, version int) {
	t := time.NewTicker(time.Duration(it.IntervalS) * time.Second)
	defer t.Stop()
	timeout := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// 配置已换代则退出（探测本身用独立超时，见 probe）
			m.mu.Lock()
			cur := m.version
			m.mu.Unlock()
			if cur != version {
				return
			}
			m.probe(ctx, it, timeout)
		}
	}
}

// probe 执行一次探测：更新状态、写时序库、驱动告警。
// 网络探测使用独立超时（不从 reload ctx 派生）——reload 取消旧代 ctx 时
// 在途探测会以 context canceled 收尾，若把取消误记为探测失败会产生假 down。
func (m *MonitorRunner) probe(ctx context.Context, it MonitorItem, timeout time.Duration) {
	pctx, pcancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer pcancel()
	var rtt float64
	var ok bool
	var code int
	switch it.Type {
	case "http":
		rtt, code, ok, _ = agent.ProbeHttp(pctx, it.Target, timeout)
	case "icmp":
		rtt, ok = agent.ProbePing(pctx, it.Target, timeout)
	default: // tcp
		rtt, ok = agent.ProbeTcp(pctx, it.Target, timeout)
	}
	now := time.Now()

	m.mu.Lock()
	st, has := m.status[it.Name]
	if !has {
		st = &MonitorStatus{Name: it.Name, Type: it.Type, Target: it.Target}
		m.status[it.Name] = st
	}
	st.Type, st.Target = it.Type, it.Target
	st.Up = ok
	st.Known = true
	st.RTTms = rtt
	st.Code = code
	st.LastTS = now.Format(time.RFC3339)
	if ok {
		m.failCnt[it.Name] = 0
	} else {
		m.failCnt[it.Name]++
	}
	failStreak := m.failCnt[it.Name]
	m.mu.Unlock()

	// 时序库（写库用 reload ctx，取消即放弃写入，不影响状态判定）
	labels := [][2]string{{"monitor", it.Name}, {"type", it.Type}}
	pts := []tsPoint{
		{metric: "nw_monitor_ok", labels: labels, value: map[bool]float64{true: 1, false: 0}[ok], tsMs: now.UnixMilli()},
	}
	if ok {
		pts = append(pts, tsPoint{metric: "nw_monitor_rtt_ms", labels: labels, value: rtt, tsMs: now.UnixMilli()})
	}
	if err := m.tsdb.WritePoints(ctx, pts); err != nil {
		m.log.Printf("tsdb: %v", err)
	}

	// 告警：连续 3 次失败触发 P2，恢复即解除
	fp := KindMonitorDown + ":" + it.Name
	if failStreak >= 3 {
		m.eng.FireMonitor(fp, it, "内网服务连续 "+itoa(failStreak)+" 次探测失败（"+it.Type+" "+it.Target+"）")
	} else if ok && failStreak == 0 {
		m.eng.ResolveMonitor(fp, it, "内网服务恢复可达")
	}
}

// Snapshot 总览 API 用
func (m *MonitorRunner) Snapshot() []MonitorStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MonitorStatus, 0, len(m.status))
	for _, st := range m.status {
		out = append(out, *st)
	}
	return out
}
