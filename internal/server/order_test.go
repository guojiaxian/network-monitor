package server

import (
	"reflect"
	"testing"
)

// 全站统一展示排序（2026-11-07 用户定）：探针按显示名序号、线路"联通线路N"序号+专线殿后、楼层按 CIDR。
func TestSortProbesForDisplay(t *testing.T) {
	in := []ProbeRow{
		{ID: "c", Name: "probe-12"},
		{ID: "a", Name: "probe-2"},
		{ID: "d", Name: "node-x"},           // 无编号
		{ID: "b", Name: "probe-1"},          // 编号最小
		{ID: "e", Name: "", Hostname: "p9"}, // 无显示名时用主机名取尾数
	}
	sortProbesForDisplay(in)
	got := make([]string, 0, len(in))
	for _, p := range in {
		got = append(got, probeDisplayName(p))
	}
	// 编号组按数字升序（1,2,9,12——字符串序会把 probe-12 排到 probe-2 前，数字序修正）
	want := []string{"probe-1", "probe-2", "p9", "probe-12", "node-x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("探针排序不符：got %v want %v", got, want)
	}
}

func TestSortLinesForDisplay(t *testing.T) {
	in := []LineRow{
		{ID: 1, Name: "联通专线线路"},
		{ID: 2, Name: "联通-115.60.4.76"}, // 自动建线：不匹配"线路N"，居中按 ID
		{ID: 3, Name: "联通线路10"},
		{ID: 4, Name: "联通线路2"},
		{ID: 5, Name: "联通线路1"},
	}
	sortLinesForDisplay(in)
	got := make([]string, 0, len(in))
	for _, l := range in {
		got = append(got, l.Name)
	}
	want := []string{"联通线路1", "联通线路2", "联通线路10", "联通-115.60.4.76", "联通专线线路"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("线路排序不符：got %v want %v", got, want)
	}
}

func TestSortFloorsForDisplay(t *testing.T) {
	in := []FloorRow{
		{ID: 1, CIDR: "10.8.26.0/24"},
		{ID: 2, CIDR: "10.8.14.0/23"}, // 网段最小 → 最前
		{ID: 3, CIDR: "10.8.30.128/25"},
		{ID: 4, CIDR: ""}, // 未配置 → 最后
		{ID: 5, CIDR: "10.8.30.0/25"},
		{ID: 6, CIDR: "10.8.25.0/24"},
		{ID: 7, CIDR: "not-a-cidr"}, // 非法 → 无效组按 ID
	}
	sortFloorsForDisplay(in)
	got := make([]string, 0, len(in))
	for _, f := range in {
		got = append(got, f.CIDR)
	}
	// 无效/未配置 CIDR 排最后（同为无效按 ID：id4="" 在 id7="not-a-cidr" 前）
	want := []string{"10.8.14.0/23", "10.8.25.0/24", "10.8.26.0/24", "10.8.30.0/25", "10.8.30.128/25", "", "not-a-cidr"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("楼层排序不符：got %v want %v", got, want)
	}
}
