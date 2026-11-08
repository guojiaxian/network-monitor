package server

import (
	"context"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"netwatch/internal/proto"
)

// SpeedSettings 测速计划设置（管理后台可改）
type SpeedSettings struct {
	Enabled       bool     `json:"enabled"`        // 定时测速开关
	Engine        string   `json:"engine"`         // ookla（测速网站，默认）/ http（大文件下载，备用）
	IntervalMin   int      `json:"interval_min"`   // 每条线路测速间隔（分钟）
	TimeoutMin    int      `json:"timeout_min"`    // 单轮任务超时（分钟），超时按放弃处理并重排
	Runs          int      `json:"runs"`           // 每轮连测次数取平均（1~5，默认 3）
	SlowMinMbps   float64  `json:"slow_min_mbps"`  // 网速慢阈值（Mbps）：一轮均值低于它 → 1 分钟后复测，仍低则告警
	MaxConcurrent int      `json:"max_concurrent"` // 同时在途测速任务上限（默认 12 = 全并行）
	HTTPURLs      []string `json:"http_urls"`      // http 引擎下载源池（备用）
}

func DefaultSpeedSettings() SpeedSettings {
	return SpeedSettings{
		Enabled:       true,
		Engine:        "http", // 多连接 CDN 测速（默认）；ookla 的大陆节点 2026-11 已全面退网，仅境外场景备用
		IntervalMin:   60,
		TimeoutMin:    10,
		Runs:          3,
		SlowMinMbps:   10,
		MaxConcurrent: 12,
		HTTPURLs: []string{
			// 多连接引擎按连接数分摊源池 URL 并循环拉取（文件下完重发），文件大小不构成瓶颈；
			// 源池需定期复核有效性（404 会退化为仅剩的可用源）。
			"https://dldir1.qq.com/weixin/Windows/WeChatSetup.exe",                                   // 腾讯 CDN 283MB（2026-11-07 实测）
			"https://mirrors.ustc.edu.cn/ubuntu-releases/24.04/ubuntu-24.04.3-live-server-amd64.iso", // 中科大镜像 ~2.7GB
		},
	}
}

// normalize 修正越界/缺省值（settings 键历史遗留或手改脏数据兜底）。
func (s *SpeedSettings) normalize() {
	if s.IntervalMin <= 0 {
		s.IntervalMin = 60
	}
	if s.TimeoutMin <= 0 {
		s.TimeoutMin = 10
	}
	if s.Engine == "" {
		s.Engine = "http"
	}
	if s.Runs < 1 {
		s.Runs = 3
	} else if s.Runs > 5 {
		s.Runs = 5
	}
	if s.SlowMinMbps <= 0 {
		s.SlowMinMbps = 10
	}
	if s.MaxConcurrent <= 0 {
		s.MaxConcurrent = 12
	}
}

// retestWaitMin 低速复测等待（用户口径固定 1 分钟，不做配置）。
const retestWaitMin = time.Minute

// SpeedVerdict 测速结果判定（低速复测确认机制：低速首测不告警，1 分钟后复测仍低速才告警）。
type SpeedVerdict struct {
	Confirmed bool    // 本轮为复测且仍低速 → 触发"网速慢"告警
	Resolved  bool    // 达标且此前处于复测链路（告警在途）→ 收口
	Pending   bool    // 低速首测 → 已排 1 分钟后复测，暂不告警
	Threshold float64 // 网速慢阈值（Mbps），告警文案用
	FirstAvg  float64 // 首测均值（Confirmed/Resolved 时文案用）
}

// SpeedSched 测速调度器（并行模型）：每探针独立任务，同一时刻最多 MaxConcurrent 个在途。
// 各探针走各自出口线路（防火墙按源 IP 策略路由），测速流量互不抢占，故并行安全；
// 同轮到期探针一起开测（不做错峰），结果或超时后各自重排。
type SpeedSched struct {
	store *Store
	log   *log.Logger

	settingsFn func() SpeedSettings // 测试注入用（nil 时读 settings 表）

	mu      sync.Mutex
	next    map[string]time.Time       // probeID → 下次期望测速时间
	runOnce map[string]bool            // 手工"立即测速"请求（不受周期开关影响）
	tasks   map[string]*speedTaskState // 在途任务表（每探针独立）
	retest  map[string]float64         // 低速链路在途标记：最近一轮低速均值（Mbps）
}

type speedTaskState struct {
	probeID   string
	runAt     time.Time // 任务执行时间（下发 RunAt，防探针重测）
	startedAt time.Time // 任务创建时间（超时回收用）
}

func NewSpeedSched(store *Store) *SpeedSched {
	return &SpeedSched{
		store:   store,
		log:     log.New(os.Stderr, "speed ", log.LstdFlags|log.Lmsgprefix),
		next:    map[string]time.Time{},
		runOnce: map[string]bool{},
		tasks:   map[string]*speedTaskState{},
		retest:  map[string]float64{},
	}
}

func (s *SpeedSched) settings(ctx context.Context) SpeedSettings {
	var st SpeedSettings
	if s.settingsFn != nil {
		st = s.settingsFn()
	} else if err := s.store.GetSetting(ctx, "speed_settings", &st); err != nil {
		st = DefaultSpeedSettings()
	}
	st.normalize()
	return st
}

func (s *SpeedSched) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *SpeedSched) tick(ctx context.Context) {
	st := s.settings(ctx)
	meta, err := s.store.ProbeLineMeta(ctx)
	if err != nil {
		return
	}
	s.tickWith(st, meta)
}

func (s *SpeedSched) tickWith(st SpeedSettings, meta map[string]LineMeta) {
	bound := map[string]bool{}
	for pid, lm := range meta {
		if lm.Bound {
			bound[pid] = true
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()

	// 已解绑/删除的探针：清理全部状态（任务作废）
	for pid := range s.next {
		if !bound[pid] {
			s.cleanupLocked(pid)
		}
	}
	for pid := range s.tasks {
		if !bound[pid] {
			s.log.Printf("task voided: probe=%s unbound/deleted", pid)
			s.cleanupLocked(pid)
		}
	}
	// 在途任务超时（探针离线/测速无结果）：放弃并重排
	for pid, t := range s.tasks {
		if now.Sub(t.startedAt) > time.Duration(st.TimeoutMin)*time.Minute {
			s.log.Printf("task timeout: probe=%s run_at=%s", pid, t.runAt.Format("15:04:05"))
			s.cleanupLocked(pid)
			if st.Enabled {
				s.next[pid] = now
			}
		}
	}

	if st.Enabled {
		// 首次出现的绑定探针（含 server 重启后 next 清空）：立即加入本轮并行开测
		for pid := range bound {
			if _, has := s.next[pid]; !has {
				if _, in := s.tasks[pid]; !in {
					s.next[pid] = now
				}
			}
		}
	}
	s.dispatchLocked(st, bound, now)
}

// dispatchLocked 把到期的探针建为在途任务（受并发上限约束，最早到期优先）。
// 手工"立即测速"优先且不受周期开关影响（真实踩坑：停用状态下手工触发曾静默失效）。
func (s *SpeedSched) dispatchLocked(st SpeedSettings, bound map[string]bool, now time.Time) {
	limit := st.MaxConcurrent
	if limit <= 0 {
		limit = 12
	}
	type cand struct {
		pid    string
		at     time.Time
		manual bool
	}
	var cands []cand
	for pid := range bound {
		if _, in := s.tasks[pid]; in {
			delete(s.runOnce, pid) // 已有任务在途：忽略重复手工触发
			continue
		}
		if s.runOnce[pid] {
			cands = append(cands, cand{pid: pid, at: now, manual: true})
			continue
		}
		if t, has := s.next[pid]; has && !t.After(now) {
			cands = append(cands, cand{pid: pid, at: t})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].manual != cands[j].manual {
			return cands[i].manual // 手工优先
		}
		return cands[i].at.Before(cands[j].at)
	})
	for _, c := range cands {
		if len(s.tasks) >= limit {
			break
		}
		delete(s.runOnce, c.pid)
		s.tasks[c.pid] = &speedTaskState{probeID: c.pid, runAt: now, startedAt: now}
		if !c.manual {
			delete(s.next, c.pid) // next 由 OnResult 按结果重排
		}
		suffix := ""
		if c.manual {
			suffix = " (manual)"
		}
		s.log.Printf("scheduled%s: probe=%s now (tasks=%d/%d)", suffix, c.pid, len(s.tasks), limit)
	}
}

// cleanupLocked 清除探针的调度/任务/复测全部状态（需持锁）。
func (s *SpeedSched) cleanupLocked(pid string) {
	delete(s.next, pid)
	delete(s.runOnce, pid)
	delete(s.tasks, pid)
	delete(s.retest, pid)
}

// TaskFor 供配置下发接口查询：该探针当前是否有在途测速任务。
func (s *SpeedSched) TaskFor(probeID string) *proto.SpeedTask {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st := s.settings(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[probeID]
	if !ok {
		return nil
	}
	// 不检查 Enabled：手工"立即测速"在周期调度停用时也应下发
	return &proto.SpeedTask{
		Engine:   st.Engine,
		RunAt:    t.runAt.Unix(),
		Times:    st.Runs,
		HTTPURLs: st.HTTPURLs,
	}
}

// OnResult 测速结果回收 + 低速复测判定（成功或失败均算任务完成）。
// 状态机：达标 → 收口（若低速链路在途）+ 排下一周期；低速首测 → 排 1 分钟后复测（不告警）；
// 低速且复测链路在途 → 确认告警（持续低速每轮刷新，fireLocked 冷却防重复）。
// 测速失败不改变复测链路状态（告警保持），仅按正常间隔重排。
func (s *SpeedSched) OnResult(probeID string, downMbps float64, ok bool) SpeedVerdict {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st := s.settings(ctx)
	return s.onResultWith(st, probeID, downMbps, ok)
}

func (s *SpeedSched) onResultWith(st SpeedSettings, probeID string, downMbps float64, ok bool) SpeedVerdict {
	interval := time.Duration(st.IntervalMin) * time.Minute
	next := time.Now().Add(interval)

	v := SpeedVerdict{Threshold: st.SlowMinMbps}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, in := s.tasks[probeID]; !in {
		return v // 非本调度器在途任务的上报（如重启后残留），忽略
	}
	delete(s.tasks, probeID)

	first, wasRetest := s.retest[probeID]
	switch {
	case !ok:
		if st.Enabled {
			s.next[probeID] = next
		}
	case downMbps >= st.SlowMinMbps:
		if st.Enabled {
			s.next[probeID] = next
		}
		if wasRetest {
			delete(s.retest, probeID)
			v.Resolved = true // 低速链路收口（复测达标或此后任一轮达标）
			v.FirstAvg = first
		}
	case wasRetest:
		// 复测（或后续轮）仍低速 → 确认/持续告警；刷新最近低速值供文案使用
		s.retest[probeID] = downMbps
		if st.Enabled {
			s.next[probeID] = next
		}
		v.Confirmed = true
		v.FirstAvg = first
	default:
		// 低速首测 → 排 1 分钟后复测确认
		if st.Enabled {
			s.retest[probeID] = downMbps
			s.next[probeID] = time.Now().Add(retestWaitMin)
			v.Pending = true
			s.log.Printf("low speed: probe=%s avg=%.1fMbps < %.0f → retest in 1min",
				probeID, downMbps, st.SlowMinMbps)
		}
	}
	return v
}

// RunNow 手工触发：指定探针尽快测速（"立即测速"/立即刷新联动；周期停用时依然生效）。
func (s *SpeedSched) RunNow(probeID string) {
	s.mu.Lock()
	s.next[probeID] = time.Now()
	s.runOnce[probeID] = true
	s.mu.Unlock()
}
