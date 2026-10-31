// Package agent 实现楼层探针：按中心端下发的配置对公网/网关做连通性探测，
// 批量上报指标；与中心失联时本地缓存补传。探针部署在楼层子网内，
// 出站流量经防火墙策略路由天然走本楼层宽带线路。
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"netwatch/internal/proto"
)

// errReRegister 触发身份重置重新注册（探针在服务端被删除、或凭证失效）
var errReRegister = errors.New("need re-register")

type Options struct {
	ServerURL   string // 中心端基地址，如 http://10.8.0.10:8080
	EnrollToken string // 注册令牌（中心端设置）
	StateDir    string // 身份与缓存目录
	Version     string // agent 版本
	Name        string // 探针名称（部署时由安装脚本传入，注册时上报）
}

type Agent struct {
	opts   Options
	log    *log.Logger
	httpc  *http.Client
	stateDir string

	identMu sync.RWMutex
	ident   Identity

	cfgMu    sync.RWMutex
	cfg      proto.ProbeConfig
	reloadCh chan struct{}

	sampleCh chan proto.Sample

	abortMu     sync.Mutex
	abortReason error
	cancelMu    sync.Mutex
	cycleCancel context.CancelFunc
}

func New(opts Options) (*Agent, error) {
	if opts.ServerURL == "" {
		return nil, fmt.Errorf("server url is required")
	}
	if opts.StateDir == "" {
		opts.StateDir = "/var/lib/netwatch-agent"
	}
	if err := os.MkdirAll(opts.StateDir, 0o755); err != nil {
		return nil, err
	}
	return &Agent{
		opts:     opts,
		log:      log.New(os.Stderr, "agent ", log.LstdFlags|log.Lmsgprefix),
		stateDir: opts.StateDir,
		httpc:    &http.Client{Timeout: 15 * time.Second},
		reloadCh: make(chan struct{}, 1),
		sampleCh: make(chan proto.Sample, 4096),
	}, nil
}

func (a *Agent) cachePath() string { return filepath.Join(a.stateDir, "cache.jsonl") }

// Run 主循环：注册 → 启动探测/心跳/上报 → 配置热更新或凭证失效时整周期重启。
func (a *Agent) Run(ctx context.Context) error {
	a.log.Printf("netwatch agent %s, server=%s, state=%s", a.opts.Version, a.opts.ServerURL, a.stateDir)
	for {
		err := a.cycle(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errReRegister) {
			a.log.Printf("identity rejected by server, re-registering")
			a.clearIdentity()
		} else if err != nil {
			a.log.Printf("cycle error: %v, retry in 5s", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// cycle 单个完整工作周期；返回 errReRegister 时外层循环重置身份。
func (a *Agent) cycle(ctx context.Context) error {
	cctx, cancel := context.WithCancel(ctx)
	a.cancelMu.Lock()
	a.cycleCancel = cancel
	a.cancelMu.Unlock()
	defer func() {
		a.cancelMu.Lock()
		a.cycleCancel = nil
		a.cancelMu.Unlock()
		cancel()
	}()

	if !a.hasIdentity() {
		if err := a.register(cctx); err != nil {
			return fmt.Errorf("register: %w", err)
		}
	} else if err := a.refreshConfig(cctx); err != nil {
		return err // 凭证失效会包装为 errReRegister
	}

	rep := newReporter(a)
	go rep.run(cctx)

	go a.probeLoops(cctx)
	go a.speedLoop(cctx)
	go a.heartbeatLoop(cctx)
	go a.configWatch(cctx)

	select {
	case <-cctx.Done():
		a.abortMu.Lock()
		reason := a.abortReason
		a.abortReason = nil
		a.abortMu.Unlock()
		return reason // nil 表示外部取消
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Agent) abort(err error) {
	a.abortMu.Lock()
	if a.abortReason == nil {
		a.abortReason = err
	}
	a.abortMu.Unlock()
	// 立即终止当前周期（探针循环/心跳/上报），让外层重新注册
	a.cancelMu.Lock()
	c := a.cycleCancel
	a.cancelMu.Unlock()
	if c != nil {
		c()
	}
}

func (a *Agent) currentConfig() proto.ProbeConfig {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

func (a *Agent) applyConfig(c proto.ProbeConfig, signal bool) {
	a.cfgMu.Lock()
	old := a.cfg
	a.cfg = c
	a.cfgMu.Unlock()
	// 仅当探测目标/参数变化时重启探测循环；测速任务等字段变化无需重启
	if signal && targetsFingerprint(old) != targetsFingerprint(c) {
		select {
		case a.reloadCh <- struct{}{}:
		default:
		}
	}
}

// targetsFingerprint 影响探测循环的配置指纹
func targetsFingerprint(c proto.ProbeConfig) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s|%s",
		c.Version,
		c.SourceIP,
		strings.Join(c.ICMPTargets, ","), strings.Join(c.TCPTargets, ","),
		strings.Join(c.DNSServers, ","), c.DNSDomain,
		strings.Join(c.HTTPTargets, ","),
		fmt.Sprintf("%d,%d,%d,%d,%d", c.IntervalPingS, c.IntervalTCPS, c.IntervalDNSS, c.IntervalHTTPS, c.TimeoutS))
}

// sourceIPOf 返回本次探测应绑定的源 IP：配置显式指定则用之，auto 模式读周期探测缓存。
func (a *Agent) sourceIPOf(cfg proto.ProbeConfig) string {
	if cfg.SourceIP != "" && cfg.SourceIP != "auto" {
		return cfg.SourceIP
	}
	return a.autoSource()
}

func (a *Agent) autoSource() string {
	if v := srcCache.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
