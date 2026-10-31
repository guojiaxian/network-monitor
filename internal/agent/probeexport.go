package agent

import (
	"context"
	"time"
)

// 导出探测原语供中心端复用（内→内内置监控：中心服务器自己当探针，Uptime Kuma 模式）。
// 与楼层探针共用同一套实现，行为完全一致（源地址绑定参数留空即可）。

// ProbePing ICMP 探测，返回 (rttMs, ok)
func ProbePing(ctx context.Context, target string, timeout time.Duration) (float64, bool) {
	rtt, ok, _ := pingOnce(ctx, target, "", timeout)
	return rtt, ok
}

// ProbeTcp TCP 连通探测，target 为 host:port
func ProbeTcp(ctx context.Context, target string, timeout time.Duration) (float64, bool) {
	rtt, ok, _ := tcpOnce(ctx, target, "", timeout)
	return rtt, ok
}

// ProbeHttp HTTP(S) 探测，返回 (ttfbMs, statusCode, ok, certDays)
func ProbeHttp(ctx context.Context, targetURL string, timeout time.Duration) (float64, int, bool, float64) {
	ttfb, code, ok, cert, _ := httpOnce(ctx, targetURL, "", timeout)
	return ttfb, code, ok, cert
}
