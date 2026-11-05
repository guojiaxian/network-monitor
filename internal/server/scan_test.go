package server

import "testing"

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
