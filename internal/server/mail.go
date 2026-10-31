package server

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SendMail 通过 SMTP 发送 HTML 邮件（支持 STARTTLS 与 SSL 端口）。
func SendMail(cfg SMTPSettings, subject, htmlBody string) error {
	if !cfg.Valid() {
		return fmt.Errorf("smtp settings incomplete")
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	host := cfg.Host

	var conn net.Conn
	var err error
	if cfg.UseSSL {
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 15*time.Second)
	}
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer c.Close()

	if !cfg.UseSSL {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err = c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}
	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, host)
		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err = c.Mail(cfg.From); err != nil {
		return err
	}
	for _, to := range cfg.To {
		if err = c.Rcpt(to); err != nil {
			return fmt.Errorf("rcpt %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	body := buildMIME(cfg.From, cfg.To, subject, htmlBody)
	if _, err = w.Write([]byte(body)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func buildMIME(from string, to []string, subject, html string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("\r\n")
	b.WriteString(html)
	return b.String()
}

// alertEmailHTML 告警邮件正文（中文 HTML 模板）
func alertEmailHTML(cfg *Config, a AlertMeta, firing bool, extra string) string {
	state := "🔴 <b>告警触发</b>"
	color := "#d9534f"
	if !firing {
		state = "🟢 <b>已恢复</b>"
		color = "#5cb85c"
	}
	probeShow := a.ProbeDisplay
	if probeShow == "" {
		probeShow = a.ProbeID
	}
	detail := ""
	if a.PublicIP != "" {
		detail += fmt.Sprintf("<tr><td style='%s'>出口公网 IP</td><td>%s</td></tr>", tdStyle, a.PublicIP)
	}
	if a.CIDR != "" {
		detail += fmt.Sprintf("<tr><td style='%s'>楼层子网</td><td>%s</td></tr>", tdStyle, a.CIDR)
	}
	if extra != "" {
		detail += fmt.Sprintf("<tr><td style='%s'>详情</td><td>%s</td></tr>", tdStyle, extra)
	}
	link := ""
	if cfg.BaseURL != "" {
		link = fmt.Sprintf(`<p style="margin-top:16px"><a href="%s/">→ 前往管理后台查看</a></p>`, cfg.BaseURL)
	}
	action := map[string]string{
		KindLineDown:   "线路中断：请拨打线路商报障电话，并核对防火墙对应出口状态。",
		KindFloorDown:  "楼层中断：楼层网关不可达，多为楼内接入交换机/网关故障，请检查楼内设备。",
		KindBlind:      "监控盲区：探针失联，无法监测该楼层。请检查探针虚拟机运行状态；若为计划内维护请设置静默窗口。",
		KindDegraded:   "线路降质：丢包或时延持续偏高，建议先观察再报障。",
		KindConflict:   "探针数据冲突：部分探针报中断但其余正常，线路大概率正常，请优先检查报中断探针的运行状态与位置。",
		KindSiteDown:   "网站从外网不可达：全部外网探针均无法访问，对互联网用户网站大概率已不可用——请立即检查 Web 服务、防火墙公网映射与出口线路。",
		KindSiteConf:   "外网探针数据冲突：部分外网探针报网站不可达，请优先检查异常探针服务器自身的网络。",
		KindCertExpire: "HTTPS 证书即将过期：请尽快在证书签发机构续签并更新到 Web 服务器。",
	}[a.Kind]

	return fmt.Sprintf(`<!DOCTYPE html><html><body style="font-family: -apple-system,'Segoe UI','Microsoft YaHei',sans-serif;max-width:640px;margin:0 auto;padding:16px">
<div style="border-left:4px solid %s;padding:12px 16px;background:#f7f7f7">
<h2 style="margin:0 0 8px">%s %s</h2>
<p style="color:#666;margin:0">网络监控系统 NetWatch · %s</p>
</div>
<table style="border-collapse:collapse;margin-top:16px;width:100%%" cellpadding="6">
<tr><td style="%s">级别</td><td>%s</td></tr>
<tr><td style="%s">线路</td><td>%s</td></tr>
<tr><td style='%s'>楼层</td><td>%s</td></tr>
<tr><td style='%s'>探针</td><td>%s</td></tr>
<tr><td style='%s'>开始时间</td><td>%s</td></tr>
%s
</table>
<p style="margin-top:16px">%s</p>
%s
<p style="color:#999;font-size:12px;margin-top:24px">本邮件由 NetWatch 网络监控系统自动发送，请勿直接回复。</p>
</body></html>`,
		color, state, a.Title, time.Now().Format("2006-01-02 15:04:05"),
		tdStyle, a.Severity,
		tdStyle, a.LineName, tdStyle, a.Floor, tdStyle, probeShow, tdStyle,
		a.StartsAt.Format("2006-01-02 15:04:05"),
		detail, action, link)
}

const tdStyle = "background:#f0f0f0;font-weight:bold;width:110px"
