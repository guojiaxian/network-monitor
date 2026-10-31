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

		gw := ""
		if !cfg.SkipGateway {
			gw = detectGateway()
		}
		if gw != "" {
			g := gw
			launch(pingInterval(cfg), func(c context.Context) *proto.Sample {
				return a.doPing(c, g, true, timeout)
			})
		} else {
			a.log.Printf("warning: default gateway not detected, gateway probe disabled")
		}
		for _, t := range cfg.ICMPTargets {
			t := t
			launch(pingInterval(cfg), func(c context.Context) *proto.Sample {
				return a.doPing(c, t, false, timeout)
			})
		}
		for _, t := range cfg.TCPTargets {
			t := t
			launch(tcpInterval(cfg), func(c context.Context) *proto.Sample {
				rtt, ok, err := tcpOnce(c, t, a.sourceIPOf(cfg), timeout)
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
				rtt, ok, err := dnsOnce(c, s, cfg.DNSDomain, a.sourceIPOf(cfg), timeout)
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
				ttfb, code, ok, certDays, _ := httpOnce(c, u, a.sourceIPOf(cfg), timeout)
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

func (a *Agent) doPing(ctx context.Context, target string, isGW bool, timeout time.Duration) *proto.Sample {
	src := a.autoSource()
	rtt, ok, err := pingOnce(ctx, target, src, timeout)
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

// heartbeatLoop 心跳：同时驱动中心端"监控盲区"判定。
func (a *Agent) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			mem, load := readMemLoad()
			hb := proto.Heartbeat{
				Version:   a.opts.Version,
				UptimeS:   uptimeS(),
				MemUsedMB: mem,
				Load1:     load,
			}
			if err := a.agentReq(ctx, "POST", "/api/agent/heartbeat", &hb, nil); err != nil {
				if err == errReRegister {
					a.abort(errReRegister)
					return
				}
				a.log.Printf("heartbeat: %v", err)
			}
		}
	}
}
