package server

import (
	"io"
	"log"
	"testing"
	"time"
)

// mkSeries 构造一条 VM 导出序列。
func mkSeries(probe, class, target string, vals map[int64]float64) ExportSeries {
	s := ExportSeries{Metric: map[string]string{"probe": probe, "class": class, "target": target}}
	for ts, v := range vals {
		s.Timestamps = append(s.Timestamps, ts) // VM export 时间戳为毫秒
		s.Values = append(s.Values, v)
	}
	return s
}

func newBackfillTestEngine() *Engine {
	return &Engine{
		probes: map[string]*probeAgg{},
		log:    log.New(io.Discard, "", 0),
	}
}

func TestApplyRingBackfill(t *testing.T) {
	now := time.Now().Unix()
	e := newBackfillTestEngine()
	known := map[string]bool{"p1": true, "p2": true}
	series := []ExportSeries{
		mkSeries("p1", "public", "223.5.5.5", map[int64]float64{(now - 120) * 1000: 1, (now - 60) * 1000: 1}),
		mkSeries("p1", "public", "114.114.114.114", map[int64]float64{(now - 120) * 1000: 0}),
		mkSeries("p1", "gw", "10.8.15.253", map[int64]float64{(now - 120) * 1000: 1}),      // 网关不进 pingWin
		mkSeries("ghost", "public", "223.5.5.5", map[int64]float64{(now - 120) * 1000: 1}), // 已删探针跳过
	}
	n := e.applyRingBackfill(series, known, time.Unix(now-65*60, 0))
	agg := e.probes["p1"]
	if agg == nil {
		t.Fatal("p1 聚合未创建")
	}
	if n != 3 || len(agg.pingWin) != 3 {
		t.Fatalf("回填样本数：got n=%d win=%d，want 3/3", n, len(agg.pingWin))
	}
	if _, ok := e.probes["ghost"]; ok {
		t.Fatal("已删探针不应创建聚合")
	}
	// 分钟聚合语义与 Ingest 同源：1 分钟 2 样本全成功=绿，1 分钟 1 失败=橙（partial）
	ms := agg.minuteStats(5) // 默认降质窗口
	if len(ms) != 2 {
		t.Fatalf("分钟桶数：got %d，want 2", len(ms))
	}
	byT := map[int64]MinStat{}
	for _, m := range ms {
		byT[m.T] = m
	}
	old := byT[(now-120)/60*60]
	if old.N != 2 || old.F != 1 {
		t.Fatalf("旧分钟：got %+v，want N=2 F=1（2 目标 1 失败）", old)
	}
	recent := byT[(now-60)/60*60]
	if recent.N != 1 || recent.F != 0 {
		t.Fatalf("近分钟：got %+v，want N=1 F=0", recent)
	}
}

func TestApplyRingBackfillDedupAndPrune(t *testing.T) {
	now := time.Now()
	e := newBackfillTestEngine()
	agg := e.aggFor("p1")
	// 在途 Ingest 已有一条同 (target,ts) 样本 + 一条新样本
	agg.pingWin = append(agg.pingWin,
		pingRec{target: "t", ok: true, ts: now.Add(-2 * time.Minute)},
		pingRec{target: "t", ok: true, ts: now.Add(-30 * time.Second)})
	// 回填带同一旧样本（应去重）+ 一条超窗旧样本（应被剪掉）
	series := []ExportSeries{
		mkSeries("p1", "public", "t", map[int64]float64{
			now.Add(-2 * time.Minute).UnixMilli():  1, // 与在途重复
			now.Add(-70 * time.Minute).UnixMilli(): 1, // 超出 65min 窗口
			now.Add(-90 * time.Second).UnixMilli(): 1,
		}),
	}
	n := e.applyRingBackfill(series, map[string]bool{"p1": true}, now.Add(-65*time.Minute))
	if len(agg.pingWin) != 3 || n != 3 {
		t.Fatalf("去重+修剪后样本：got %d/%d，want 3/3（2 在途 + 1 去重回填）", n, len(agg.pingWin))
	}
	// 时间升序
	for i := 1; i < len(agg.pingWin); i++ {
		if agg.pingWin[i].ts.Before(agg.pingWin[i-1].ts) {
			t.Fatal("回填后 pingWin 未按时间升序")
		}
	}
}
