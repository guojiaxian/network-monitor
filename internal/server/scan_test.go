package server

import (
	"testing"
	"time"
)

func TestAttachLineOnlineIPs(t *testing.T) {
	t1 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(30 * time.Minute)
	scans := map[int]FloorScanSummary{
		7: {Up: 30, Total: 254, At: t1},
		8: {Up: 42, Total: 254, At: t2},
	}
	lines := []LineRow{
		{ID: 1, Name: "线路1", Floors: []FloorBrief{{ID: 7, Name: "1-3楼"}, {ID: 8, Name: "4楼"}}}, // 两楼层都有扫描
		{ID: 2, Name: "线路2", Floors: []FloorBrief{{ID: 9, Name: "5楼"}}},                        // 未扫描
		{ID: 3, Name: "线路3", Floors: nil},                                                      // 未关联楼层
	}
	attachLineOnlineIPs(lines, scans)

	if lines[0].OnlineSum == nil || *lines[0].OnlineSum != 72 || *lines[0].ScanSum != 508 {
		t.Fatalf("线路1 汇总错误: %+v", lines[0])
	}
	if len(lines[0].OnlineIPs) != 2 {
		t.Fatalf("线路1 明细条数错误: %+v", lines[0].OnlineIPs)
	}
	if lines[0].ScannedAt == nil || !lines[0].ScannedAt.Equal(t2) {
		t.Fatalf("线路1 扫描时间应取最新: %+v", lines[0].ScannedAt)
	}
	if lines[1].OnlineSum != nil {
		t.Fatalf("未扫描线路应为 nil（前端显示—）: %+v", lines[1].OnlineSum)
	}
	if lines[2].OnlineSum != nil {
		t.Fatalf("未关联楼层应为 nil: %+v", lines[2].OnlineSum)
	}
}

func TestScanSettingsNormalize(t *testing.T) {
	t.Run("间隔越界回落 60", func(t *testing.T) {
		for _, in := range []int{0, -5, 4, 1441, 100000} {
			s := ScanSettings{IntervalMin: in}
			s.normalize()
			if s.IntervalMin != 60 {
				t.Fatalf("间隔 %d 应回落 60，实际 %d", in, s.IntervalMin)
			}
		}
	})
	t.Run("间隔上下限内保留", func(t *testing.T) {
		for _, in := range []int{5, 60, 1440} {
			s := ScanSettings{IntervalMin: in}
			s.normalize()
			if s.IntervalMin != in {
				t.Fatalf("间隔 %d 应保留，实际 %d", in, s.IntervalMin)
			}
		}
	})
	t.Run("楼层去重并剔除非法", func(t *testing.T) {
		s := ScanSettings{IntervalMin: 60, FloorIDs: []int{3, 1, 3, 0, -2}}
		s.normalize()
		if len(s.FloorIDs) != 2 || s.FloorIDs[0] != 3 || s.FloorIDs[1] != 1 {
			t.Fatalf("楼层去重错误: %v", s.FloorIDs)
		}
	})
	t.Run("单IP超时越界回落 1000ms", func(t *testing.T) {
		for _, in := range []int{0, -100, 199, 5001, 100000} {
			s := ScanSettings{IntervalMin: 60, TimeoutMs: in}
			s.normalize()
			if s.TimeoutMs != 1000 {
				t.Fatalf("超时 %d 应回落 1000，实际 %d", in, s.TimeoutMs)
			}
		}
	})
	t.Run("单IP超时上下限内保留", func(t *testing.T) {
		for _, in := range []int{200, 1000, 5000} {
			s := ScanSettings{IntervalMin: 60, TimeoutMs: in}
			s.normalize()
			if s.TimeoutMs != in {
				t.Fatalf("超时 %d 应保留，实际 %d", in, s.TimeoutMs)
			}
		}
	})
}

func TestPickScanFloors(t *testing.T) {
	all := []FloorRow{
		{ID: 1, Name: "1-3楼", CIDR: "10.8.30.0/25"},
		{ID: 2, Name: "4楼", CIDR: "10.8.31.0/24"},
		{ID: 3, Name: "测试楼层", CIDR: "10.8.14.0/23"},
	}
	t.Run("按入参顺序去重选取", func(t *testing.T) {
		fs, err := pickScanFloors(all, []int{2, 1, 2})
		if err != nil {
			t.Fatalf("应通过: %v", err)
		}
		if len(fs) != 2 || fs[0].ID != 2 || fs[1].ID != 1 {
			t.Fatalf("顺序/去重错误: %+v", fs)
		}
	})
	t.Run("空选择拒绝", func(t *testing.T) {
		if _, err := pickScanFloors(all, nil); err == nil {
			t.Fatal("空选择应拒绝")
		}
	})
	t.Run("已删除楼层拒绝", func(t *testing.T) {
		if _, err := pickScanFloors(all, []int{1, 9}); err == nil {
			t.Fatal("不存在的楼层应拒绝")
		}
	})
}

func TestScanTargets(t *testing.T) {
	t.Run("24 网段枚举 254 主机并跳过网络广播地址", func(t *testing.T) {
		ips, err := scanTargets("10.8.20.0/24")
		if err != nil {
			t.Fatalf("应通过: %v", err)
		}
		if len(ips) != 254 {
			t.Fatalf("主机数=%d，期望 254", len(ips))
		}
		if ips[0] != "10.8.20.1" || ips[len(ips)-1] != "10.8.20.254" {
			t.Fatalf("首尾主机错误: %s ~ %s", ips[0], ips[len(ips)-1])
		}
	})
	t.Run("30 网段 2 主机", func(t *testing.T) {
		ips, err := scanTargets("10.8.14.200/30")
		if err != nil {
			t.Fatalf("应通过: %v", err)
		}
		if len(ips) != 2 {
			t.Fatalf("主机数=%d，期望 2", len(ips))
		}
		if ips[0] != "10.8.14.201" || ips[1] != "10.8.14.202" {
			t.Fatalf("主机错误: %v", ips)
		}
	})
	t.Run("32 单主机", func(t *testing.T) {
		ips, err := scanTargets("10.5.0.1/32")
		if err != nil {
			t.Fatalf("应通过: %v", err)
		}
		if len(ips) != 1 || ips[0] != "10.5.0.1" {
			t.Fatalf("结果错误: %v", ips)
		}
	})
	t.Run("超大网段拒绝", func(t *testing.T) {
		if _, err := scanTargets("10.0.0.0/16"); err == nil {
			t.Fatal("/16 应被拒绝")
		}
		if _, err := scanTargets("10.8.0.0/21"); err == nil {
			t.Fatal("/21 应被拒绝")
		}
		if _, err := scanTargets("10.8.0.0/22"); err != nil {
			t.Fatalf("/22 应允许: %v", err)
		}
	})
	t.Run("非法与不支持输入", func(t *testing.T) {
		if _, err := scanTargets("not-a-cidr"); err == nil {
			t.Fatal("非法 CIDR 应报错")
		}
		if _, err := scanTargets("fd00::/64"); err == nil {
			t.Fatal("IPv6 应报错")
		}
	})
}
