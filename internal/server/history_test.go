package server

import "testing"

func mkRec(sec int64, typ string, ok bool, rtt float64) HistoryRecord {
	return HistoryRecord{TS: sec * 1000, Type: typ, OK: ok, RTTms: rtt}
}

// 桶聚合：总数/成败/平均时延（失败记录无 rtt 不计入）/桶数与桶内计数
func TestHistStatsBuckets(t *testing.T) {
	start := int64(1000)
	end := start + 240*10 // span=10s，恰好 240 桶
	recs := []HistoryRecord{
		mkRec(start+5, "ping", true, 10),
		mkRec(start+15, "ping", false, 0),
		mkRec(start+25, "tcp", true, 30),
	}
	st, buckets, span := histStatsBuckets(recs, start, end, false, 0, 240)
	if span != 10 {
		t.Fatalf("span=%d 期望 10", span)
	}
	if st.Total != 3 || st.OK != 2 || st.Fail != 1 {
		t.Fatalf("stats 错误: %+v", st)
	}
	if st.AvgRTT != 20 { // (10+30)/2
		t.Fatalf("avg_rtt=%v 期望 20", st.AvgRTT)
	}
	if len(buckets) != 240 {
		t.Fatalf("桶数=%d 期望 240", len(buckets))
	}
	if buckets[0].N != 1 || buckets[0].OKN != 1 || buckets[0].AvgRTT != 10 {
		t.Fatalf("桶0: %+v", buckets[0])
	}
	if buckets[1].N != 1 || buckets[1].OKN != 0 {
		t.Fatalf("桶1: %+v", buckets[1])
	}
	if buckets[2].N != 1 || buckets[2].AvgRTT != 30 {
		t.Fatalf("桶2: %+v", buckets[2])
	}
	if buckets[3].N != 0 {
		t.Fatalf("空桶应为 0: %+v", buckets[3])
	}
}

// speed 速率默认不进时延均值；type=speed 查询（includeSpeedRTT）时计入
func TestHistStatsBucketsSpeedExcluded(t *testing.T) {
	start := int64(1000)
	end := start + 2400
	recs := []HistoryRecord{
		mkRec(start+5, "ping", true, 10),
		mkRec(start+6, "speed", true, 300),
	}
	st, _, _ := histStatsBuckets(recs, start, end, false, 0, 240)
	if st.AvgRTT != 10 {
		t.Fatalf("speed 应排除: avg=%v 期望 10", st.AvgRTT)
	}
	st2, b2, _ := histStatsBuckets(recs, start, end, true, 0, 240)
	if st2.AvgRTT != 155 {
		t.Fatalf("includeSpeedRTT 应计入: avg=%v 期望 155", st2.AvgRTT)
	}
	if b2[0].AvgRTT != 155 {
		t.Fatalf("桶均值: %v 期望 155", b2[0].AvgRTT)
	}
}

// 超长窗口钳位到 31 天；短窗口不动
func TestHistClampWindow(t *testing.T) {
	end := int64(2000000)
	s, e := histClampWindow(end-40*86400, end)
	if e-s != int64(31*86400) {
		t.Fatalf("钳位失败: 窗口=%d", e-s)
	}
	in := end - 10*86400
	s2, e2 := histClampWindow(in, end)
	if s2 != in || e2 != end {
		t.Fatalf("短窗口不应改动: %d %d", s2, e2)
	}
}

// 选择器构造：全部目标/单目标精确（引号与反斜杠转义）
func TestHistSelector(t *testing.T) {
	if got := histSelAll("ping", "p1"); got != `{probe="p1"}` {
		t.Fatalf("ping all: %s", got)
	}
	if got := histSelAll("monitor", ""); got != `{monitor!=""}` {
		t.Fatalf("monitor all: %s", got)
	}
	if got := histSelOne("ping", "p1", "223.5.5.5"); got != `{probe="p1",target="223.5.5.5"}` {
		t.Fatalf("ping one: %s", got)
	}
	if got := histSelOne("monitor", "", "中心端Web"); got != `{monitor="中心端Web"}` {
		t.Fatalf("monitor one: %s", got)
	}
	if got := histSelOne("ping", "p1", `a"b\c`); got != `{probe="p1",target="a\"b\\c"}` {
		t.Fatalf("转义: %s", got)
	}
}

// 按系列（类型×目标）分桶：同名目标不混线、排序稳定、桶计数正确
func TestHistSeriesBuckets(t *testing.T) {
	start := int64(1000)
	end := start + 2400 // span=10s，240 桶
	recs := []HistoryRecord{
		{TS: (start + 5) * 1000, Type: "ping", Target: "A", OK: true, RTTms: 10},
		{TS: (start + 8) * 1000, Type: "ping", Target: "A", OK: true, RTTms: 20},
		{TS: (start + 5) * 1000, Type: "dns", Target: "A", OK: true, RTTms: 30}, // 与 ping A 同名目标，不得混线
		{TS: (start + 5) * 1000, Type: "ping", Target: "B", OK: false},
	}
	series := histSeriesBuckets(recs, start, end, 10, 240, false)
	if len(series) != 3 {
		t.Fatalf("系列数=%d 期望 3（ping A/ping B/dns A）", len(series))
	}
	if series[0].Type != "ping" || series[0].Target != "A" || series[1].Target != "B" || series[2].Type != "dns" {
		t.Fatalf("系列排序错误: %+v", series)
	}
	if series[0].Buckets[0].N != 2 || series[0].Buckets[0].AvgRTT != 15 {
		t.Fatalf("ping A 桶0: %+v", series[0].Buckets[0])
	}
	if series[1].Buckets[0].N != 1 || series[1].Buckets[0].OKN != 0 {
		t.Fatalf("ping B 桶0: %+v", series[1].Buckets[0])
	}
	if series[2].Buckets[0].N != 1 || series[2].Buckets[0].AvgRTT != 30 {
		t.Fatalf("dns A 桶0: %+v", series[2].Buckets[0])
	}
}

// all 型系列携带测速自身 Mbps（前端右侧副轴绘制），不影响其他类型系列
func TestHistSeriesBucketsSpeedCarry(t *testing.T) {
	start := int64(1000)
	end := start + 2400
	recs := []HistoryRecord{
		{TS: (start + 5) * 1000, Type: "speed", Target: "cdn", OK: true, RTTms: 335},
		{TS: (start + 6) * 1000, Type: "ping", Target: "A", OK: true, RTTms: 20},
	}
	series := histSeriesBuckets(recs, start, end, 10, 240, true)
	var sp, pg *histSeries
	for i := range series {
		if series[i].Type == "speed" {
			sp = &series[i]
		} else {
			pg = &series[i]
		}
	}
	if sp == nil || pg == nil {
		t.Fatalf("系列缺失: %+v", series)
	}
	if sp.Buckets[0].AvgRTT != 335 {
		t.Fatalf("测速系列应携带 Mbps: %+v", sp.Buckets[0])
	}
	if pg.Buckets[0].AvgRTT != 20 {
		t.Fatalf("ping 系列不应受影响: %+v", pg.Buckets[0])
	}
}

// 结果筛选：ok/fail/其他值原样返回
func TestHistFilterStatus(t *testing.T) {
	recs := []HistoryRecord{
		mkRec(1, "ping", true, 1),
		mkRec(2, "ping", false, 0),
		mkRec(3, "tcp", true, 2),
	}
	if got := histFilterStatus(append([]HistoryRecord{}, recs...), "ok"); len(got) != 2 {
		t.Fatalf("ok 过滤: %d 期望 2", len(got))
	}
	if got := histFilterStatus(append([]HistoryRecord{}, recs...), "fail"); len(got) != 1 || got[0].OK {
		t.Fatalf("fail 过滤错误: %+v", got)
	}
	if got := histFilterStatus(recs, ""); len(got) != 3 {
		t.Fatalf("空 status 应原样返回: %d", len(got))
	}
}

// 低频类型自适应桶跨度：期望间隔 1h、minSpan=1h（一格一个探测周期）→
// 24h 窗口 24 桶，每小时一条记录时每桶恰一条，正常探测间隙不产生空桶
func TestHistStatsBucketsAdaptiveSpan(t *testing.T) {
	start := int64(1000)
	end := start + 86400
	var recs []HistoryRecord
	for s := start + 1800; s < end; s += 3600 { // 每小时一条（偏移半间隔避开桶边界）
		recs = append(recs, mkRec(s, "speed", true, 300))
	}
	_, buckets, span := histStatsBuckets(recs, start, end, true, 3600, 240)
	if span != 3600 {
		t.Fatalf("span=%d 期望 3600", span)
	}
	if len(buckets) != 24 {
		t.Fatalf("桶数=%d 期望 24", len(buckets))
	}
	for i, b := range buckets {
		if b.N == 0 {
			t.Fatalf("正常间隙不应有空桶，桶 %d 为空", i)
		}
	}
}

// 漏探/停探时段（某周期无样本）产生空桶（灰色只属于真实无探测）
func TestHistStatsBucketsPauseGray(t *testing.T) {
	start := int64(1000)
	end := start + 86400
	var recs []HistoryRecord
	for s := start + 1800; s < start+6*3600; s += 3600 { // 前 5 小时有，之后停探
		recs = append(recs, mkRec(s, "speed", true, 300))
	}
	_, buckets, _ := histStatsBuckets(recs, start, end, true, 3600, 240)
	empty := 0
	for _, b := range buckets {
		if b.N == 0 {
			empty++
		}
	}
	if empty == 0 {
		t.Fatalf("停探时段应产生空桶")
	}
}

// 桶级丢包 PL：按目标下中位数（与告警同口径）——单坏目标 0、对称丢包超阈值、tcp 不计入
func TestHistBucketizeLoss(t *testing.T) {
	start := int64(1000)
	span := int64(300)
	var recs []HistoryRecord
	for i := 0; i < 20; i++ { // a 目标 20% 丢包、b 目标全成功 → 中位数 0
		ok := i >= 4
		recs = append(recs, HistoryRecord{TS: (start + 10 + int64(i)) * 1000, Type: "ping", Target: "a", OK: ok},
			HistoryRecord{TS: (start + 10 + int64(i)) * 1000, Type: "ping", Target: "b", OK: true})
	}
	recs = append(recs, HistoryRecord{TS: (start + 15) * 1000, Type: "tcp", Target: "x", OK: false})
	bs := histBucketize(recs, start, span, 1, false, true)
	if bs[0].PN != 40 {
		t.Fatalf("PN 应只计 ping: %+v", bs[0])
	}
	if bs[0].PL != 0 {
		t.Fatalf("单坏目标桶 PL 应 0: %+v", bs[0])
	}
	// 对称：两目标各 20% → 下中位数 20
	var recs2 []HistoryRecord
	for i := 0; i < 20; i++ {
		ok := i >= 4
		recs2 = append(recs2, HistoryRecord{TS: (start + 10 + int64(i)) * 1000, Type: "ping", Target: "a", OK: ok},
			HistoryRecord{TS: (start + 10 + int64(i)) * 1000, Type: "ping", Target: "b", OK: ok})
	}
	if bs2 := histBucketize(recs2, start, span, 1, false, true); bs2[0].PL != 20 {
		t.Fatalf("对称丢包 PL 应 20: %+v", bs2[0])
	}
	// withLoss=false（系列折线路径）：不产出 PN/PL
	if bs3 := histBucketize(recs2, start, span, 1, false, false); bs3[0].PN != 0 || bs3[0].PL != 0 {
		t.Fatalf("withLoss=false 不应产出 PN/PL: %+v", bs3[0])
	}
}
