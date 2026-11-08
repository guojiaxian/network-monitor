package server

import (
	"io"
	"log"
	"testing"
	"time"

	"netwatch/internal/proto"
)

// 多目标丢包统计的下中位数抗单点性：
// 3 个目标中 1 个全失败 → 中位数 0（不误报降质）
func TestLossStatsSingleBadTarget(t *testing.T) {
	agg := &probeAgg{}
	now := time.Now()
	for i := 0; i < 20; i++ {
		ts := now.Add(-time.Duration(20-i) * time.Second)
		agg.pingWin = append(agg.pingWin,
			pingRec{"a", true, ts}, pingRec{"b", true, ts}, pingRec{"c", false, ts})
	}
	ok45, med, enough := agg.lossStats(now, 5*time.Minute)
	if !ok45 {
		t.Fatal("45s 内应存在成功样本")
	}
	if !enough {
		t.Fatal("样本量应足够")
	}
	if med != 0 {
		t.Fatalf("单个坏目标不应抬高中位数，got %v", med)
	}
}

// 2/3 目标各 50% 丢包 → 下中位数 50（应触发降质）
func TestLossStatsMajorityDegraded(t *testing.T) {
	agg := &probeAgg{}
	now := time.Now()
	for i := 0; i < 20; i++ {
		ts := now.Add(-time.Duration(20-i) * time.Second)
		ok := i%2 == 0
		agg.pingWin = append(agg.pingWin,
			pingRec{"a", ok, ts}, pingRec{"b", ok, ts}, pingRec{"c", true, ts})
	}
	_, med, _ := agg.lossStats(now, 5*time.Minute)
	if med <= 10 {
		t.Fatalf("多数目标劣化应抬高中位数，got %v", med)
	}
}

// 全部目标持续失败 → 无 45s 内成功样本（应判线路中断而非降质）
func TestLossStatsAllDown(t *testing.T) {
	agg := &probeAgg{}
	now := time.Now()
	for i := 0; i < 20; i++ {
		ts := now.Add(-time.Duration(20-i) * time.Second)
		agg.pingWin = append(agg.pingWin,
			pingRec{"a", false, ts}, pingRec{"b", false, ts}, pingRec{"c", false, ts})
	}
	ok45, med, enough := agg.lossStats(now, 5*time.Minute)
	if ok45 {
		t.Fatal("全失败时 45s 内不应有成功样本")
	}
	if !enough || med != 100 {
		t.Fatalf("全失败中位数应为 100，got %v (enough=%v)", med, enough)
	}
}

// 窗口内每目标样本量不足 10 → 统计不生效（防止刚上线误判）
func TestLossStatsNotEnoughSamples(t *testing.T) {
	agg := &probeAgg{}
	now := time.Now()
	for i := 0; i < 5; i++ {
		ts := now.Add(-time.Duration(5-i) * time.Second)
		agg.pingWin = append(agg.pingWin, pingRec{"a", false, ts})
	}
	_, _, enough := agg.lossStats(now, 5*time.Minute)
	if enough {
		t.Fatal("样本量不足时 enough 应为 false")
	}
}

// 过期窗口样本不参与统计
func TestLossStatsWindowPrune(t *testing.T) {
	agg := &probeAgg{}
	now := time.Now()
	for i := 0; i < 30; i++ {
		ts := now.Add(-10 * time.Minute) // 全部超出窗口
		agg.pingWin = append(agg.pingWin, pingRec{"a", false, ts})
	}
	_, _, enough := agg.lossStats(now, 5*time.Minute)
	if enough {
		t.Fatal("窗口外样本不应参与统计")
	}
}

// 样本量门槛必须对所有目标同时成立（真实踩坑回归）：
// 探针刚启动时各目标样本数因抖动不同步，坏目标先凑满 10 个样本而好目标还差 1 个，
// 中位数退化为坏目标自身的 100% → 误报降质。修复后门槛不一致时应判"样本不足"。
func TestLossStatsAsymmetricSampleCounts(t *testing.T) {
	agg := &probeAgg{}
	now := time.Now()
	// 坏目标 a 已有 12 个全失败样本；好目标 b/c 各只有 9 个成功样本
	for i := 0; i < 12; i++ {
		ts := now.Add(-time.Duration(12-i) * time.Second)
		agg.pingWin = append(agg.pingWin, pingRec{"a", false, ts})
	}
	for i := 0; i < 9; i++ {
		ts := now.Add(-time.Duration(9-i) * time.Second)
		agg.pingWin = append(agg.pingWin, pingRec{"b", true, ts}, pingRec{"c", true, ts})
	}
	_, med, enough := agg.lossStats(now, 5*time.Minute)
	if enough {
		t.Fatalf("各目标样本量不一致时不应参与统计（误报中位数 %v）", med)
	}
	// 好目标补足到 10 个后：[0,0,100] 中位数 0，单坏目标不误报
	ts := now
	agg.pingWin = append(agg.pingWin, pingRec{"b", true, ts}, pingRec{"c", true, ts})
	_, med, enough = agg.lossStats(now, 5*time.Minute)
	if !enough {
		t.Fatal("全部目标样本量足够后应参与统计")
	}
	if med != 0 {
		t.Fatalf("单个坏目标不应抬高中位数，got %v", med)
	}
}

func TestReportCSV(t *testing.T) {
	v := func(f float64) *float64 { return &f }
	d := &reportData{Kind: "day", Start: "2026-10-25 00:00", End: "2026-10-26 00:00",
		Lines: []reportLine{
			{Name: "联通1号线", Floor: "1楼", AvailPct: v(99.99), AvgRTTms: v(15.2), AvgLossPct: v(0.5), SpeedMax: v(940), AlertCount: 1},
			{Name: "联通2号线", Floor: "2楼"}, // 无数据
		}}
	csv := string(ReportCSV(d))
	if len(csv) < 3 || csv[0] != 0xEF || csv[1] != 0xBB || csv[2] != 0xBF {
		t.Fatal("CSV 应带 UTF-8 BOM")
	}
	want := "联通1号线,1楼,99.99,15.2,0.50,940,,1\n"
	if !contains(csv, want) {
		t.Fatalf("CSV 行内容不符:\n%s", csv)
	}
	if !contains(csv, "联通2号线,2楼,,,,,,0\n") {
		t.Fatalf("无数据行应输出空字段:\n%s", csv)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || index(s, sub) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// 线路级交叉验证投票判定
func TestDecideLine(t *testing.T) {
	if d, c := decideLine(2, 0); !d || c {
		t.Fatal("全部证人报中断应确认 P1")
	}
	if d, c := decideLine(1, 1); d || !c {
		t.Fatal("部分报中断应产生冲突告警而非 P1")
	}
	if d, c := decideLine(0, 2); d || c {
		t.Fatal("无中断不应触发任何告警")
	}
	if d, c := decideLine(0, 0); d || c {
		t.Fatal("无有效证人不应触发任何告警")
	}
}

// TestTrackTypeFail 类型级失败跟踪：失败记起始、成功清除、首次失败时间不被覆盖
func TestTrackTypeFail(t *testing.T) {
	agg := &probeAgg{}
	t0 := time.Now().Add(-2 * time.Minute)
	trackTypeFail(agg, "dns", "223.5.5.5", false, t0)
	trackTypeFail(agg, "dns", "223.5.5.5", false, time.Now()) // 连续失败不刷新起始
	if got := agg.typeFailTargets["dns"]["223.5.5.5"]; !got.Equal(t0) {
		t.Fatalf("连续失败应保留首次起始时间: got=%v want=%v", got, t0)
	}
	trackTypeFail(agg, "dns", "223.5.5.5", true, time.Now())
	if len(agg.typeFailTargets["dns"]) != 0 {
		t.Fatalf("成功样本应清除失败记录: %+v", agg.typeFailTargets["dns"])
	}
}

// TestTypeFailHit 全部目标连续失败判定：覆盖/时长/多目标取最早起始
func TestTypeFailHit(t *testing.T) {
	now := time.Now()
	targets := []string{"a:1", "b:2"}
	// 部分目标失败：不触发
	m := map[string]time.Time{"a:1": now.Add(-5 * time.Minute)}
	if hit, _ := typeFailHit(m, targets, now, time.Minute); hit {
		t.Fatal("部分目标失败不应触发")
	}
	// 全部失败但最早起始未满窗：不触发
	m2 := map[string]time.Time{"a:1": now.Add(-50 * time.Second), "b:2": now.Add(-30 * time.Second)}
	if hit, _ := typeFailHit(m2, targets, now, time.Minute); hit {
		t.Fatal("未满 1 分钟不应触发")
	}
	// 全部失败超窗：触发，起始取最早
	m3 := map[string]time.Time{"a:1": now.Add(-5 * time.Minute), "b:2": now.Add(-2 * time.Minute)}
	hit, earliest := typeFailHit(m3, targets, now, time.Minute)
	if !hit || !earliest.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("应触发且起始取最早: hit=%v earliest=%v", hit, earliest)
	}
	// 任一目标恢复：立即不触发
	m4 := map[string]time.Time{"b:2": now.Add(-2 * time.Minute)}
	if hit, _ := typeFailHit(m4, targets, now, time.Minute); hit {
		t.Fatal("目标恢复后不应触发")
	}
	// 空目标列表：跳过判定
	if hit, _ := typeFailHit(map[string]time.Time{}, nil, now, time.Minute); hit {
		t.Fatal("无目标不应触发")
	}
}

// medianLoss 共享口径：对称丢包取下中位数、单坏目标不降级、样本不足不下结论
func TestMedianLoss(t *testing.T) {
	pt := map[string][2]int{"a": {20, 4}, "b": {20, 4}, "c": {20, 4}}
	if v, ok := lossMedian(pt); !ok || v != 20 {
		t.Fatalf("对称丢包: %v %v", v, ok)
	}
	pt2 := map[string][2]int{"a": {20, 20}, "b": {20, 0}, "c": {20, 0}}
	if v, ok := lossMedian(pt2); !ok || v != 0 {
		t.Fatalf("单坏目标中位数应为 0: %v %v", v, ok)
	}
	if _, ok := lossMedian(map[string][2]int{"a": {20, 10}, "b": {5, 5}}); ok {
		t.Fatal("部分目标样本不足应 false")
	}
	if v, _ := lossMedian(map[string][2]int{"a": {20, 10}, "b": {20, 4}}); v != 20 {
		t.Fatalf("两目标下中位数: %v 期望 20", v)
	}
}

// minuteStats 的 M：滚动窗口=告警同款下中位数——单坏目标 0、对称丢包 50
func TestMinuteStatsMedian(t *testing.T) {
	base := time.Now().Add(-6 * time.Minute).Truncate(time.Minute)
	// 单坏目标：b 全失败、a 全成功 → 中位数 0（不黄，与告警一致）
	agg := &probeAgg{}
	for m := 0; m < 6; m++ {
		for s := 0; s < 6; s++ {
			ts := base.Add(time.Duration(m*60+s*10) * time.Second)
			agg.pingWin = append(agg.pingWin, pingRec{"a", true, ts}, pingRec{"b", false, ts})
		}
	}
	out := agg.minuteStats(5)
	if len(out) == 0 {
		t.Fatal("无输出")
	}
	if last := out[len(out)-1]; last.M != 0 {
		t.Fatalf("单坏目标 M 应 0: %+v", last)
	}
	// 对称降级：两目标各丢一半 → 中位数 50
	agg2 := &probeAgg{}
	for m := 0; m < 6; m++ {
		for s := 0; s < 6; s++ {
			ts := base.Add(time.Duration(m*60+s*10) * time.Second)
			agg2.pingWin = append(agg2.pingWin, pingRec{"a", s%2 == 0, ts}, pingRec{"b", s%2 == 0, ts})
		}
	}
	out2 := agg2.minuteStats(5)
	last2 := out2[len(out2)-1]
	if last2.M != 50 {
		t.Fatalf("对称丢包 M 应 50: %+v", last2)
	}
	if last2.N != 12 || last2.F != 6 {
		t.Fatalf("分钟计数错误: %+v", last2)
	}
}

// poolMinStats 的 M 取最差（与状态聚合"取最差探针/线路"同构）
func TestPoolMinStatsMedianMax(t *testing.T) {
	out := poolMinStats([]MinStat{{T: 60, N: 6, F: 0, M: 12.5}}, []MinStat{{T: 60, N: 6, F: 1, M: 33.3}})
	if len(out) != 1 || out[0].N != 12 || out[0].F != 1 || out[0].M != 33.3 {
		t.Fatalf("并集错误: %+v", out)
	}
	if out2 := poolMinStats(nil, []MinStat{{T: 60, N: 1, M: 8}}); len(out2) != 1 || out2[0].M != 8 {
		t.Fatalf("单边输入应原样保留: %+v", out2)
	}
}

// 中断收口后的降质抑制：同一故障不双重告警（生产演练：中断恢复后窗口内
// 残留失败样本会立刻触发 P2 降质）。抑制仅覆盖一个丢包窗口，窗口外恢复评估。
func TestRecentOutageLocked(t *testing.T) {
	now := time.Now()
	e := &Engine{outageEnd: map[string]time.Time{}}
	lm := LineMeta{LineID: 7, Bound: true}
	if e.recentOutageLocked(lm, "p1", now, 5*time.Minute) {
		t.Fatal("无中断记录时不应抑制")
	}
	e.outageEnd[KindLineDown+":line7"] = now.Add(-2 * time.Minute)
	if !e.recentOutageLocked(lm, "p1", now, 5*time.Minute) {
		t.Fatal("线路级中断收口 2 分钟内应抑制")
	}
	if e.recentOutageLocked(lm, "p1", now.Add(10*time.Minute), 5*time.Minute) {
		t.Fatal("超出窗口后不应继续抑制")
	}
	// 未绑定线路：按探针级 fingerprint 判定
	e2 := &Engine{outageEnd: map[string]time.Time{KindLineDown + ":p2": now.Add(-time.Minute)}}
	if !e2.recentOutageLocked(LineMeta{Bound: false}, "p2", now, 5*time.Minute) {
		t.Fatal("探针级中断收口后应抑制")
	}
	if e2.recentOutageLocked(LineMeta{Bound: false}, "p3", now, 5*time.Minute) {
		t.Fatal("其他探针不应被抑制")
	}
}

// 判活窗口新鲜度：证人（心跳/网关样本）超过 45s 未更新时不能作为线路中断依据，
// 否则探针猝死会在 45s 先报 P1、60s 才报 P0。
func TestFreshEnough(t *testing.T) {
	now := time.Now()
	if !freshEnough(now.Add(-44*time.Second), now) {
		t.Fatal("44s 内应视为新鲜")
	}
	if freshEnough(now.Add(-46*time.Second), now) {
		t.Fatal("超过判活窗口不应视为新鲜")
	}
	if freshEnough(time.Time{}, now) {
		t.Fatal("零值时间不应视为新鲜")
	}
}

// 线路中断在场时不评估类型故障：全断 60s 后 TCP/DNS/HTTP 会同时满足
// "全部目标连续失败"，若不抑制会在 P1 之外叠加三条文案错误的 P2。
func TestEvalTypeFailSuppressedDuringOutage(t *testing.T) {
	now := time.Now()
	e := &Engine{
		live: map[string]*liveAlert{
			KindLineDown + ":line7": {}, // 线路中断在场
		},
		outageEnd: map[string]time.Time{},
		log:       log.New(io.Discard, "", 0),
	}
	agg := &probeAgg{typeFailTargets: map[string]map[string]time.Time{
		"tcp": {
			"www.qq.com:443":    now.Add(-2 * time.Minute),
			"www.baidu.com:443": now.Add(-2 * time.Minute),
		},
	}}
	cfg := &proto.ProbeConfig{TCPTargets: []string{"www.qq.com:443", "www.baidu.com:443"}}
	e.evalTypeFail("p1", agg, cfg, LineMeta{LineID: 7, Bound: true}, now)
	for _, typ := range []string{"tcp", "dns", "http"} {
		if e.live[KindProbeTypeFail+":p1:"+typ] != nil {
			t.Fatalf("线路中断期间不应触发类型故障: %s", typ)
		}
	}

	// 中断刚收口 10s：旧失败计时不应在恢复瞬间补发 P2（等一个最慢协议周期）
	e2 := &Engine{
		live:      map[string]*liveAlert{},
		outageEnd: map[string]time.Time{KindLineDown + ":line7": now.Add(-10 * time.Second)},
		log:       log.New(io.Discard, "", 0),
	}
	agg2 := &probeAgg{typeFailTargets: map[string]map[string]time.Time{
		"tcp": {
			"www.qq.com:443":    now.Add(-2 * time.Minute),
			"www.baidu.com:443": now.Add(-2 * time.Minute),
		},
	}}
	e2.evalTypeFail("p1", agg2, cfg, LineMeta{LineID: 7, Bound: true}, now)
	if e2.live[KindProbeTypeFail+":p1:tcp"] != nil {
		t.Fatal("中断刚收口时不应补发类型故障")
	}
}

// anyOKWithin：ICMP 成功与 TCP/DNS/HTTP 成功都算；失败样本不算。
func TestAnyOKWithin(t *testing.T) {
	now := time.Now()
	agg := &probeAgg{pingWin: []pingRec{{target: "a", ok: true, ts: now.Add(-10 * time.Second)}}}
	if !agg.anyOKWithin(now, 20*time.Second) {
		t.Fatal("10s 前成功应命中 20s 窗口")
	}
	if agg.anyOKWithin(now, 5*time.Second) {
		t.Fatal("10s 前成功不应命中 5s 窗口")
	}
	agg2 := &probeAgg{lastPubOK: now.Add(-3 * time.Second)}
	if !agg2.anyOKWithin(now, 5*time.Second) {
		t.Fatal("TCP/DNS/HTTP 成功也应计入")
	}
	agg3 := &probeAgg{pingWin: []pingRec{{target: "a", ok: false, ts: now}}}
	if agg3.anyOKWithin(now, 20*time.Second) {
		t.Fatal("失败样本不应计入")
	}
}

// typeFailGrace：最慢协议周期（含 10% 抖动）+ 5s 余量，最低 60s 窗口。
func TestTypeFailGrace(t *testing.T) {
	cfg := &proto.ProbeConfig{IntervalTCPS: 20, IntervalDNSS: 30, IntervalHTTPS: 60}
	if got := typeFailGrace(cfg); got != 71*time.Second {
		t.Fatalf("60s 周期应为 71s，got %v", got)
	}
	cfg2 := &proto.ProbeConfig{IntervalTCPS: 5, IntervalDNSS: 5, IntervalHTTPS: 5}
	if got := typeFailGrace(cfg2); got != 65*time.Second {
		t.Fatalf("最低窗口应为 65s，got %v", got)
	}
}

// 降质抑制：全断（45s 零成功）/中断在场/中断刚收口一个窗口内 → 不判定降质；
// 正常线路（pubOK45=true 且无中断）不抑制。
func TestDegradedSuppressed(t *testing.T) {
	now := time.Now()
	e := &Engine{live: map[string]*liveAlert{}, outageEnd: map[string]time.Time{}}
	lm := LineMeta{LineID: 7, Bound: true}
	if !e.degradedSuppressed(false, lm, "p1", now, 5*time.Minute) {
		t.Fatal("45s 内零成功应抑制降质")
	}
	if e.degradedSuppressed(true, lm, "p1", now, 5*time.Minute) {
		t.Fatal("正常线路不应抑制")
	}
	e.live[KindLineDown+":line7"] = &liveAlert{}
	if !e.degradedSuppressed(true, lm, "p1", now, 5*time.Minute) {
		t.Fatal("线路中断在场应抑制")
	}
	delete(e.live, KindLineDown+":line7")
	e.outageEnd[KindLineDown+":line7"] = now.Add(-time.Minute)
	if !e.degradedSuppressed(true, lm, "p1", now, 5*time.Minute) {
		t.Fatal("中断刚收口一个窗口内应抑制")
	}
}
