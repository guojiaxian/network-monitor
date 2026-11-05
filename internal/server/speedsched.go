package server

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"netwatch/internal/proto"
)

// SpeedSettings 测速计划设置（管理后台可改）
type SpeedSettings struct {
	Enabled     bool     `json:"enabled"`
	Engine      string   `json:"engine"`       // ookla / http
	IntervalMin int      `json:"interval_min"` // 每条线路测速间隔（分钟）
	TimeoutMin  int      `json:"timeout_min"`  // 单次任务超时（分钟），超时按放弃处理并重排
	HTTPURLs    []string `json:"http_urls"`    // http 引擎下载源池
}

func DefaultSpeedSettings() SpeedSettings {
	return SpeedSettings{
		Enabled:     false,
		Engine:      "ookla",
		IntervalMin: 30,
		TimeoutMin:  5,
		HTTPURLs: []string{
			"https://dldir1.qq.com/weixin/Windows/WeChatSetup.exe",
			// 旧默认源 QQ9.7.17 路径已 404（2026-11-05 现场实测）；换为同 CDN 的
			// http 入口（302 跳 https，agent 客户端自动跟随）。URL 池需定期复核有效性。
			"http://dldir1.qq.com/weixin/Windows/WeChatSetup.exe",
		},
	}
}

// SpeedSched 测速全局串行调度器：
// 同一时刻全网只有一个探针在测速（避免测速互相干扰、避免占满测速节点），
// 各线路按 interval 错峰轮转；结果（或超时）后重新排队。
type SpeedSched struct {
	store *Store
	log   *log.Logger

	mu      sync.Mutex
	next    map[string]time.Time // probeID → 下次期望测速时间
	runOnce map[string]bool      // 手工"立即测速"请求（不受周期开关影响）
	active  *speedActive
}

type speedActive struct {
	probeID string
	runAt   time.Time
}

func NewSpeedSched(store *Store) *SpeedSched {
	return &SpeedSched{
		store:   store,
		log:     log.New(os.Stderr, "speed ", log.LstdFlags|log.Lmsgprefix),
		next:    map[string]time.Time{},
		runOnce: map[string]bool{},
	}
}

func (s *SpeedSched) settings(ctx context.Context) SpeedSettings {
	var st SpeedSettings
	if err := s.store.GetSetting(ctx, "speed_settings", &st); err != nil {
		return DefaultSpeedSettings()
	}
	if st.IntervalMin <= 0 {
		st.IntervalMin = 30
	}
	if st.TimeoutMin <= 0 {
		st.TimeoutMin = 10
	}
	if st.Engine == "" {
		st.Engine = "ookla"
	}
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
	var bound []string
	for pid, lm := range meta {
		if lm.Bound {
			bound = append(bound, pid)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	// 已解绑的探针不再调度（含手工请求）
	keep := map[string]bool{}
	for _, pid := range bound {
		keep[pid] = true
	}
	for pid := range s.next {
		if !keep[pid] {
			delete(s.next, pid)
			delete(s.runOnce, pid)
		}
	}
	// 活动任务的探针已被删除/解绑：任务立即作废
	if s.active != nil && !keep[s.active.probeID] {
		s.log.Printf("task voided: probe=%s unbound/deleted", s.active.probeID)
		s.active = nil
	}

	// 活动任务超时（探针离线/测速失败无结果）：放弃（周期模式重排，停用模式直接丢弃）
	if s.active != nil && now.Sub(s.active.runAt) > time.Duration(st.TimeoutMin)*time.Minute {
		s.log.Printf("task timeout: probe=%s run_at=%s",
			s.active.probeID, s.active.runAt.Format("15:04:05"))
		if st.Enabled {
			s.next[s.active.probeID] = now
		}
		s.active = nil
	}

	// 手工"立即测速"优先：周期调度停用时"立即测速"按钮仍应生效（真实踩坑：
	// 原实现在停用状态下静默忽略手工触发，按钮无任何效果）
	if s.active == nil && len(s.runOnce) > 0 {
		for _, pid := range bound {
			if s.runOnce[pid] {
				delete(s.runOnce, pid)
				s.active = &speedActive{probeID: pid, runAt: now}
				s.log.Printf("scheduled (manual): probe=%s now", pid)
				break
			}
		}
	}

	if !st.Enabled {
		return // 周期调度停用：在途手工任务照常下发/回收，仅不再自动排队
	}

	// 新绑定的探针：在间隔窗口内均匀错峰
	var fresh []string
	inNext := map[string]bool{}
	for pid := range s.next {
		inNext[pid] = true
	}
	for _, pid := range bound {
		if !inNext[pid] {
			fresh = append(fresh, pid)
		}
	}
	if len(fresh) > 0 {
		step := time.Duration(st.IntervalMin) * time.Minute / time.Duration(len(bound)+1)
		for i, pid := range fresh {
			s.next[pid] = now.Add(step * time.Duration(i+1))
		}
	}

	// 空闲时调度最早到期的探针
	if s.active == nil {
		var pick string
		var earliest time.Time
		for _, pid := range bound {
			t, ok := s.next[pid]
			if !ok || t.IsZero() {
				continue
			}
			if t.After(now) {
				continue
			}
			if pick == "" || t.Before(earliest) {
				pick, earliest = pid, t
			}
		}
		if pick != "" {
			s.active = &speedActive{probeID: pick, runAt: now}
			s.log.Printf("scheduled: probe=%s now (next round in %dmin)", pick, st.IntervalMin)
		}
	}
}

// TaskFor 供配置下发接口查询：该探针当前是否有测速任务。
func (s *SpeedSched) TaskFor(probeID string) *proto.SpeedTask {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st := s.settings(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	// 不检查 Enabled：手工"立即测速"在周期调度停用时也应下发
	if s.active == nil || s.active.probeID != probeID {
		return nil
	}
	return &proto.SpeedTask{
		Engine:   st.Engine,
		RunAt:    s.active.runAt.Unix(),
		HTTPURLs: st.HTTPURLs,
	}
}

// OnResult 测速结果回收（成功或失败均算任务完成），按间隔排下一轮。
func (s *SpeedSched) OnResult(probeID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	interval := time.Duration(s.settings(ctx).IntervalMin) * time.Minute

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active.probeID == probeID {
		s.next[probeID] = time.Now().Add(interval)
		s.active = nil
	}
}

// RunNow 手工触发：指定探针尽快测速（线路页"立即测速"；周期调度停用时依然生效）。
func (s *SpeedSched) RunNow(probeID string) {
	s.mu.Lock()
	s.next[probeID] = time.Now()
	s.runOnce[probeID] = true
	s.mu.Unlock()
}
