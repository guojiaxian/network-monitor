package server

import (
	"mime"
	"strings"
	"testing"
	"time"
)

// mkMailMeta 构造告警邮件元信息样例
func mkMailMeta(kind, title, line string) AlertMeta {
	return AlertMeta{
		Kind: kind, Severity: kindSeverity[kind], Title: title,
		LineName: line, Floor: "4楼", PublicIP: "115.60.2.222", CIDR: "10.8.20.0/24",
		ProbeID: "0123abcd-4567", ProbeDisplay: "probe-2",
		StartsAt: time.Date(2026, 11, 9, 12, 34, 56, 0, time.Local),
	}
}

// TestMailSubject 邮件主题对齐 Uptime Kuma 默认模板：[对象] [🔴 Down/✅ Up] 消息（加 [NetWatch] 前缀）
func TestMailSubject(t *testing.T) {
	cases := []struct {
		meta   AlertMeta
		firing bool
		want   string
	}{
		{mkMailMeta(KindLineDown, "联通线路2：宽带线路中断", "联通线路2"), true,
			"[NetWatch] [联通线路2] [🔴 Down] 宽带线路中断"},
		{mkMailMeta(KindLineDown, "联通线路2：宽带线路中断", "联通线路2"), false,
			"[NetWatch] [联通线路2] [✅ Up] 宽带线路恢复"},
		{AlertMeta{Kind: KindProbeTypeFail, Severity: "P2", Title: "联通线路2：探测异常（DNS）", LineName: "联通线路2"}, false,
			"[NetWatch] [联通线路2] [✅ Up] 探测恢复（DNS）"},
		{mkMailMeta(KindMonitorDown, "内网服务不可达：中心端Web", ""), true,
			"[NetWatch] [内网服务] [🔴 Down] 中心端Web"},
		{mkMailMeta(KindIndustryDown, "行业网不可达：台网中心", ""), false,
			"[NetWatch] [行业网] [✅ Up] 台网中心 恢复"},
	}
	for i, c := range cases {
		if got := mailSubject(c.meta, c.firing); got != c.want {
			t.Errorf("case %d subject = %q, want %q", i, got, c.want)
		}
	}
	// 未绑定线路的盲区告警：对象名回退 未分配线路(探针ID)，与 decorateTitle 同口径
	blind := mkMailMeta(KindBlind, "未分配线路(deadbeef)：监控盲区：探针失联", "")
	blind.ProbeID = "deadbeef"
	if got := mailSubject(blind, true); got != "[NetWatch] [未分配线路(deadbeef)] [🔴 Down] 监控盲区：探针失联" {
		t.Errorf("blind subject = %q", got)
	}
}

// TestAlertEmailHTMLKumaStyle 邮件正文为 Kuma 风格：对象名+彩色状态标题、逐行信息、时间带时区标注
func TestAlertEmailHTMLKumaStyle(t *testing.T) {
	cfg := &Config{BaseURL: "http://10.8.14.200"}
	meta := mkMailMeta(KindLineDown, "联通线路2：宽带线路中断", "联通线路2")

	fire := alertEmailHTML(cfg, meta, true, "")
	for _, want := range []string{
		`>联通线路2 <span style="color:#d9534f">🔴 Down</span></h2>`,
		"物探中心网络监控系统（NetWatch）",
		"<b style=\"color:#555\">级别：</b>P1",
		"时间 (" + mailTimeZone + ")：</b>2026-11-09 12:34:56",
		"探针：</b>probe-2",
		"出口公网 IP：</b>115.60.2.222",
		"楼层子网：</b>10.8.20.0/24",
		"消息：</b>宽带线路中断",
		"建议：</b>线路中断",
		`href="http://10.8.14.200/"`,
	} {
		if !strings.Contains(fire, want) {
			t.Errorf("fire body missing %q", want)
		}
	}
	if strings.Contains(fire, "✅ Up") {
		t.Error("fire body should not contain Up state")
	}

	res := alertEmailHTML(cfg, meta, false, "线路恢复（连续 3 分钟丢包率 ≤70%）")
	meta.ResolvedAt = meta.StartsAt.Add(62*time.Minute + 3*time.Second)
	res = alertEmailHTML(cfg, meta, false, "线路恢复（连续 3 分钟丢包率 ≤70%）")
	for _, want := range []string{
		`<span style="color:#5cb85c">✅ Up</span>`,
		"消息：</b>宽带线路恢复",
		"恢复时间 (Asia/Shanghai)：</b>2026-11-09 13:36:59",
		"停机时长：</b>1 小时 2 分 3 秒",
		"详情：</b>线路恢复（连续 3 分钟丢包率 ≤70%）",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("resolve body missing %q", want)
		}
	}
	if strings.Contains(res, "建议：</b>") {
		t.Error("resolve body should not contain action row")
	}
	// 触发邮件不含恢复专属行
	if strings.Contains(fire, "停机时长") || strings.Contains(fire, "恢复时间") {
		t.Error("fire body should not contain resolve-only rows")
	}

	// 字段缺省时不出现空行（楼层留空 → 无楼层行）
	meta.Floor = ""
	if strings.Contains(alertEmailHTML(cfg, meta, true, ""), "楼层：</b>") {
		t.Error("empty floor should be omitted")
	}
}

// TestMailShouldSend 邮件级别门控：默认只报 P0/P1，P2 需显式开启
func TestMailShouldSend(t *testing.T) {
	if !mailShouldSend("P0", false) || !mailShouldSend("P1", false) {
		t.Error("P0/P1 should always mail by default")
	}
	if mailShouldSend("P2", false) {
		t.Error("P2 should be suppressed by default")
	}
	if !mailShouldSend("P2", true) {
		t.Error("P2 should mail when mail_send_p2 is on")
	}
}

// TestBuildMIMEFromDisplayName 发件人显示系统名称：From: RFC2047 编码名 <地址>
func TestBuildMIMEFromDisplayName(t *testing.T) {
	raw := buildMIME("warning@gec.ac.cn", []string{"guojx98@dingtalk.com"}, "[NetWatch] [联通线路2] [🔴 Down] 宽带线路中断", "<p>x</p>")
	if !strings.Contains(raw, `From: `) || !strings.HasSuffix(strings.Split(raw, "\r\n")[0], " <warning@gec.ac.cn>") {
		t.Errorf("From header unexpected: %q", strings.Split(raw, "\r\n")[0])
	}
	if !strings.Contains(raw, mailFromName) && !strings.Contains(raw, mime.QEncoding.Encode("utf-8", mailFromName)) {
		t.Errorf("From header missing display name: %q", strings.Split(raw, "\r\n")[0])
	}
}
