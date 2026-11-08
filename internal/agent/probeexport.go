package agent

import (
	"context"
	"time"
)

// 导出探测原语供中心端复用（内→内内置监控：中心服务器自己当探针，Uptime Kuma 模式）。
// 与楼层探针共用同一套实现，行为完全一致（源地址绑定参数留空即可）；
// 均带一次重试——失败后短暂等待重试，连续失败才判断连。

// ProbePing ICMP 探测，返回 (rttMs, ok)
func ProbePing(ctx context.Context, target string, timeout time.Duration) (float64, bool) {
	rtt, ok, _ := probeRetry(1, probeRetryPause, func() (float64, bool, error) {
		return pingOnce(ctx, target, "", timeout)
	})
	return rtt, ok
}

// ProbePingOnce ICMP 单次探测（无重试）——IP 扫描专用：保持扫描速度，
// 在线/离线判定由单包直达，避免逐 IP 重试导致扫描时长翻倍。
func ProbePingOnce(ctx context.Context, target string, timeout time.Duration) (float64, bool) {
	rtt, ok, _ := pingOnce(ctx, target, "", timeout)
	return rtt, ok
}

// ProbeTcp TCP 连通探测，target 为 host:port
func ProbeTcp(ctx context.Context, target string, timeout time.Duration) (float64, bool) {
	rtt, ok, _ := probeRetry(1, probeRetryPause, func() (float64, bool, error) {
		return tcpOnce(ctx, target, "", timeout)
	})
	return rtt, ok
}

// ProbeHttp HTTP(S) 探测，返回 (ttfbMs, statusCode, ok, certDays)
func ProbeHttp(ctx context.Context, targetURL string, timeout time.Duration) (float64, int, bool, float64) {
	var code int
	var cert float64
	ttfb, ok, _ := probeRetry(1, probeRetryPause, func() (float64, bool, error) {
		v, c2, o, d, err := httpOnce(ctx, targetURL, "", timeout)
		code, cert = c2, d // 重试成功那次为准
		return v, o, err
	})
	return ttfb, code, ok, cert
}
