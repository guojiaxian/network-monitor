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
	"syscall"

	"netwatch/internal/agent"
)

var version = "1.0.0"

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
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
