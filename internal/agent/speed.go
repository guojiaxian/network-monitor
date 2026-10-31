package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/showwin/speedtest-go/speedtest"

	"netwatch/internal/proto"
)

// 测速任务执行：中心端全局串行调度，同一时刻只有一个探针在测速。
// 引擎 A：Ookla Speedtest.net 公共节点（开源协议实现，非官方 CLI，无商用许可问题）；
// 引擎 B：公共大文件 HTTP 限时下载（兜底，支持源 IP 绑定）。

const (
	speedDLBudget    = 60 * time.Second  // 下载阶段上限
	speedULBudget    = 45 * time.Second  // 上传阶段上限
	speedHTTPWindow  = 15 * time.Second  // HTTP 引擎单次采样窗口
	speedMinBytes    = 5 << 20           // HTTP 引擎有效判定（≥5MB）
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
	a.log.Printf("speedtest start: engine=%s run_at=%d", task.Engine, task.RunAt)
	var s *proto.Sample
	switch task.Engine {
	case "http":
		s = httpSpeedTest(ctx, task.HTTPURLs, a.autoSource())
	default:
		s = ooklaSpeedTest(ctx)
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
	a.log.Printf("speedtest done: target=%s ok=%v", s.Target, s.OK)
}

// ooklaSpeedTest 引擎 A：就近 Ookla 公共节点，测下载/上传/时延。
func ooklaSpeedTest(ctx context.Context) *proto.Sample {
	st := speedtest.New()
	cctx, cancel := context.WithTimeout(ctx, speedDLBudget+speedULBudget+30*time.Second)
	defer cancel()

	servers, err := st.FetchServerListContext(cctx)
	if err != nil || len(servers) == 0 {
		return speedFailSample("ookla", "获取节点列表失败: "+errString(err))
	}
	srv := servers[0] // 默认按距离排序，取最近节点

	if err := srv.PingTestContext(cctx, nil); err != nil {
		// ping 失败不必然代表测速不可用，继续尝试下载
		_ = err
	}
	dctx, dcancel := context.WithTimeout(cctx, speedDLBudget)
	defer dcancel()
	if err := srv.DownloadTestContext(dctx); err != nil {
		return speedFailSample(srv.Sponsor, "下载测速失败: "+errString(err))
	}
	downMbps := float64(srv.DLSpeed) * 8 / 1e6

	uctx, ucancel := context.WithTimeout(cctx, speedULBudget)
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

// httpSpeedTest 引擎 B：对 URL 池逐个尝试限时下载，计算吞吐。
func httpSpeedTest(ctx context.Context, urls []string, sourceIP string) *proto.Sample {
	var lastErr string
	for _, u := range urls {
		mbps, err := httpDownloadThroughput(ctx, u, sourceIP, speedHTTPWindow)
		if err == nil {
			return &proto.Sample{
				Type: proto.TypeSpeed, Target: u, OK: true,
				Extra: map[string]float64{"down_mbps": mbps},
			}
		}
		lastErr = err.Error()
	}
	return speedFailSample("http", lastErr)
}

// httpDownloadThroughput 限时下载统计吞吐；超时即认为窗口结束，返回速率。
func httpDownloadThroughput(ctx context.Context, url, sourceIP string, window time.Duration) (float64, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if ip := net.ParseIP(sourceIP); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	client := &http.Client{
		Transport: &http.Transport{DialContext: d.DialContext, Proxy: nil, DisableKeepAlives: true},
	}
	wctx, cancel := context.WithTimeout(ctx, window+10*time.Second)
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

	start := time.Now()
	wctx2, cancel2 := context.WithTimeout(wctx, window)
	defer cancel2()
	var total int64
	buf := make([]byte, 128<<10)
	for {
		if wctx2.Err() != nil {
			break // 窗口结束，正常退出计速
		}
		n, rerr := resp.Body.Read(buf)
		total += int64(n)
		if rerr != nil {
			break
		}
	}
	elapsed := time.Since(start)
	if elapsed < 500*time.Millisecond {
		return 0, fmt.Errorf("采样过短")
	}
	if total < speedMinBytes {
		return 0, fmt.Errorf("数据量不足 %d bytes", total)
	}
	return float64(total) * 8 / elapsed.Seconds() / 1e6, nil
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
