package server

import "testing"

// 楼层网关告警邮件对象/标题：触发保留楼层与 IP，恢复把"不可达"替换为"恢复"。
func TestFloorNetMailMeta(t *testing.T) {
	meta := AlertMeta{
		Kind:  KindFloorGWDown,
		Floor: "10楼",
		Title: "10楼：核心网关不可达（10.8.32.253）",
	}
	if got := mailObject(meta); got != "10楼" {
		t.Fatalf("mailObject=%q", got)
	}
	if got := mailMsg(meta, true); got != "核心网关不可达（10.8.32.253）" {
		t.Fatalf("触发消息=%q", got)
	}
	if got := mailMsg(meta, false); got != "10楼：核心网关恢复（10.8.32.253）" {
		t.Fatalf("恢复消息=%q", got)
	}
}

// 共享网关/共享锚点只生成一个探测目标，标题列出全部覆盖楼层段。
func TestBuildFloorNetTargetsDedup(t *testing.T) {
	floors := []FloorRow{
		{ID: 1, Name: "10楼", CIDR: "10.8.32.0/24", Gateway: "10.8.32.253"},
		{ID: 2, Name: "wifi-34段", CIDR: "10.8.34.0/24", Gateway: "10.8.35.253"},
		{ID: 3, Name: "wifi-35段", CIDR: "10.8.35.0/24", Gateway: "10.8.35.253"},
	}
	st := FloorNetSettings{Anchors: []FloorNetAnchor{
		{Name: "2L汇聚交换机", Target: "10.8.37.1", Floors: "1-3楼", Type: floorNetKindSwitch},
		{Name: "3L汇聚交换机", Target: "10.8.37.2", Floors: "1-3楼", Type: floorNetKindSwitch},
		{Name: "8L汇聚交换机", Target: "10.8.37.7", Floors: "8楼-1、8楼-2", Type: floorNetKindSwitch},
	}}
	targets := buildFloorNetTargets(floors, st)
	if len(targets) != 5 {
		t.Fatalf("目标数=%d 期望 5（2 个网关 + 3 个锚点）：%+v", len(targets), targets)
	}
	if targets[0].Class != floorNetClassGateway || targets[1].Class != floorNetClassGateway {
		t.Fatalf("网关应排在前面：%+v", targets)
	}
	var shared *FloorNetTarget
	for i := range targets {
		if targets[i].Target == "10.8.35.253" {
			shared = &targets[i]
		}
	}
	if shared == nil {
		t.Fatal("缺少 wifi 共同网关目标")
	}
	if shared.Floors != "wifi-34段、wifi-35段" {
		t.Fatalf("共享网关覆盖段=%q", shared.Floors)
	}
	if shared.Title != "wifi-34段、wifi-35段：共同网关不可达（10.8.35.253）" {
		t.Fatalf("共享网关标题=%q", shared.Title)
	}
	if got := floorNetFingerprint(*shared); got != "floor_gw_down:10.8.35.253" {
		t.Fatalf("共享网关指纹=%q", got)
	}
}
