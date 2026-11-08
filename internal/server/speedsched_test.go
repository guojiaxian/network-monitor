package server

import (
	"testing"
	"time"
)

func testSpeedSettings() SpeedSettings {
	st := SpeedSettings{Enabled: true, Engine: "ookla", IntervalMin: 60, TimeoutMin: 10,
		Runs: 3, SlowMinMbps: 10, MaxConcurrent: 12}
	st.normalize()
	return st
}

func newTestSched(st SpeedSettings) *SpeedSched {
	s := NewSpeedSched(nil)
	s.settingsFn = func() SpeedSettings { return st }
	return s
}

// TestSpeedSettingsNormalize 缺省/越界值兜底
func TestSpeedSettingsNormalize(t *testing.T) {
	st := SpeedSettings{Enabled: true}
	st.normalize()
	if st.IntervalMin != 60 || st.TimeoutMin != 10 || st.Engine != "http" ||
		st.Runs != 3 || st.SlowMinMbps != 10 || st.MaxConcurrent != 12 {
		t.Fatalf("缺省值兜底不符合预期: %+v", st)
	}
	st = SpeedSettings{Runs: 9, SlowMinMbps: -1, MaxConcurrent: 0, IntervalMin: -5, TimeoutMin: -5}
	st.normalize()
	if st.Runs != 5 || st.SlowMinMbps != 10 || st.MaxConcurrent != 12 || st.IntervalMin != 60 || st.TimeoutMin != 10 {
		t.Fatalf("越界值钳位不符合预期: %+v", st)
	}
}

// TestSpeedSchedParallelDispatch 到期探针并行建任务；并发上限生效
func TestSpeedSchedParallelDispatch(t *testing.T) {
	st := testSpeedSettings()
	s := newTestSched(st)
	meta := map[string]LineMeta{
		"p1": {Bound: true}, "p2": {Bound: true}, "p3": {Bound: true},
	}
	s.tickWith(st, meta)
	if len(s.tasks) != 3 {
		t.Fatalf("3 个绑定探针应全部并行建任务，实际=%d", len(s.tasks))
	}
	// 手工触发不排在途中的重复任务
	s.RunNow("p1")
	s.tickWith(st, meta)
	if len(s.tasks) != 3 {
		t.Fatalf("在途探针不应重复建任务，实际=%d", len(s.tasks))
	}
	// 并发上限：MaxConcurrent=2 时只建 2 个
	st2 := testSpeedSettings()
	st2.MaxConcurrent = 2
	s2 := newTestSched(st2)
	s2.tickWith(st2, meta)
	if len(s2.tasks) != 2 {
		t.Fatalf("并发上限 2 应只建 2 个任务，实际=%d", len(s2.tasks))
	}
}

// TestSpeedSchedVerdictFlow 低速复测确认状态机全流程：
// 首测低速 Pending → 复测低速 Confirmed → 持续低速持续 Confirmed → 达标 Resolved
func TestSpeedSchedVerdictFlow(t *testing.T) {
	st := testSpeedSettings()
	s := newTestSched(st)
	meta := map[string]LineMeta{"p1": {Bound: true}}
	s.tickWith(st, meta)

	// 首测低速 → Pending（不告警），复测排程 ≈1 分钟后
	v := s.onResultWith(st, "p1", 5, true)
	if !v.Pending || v.Confirmed || v.Resolved {
		t.Fatalf("低速首测应为 Pending: %+v", v)
	}
	if got := time.Until(s.next["p1"]); got < 30*time.Second || got > 90*time.Second {
		t.Fatalf("复测应排在约 1 分钟后，实际=%v", got)
	}
	s.next["p1"] = time.Now().Add(-time.Second) // 模拟 1 分钟复测窗口到期
	s.tickWith(st, meta)                        // 复测到期建任务
	if _, ok := s.tasks["p1"]; !ok {
		t.Fatal("复测任务应已建立")
	}
	// 复测仍低速 → Confirmed（触发告警）
	v = s.onResultWith(st, "p1", 6, true)
	if !v.Confirmed || v.FirstAvg != 5 {
		t.Fatalf("复测低速应为 Confirmed 且携带首测均值: %+v", v)
	}
	// 持续低速：仍 Confirmed（live 冷却防重复告警由 alert 层处理）
	s.next["p1"] = time.Now().Add(-time.Second) // 模拟下一周期（60 分钟）到期
	s.tickWith(st, meta)
	v = s.onResultWith(st, "p1", 7, true)
	if !v.Confirmed {
		t.Fatalf("持续低速应保持 Confirmed: %+v", v)
	}
	// 达标 → Resolved（告警收口）
	s.next["p1"] = time.Now().Add(-time.Second)
	s.tickWith(st, meta)
	v = s.onResultWith(st, "p1", 900, true)
	if !v.Resolved || v.FirstAvg != 7 {
		t.Fatalf("达标应 Resolved 并携带上一轮低速值: %+v", v)
	}
	if _, was := s.retest["p1"]; was {
		t.Fatal("收口后复测标记应清除")
	}
}

// TestSpeedSchedOKFirst 达标首测：直接排下一周期，无任何判定动作
func TestSpeedSchedOKFirst(t *testing.T) {
	st := testSpeedSettings()
	s := newTestSched(st)
	meta := map[string]LineMeta{"p1": {Bound: true}}
	s.tickWith(st, meta)
	v := s.onResultWith(st, "p1", 800, true)
	if v.Pending || v.Confirmed || v.Resolved {
		t.Fatalf("正常达标不应产生判定: %+v", v)
	}
	if got := time.Until(s.next["p1"]); got < 55*time.Minute || got > 61*time.Minute {
		t.Fatalf("达标应排约 60 分钟后，实际=%v", got)
	}
}

// TestSpeedSchedFailNoRetestLoop 测速失败：正常重排、不进复测循环、低速链路状态保持
func TestSpeedSchedFailNoRetestLoop(t *testing.T) {
	st := testSpeedSettings()
	s := newTestSched(st)
	meta := map[string]LineMeta{"p1": {Bound: true}}
	s.tickWith(st, meta)
	s.onResultWith(st, "p1", 5, true) // 低速首测 → 复测链路建立
	s.tickWith(st, meta)
	v := s.onResultWith(st, "p1", 0, false) // 复测轮失败
	if v.Confirmed || v.Pending {
		t.Fatalf("失败不应触发判定: %+v", v)
	}
	if _, was := s.retest["p1"]; !was {
		t.Fatal("失败不应清除低速链路标记（告警保持，避免失败逃逸）")
	}
	// 恢复后下一轮达标 → 收口（失败轮按正常间隔重排，模拟时间推进到下一轮到期）
	s.next["p1"] = time.Now().Add(-time.Second)
	s.tickWith(st, meta)
	if v := s.onResultWith(st, "p1", 500, true); !v.Resolved {
		t.Fatalf("失败恢复后达标应收口: %+v", v)
	}
}

// TestSpeedSchedTaskTimeout 在途任务超时回收并重排
func TestSpeedSchedTaskTimeout(t *testing.T) {
	st := testSpeedSettings()
	s := newTestSched(st)
	meta := map[string]LineMeta{"p1": {Bound: true}}
	s.tickWith(st, meta)
	if len(s.tasks) != 1 {
		t.Fatal("任务应已建立")
	}
	s.tasks["p1"].startedAt = time.Now().Add(-11 * time.Minute) // 超过 timeout_min=10
	old := s.tasks["p1"].runAt
	s.tickWith(st, meta)
	nt, ok := s.tasks["p1"]
	if !ok {
		t.Fatal("超时应回收并按重排重建任务")
	}
	if !nt.runAt.After(old) {
		t.Fatal("重建的应为新任务（runAt 更新）")
	}
}

// TestSpeedSchedUnboundCleanup 解绑探针状态全清
func TestSpeedSchedUnboundCleanup(t *testing.T) {
	st := testSpeedSettings()
	s := newTestSched(st)
	meta := map[string]LineMeta{"p1": {Bound: true}, "p2": {Bound: true}}
	s.tickWith(st, meta)
	s.onResultWith(st, "p1", 5, true) // p1 进入复测链路
	meta["p2"] = LineMeta{}           // p2 解绑
	s.tickWith(st, meta)
	if _, has := s.tasks["p2"]; has {
		t.Fatal("解绑探针任务应作废")
	}
	if _, has := s.next["p2"]; has {
		t.Fatal("解绑探针 next 应清除")
	}
	if _, was := s.retest["p1"]; !was {
		t.Fatal("绑定探针 p1 状态不应受影响")
	}
}
