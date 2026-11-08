package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeGetter 构造 settings 注入器（key → JSON 值；未配置的键回 err）
func fakeGetter(m map[string]any, missErr error) func(string, any) error {
	return func(key string, v any) error {
		raw, has := m[key]
		if !has {
			return missErr
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, v)
	}
}

// docMap 把强类型结果转成通用 JSON map（模拟真实 HTTP 序列化输出，便于断言）。
func docMap(t *testing.T, d map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal doc: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}
	return out
}

func docRow(d map[string]any, groupTitle, rowName string) map[string]any {
	for _, g := range d["groups"].([]any) {
		gm := g.(map[string]any)
		if gm["title"] != groupTitle {
			continue
		}
		for _, r := range gm["rows"].([]any) {
			rm := r.(map[string]any)
			if rm["name"] == rowName {
				return rm
			}
		}
	}
	return nil
}

// TestMonitorDocDefaults 全部设置缺省：显示默认值且状态正确（零值回退兜底）。
func TestMonitorDocDefaults(t *testing.T) {
	d := docMap(t, monitorDocData(fakeGetter(map[string]any{}, errors.New("no pg"))))
	if row := docRow(d, "一、自动探测 / 监测项 · 楼层探针", "网关探测（区分楼层 / 线路故障域）"); row == nil || row["period"] != "10s ±10%" || row["timeout"] != "3s" {
		t.Fatalf("网关探测默认周期/超时错误: %+v", row)
	}
	if row := docRow(d, "二、自动探测 / 监测项 · 探针 → 中心上报", "心跳 + 探针资源（CPU / 内存 / 磁盘）"); row == nil || row["period"] != "15s" {
		t.Fatalf("心跳默认周期应为 15s: %+v", row)
	}
	if row := docRow(d, "一、自动探测 / 监测项 · 楼层探针", "探测重试（每轮失败后）"); row == nil || row["period"] != "重试 1 次" || row["timeout"] != "间隔 300ms" {
		t.Fatalf("重试默认值错误: %+v", row)
	}
	if row := docRow(d, "三、自动探测 / 监测项 · 中心服务器", "告警评估（对全部探测数据判定，见下表）"); row == nil || row["period"] != "5s" {
		t.Fatalf("告警评估默认周期应为 5s: %+v", row)
	}
	// 告警表实时值：探测异常窗口默认 60s → "1 分钟"
	if len(d["alerts"].([]any)) == 0 {
		t.Fatalf("告警表不应为空")
	}
	found := false
	for _, a := range d["alerts"].([]any) {
		am := a.(map[string]any)
		if am["name"] == "探测异常（TCP/DNS/HTTP）" {
			found = true
			if !strings.Contains(am["trigger"].(string), "1 分钟") {
				t.Fatalf("探测异常窗口默认应显示 1 分钟: %s", am["trigger"])
			}
		}
	}
	if !found {
		t.Fatalf("告警表缺少探测异常行")
	}
}

// TestMonitorDocLiveValues 配置值实时反映：周期/超时/状态/items 全部跟随 settings。
func TestMonitorDocLiveValues(t *testing.T) {
	d := docMap(t, monitorDocData(fakeGetter(map[string]any{
		"agent_config": map[string]any{
			"interval_ping_s": 7, "interval_tcp_s": 22, "interval_dns_s": 44, "interval_http_s": 55,
			"timeout_s": 4, "retry_n": 2, "retry_pause_ms": 500, "heartbeat_sec": 20, "egress_min": 9,
			"icmp_targets": []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"},
			"tcp_targets":  []string{"a:443", "b:443", "c:443"},
			"dns_servers":  []string{"8.8.8.8"},
			"http_targets": []string{"http://x"},
		},
		"speed_settings":    map[string]any{"enabled": false, "engine": "http", "interval_min": 30, "runs": 2},
		"scan_settings":     map[string]any{"enabled": true, "interval_min": 15, "timeout_ms": 2500},
		"internal_monitors": map[string]any{"enabled": true, "items": []any{map[string]any{"name": "OA", "type": "tcp", "target": "10.0.0.9:80", "interval_s": 20, "timeout_s": 8}}},
		"industry_monitors": map[string]any{"enabled": true, "items": []any{map[string]any{"name": "行业网", "target": "10.5.0.1", "interval_s": 30, "timeout_s": 6}}},
		"alert_settings":    map[string]any{"blind_secs": 90, "type_fail_window_s": 120},
		"snmp_settings":     map[string]any{"enabled": true, "interval_s": 60, "timeout_s": 9},
		"syslog_settings":   map[string]any{"enabled": true, "host": "1.2.3.4", "proto": "tcp", "timeout_s": 7},
		"report_settings":   map[string]any{"daily_enabled": true, "daily_at": "07:30", "weekly_enabled": true, "weekly_day": 2, "weekly_at": "08:45"},
	}, errors.New("no pg"))))
	g1 := "一、自动探测 / 监测项 · 楼层探针"
	if row := docRow(d, g1, "网关探测（区分楼层 / 线路故障域）"); row == nil || row["period"] != "7s ±10%" || row["timeout"] != "4s" {
		t.Fatalf("网关探测实时周期错误: %+v", row)
	}
	if row := docRow(d, g1, "公网 ICMP（当前 4 目标投票）"); row == nil {
		t.Fatalf("ICMP 目标数应实时为 4: %+v", row)
	}
	if row := docRow(d, g1, "探测重试（每轮失败后）"); row == nil || row["period"] != "重试 2 次" || row["timeout"] != "间隔 500ms" {
		t.Fatalf("重试实时值错误: %+v", row)
	}
	if row := docRow(d, "二、自动探测 / 监测项 · 探针 → 中心上报", "心跳 + 探针资源（CPU / 内存 / 磁盘）"); row == nil || row["period"] != "20s" {
		t.Fatalf("心跳实时周期应为 20s: %+v", row)
	}
	if row := docRow(d, "二、自动探测 / 监测项 · 探针 → 中心上报", "出口 IP / 运营商 / 归属地识别"); row == nil || row["period"] != "9min" {
		t.Fatalf("出口识别实时周期应为 9min: %+v", row)
	}
	speedRow := docRow(d, "二、自动探测 / 监测项 · 探针 → 中心上报", "定时测速（http 引擎，全部探针并行）")
	if speedRow == nil || speedRow["status"] != "off" || speedRow["period"] != "30min" {
		t.Fatalf("测速行状态/周期错误: %+v", speedRow)
	}
	scanRow := docRow(d, "三、自动探测 / 监测项 · 中心服务器", "楼层 IP 扫描（逐 IP ping 单包，并发 64〔内置固定〕）")
	if scanRow == nil || scanRow["period"] != "15min" || scanRow["timeout"] != "2500ms/IP" {
		t.Fatalf("扫描行实时值错误: %+v", scanRow)
	}
	monRow := docRow(d, "三、自动探测 / 监测项 · 中心服务器", "内→内服务监控（中心自探，Uptime Kuma 模式）")
	if monRow == nil || monRow["status"] != "on" {
		t.Fatalf("内→内行应 on: %+v", monRow)
	}
	items, _ := monRow["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("内→内 items 应 1 项: %+v", monRow)
	}
	it := items[0].(map[string]any)
	if it["period"] != "20s" || it["timeout"] != "8s" {
		t.Fatalf("监控项实时间隔/超时错误: %+v", it)
	}
	indRow := docRow(d, "三、自动探测 / 监测项 · 中心服务器", "行业网监测（仅 ICMP，不做其他探测以免被封）")
	if indRow == nil || indRow["status"] != "on" {
		t.Fatalf("行业网行应 on: %+v", indRow)
	}
	snmpRow := docRow(d, "三、自动探测 / 监测项 · 中心服务器", "SNMP 真实流量采集（防火墙 WAN 口）")
	if snmpRow == nil || snmpRow["period"] != "60s" || snmpRow["timeout"] != "9s" {
		t.Fatalf("SNMP 行实时值错误: %+v", snmpRow)
	}
	repRow := docRow(d, "三、自动探测 / 监测项 · 中心服务器", "日报 / 周报 / 月报（自动生成并邮件推送）")
	if repRow == nil || !strings.Contains(repRow["period"].(string), "07:30") || !strings.Contains(repRow["period"].(string), "周二") {
		t.Fatalf("报表实时文案错误: %+v", repRow)
	}
	// 告警表：盲区 90s、探测异常 120s → "2 分钟"
	for _, a := range d["alerts"].([]any) {
		am := a.(map[string]any)
		if am["name"] == "监控盲区" && !strings.Contains(am["trigger"].(string), "90s") {
			t.Fatalf("盲区窗口应实时为 90s: %s", am["trigger"])
		}
		if am["name"] == "探测异常（TCP/DNS/HTTP）" && !strings.Contains(am["trigger"].(string), "2 分钟") {
			t.Fatalf("探测异常窗口应实时为 2 分钟: %s", am["trigger"])
		}
	}
}

// TestMonitorDocHeartbeatClamp 心跳超界回退：极端值不进文案（与 agent 端 5~60 钳位一致）。
func TestMonitorDocHeartbeatClamp(t *testing.T) {
	d := docMap(t, monitorDocData(fakeGetter(map[string]any{
		"agent_config": map[string]any{"heartbeat_sec": 999},
	}, errors.New("no pg"))))
	if row := docRow(d, "二、自动探测 / 监测项 · 探针 → 中心上报", "心跳 + 探针资源（CPU / 内存 / 磁盘）"); row == nil || row["period"] != "15s" {
		t.Fatalf("心跳越界应回退 15s: %+v", row)
	}
}
