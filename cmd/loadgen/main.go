// loadgen 压测与数据回放工具（仅测试环境使用，勿在生产运行）。
// 模拟 N 台探针：注册 → 按真实指标结构回填 N 天历史数据 → 可选实时流。
// 用途：验证中心端入库吞吐、VictoriaMetrics 容量、报表/曲线查询性能。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"netwatch/internal/proto"
)

type simProbe struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
	Index  int    `json:"index"`
}

func main() {
	server := flag.String("server", "http://127.0.0.1:28765", "中心端地址")
	token := flag.String("token", "test-token", "注册令牌")
	probes := flag.Int("probes", 12, "模拟探针数")
	backfillDays := flag.Int("backfill", 0, "回填历史天数（0=不回填）")
	liveMinutes := flag.Int("live", 0, "实时流持续分钟数（0=不开）")
	batch := flag.Int("batch", 500, "每次上报样本数")
	state := flag.String("state", "/tmp/loadgen-state.json", "身份持久化文件（跨次运行复用探针身份，便于先绑定后回填）")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.Printf("loadgen: %d probes, backfill=%dd, live=%dmin, server=%s", *probes, *backfillDays, *liveMinutes, *server)

	start := time.Now()
	ps := loadIdentities(*state)
	var wg sync.WaitGroup
	for i := 0; i < *probes; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := ps[idx]
			if p == nil {
				p = register(*server, *token, idx)
				saveIdentity(*state, idx, p)
				log.Printf("probe-%02d: registered as %s", idx, p.ID)
			} else {
				log.Printf("probe-%02d: reuse identity %s", idx, p.ID)
			}
			if *backfillDays > 0 {
				t0 := time.Now()
				n := backfill(p, *server, *backfillDays, *batch)
				log.Printf("probe-%02d: backfilled %d samples in %s", idx, n, time.Since(t0).Round(time.Second))
			}
			if *liveMinutes > 0 {
				live(p, *server, *liveMinutes)
			}
		}(i)
	}
	wg.Wait()
	log.Printf("loadgen done in %s", time.Since(start).Round(time.Second))
}

func loadIdentities(path string) []*simProbe {
	b, err := os.ReadFile(path)
	if err != nil {
		return make([]*simProbe, 64)
	}
	var m map[int]*simProbe
	if json.Unmarshal(b, &m) != nil {
		return make([]*simProbe, 64)
	}
	out := make([]*simProbe, 64)
	for i, p := range m {
		if i < len(out) {
			out[i] = p
		}
	}
	return out
}

func saveIdentity(path string, idx int, p *simProbe) {
	mu.Lock()
	defer mu.Unlock()
	ps := loadIdentitiesNoLock(path)
	ps[idx] = p
	b, _ := json.MarshalIndent(ps, "", "  ")
	_ = os.WriteFile(path, b, 0o600)
}

func loadIdentitiesNoLock(path string) map[int]*simProbe {
	m := map[int]*simProbe{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

var mu sync.Mutex

func register(server, token string, idx int) *simProbe {
	req := proto.RegisterReq{
		EnrollToken: token,
		Hostname:    fmt.Sprintf("loadgen-%02d", idx),
		OS:          "linux", Arch: "amd64", AgentVer: "loadgen",
	}
	b, _ := json.Marshal(&req)
	resp, err := http.Post(server+"/api/agent/register", "application/json", bytes.NewReader(b))
	must(err)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("register: HTTP %d", resp.StatusCode)
	}
	var out proto.RegisterResp
	must(json.NewDecoder(resp.Body).Decode(&out))
	return &simProbe{ID: out.AgentID, Secret: out.Secret, Index: idx}
}

// post 发送一批样本（带历史时间戳也可，服务端按样本时间入库）
func post(p *simProbe, server string, samples []proto.Sample) {
	b, _ := json.Marshal(&proto.MetricsBatch{AgentID: p.ID, Samples: samples})
	req, err := http.NewRequest("POST", server+"/api/agent/metrics", bytes.NewReader(b))
	must(err)
	req.Header.Set("X-NW-Agent", p.ID)
	req.Header.Set("X-NW-Secret", p.Secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	must(err)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("probe-%02d metrics: HTTP %d", p.Index, resp.StatusCode)
	}
}

// backfill 按 60s 粒度回填：公网 ping×3 + 网关、TCP/DNS×2 每 5 分钟、HTTP 每 10 分钟、测速每小时
func backfill(p *simProbe, server string, days, batch int) int {
	end := time.Now().Truncate(time.Minute)
	start := end.Add(-time.Duration(days) * 24 * time.Hour)
	rttBase := 12.0 + float64(p.Index)*0.8

	var buf []proto.Sample
	sent := 0
	flush := func() {
		if len(buf) == 0 {
			return
		}
		post(p, server, buf)
		sent += len(buf)
		buf = buf[:0]
	}

	for ts := start; !ts.After(end); ts = ts.Add(time.Minute) {
		t := ts.UnixMilli()
		jitter := rttBase + rand.Float64()*4 - 2
		// 公网 ping ×3（114 模拟约 8% 丢包，其余健康）
		for ti, tgt := range []string{"223.5.5.5", "119.29.29.29", "114.114.114.114"} {
			ok := !(ti == 2 && rand.Float64() < 0.08)
			rtt := jitter + float64(ti)*1.5 + rand.Float64()*2
			buf = append(buf, proto.Sample{Type: "ping", Target: tgt, TS: t, OK: ok,
				RTTms: rtt, LossPct: map[bool]float64{true: 0, false: 100}[ok]})
		}
		// 网关
		buf = append(buf, proto.Sample{Type: "ping", Target: fmt.Sprintf("10.8.%d.1", 25+p.Index), TS: t,
			OK: true, RTTms: 0.4 + rand.Float64()*0.3, LossPct: 0, Extra: map[string]float64{"gw": 1}})
		// TCP/DNS 每 5 分钟
		if ts.Minute()%5 == 0 {
			for _, tgt := range []string{"www.qq.com:443", "www.baidu.com:443"} {
				buf = append(buf, proto.Sample{Type: "tcp", Target: tgt, TS: t, OK: true,
					RTTms: jitter + 8 + rand.Float64()*10})
			}
			for _, srv := range []string{"223.5.5.5", "114.114.114.114"} {
				buf = append(buf, proto.Sample{Type: "dns", Target: srv, TS: t, OK: true,
					RTTms: 2 + rand.Float64()*4})
			}
		}
		// HTTP 每 10 分钟
		if ts.Minute()%10 == 0 {
			buf = append(buf, proto.Sample{Type: "http", Target: "http://www.qq.com/generate_204", TS: t,
				OK: true, RTTms: 30 + rand.Float64()*40, Extra: map[string]float64{"code": 204}})
		}
		// 测速每小时（300M 契约附近波动 + 偶发低值）
		if ts.Minute() == 0 && rand.Float64() < 0.9 {
			down := 260 + rand.Float64()*60
			if rand.Float64() < 0.06 {
				down = 150 + rand.Float64()*40 // 偶发低值
			}
			buf = append(buf, proto.Sample{Type: "speed", Target: "sim-node", TS: t, OK: true,
				RTTms: 8 + rand.Float64()*4, Extra: map[string]float64{"down_mbps": down, "up_mbps": down / 8}})
		}
		if len(buf) >= batch {
			flush()
		}
	}
	flush()
	return sent
}

// live 实时流：ping 每 10s、心跳每 15s，持续 minutes 分钟
func live(p *simProbe, server string, minutes int) {
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	ping := time.NewTicker(10 * time.Second)
	hb := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	defer hb.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ping.C:
			rtt := 12.0 + float64(p.Index)*0.8 + rand.Float64()*4 - 2
			post(p, server, []proto.Sample{{
				Type: "ping", Target: "223.5.5.5", TS: time.Now().UnixMilli(),
				OK: true, RTTms: rtt, LossPct: 0,
			}})
		case <-hb.C:
			b, _ := json.Marshal(&proto.Heartbeat{AgentID: p.ID, Version: "loadgen", UptimeS: 1000, MemUsedMB: 120, Load1: 0.1})
			req, _ := http.NewRequest("POST", server+"/api/agent/heartbeat", bytes.NewReader(b))
			req.Header.Set("X-NW-Agent", p.ID)
			req.Header.Set("X-NW-Secret", p.Secret)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			must(err)
			resp.Body.Close()
		}
	}
}

func must(err error) {
	if err != nil {
		log.Fatalf("loadgen: %v", err)
	}
}
