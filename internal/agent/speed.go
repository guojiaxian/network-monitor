package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/showwin/speedtest-go/speedtest"

	"netwatch/internal/proto"
)

// 测速任务执行：每探针独立并行（各测各的出口线路，互不干扰）。
// 引擎 A（http，默认）：多连接 CDN 测速——与测速网站同原理（多连接并发 + 限时窗口采样），
//         对国内权威 CDN（源池可配）16 连接循环拉取取聚合吞吐；实测 Ookla speedtest.net
//         的大陆节点已全面退网（最近节点列表全为境外/教育网，2026-11-07 现场验证），
//         故以多连接 CDN 方案为主，结果与运营商口径接近且不依赖第三方私有接口。
// 引擎 B（ookla）：Ookla Speedtest.net 协议（节点优选联通），境外/专线场景备用。
// 一轮任务连测 task.Times 次取平均，作为该轮测速结果上报。

const (
	speedDLBudget   = 60 * time.Second // ookla 下载阶段上限
	speedULBudget   = 45 * time.Second // ookla 上传阶段上限
	speedHTTPWindow = 15 * time.Second // 多连接引擎采样窗口
	speedHTTPConns  = 16               // 多连接引擎并发连接数（单 CDN 流约 20~30Mbps，1C 探针实测 64 连接无增益）
	speedMinBytes   = 50 << 20         // 多连接引擎有效判定（窗口内总字节 ≥50MB，防把握手失败算成低速）
)

type speedState struct {
	LastRunAt int64 `json:"last_run_at"`
}

func (a *Agent) speedStatePath() string { return filepath.Join(a.stateDir, "speedstate.json") }

func (a *Agent) loadSpeedState() speedState {
	var s speedState
	if b, err := os.ReadFile(a.speedStatePath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (a *Agent) saveSpeedState(s speedState) {
	b, _ := json.Marshal(s)
	_ = os.WriteFile(a.speedStatePath(), b, 0o600)
}

// speedLoop 每 15 秒检查中心端下发的测速任务，到点执行一次（先落盘标记防崩溃重测）。
func (a *Agent) speedLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			task := a.currentConfig().SpeedTask
			if task == nil || task.RunAt <= 0 {
				continue
			}
			st := a.loadSpeedState()
			if st.LastRunAt == task.RunAt || time.Now().Unix() < task.RunAt {
				continue
			}
			a.saveSpeedState(speedState{LastRunAt: task.RunAt})
			a.runSpeedTest(ctx, task)
		}
	}
}

func (a *Agent) runSpeedTest(ctx context.Context, task *proto.SpeedTask) {
	times := task.Times
	if times <= 0 {
		times = 1 // 旧中心端无此字段：保持单次行为（协议只增兼容）
	}
	a.log.Printf("speedtest start: engine=%s run_at=%d times=%d", task.Engine, task.RunAt, times)
	var samples []*proto.Sample
	switch task.Engine {
	case "http":
		samples = httpRound(ctx, task.HTTPURLs, a.autoSource(), times)
	default:
		samples = ooklaRound(ctx, times)
	}
	s := averageSample(samples, times)
	if s == nil {
		// 全部失败：上报最后一个失败样本（含错误信息）
		for i := len(samples) - 1; i >= 0; i-- {
			if samples[i] != nil {
				s = samples[i]
				break
			}
		}
	}
	if s == nil {
		return
	}
	s.TS = nowMs()
	select {
	case a.sampleCh <- *s:
	default:
		a.log.Printf("sample queue full, speed result dropped")
	}
	a.log.Printf("speedtest done: target=%s ok=%v down=%.1fMbps runs=%d/%d",
		s.Target, s.OK, s.Extra["down_mbps"], int(s.Extra["ok_runs"]), times)
}

// averageSample 将一轮连测合并为一条平均样本：≥1 次成功即成功，down/up/RTT 取成功样本均值；
// 全部失败返回 nil（由调用方兜底上报失败样本）。
func averageSample(samples []*proto.Sample, times int) *proto.Sample {
	var sumDown, sumUp, sumRTT float64
	n := 0
	target := ""
	for _, s := range samples {
		if s == nil || !s.OK {
			continue
		}
		sumDown += s.Extra["down_mbps"]
		sumUp += s.Extra["up_mbps"]
		sumRTT += s.RTTms
		if target == "" {
			target = s.Target
		}
		n++
	}
	if n == 0 {
		return nil
	}
	return &proto.Sample{
		Type:   proto.TypeSpeed,
		Target: target,
		OK:     true,
		RTTms:  sumRTT / float64(n),
		Extra: map[string]float64{
			"down_mbps": sumDown / float64(n),
			"up_mbps":   sumUp / float64(n),
			"runs":      float64(times),
			"ok_runs":   float64(n),
		},
	}
}

// ooklaRound 引擎 A：Ookla Speedtest.net，一轮连测 times 次（同一节点，保证口径一致可比）。
func ooklaRound(ctx context.Context, times int) []*proto.Sample {
	st := speedtest.New()
	cctx, cancel := context.WithTimeout(ctx, time.Duration(times)*(speedDLBudget+speedULBudget)+60*time.Second)
	defer cancel()

	servers, err := st.FetchServerListContext(cctx)
	if err != nil || len(servers) == 0 {
		return []*proto.Sample{speedFailSample("ookla", "获取节点列表失败: "+errString(err))}
	}
	srv := pickUnicomServer(servers)
	if err := srv.PingTestContext(cctx, nil); err != nil {
		// ping 失败不必然代表测速不可用，继续尝试下载
		_ = err
	}
	out := make([]*proto.Sample, 0, times)
	for i := 0; i < times; i++ {
		out = append(out, ooklaOnce(cctx, srv))
	}
	return out
}

// pickUnicomServer 节点挑选：优先联通节点（Sponsor 含 Unicom/联通），
// 无匹配回退距离最近的节点（库返回列表已按距离排序）。
func pickUnicomServer(servers []*speedtest.Server) *speedtest.Server {
	for _, srv := range servers {
		s := strings.ToLower(srv.Sponsor)
		if strings.Contains(s, "unicom") || strings.Contains(srv.Sponsor, "联通") {
			return srv
		}
	}
	return servers[0]
}

// ooklaOnce 单次 Ookla 测速（下载/上传/时延），复用已选节点与已建连接参数。
func ooklaOnce(ctx context.Context, srv *speedtest.Server) *proto.Sample {
	dctx, dcancel := context.WithTimeout(ctx, speedDLBudget)
	defer dcancel()
	if err := srv.DownloadTestContext(dctx); err != nil {
		return speedFailSample(srv.Sponsor, "下载测速失败: "+errString(err))
	}
	downMbps := float64(srv.DLSpeed) * 8 / 1e6

	uctx, ucancel := context.WithTimeout(ctx, speedULBudget)
	defer ucancel()
	upMbps := 0.0
	if err := srv.UploadTestContext(uctx); err == nil {
		upMbps = float64(srv.ULSpeed) * 8 / 1e6
	}

	return &proto.Sample{
		Type:   proto.TypeSpeed,
		Target: fmt.Sprintf("%s|%s", srv.Sponsor, srv.Name),
		OK:     true,
		RTTms:  float64(srv.Latency.Microseconds()) / 1000.0,
		Extra:  map[string]float64{"down_mbps": downMbps, "up_mbps": upMbps},
	}
}

// httpRound 引擎 A：多连接 CDN 测速，连测 times 次（与 ookla 引擎同轮次口径）。
func httpRound(ctx context.Context, urls []string, sourceIP string, times int) []*proto.Sample {
	out := make([]*proto.Sample, 0, times)
	for i := 0; i < times; i++ {
		out = append(out, httpSpeedTest(ctx, urls, sourceIP))
	}
	return out
}

// httpSpeedTest 引擎 A 单次：多连接并发对源池 CDN 限时采样，聚合吞吐。
// 连接 i 绑定源池第 i%len(urls) 个 URL（多源分摊单源限速）；窗口内文件下完即重发
// （循环拉取，文件大小不构成瓶颈）；聚合吞吐 = 总字节 / 窗口实际时长。
func httpSpeedTest(ctx context.Context, urls []string, sourceIP string) *proto.Sample {
	if len(urls) == 0 {
		return speedFailSample("http", "下载源池为空")
	}
	ctx, cancel := context.WithTimeout(ctx, speedHTTPWindow+15*time.Second)
	defer cancel()

	d := &net.Dialer{Timeout: 10 * time.Second}
	if ip := net.ParseIP(sourceIP); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: d.DialContext, Proxy: nil, DisableKeepAlives: true,
			MaxIdleConnsPerHost: speedHTTPConns,
		},
	}

	var total int64
	var mu sync.Mutex
	var firstErr error
	start := time.Now()
	deadline := start.Add(speedHTTPWindow)
	var wg sync.WaitGroup
	for i := 0; i < speedHTTPConns; i++ {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				n, err := httpWindowFetch(ctx, client, u, deadline)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return // 该连接失败即退出，其余连接继续（≥1 连接成功仍可计速）
				}
				if n == 0 {
					return
				}
				mu.Lock()
				total += n
				mu.Unlock()
			}
		}(urls[i%len(urls)])
	}
	wg.Wait()
	elapsed := time.Since(start)

	if total < speedMinBytes {
		msg := fmt.Sprintf("窗口内数据量不足 %dMB", speedMinBytes>>20)
		if firstErr != nil {
			msg = firstErr.Error()
		}
		return speedFailSample("http", msg)
	}
	return &proto.Sample{
		Type:   proto.TypeSpeed,
		Target: fmt.Sprintf("cdn-multi×%dconn", speedHTTPConns),
		OK:     true,
		Extra:  map[string]float64{"down_mbps": float64(total) * 8 / elapsed.Seconds() / 1e6},
	}
}

// httpWindowFetch 单连接一次拉取：从当前 URL 下载到窗口结束或文件结束（EOF），
// 返回字节数；EOF 返回已计字节数（调用方循环重拉，防小文件提前结束拉不满窗口）。
func httpWindowFetch(ctx context.Context, client *http.Client, url string, deadline time.Time) (int64, error) {
	wctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(wctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "netwatch-agent/1.0")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var total int64
	buf := make([]byte, 128<<10)
	for {
		if wctx.Err() != nil {
			return total, nil // 窗口结束，正常计速
		}
		n, rerr := resp.Body.Read(buf)
		total += int64(n)
		if rerr != nil {
			if total > 0 {
				return total, nil // 文件结束：本次拉取完成
			}
			return 0, rerr
		}
	}
}

func speedFailSample(engine, msg string) *proto.Sample {
	if len(msg) > 80 {
		msg = msg[:80]
	}
	return &proto.Sample{
		Type: proto.TypeSpeed, Target: fmt.Sprintf("%s|%s", engine, msg), OK: false,
		Extra: map[string]float64{"err": 1},
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
