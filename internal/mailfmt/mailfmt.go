// Package mailfmt 统一告警邮件格式：中心告警与探针中继共用同一套主题/正文/MIME，
// 避免两处格式漂移。收件箱显示名、Uptime Kuma 风格状态、时间行均在此维护。
package mailfmt

import (
	"fmt"
	"html"
	"mime"
	"strings"
	"time"
)

const (
	FromName = "物探中心网络监控系统（NetWatch）"
	TimeZone = "Asia/Shanghai"
)

type Row struct {
	Key   string
	Value string // 调用方负责转义；空值渲染时跳过
}

// State 返回 Uptime Kuma 风格状态与配色：触发红 / 恢复绿。
func State(firing bool) (string, string) {
	if firing {
		return "🔴 Down", "#d9534f"
	}
	return "✅ Up", "#5cb85c"
}

// Subject 告警邮件主题：[NetWatch] [对象] [🔴 Down/✅ Up] 消息
func Subject(object string, firing bool, message string) string {
	state, _ := State(firing)
	return fmt.Sprintf("[NetWatch] [%s] [%s] %s", object, state, message)
}

// HTML 告警邮件正文（与中心告警邮件完全同一模板）。
func HTML(object string, firing bool, rows []Row, baseURL string) string {
	state, color := State(firing)
	var body strings.Builder
	for _, r := range rows {
		if r.Value == "" {
			continue
		}
		fmt.Fprintf(&body, `<p style="margin:3px 0"><b style="color:#555">%s：</b>%s</p>`, r.Key, r.Value)
	}
	link := ""
	if baseURL != "" {
		link = fmt.Sprintf(`<p style="margin-top:16px"><a href="%s/">→ 前往管理后台查看</a></p>`, baseURL)
	}
	return fmt.Sprintf(`<!DOCTYPE html><html><body style="font-family: -apple-system,'Segoe UI','Microsoft YaHei',sans-serif;max-width:640px;margin:0 auto;padding:16px">
<h2 style="margin:0 0 2px">%s <span style="color:%s">%s</span></h2>
<p style="color:#999;margin:0 0 12px">物探中心网络监控系统（NetWatch）</p>
<div style="line-height:1.8">%s</div>
%s
<p style="color:#999;font-size:12px;margin-top:24px">本邮件由物探中心网络监控系统（NetWatch）自动发送，请勿直接回复。</p>
</body></html>`,
		html.EscapeString(object), color, state, body.String(), link)
}

// BuildMIME 构造 HTML 邮件原始报文（发件人显示名统一）。
func BuildMIME(from string, to []string, subject, htmlBody string) string {
	var b strings.Builder
	b.WriteString("From: " + mime.QEncoding.Encode("utf-8", FromName) + " <" + from + ">\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return b.String()
}

// Duration 中文时长（停机时长行用）。
func Duration(d time.Duration) string {
	s := int(d.Seconds() + 0.5)
	h, m, sec := s/3600, s%3600/60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%d 小时 %d 分 %d 秒", h, m, sec)
	case m > 0:
		return fmt.Sprintf("%d 分 %d 秒", m, sec)
	default:
		return fmt.Sprintf("%d 秒", sec)
	}
}
