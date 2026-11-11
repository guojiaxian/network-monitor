package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

// 探测实现均支持绑定源 IP，确保流量经策略路由从本楼层线路出站。

// probeRetryPause 重试前短暂等待（给瞬时拥塞/丢包恢复留出间隔）。
const probeRetryPause = 300 * time.Millisecond

// PingBurstCount 公网 ICMP 每轮发包数（方案C）：一轮 5 包聚合为一条带丢包率的样本。
// 网关探测仍单包（网关只用于楼层中断判定，不需要丢包率精度）。
const PingBurstCount = 5

// probeRetry 探测重试包装：失败后短暂等待重试，任一次成功即成功（返回成功那次的值），
// 连续失败才判断连（返回最后一次错误）——偶发丢包/瞬时抖动不产生失败样本。
// 重试次数与等待间隔来自下发的探测配置（agent_config.retry_n / retry_pause_ms，
// 缺省/越界由调用方回退到 1 次 / probeRetryPause）。调用方主动取消（context.Canceled）不重试。
func probeRetry(retries int, pause time.Duration, fn func() (float64, bool, error)) (float64, bool, error) {
	rtt, ok, err := fn()
	for i := 0; !ok && i < retries; i++ {
		if errors.Is(err, context.Canceled) {
			return rtt, ok, err
		}
		if pause <= 0 {
			pause = probeRetryPause
		}
		time.Sleep(pause)
		rtt, ok, err = fn()
	}
	return rtt, ok, err
}

// pingOnce 发一个 ICMP 包。target 为 IP 或可解析主机名。
func pingOnce(ctx context.Context, target, sourceIP string, timeout time.Duration) (rttMs float64, ok bool, err error) {
	p := probing.New(target)
	p.SetPrivileged(true) // 需 root 或 CAP_NET_RAW（systemd 单元已授权）
	p.Count = 1
	p.Timeout = timeout
	if sourceIP != "" {
		p.Source = sourceIP
	}
	if err = p.Run(); err != nil {
		return 0, false, err
	}
	st := p.Statistics()
	if st.PacketsRecv == 0 {
		return 0, false, fmt.Errorf("icmp timeout")
	}
	return float64(st.AvgRtt.Microseconds()) / 1000.0, true, nil
}

// pingBurst 对一个目标并发发 count 个单包 ICMP，聚合为本轮结果：
// OK=至少收到 1 包，LossPct=丢包比例（%），RTT=成功包平均时延。
// 本轮不做逐包重试——重试会把真实丢包掩盖成"重试后失败率"。
func pingBurst(ctx context.Context, target, sourceIP string, timeout time.Duration, count int) (rttMs float64, ok bool, lossPct float64, err error) {
	if count <= 0 {
		count = PingBurstCount
	}
	if cerr := ctx.Err(); cerr != nil {
		return 0, false, 100, cerr
	}
	type pingResult struct {
		rtt float64
		ok  bool
		err error
	}
	ch := make(chan pingResult, count)
	for i := 0; i < count; i++ {
		go func() {
			r, o, e := pingOnce(ctx, target, sourceIP, timeout)
			ch <- pingResult{r, o, e}
		}()
	}
	recv := 0
	var sum float64
	var firstErr error
	for i := 0; i < count; i++ {
		r := <-ch
		if r.ok {
			recv++
			sum += r.rtt
		} else if firstErr == nil {
			firstErr = r.err
		}
	}
	lossPct = burstLossPct(count, recv)
	if recv == 0 {
		return 0, false, lossPct, firstErr
	}
	return sum / float64(recv), true, lossPct, nil
}

// burstLossPct 一轮 sent 包中 recv 包成功时的丢包率（%）。
func burstLossPct(sent, recv int) float64 {
	if sent <= 0 {
		return 100
	}
	if recv < 0 {
		recv = 0
	}
	if recv > sent {
		recv = sent
	}
	return float64(sent-recv) * 100 / float64(sent)
}

// tcpOnce TCP 连通性探测，target 为 host:port。
func tcpOnce(ctx context.Context, target, sourceIP string, timeout time.Duration) (rttMs float64, ok bool, err error) {
	d := &net.Dialer{Timeout: timeout}
	if ip := net.ParseIP(sourceIP); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return 0, false, err
	}
	rtt := float64(time.Since(start).Microseconds()) / 1000.0
	_ = conn.Close()
	return rtt, true, nil
}

// ensurePort53 DNS 服务器地址补标准端口：Go 的 Dial 地址必须为 host:port，
// 配置中的 "223.5.5.5" 直拨会报 missing port in address（2026-11-07 实测 DNS 探测 100% 失败的根因）。
func ensurePort53(server string) string {
	if _, _, err := net.SplitHostPort(server); err != nil {
		return server + ":53"
	}
	return server
}

// dnsOnce 向指定 DNS 服务器解析测试域名。
func dnsOnce(ctx context.Context, server, domain, sourceIP string, timeout time.Duration) (rttMs float64, ok bool, err error) {
	d := &net.Dialer{Timeout: timeout}
	if ip := net.ParseIP(sourceIP); ip != nil {
		d.LocalAddr = &net.UDPAddr{IP: ip}
	}
	server = ensurePort53(server)
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return d.DialContext(ctx, "udp", server)
		},
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	if _, err = r.LookupHost(cctx, domain); err != nil {
		return 0, false, err
	}
	return float64(time.Since(start).Microseconds()) / 1000.0, true, nil
}

// httpOnce HTTP 探测：任意响应（含 3xx/4xx）均视为链路可达，记录状态码、TTFB 与证书剩余天数。
func httpOnce(ctx context.Context, targetURL, sourceIP string, timeout time.Duration) (ttfbMs float64, code int, ok bool, certDays float64, err error) {
	d := &net.Dialer{Timeout: timeout}
	if ip := net.ParseIP(sourceIP); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	var tlsConn *tls.Conn
	transport := &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
		Proxy:                 nil, // 探针直连，不走系统代理
		// 自定义 TLS 拨号：不校验证书（仅测可达性），但读取对端证书计算剩余天数
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			tc := tls.Client(raw, &tls.Config{ServerName: host, InsecureSkipVerify: true})
			if err := tc.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			tlsConn = tc
			return tc, nil
		},
	}
	client := &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	var ttfb time.Duration
	start := time.Now()
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() { ttfb = time.Since(start) },
	}

	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, 0, false, 0, err
	}
	req.Header.Set("User-Agent", "netwatch-agent/1.0")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, false, 0, err
	}
	defer resp.Body.Close()
	_ = resp.Body.Close()
	if tlsConn != nil && len(tlsConn.ConnectionState().PeerCertificates) > 0 {
		notAfter := tlsConn.ConnectionState().PeerCertificates[0].NotAfter
		certDays = time.Until(notAfter).Hours() / 24
	}
	return float64(ttfb.Microseconds()) / 1000.0, resp.StatusCode, true, certDays, nil
}

// nowMs 当前毫秒时间戳
func nowMs() int64 { return time.Now().UnixMilli() }
