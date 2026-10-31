package server

import (
	"testing"
	"time"
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
