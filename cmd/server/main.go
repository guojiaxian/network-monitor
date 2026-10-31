// netwatch-server 中心端服务。
// 组成：探针接入 API + 管理端 API + VictoriaMetrics 写入 + 告警引擎 + SMTP 邮件。
package main

import (
	"context"
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

var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	cfg := server.LoadConfig()

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

	r.Route("/api/agent", server.NewAgentAPI(cfg, store, eng, sched).Routes)
	r.Route("/api/admin", server.NewAdminAPI(cfg, store, eng, sched, rep, mon, tsdb).Routes)
	r.Route("/api/screen", server.NewScreenAPI(store, eng, mon).Routes)

	// 前端静态文件（web/dist 存在时挂载；SPA 路由回退到 index.html）
	if st, err := os.Stat(cfg.WebDir); err == nil && st.IsDir() {
		index := filepath.Join(cfg.WebDir, "index.html")
		r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
			p := filepath.Join(cfg.WebDir, filepath.Clean("/"+strings.TrimPrefix(req.URL.Path, "/")))
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				http.ServeFile(w, req, p)
				return
			}
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
