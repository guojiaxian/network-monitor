// netwatch-agent 楼层探针。
// 部署在每层楼子网的精简 VM 中（1C1G），出站流量经防火墙策略路由
// 天然走本楼层宽带，探测结果即该楼层用户的真实出口体验。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"netwatch/internal/agent"
)

var version = "1.0.8"

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	// relay 子命令：probe-1 兼职告警中继（独立 systemd 单元，与探测主进程并存）。
	if len(os.Args) > 1 && os.Args[1] == "relay" {
		fs := flag.NewFlagSet("relay", flag.ExitOnError)
		server := fs.String("server", envOr("NW_SERVER", "http://10.8.14.200"), "中心端管理网地址")
		stateDir := fs.String("state-dir", envOr("NW_STATE_DIR", "/var/lib/netwatch-agent"), "身份与中继状态目录")
		webhook := fs.String("webhook", envOr("NW_RELAY_WEBHOOK", ""), "外部通知 webhook（钉钉/企微/通用）")
		webhookType := fs.String("webhook-type", envOr("NW_RELAY_WEBHOOK_TYPE", "dingtalk"), "webhook 类型：dingtalk/wecom/generic")
		lineName := fs.String("line-name", envOr("NW_RELAY_LINE_NAME", "联通专线"), "中心所在专线名称（与系统线路名一致）")
		smtpHost := fs.String("smtp-host", envOr("NW_RELAY_SMTP_HOST", ""), "SMTP 主机（配置后优先用邮件）")
		smtpPort := fs.Int("smtp-port", envInt("NW_RELAY_SMTP_PORT", 25), "SMTP 端口")
		smtpUser := fs.String("smtp-user", envOr("NW_RELAY_SMTP_USER", ""), "SMTP 用户名")
		smtpPass := fs.String("smtp-pass", envOr("NW_RELAY_SMTP_PASS", ""), "SMTP 密码")
		smtpFrom := fs.String("smtp-from", envOr("NW_RELAY_SMTP_FROM", ""), "发件人")
		smtpTo := fs.String("smtp-to", envOr("NW_RELAY_SMTP_TO", ""), "收件人，逗号分隔")
		interval := fs.Duration("interval", 15*time.Second, "中心健康轮询间隔")
		_ = fs.Parse(os.Args[2:])
		var to []string
		for _, s := range strings.Split(*smtpTo, ",") {
			if s = strings.TrimSpace(s); s != "" {
				to = append(to, s)
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := agent.RunRelay(ctx, agent.RelayOptions{
			ServerURL:   *server,
			StateDir:    *stateDir,
			Webhook:     *webhook,
			WebhookType: *webhookType,
			LineName:    *lineName,
			Interval:    *interval,
			SMTPHost:    *smtpHost,
			SMTPPort:    *smtpPort,
			SMTPUser:    *smtpUser,
			SMTPPass:    *smtpPass,
			SMTPFrom:    *smtpFrom,
			SMTPTo:      to,
		}); err != nil {
			log.Fatalf("relay: %v", err)
		}
		return
	}

	var (
		server   = flag.String("server", envOr("NW_SERVER", "http://127.0.0.1:8080"), "中心端地址")
		token    = flag.String("token", envOr("NW_ENROLL_TOKEN", ""), "注册令牌（首次注册用）")
		stateDir = flag.String("state-dir", envOr("NW_STATE_DIR", "/var/lib/netwatch-agent"), "身份与缓存目录")
		name     = flag.String("name", envOr("NW_PROBE_NAME", ""), "探针名称（注册时上报，如：探针1）")
		showVer  = flag.Bool("version", false, "显示版本")
	)
	flag.Parse()
	if *showVer {
		log.Printf("netwatch-agent %s", version)
		return
	}

	a, err := agent.New(agent.Options{
		ServerURL:   *server,
		EnrollToken: *token,
		StateDir:    *stateDir,
		Version:     version,
		Name:        *name,
	})
	if err != nil {
		log.Fatalf("init: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("exit: %v", err)
	}
	log.Printf("agent stopped")
}
