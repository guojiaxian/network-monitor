package server

import (
	"reflect"
	"testing"
	"time"
)

// TestAggregateFloorStatus 楼层状态聚合：取最差线路状态、心跳跨线路逐位与、扫描摘要透传、无线路=unknown
func TestAggregateFloorStatus(t *testing.T) {
	now := time.Now()
	floors := []FloorRow{
		{ID: 1, Name: "4楼", CIDR: "10.8.32.0/24", Lines: []FloorLineBrief{{ID: 2, Name: "联通线路2", PublicIP: "2.2.2.2"}}},
		{ID: 2, Name: "5楼", CIDR: "10.8.33.0/24", Lines: []FloorLineBrief{
			{ID: 3, Name: "联通线路3", PublicIP: "3.3.3.3"}, {ID: 4, Name: "联通线路4", PublicIP: "4.4.4.4"},
		}},
		{ID: 3, Name: "管理网段", CIDR: "10.8.14.0/23"}, // 未关联线路
	}
	ovs := []overviewLine{
		{LineRow: LineRow{ID: 2, Name: "联通线路2"}, ProbeIDs: []string{"p2"},
			Statuses: []ProbeStatus{
				{Status: "online", RTTms: 3, LossPct: 0, TCPOk: true, HTTPOk: true},
				{Status: "online", RTTms: 7, LossPct: 1, TCPOk: true, HTTPOk: false},
			},
			// 生产数据流中该字段由 aggregateLineOverviews 对两探针 Minutes 并集产生
			Minutes: []MinStat{{T: 100, N: 4, F: 0}, {T: 200, N: 4, F: 2}, {T: 300, N: 4, F: 0}},
			Status:  "online"},
		// 线路3 状态差（down），线路4 好（online）→ 5楼取 down
		{LineRow: LineRow{ID: 3, Name: "联通线路3"}, ProbeIDs: []string{"p3"},
			Statuses: []ProbeStatus{{Status: "down", RTTms: 9, LossPct: 100, TCPOk: false, HTTPOk: false}},
			Minutes:  []MinStat{{T: 100, N: 1, F: 0}, {T: 200, N: 1, F: 0}, {T: 300, N: 1, F: 1}},
			Status:   "down"},
		{LineRow: LineRow{ID: 4, Name: "联通线路4"}, ProbeIDs: []string{"p4"},
			Statuses: []ProbeStatus{{Status: "online", RTTms: 5, LossPct: 0, TCPOk: true, HTTPOk: true}},
			Minutes:  []MinStat{{T: 100, N: 1, F: 0}, {T: 200, N: 1, F: 0}, {T: 300, N: 1, F: 0}},
			Status:   "online"},
	}
	scans := map[int]FloorScanSummary{1: {Up: 42, Total: 254, At: now}}

	out := aggregateFloorStatus(floors, ovs, scans, map[string]string{"p2": "probe-2", "p3": "probe-3", "p4": "probe-4"})

	if len(out) != 3 {
		t.Fatalf("应输出 3 个楼层，实际 %d", len(out))
	}
	f1 := out[0]
	if f1.Status != "online" || f1.RTTms != 7 || f1.LossPct != 1 || !f1.TCPOk || f1.HTTPOk {
		t.Errorf("4楼聚合指标不符：status=%s rtt=%v loss=%v tcp=%v http=%v", f1.Status, f1.RTTms, f1.LossPct, f1.TCPOk, f1.HTTPOk)
	}
	// 心跳条（分钟样本并集）：线路内两探针合并 → 中间分钟有失败样本
	if !reflect.DeepEqual(f1.Minutes, []MinStat{{T: 100, N: 4, F: 0}, {T: 200, N: 4, F: 2}, {T: 300, N: 4, F: 0}}) {
		t.Errorf("4楼分钟汇总不符：%v", f1.Minutes)
	}
	if f1.Online == nil || *f1.Online != 42 || f1.Total == nil || *f1.Total != 254 || f1.ScannedAt.IsZero() {
		t.Errorf("4楼扫描摘要透传不符：online=%v total=%v at=%v", f1.Online, f1.Total, f1.ScannedAt)
	}
	if len(f1.LineStats) != 1 || f1.LineStats[0].Name != "联通线路2" || f1.LineStats[0].Status != "online" {
		t.Errorf("4楼关联线路明细不符：%+v", f1.LineStats)
	}
	if !reflect.DeepEqual(f1.ProbeNames, []string{"probe-2"}) {
		t.Errorf("4楼探针名应为 [probe-2]，实际 %v", f1.ProbeNames)
	}

	f2 := out[1]
	if f2.Status != "down" {
		t.Errorf("多线路楼层应取最差状态 down，实际 %s", f2.Status)
	}
	// 跨线路心跳样本并集：末分钟有失败样本
	if !reflect.DeepEqual(f2.Minutes, []MinStat{{T: 100, N: 2, F: 0}, {T: 200, N: 2, F: 0}, {T: 300, N: 2, F: 1}}) {
		t.Errorf("5楼分钟汇总不符：%v", f2.Minutes)
	}
	if f2.Online != nil {
		t.Errorf("未扫描楼层的在线数应为 nil（前端显示 —），实际 %v", *f2.Online)
	}

	f3 := out[2]
	if f3.Status != "unknown" || len(f3.LineStats) != 0 || len(f3.ProbeNames) != 0 {
		t.Errorf("无线路楼层应为 unknown 且无线路/探针：%+v", f3)
	}
}

// TestPoolMinStats 分钟样本并集：同分钟 n/f 累加、升序、空组透传
func TestPoolMinStats(t *testing.T) {
	got := poolMinStats(
		[]MinStat{{T: 200, N: 2, F: 1}, {T: 100, N: 2, F: 0}},
		[]MinStat{{T: 100, N: 3, F: 3}, {T: 300, N: 1, F: 0}},
	)
	want := []MinStat{{T: 100, N: 5, F: 3}, {T: 200, N: 2, F: 1}, {T: 300, N: 1, F: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("并集汇总不符：%v", got)
	}
	if got := poolMinStats(nil, []MinStat{{T: 1, N: 1}}); !reflect.DeepEqual(got, []MinStat{{T: 1, N: 1}}) {
		t.Errorf("空组应透传：%v", got)
	}
	if got := poolMinStats(nil, nil); got != nil {
		t.Errorf("全空应返回 nil：%v", got)
	}
}

// TestMinuteStats pingWin 按分钟聚合：n/f 计数正确、跨 60 分钟截断
func TestMinuteStats(t *testing.T) {
	base := time.Now().Add(-90 * time.Minute).Truncate(time.Minute)
	agg := &probeAgg{}
	// 3 分钟前的分钟：2 样本 1 失败；当前回看窗口外样本应被 Snapshot 前的修剪逻辑处理，这里直接验证聚合本身
	for i := 0; i < 2; i++ {
		agg.pingWin = append(agg.pingWin, pingRec{target: "t", ok: i == 0, ts: base})
	}
	agg.pingWin = append(agg.pingWin, pingRec{target: "t", ok: true, ts: base.Add(2 * time.Minute)})
	got := agg.minuteStats()
	// 首分钟 2 轮：1 成功(0%) + 1 失败(按 100% 兜底) → L=50
	want := []MinStat{{T: base.Unix(), N: 2, F: 1, L: 50}, {T: base.Add(2 * time.Minute).Unix(), N: 1, F: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("分钟聚合不符：%v", got)
	}
}
