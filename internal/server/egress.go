package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// EgressMonitor 中心公网出口自检：周期访问两个外部 HTTPS 目标。
// 只有两个目标都失败才判定 egress_ok=false；单目标故障不误报。
// 该状态供 /api/health 与 probe-1 中继读取：中心专线断时，管理网 API 仍可访问，
// 中继据此用自己的线路代发告警。
type EgressMonitor struct {
	urls      []string
	ok        atomic.Bool
	checkedAt atomic.Int64
}

func NewEgressMonitor(urls []string) *EgressMonitor {
	if len(urls) == 0 {
		urls = []string{"https://www.baidu.com", "https://www.qq.com"}
	}
	m := &EgressMonitor{urls: urls}
	m.ok.Store(true) // 首次检查完成前不误报
	return m
}

func (m *EgressMonitor) Run(ctx context.Context) {
	m.check(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.check(ctx)
		}
	}
}

func (m *EgressMonitor) check(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	ok := false
	for _, u := range m.urls {
		req, err := http.NewRequestWithContext(cctx, http.MethodHead, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				ok = true
				break
			}
		}
	}
	m.ok.Store(ok)
	m.checkedAt.Store(time.Now().Unix())
}

func (m *EgressMonitor) OK() bool { return m.ok.Load() }

func (m *EgressMonitor) CheckedAt() time.Time {
	t := m.checkedAt.Load()
	if t == 0 {
		return time.Time{}
	}
	return time.Unix(t, 0)
}
