package agent

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"netwatch/internal/proto"
)

var srcCache atomic.Value // string：出口源 IP

// probeLoops 探测主循环：按当前配置为每类探测启动独立 goroutine，
// 配置版本变化（reloadCh）时全部退出并重建。
func (a *Agent) probeLoops(ctx context.Context) {
	for {
		cfg := a.currentConfig()
		loopCtx, cancel := context.WithCancel(ctx)

		go startSourceRefresher(loopCtx.Done(), &srcCache, time.Minute)

		var wg sync.WaitGroup
		launch := func(interval time.Duration, fn func(context.Context) *proto.Sample) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.probeLoop(loopCtx, interval, fn)
			}()
		}

		timeout := time.Duration(cfg.TimeoutS) * time.Second
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		// 重试次数/间隔来自下发的探测配置（旧中心缺省=0 时回退 1 次 / 300ms，字段只增兼容）
		retries := cfg.RetryN
		if retries <= 0 {
			retries = 1
		}
		retryPause := time.Duration(cfg.RetryPauseMs) * time.Millisecond
		if retryPause <= 0 {
			retryPause = probeRetryPause
		}

		gw := detectGateway()
		if gw != "" {
			g := gw
			launch(pingInterval(cfg), func(c context.Context) *proto.Sample {
				return a.doPing(c, g, true, timeout, retries, retryPause)
			})
		} else {
			a.log.Printf("warning: default gateway not detected, gateway probe disabled")
		}
		for _, t := range cfg.ICMPTargets {
			t := t
			launch(pingInterval(cfg), func(c context.Context) *proto.Sample {
				// 公网目标每轮 5 包聚合为一条样本：OK=收到任意包，LossPct=本轮丢包率。
				// 不逐包重试（重试会掩盖真实丢包）；网关探测仍走 doPing 的单包+重试。
				rtt, ok, loss, err := pingBurst(c, t, a.autoSource(), timeout, PingBurstCount)
				s := &proto.Sample{Type: proto.TypePing, Target: t, TS: nowMs(), OK: ok, RTTms: rtt, LossPct: loss}
				if err != nil {
					s.Extra = map[string]float64{"err": 1}
				}
				return s
			})
		}
		for _, t := range cfg.TCPTargets {
			t := t
			launch(tcpInterval(cfg), func(c context.Context) *proto.Sample {
				rtt, ok, err := probeRetry(retries, retryPause, func() (float64, bool, error) {
					return tcpOnce(c, t, a.sourceIPOf(cfg), timeout)
				})
				s := &proto.Sample{Type: proto.TypeTCP, Target: t, TS: nowMs(), OK: ok, RTTms: rtt}
				if err != nil {
					s.Extra = map[string]float64{"err": 1}
				}
				return s
			})
		}
		for _, s := range cfg.DNSServers {
			s := s
			launch(dnsInterval(cfg), func(c context.Context) *proto.Sample {
				rtt, ok, err := probeRetry(retries, retryPause, func() (float64, bool, error) {
					return dnsOnce(c, s, cfg.DNSDomain, a.sourceIPOf(cfg), timeout)
				})
				sm := &proto.Sample{Type: proto.TypeDNS, Target: s, TS: nowMs(), OK: ok, RTTms: rtt}
				if err != nil {
					sm.Extra = map[string]float64{"err": 1}
				}
				return sm
			})
		}
		for _, u := range cfg.HTTPTargets {
			u := u
			launch(httpInterval(cfg), func(c context.Context) *proto.Sample {
				var code int
				var certDays float64
				ttfb, ok, _ := probeRetry(retries, retryPause, func() (float64, bool, error) {
					v, c2, o, d, err := httpOnce(c, u, a.sourceIPOf(cfg), timeout)
					code, certDays = c2, d // 重试成功那次为准（失败时零值，样本不引用）
					return v, o, err
				})
				sm := &proto.Sample{Type: proto.TypeHTTP, Target: u, TS: nowMs(), OK: ok, RTTms: ttfb}
				if ok {
					sm.Extra = map[string]float64{"code": float64(code)}
					if certDays > 0 {
						sm.Extra["cert_days"] = certDays
					}
				} else {
					sm.Extra = map[string]float64{"err": 1}
				}
				return sm
			})
		}

		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()
			return
		case <-a.reloadCh:
			cancel()
			wg.Wait()
		}
	}
}

func pingInterval(c proto.ProbeConfig) time.Duration { return jitter(c.IntervalPingS, 10) }
func tcpInterval(c proto.ProbeConfig) time.Duration  { return jitter(c.IntervalTCPS, 10) }
func dnsInterval(c proto.ProbeConfig) time.Duration  { return jitter(c.IntervalDNSS, 10) }
func httpInterval(c proto.ProbeConfig) time.Duration { return jitter(c.IntervalHTTPS, 10) }

// jitter 返回 seconds±pct% 的抖动周期，避免所有探测对齐同时发出。
func jitter(seconds, pct int) time.Duration {
	if seconds <= 0 {
		seconds = 10
	}
	base := time.Duration(seconds) * time.Second
	if pct <= 0 {
		return base
	}
	delta := time.Duration(int64(base) * int64(pct) / 100)
	return base + time.Duration(rand.Int63n(2*int64(delta)+1)) - delta
}

// probeLoop 单个探测目标的循环，结果投递到 sampleCh（满则丢弃并计数）。
func (a *Agent) probeLoop(ctx context.Context, interval time.Duration, fn func(context.Context) *proto.Sample) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var dropped int
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s := fn(ctx)
			if s == nil {
				continue
			}
			select {
			case a.sampleCh <- *s:
			default:
				dropped++
				if dropped%1000 == 1 {
					a.log.Printf("sample queue full, dropped=%d", dropped)
				}
			}
		}
	}
}

func (a *Agent) doPing(ctx context.Context, target string, isGW bool, timeout time.Duration, retries int, retryPause time.Duration) *proto.Sample {
	src := a.autoSource()
	rtt, ok, err := probeRetry(retries, retryPause, func() (float64, bool, error) {
		return pingOnce(ctx, target, src, timeout)
	})
	s := &proto.Sample{Type: proto.TypePing, Target: target, TS: nowMs(), OK: ok, RTTms: rtt}
	if ok {
		s.LossPct = 0
	} else {
		s.LossPct = 100
	}
	if isGW {
		if s.Extra == nil {
			s.Extra = map[string]float64{}
		}
		s.Extra["gw"] = 1
	}
	if err != nil {
		if s.Extra == nil {
			s.Extra = map[string]float64{}
		}
		s.Extra["err"] = 1
	}
	return s
}

// heartbeatInterval 心跳间隔（agent_config.heartbeat_sec，缺省/越界回退 15s；下限 5s 防误配打爆中心）
func (a *Agent) heartbeatInterval() time.Duration {
	sec := a.currentConfig().HeartbeatSec
	if sec < 5 || sec > 60 {
		sec = 15
	}
	return time.Duration(sec) * time.Second
}

// heartbeatLoop 心跳：同时驱动中心端"监控盲区"判定。
// 心跳间隔来自下发的探测配置（agent_config.heartbeat_sec，缺省 15s），
// 每轮发送后与当前周期比对，配置变更即时重置节奏（无需重启探针）。
func (a *Agent) heartbeatLoop(ctx context.Context) {
	interval := a.heartbeatInterval()
	t := time.NewTicker(interval)
	defer t.Stop()
	var cpuPrev [2]uint64 // 上次心跳的 CPU 累计节拍（idle, total），差分算使用率
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if want := a.heartbeatInterval(); want != interval {
				interval = want
				t.Reset(interval)
			}
			mem, load := readMemLoad()
			cpuPct, memPct, diskPct := readSelfHealth(&cpuPrev)
			isp, geo, ipv6 := a.egressExtras()
			hb := proto.Heartbeat{
				Version:   a.opts.Version,
				UptimeS:   uptimeS(),
				MemUsedMB: mem,
				Load1:     load,
				PublicIP:  a.egressIPOf(),
				IPISP:     isp,
				IPGeo:     geo,
				IPv6:      ipv6,
				CPUPct:    cpuPct,
				MemPct:    memPct,
				DiskPct:   diskPct,
				AgentTS:   time.Now().UnixMilli(),
				SysInfo:   a.sysInfo,
			}
			var hbResp struct {
				RefreshEgress bool `json:"refresh_egress"`
			}
			if err := a.agentReq(ctx, "POST", "/api/agent/heartbeat", &hb, &hbResp); err != nil {
				if err == errReRegister {
					a.abort(errReRegister)
					return
				}
				a.log.Printf("heartbeat: %v", err)
				continue
			}
			if hbResp.RefreshEgress {
				// UI"立即刷新"：马上重探出口三件套（IP/运营商/归属地/IPv6）并立即回报，
				// 不等 5 分钟周期（策略路由调整后可即时看到新出口）
				a.log.Printf("egress refresh requested by center, re-probing now")
				a.refreshEgress(ctx)
				hb.PublicIP = a.egressIPOf()
				hb.IPISP, hb.IPGeo, hb.IPv6 = a.egressExtras()
				hb.AgentTS = time.Now().UnixMilli()
				if err := a.agentReq(ctx, "POST", "/api/agent/heartbeat", &hb, nil); err != nil {
					a.log.Printf("heartbeat: %v", err)
				}
			}
		}
	}
}
