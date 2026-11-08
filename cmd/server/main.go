// netwatch-server 中心端服务。
// 组成：探针接入 API + 管理端 API + VictoriaMetrics 写入 + 告警引擎 + SMTP 邮件。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"netwatch/internal/server"
)

var version = "1.0.0"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	cfg := server.LoadConfig()
	cfg.Version = version
	startedAt := time.Now()

	_ = os.MkdirAll(filepath.Join(cfg.DataDir, "agent-releases"), 0o755)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := server.NewStore(ctx, cfg.DBDSN)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer store.Close()

	tsdb := server.NewTSDB(cfg.VMURL)
	eng := server.NewEngine(cfg, store, tsdb)
	sched := server.NewSpeedSched(store)
	shipper := server.NewLogShipper(cfg, store) // 日志外发（syslog → 奇安信审计服务器）
	eng.SetShipper(shipper)
	eng.SetSched(sched)
	eng.Start(ctx)
	sched.Start(ctx)

	rep := server.NewReporter(store, tsdb, cfg)
	rep.SetMailFunc(func(subject, html string) error {
		var smtpCfg server.SMTPSettings
		if err := store.GetSetting(context.Background(), "smtp", &smtpCfg); err != nil || !smtpCfg.Valid() {
			return fmt.Errorf("smtp not configured")
		}
		return server.SendMail(smtpCfg, subject, html)
	})
	rep.Start(ctx)

	snmp := server.NewSnmpPoller(store, tsdb)
	snmp.Start(ctx)

	mon := server.NewMonitorRunner(store, tsdb, eng)
	mon.Start(ctx)

	ind := server.NewIndustryRunner(store, tsdb, eng)
	ind.Start(ctx)

	scan := server.NewScanRunner(store)
	scan.SetShipper(shipper)
	scan.Start(ctx)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	r.Head("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})

	// health API：JSON 返回状态/版本/运行时长/依赖检查（公开，供运维与监控探针使用）
	r.Get("/api/health", func(w http.ResponseWriter, req *http.Request) {
		cctx, ccancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer ccancel()
		dbOK := store.Ping(cctx) == nil
		vmOK := false
		if resp, err := http.Get(cfg.VMURL + "/health"); err == nil {
			vmOK = resp.StatusCode == 200
			resp.Body.Close()
		}
		status := "ok"
		if !dbOK || !vmOK {
			status = "degraded"
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":   status,
			"version":  cfg.Version,
			"uptime_s": int(time.Since(startedAt).Seconds()),
			"checks":   map[string]bool{"db": dbOK, "vm": vmOK},
			"time":     time.Now().Format(time.RFC3339),
		})
	})

	r.Route("/api/agent", server.NewAgentAPI(cfg, store, eng, sched).Routes)
	r.Route("/api/admin", server.NewAdminAPI(cfg, store, eng, sched, rep, mon, ind, tsdb, scan).Routes)
	r.Route("/api/screen", server.NewScreenAPI(store, eng, mon, ind, tsdb).Routes)

	// 前端静态文件（web/dist 存在时挂载；SPA 路由回退到 index.html）
	if st, err := os.Stat(cfg.WebDir); err == nil && st.IsDir() {
		index := filepath.Join(cfg.WebDir, "index.html")
		r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
			p := filepath.Join(cfg.WebDir, filepath.Clean("/"+strings.TrimPrefix(req.URL.Path, "/")))
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				http.ServeFile(w, req, p)
				return
			}
			// index.html 只发 no-cache（要求浏览器每次回源校验 Last-Modified）：
			// 其引用的 assets 文件名带内容哈希可长期缓存，但 index.html 若被浏览器
			// 启发式缓存，升级后用户会一直加载旧 bundle，表现为"发布了页面没变化"。
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, req, index)
		})
		log.Printf("serving web ui from %s", cfg.WebDir)
	} else {
		log.Printf("web dir %s not found, api-only mode (前端未部署)", cfg.WebDir)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("netwatch-server %s listening on %s", version, cfg.Listen)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shCtx)
	log.Printf("server stopped")
}
