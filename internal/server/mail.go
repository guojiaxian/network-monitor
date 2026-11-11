package server

import (
	"crypto/tls"
	"fmt"
	"html"
	"net"
	"net/smtp"
	"strings"
	"time"

	"netwatch/internal/mailfmt"
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
	return mailfmt.BuildMIME(from, to, subject, html)
}

// mailFromName 发件人显示名（收件箱里显示系统名称而非邮箱账号前缀）
const mailFromName = mailfmt.FromName

// mailTimeZone 邮件时间行的时区标注（Uptime Kuma 风格 "Time (Asia/Shanghai)"；部署机 /etc/localtime 即 Asia/Shanghai）
const mailTimeZone = mailfmt.TimeZone

// mailState 邮件状态段（Uptime Kuma 风格：触发=🔴 Down，恢复=✅ Up）
func mailState(firing bool) string {
	s, _ := mailfmt.State(firing)
	return s
}

// mailStateColor 状态配色（触发红 / 恢复绿）
func mailStateColor(firing bool) string {
	_, c := mailfmt.State(firing)
	return c
}

// mailObject 告警对象名（主题第一个方括号，对应 Uptime Kuma 的监控名）
func mailObject(a AlertMeta) string {
	switch a.Kind {
	case KindMonitorDown:
		return "内网服务"
	case KindIndustryDown:
		return "行业网"
	case KindFloorGWDown, KindFloorAnchorDown:
		if a.Floor != "" {
			return a.Floor
		}
		return "楼层网络"
	}
	name := a.LineName
	if name == "" {
		name = "未分配线路(" + a.ProbeID + ")"
	}
	return name
}

// mailFireMsg 触发（🔴 Down）邮件的消息段：告警标题去掉对象名前缀
func mailFireMsg(a AlertMeta) string {
	t := strings.TrimPrefix(a.Title, mailObject(a)+"：")
	switch a.Kind {
	case KindMonitorDown:
		t = strings.TrimPrefix(t, "内网服务不可达：")
	case KindIndustryDown:
		t = strings.TrimPrefix(t, "行业网不可达：")
	}
	return t
}

// mailMsg 告警消息段：触发讲"发生了什么"，恢复讲"什么恢复了"（如 宽带线路恢复）。
// 恢复标题取 kindResolvedTitle，保留原触发标题的「（DNS）」类标注后缀；
// 内置监控/行业网的恢复消息带上具体对象名（如 中心端Web 恢复）。
func mailMsg(a AlertMeta, firing bool) string {
	if firing {
		return mailFireMsg(a)
	}
	switch a.Kind {
	case KindMonitorDown:
		return strings.TrimPrefix(a.Title, "内网服务不可达：") + " 恢复"
	case KindIndustryDown:
		return strings.TrimPrefix(a.Title, "行业网不可达：") + " 恢复"
	case KindFloorGWDown, KindFloorAnchorDown:
		// 标题形如“10楼：核心网关不可达（10.8.32.253）”，恢复时保留楼层与 IP。
		return strings.Replace(a.Title, "不可达", "恢复", 1)
	}
	rt, ok := kindResolvedTitle[a.Kind]
	if !ok {
		return a.Title
	}
	if i := strings.LastIndex(a.Title, "（"); i > 0 && strings.HasSuffix(a.Title, "）") {
		rt += a.Title[i:]
	}
	return rt
}

// mailSubject 告警邮件主题（Uptime Kuma 默认模板 "[监控名] [🔴 Down/✅ Up] 消息"，加 [NetWatch] 前缀区分来源系统）
func mailSubject(a AlertMeta, firing bool) string {
	return mailfmt.Subject(mailObject(a), firing, mailMsg(a, firing))
}

// mailShouldSend 邮件是否发送：默认只报 P0/P1；P2 需后台「P2 告警也发邮件」显式开启（IM 渠道另有自身开关）
func mailShouldSend(severity string, mailP2 bool) bool {
	return severity != "P2" || mailP2
}

// fmtCNDuration 中文时长（停机时长行用）
func fmtCNDuration(d time.Duration) string {
	return mailfmt.Duration(d)
}

// alertEmailHTML 告警邮件正文（Uptime Kuma 风格：对象名+彩色状态标题，下面一行一项信息）
func alertEmailHTML(cfg *Config, a AlertMeta, firing bool, extra string) string {
	probeShow := a.ProbeDisplay
	if probeShow == "" {
		probeShow = a.ProbeID
	}
	rows := []mailfmt.Row{}
	add := func(k, v string) {
		if v != "" {
			rows = append(rows, mailfmt.Row{Key: k, Value: html.EscapeString(v)})
		}
	}
	add("级别", a.Severity)
	add("时间 ("+mailTimeZone+")", a.StartsAt.Format("2006-01-02 15:04:05"))
	if !firing && !a.ResolvedAt.IsZero() {
		add("恢复时间 ("+mailTimeZone+")", a.ResolvedAt.Format("2006-01-02 15:04:05"))
		add("停机时长", fmtCNDuration(a.ResolvedAt.Sub(a.StartsAt)))
	}
	add("探针", probeShow)
	add("楼层", a.Floor)
	add("出口公网 IP", a.PublicIP)
	add("楼层子网", a.CIDR)
	add("消息", mailMsg(a, firing))
	add("详情", extra)
	if firing {
		action := map[string]string{
			KindLineDown:        "线路中断：请拨打线路商报障电话，并核对防火墙对应出口状态。",
			KindFloorDown:       "楼层中断：楼层网关不可达，多为楼内接入交换机/网关故障，请检查楼内设备。",
			KindBlind:           "监控盲区：探针失联，无法监测该楼层。请检查探针虚拟机运行状态；若为计划内维护请设置静默窗口。",
			KindDegraded:        "线路降质：丢包持续偏高，建议先观察再报障。",
			KindLossSevere:      "短时严重丢包：单分钟丢包率异常高，请检查线路瞬时拥塞或设备状态。",
			KindConflict:        "探针数据冲突：部分探针报中断但其余正常，线路大概率正常，请优先检查报中断探针的运行状态与位置。",
			KindFloorGWDown:     "楼层网关不可达：核心 SVI、VLAN 或 ACL/路由异常，请检查核心交换机对应 VLAN 接口。",
			KindFloorAnchorDown: "楼层汇聚设备不可达：请检查楼层交换机/AC/汇聚设备供电与上联链路。",
		}[a.Kind]
		add("建议", action)
	}
	return mailfmt.HTML(mailObject(a), firing, rows, cfg.BaseURL)
}
