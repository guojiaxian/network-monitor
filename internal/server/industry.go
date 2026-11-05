package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"netwatch/internal/agent"
)

// 行业网监测：中心服务器对管理员配置的行业网（10.5 网段等）IP **仅做 ICMP ping**，
// 不做 TCP/HTTP 等任何其他探测（避免被行业网封禁）。复用楼层探针的 ping 原语。
// 引擎结构照搬 monitor.go：配置指纹才换代（坑 17）、探测独立超时（坑 11）、
// 立即首探、连续 3 次失败触发 P2、恢复自动解除、删除目标自动收口。

// IndustryItem 单个行业网监测目标（仅 ping，target 为 IP）
type IndustryItem struct {
	Name      string `json:"name"`
	Target    string `json:"target"` // IP 地址
	IntervalS int    `json:"interval_s"`
}

type IndustrySettings struct {
	Enabled bool           `json:"enabled"`
	Items   []IndustryItem `json:"items"`
}

func DefaultIndustrySettings() IndustrySettings { return IndustrySettings{} }

// IndustryStatus 单目标实时状态（总览 API）
type IndustryStatus struct {
	Name   string  `json:"name"`
	Target string  `json:"target"`
	Up     bool    `json:"up"`
	Known  bool    `json:"known"` // 是否已有探测结果
	RTTms  float64 `json:"rtt_ms"`
	LastTS string  `json:"last_ts"`
}

// IndustryRunner 行业网监测器
type IndustryRunner struct {
	store   *Store
	tsdb    *TSDB
	eng     *Engine
	log     *log.Logger

	mu      sync.Mutex
	status  map[string]*IndustryStatus
	failCnt map[string]int
	version int // 配置代次（热更新用）
}

func NewIndustryRunner(store *Store, tsdb *TSDB, eng *Engine) *IndustryRunner {
	return &IndustryRunner{store: store, tsdb: tsdb, eng: eng,
		log:     log.New(os.Stderr, "industry ", log.LstdFlags|log.Lmsgprefix),
		status:  map[string]*IndustryStatus{},
		failCnt: map[string]int{}}
}

func (r *IndustryRunner) Start(ctx context.Context) {
	go r.reloadLoop(ctx)
}

// reloadLoop 每 30s 重读设置，配置真正变化时才换代重启全部目标循环（启动时立即加载），
// 并清理已删除目标的运行状态与进行中告警。
// 注意：不能无条件换代——否则在途探测的时序写入会被 context canceled 打断
// （同 monitor.go 坑 17 的教训）。
func (r *IndustryRunner) reloadLoop(ctx context.Context) {
	var cancel context.CancelFunc
	var lastFP string
	run := func() {
		st := r.settings(ctx)
		// 清理已删除目标的遗留状态（每次都执行，代价极小）
		r.mu.Lock()
		live := map[string]bool{}
		for _, it := range st.Items {
			live[it.Name] = true
		}
		for name := range r.status {
			if !live[name] {
				delete(r.status, name)
				delete(r.failCnt, name)
			}
		}
		r.mu.Unlock()
		// 已删除目标的进行中告警交由 Engine 清理
		r.eng.CleanIndustries(live)

		if !st.Enabled {
			if cancel != nil {
				cancel()
				cancel = nil
			}
			lastFP = ""
			return
		}
		b, _ := json.Marshal(st)
		if string(b) == lastFP && cancel != nil {
			return // 配置未变化：保持现有循环，不打断在途探测/写入
		}
		lastFP = string(b)
		if cancel != nil {
			cancel()
		}
		var cctx context.Context
		cctx, cancel = context.WithCancel(ctx)
		r.mu.Lock()
		r.version++
		v := r.version
		r.mu.Unlock()
		go r.runAll(cctx, st, v)
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

func (r *IndustryRunner) settings(ctx context.Context) IndustrySettings {
	var st IndustrySettings
	if err := r.store.GetSetting(ctx, "industry_monitors", &st); err != nil {
		return DefaultIndustrySettings()
	}
	for i := range st.Items {
		if st.Items[i].IntervalS < 10 {
			st.Items[i].IntervalS = 30
		}
	}
	return st
}

func (r *IndustryRunner) runAll(ctx context.Context, st IndustrySettings, version int) {
	for _, it := range st.Items {
		go r.runOne(ctx, it, version)
	}
}

func (r *IndustryRunner) runOne(ctx context.Context, it IndustryItem, version int) {
	t := time.NewTicker(time.Duration(it.IntervalS) * time.Second)
	defer t.Stop()
	timeout := 5 * time.Second
	// 立即执行首次探测：否则首个状态要等一个完整间隔才出现
	r.mu.Lock()
	cur := r.version
	r.mu.Unlock()
	if cur == version {
		r.probe(ctx, it, timeout)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// 配置已换代则退出（探测本身用独立超时，见 probe）
			r.mu.Lock()
			cur := r.version
			r.mu.Unlock()
			if cur != version {
				return
			}
			r.probe(ctx, it, timeout)
		}
	}
}

// probe 执行一次 ping：更新状态、写时序库、驱动告警。
// 网络探测使用独立超时（不从 reload ctx 派生）——reload 取消旧代 ctx 时
// 在途探测会以 context canceled 收尾，若把取消误记为探测失败会产生假 down。
// 仅 ICMP：无论配置如何都不发起 TCP/HTTP 等其他探测。
func (r *IndustryRunner) probe(ctx context.Context, it IndustryItem, timeout time.Duration) {
	pctx, pcancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer pcancel()
	rtt, ok := agent.ProbePing(pctx, it.Target, timeout)
	now := time.Now()

	r.mu.Lock()
	st, has := r.status[it.Name]
	if !has {
		st = &IndustryStatus{Name: it.Name, Target: it.Target}
		r.status[it.Name] = st
	}
	st.Target = it.Target
	st.Up = ok
	st.Known = true
	st.RTTms = rtt
	st.LastTS = now.Format(time.RFC3339)
	if ok {
		r.failCnt[it.Name] = 0
	} else {
		r.failCnt[it.Name]++
	}
	failStreak := r.failCnt[it.Name]
	r.mu.Unlock()

	// 时序库（写库用 reload ctx，取消即放弃写入，不影响状态判定）
	labels := [][2]string{{"industry", it.Name}}
	pts := []tsPoint{
		{metric: "nw_industry_ok", labels: labels, value: map[bool]float64{true: 1, false: 0}[ok], tsMs: now.UnixMilli()},
	}
	if ok {
		pts = append(pts, tsPoint{metric: "nw_industry_rtt_ms", labels: labels, value: rtt, tsMs: now.UnixMilli()})
	}
	if err := r.tsdb.WritePoints(ctx, pts); err != nil {
		r.log.Printf("tsdb: %v", err)
	}

	// 告警：连续 3 次失败触发 P2，恢复即解除
	fp := KindIndustryDown + ":" + it.Name
	if failStreak >= 3 {
		r.eng.FireIndustry(fp, it, "行业网目标连续 "+itoa(failStreak)+" 次 ping 失败（ICMP "+it.Target+"）")
	} else if ok && failStreak == 0 {
		r.eng.ResolveIndustry(fp, it, "行业网目标恢复可达")
	}
}

// Snapshot 总览 API 用
func (r *IndustryRunner) Snapshot() []IndustryStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]IndustryStatus, 0, len(r.status))
	for _, st := range r.status {
		out = append(out, *st)
	}
	return out
}

// ValidateIndustrySettings 保存前的校验：名称/目标非空、去重、目标必须是合法 IP。
// 仅 ping，不需要端口/URL；不硬性限定网段（由管理员确保目标是行业网地址）。
func ValidateIndustrySettings(st *IndustrySettings) error {
	seen := map[string]bool{}
	out := st.Items[:0]
	for _, it := range st.Items {
		if it.Name == "" || it.Target == "" {
			return errIndustry("名称与 IP 不能为空")
		}
		if seen[it.Name] {
			return errIndustry("监测名称重复：" + it.Name)
		}
		if net.ParseIP(it.Target) == nil {
			return errIndustry("目标不是合法 IP 地址：" + it.Target)
		}
		seen[it.Name] = true
		if it.IntervalS < 10 {
			it.IntervalS = 30
		}
		out = append(out, it)
	}
	st.Items = out
	return nil
}

type industryError string

func (e industryError) Error() string { return string(e) }

func errIndustry(msg string) error { return industryError(msg) }
