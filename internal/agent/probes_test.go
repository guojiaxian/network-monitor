package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestProbeRetryFirstOK 首次成功：不重试，返回首次值
func TestProbeRetryFirstOK(t *testing.T) {
	calls := 0
	rtt, ok, err := probeRetry(1, probeRetryPause, func() (float64, bool, error) {
		calls++
		return 12.5, true, nil
	})
	if !ok || err != nil || calls != 1 || rtt != 12.5 {
		t.Fatalf("首次成功不应重试: ok=%v calls=%d rtt=%v err=%v", ok, calls, rtt, err)
	}
}

// TestProbeRetrySecondOK 首次失败重试成功：返回成功那次的值
func TestProbeRetrySecondOK(t *testing.T) {
	calls := 0
	rtt, ok, err := probeRetry(1, probeRetryPause, func() (float64, bool, error) {
		calls++
		if calls == 1 {
			return 0, false, errors.New("icmp timeout")
		}
		return 8.8, true, nil
	})
	if !ok || err != nil || calls != 2 || rtt != 8.8 {
		t.Fatalf("重试成功应返回成功值: ok=%v calls=%d rtt=%v err=%v", ok, calls, rtt, err)
	}
}

// TestProbeRetryAllFail 连续失败：重试次数用尽后判断连（返回最后一次错误）
func TestProbeRetryAllFail(t *testing.T) {
	calls := 0
	_, ok, err := probeRetry(1, probeRetryPause, func() (float64, bool, error) {
		calls++
		return 0, false, errors.New("connection refused")
	})
	if ok || err == nil || calls != 2 {
		t.Fatalf("连续失败应重试 1 次后判断连: ok=%v calls=%d err=%v", ok, calls, err)
	}
}

// TestProbeRetryNoRetries retries=0：不重试，单次失败即失败（IP 扫描语义）
func TestProbeRetryNoRetries(t *testing.T) {
	calls := 0
	_, ok, _ := probeRetry(0, probeRetryPause, func() (float64, bool, error) {
		calls++
		return 0, false, errors.New("timeout")
	})
	if ok || calls != 1 {
		t.Fatalf("retries=0 应单次失败即返回: calls=%d", calls)
	}
}

// TestProbeRetryMultiAndPause 重试 2 次 + 自定义间隔：调用 3 次、总等待 ≥ 2×pause（配置化间隔生效）
func TestProbeRetryMultiAndPause(t *testing.T) {
	calls := 0
	const pause = 30 * time.Millisecond
	start := time.Now()
	_, ok, _ := probeRetry(2, pause, func() (float64, bool, error) {
		calls++
		return 0, false, errors.New("down")
	})
	elapsed := time.Since(start)
	if ok || calls != 3 {
		t.Fatalf("retries=2 应共调用 3 次: calls=%d ok=%v", calls, ok)
	}
	if elapsed < 2*pause {
		t.Fatalf("应等待 2×pause=%v，实际 %v", 2*pause, elapsed)
	}
}

// TestProbeRetryZeroPause pause=0（旧中心缺省=0）：回退内置默认间隔，不 panic、仍重试
func TestProbeRetryZeroPause(t *testing.T) {
	calls := 0
	_, ok, _ := probeRetry(1, 0, func() (float64, bool, error) {
		calls++
		return 0, false, errors.New("down")
	})
	if ok || calls != 2 {
		t.Fatalf("pause=0 应回退默认间隔并重试 1 次: calls=%d ok=%v", calls, ok)
	}
}

// TestEnsurePort53 DNS 地址补端口：裸 IP 补 :53，带端口/IPv6 带端口原样保留
func TestEnsurePort53(t *testing.T) {
	cases := map[string]string{
		"223.5.5.5":        "223.5.5.5:53",
		"114.114.114.114":  "114.114.114.114:53",
		"223.5.5.5:53":     "223.5.5.5:53",
		"[2001:db8::1]:53": "[2001:db8::1]:53",
	}
	for in, want := range cases {
		if got := ensurePort53(in); got != want {
			t.Fatalf("ensurePort53(%q)=%q，期望 %q", in, got, want)
		}
	}
}

// TestProbeRetryCanceled 调用方取消：不重试立即返回（探测循环退出场景）
func TestProbeRetryCanceled(t *testing.T) {
	calls := 0
	start := time.Now()
	_, ok, _ := probeRetry(1, probeRetryPause, func() (float64, bool, error) {
		calls++
		return 0, false, context.Canceled
	})
	if ok || calls != 1 {
		t.Fatalf("context.Canceled 不应重试: calls=%d", calls)
	}
	if elapsed := time.Since(start); elapsed > probeRetryPause {
		t.Fatalf("取消路径不应等待重试间隔: %v", elapsed)
	}
}

// TestBurstLossPct 一轮 N 包的丢包率：全收=0、全丢=100、4/5=20、边界钳位
func TestBurstLossPct(t *testing.T) {
	cases := []struct {
		sent, recv int
		want       float64
	}{
		{5, 5, 0},
		{5, 0, 100},
		{5, 4, 20},
		{5, 3, 40},
		{0, 0, 100},  // 非法轮次按全丢处理
		{5, 9, 0},    // recv 超过 sent 钳位
		{5, -1, 100}, // 非法 recv 钳位
	}
	for _, c := range cases {
		if got := burstLossPct(c.sent, c.recv); got != c.want {
			t.Fatalf("burstLossPct(%d,%d)=%v，期望 %v", c.sent, c.recv, got, c.want)
		}
	}
}
