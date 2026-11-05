// 出口公网 IP 自动探测：探针位于楼层线路之后，其出站流量经策略路由走本楼层
// 宽带，访问公网 IP 回显服务即得该线路的出口公网 IP。结果缓存并随心跳上报，
// 由中心端回填 lines.public_ip。每 5 分钟刷新（宽带重拨换 IP 后自动跟随）。
package agent

import (
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"
)

// 内置回显服务（2026-11-05 现场实测自该类线路可达；依次尝试，任一成功即止）
var defaultIPEchoURLs = []string{
	"http://ip.3322.net",                  // 纯文本 IP
	"http://members.3322.org/dyndns/getip", // 纯文本 IP
	"https://v4.ident.me",                 // 纯文本 IPv4
	"https://myip.ipip.net",               // "当前 IP：x.x.x.x  来自于：中国 …"
}

// IPv4 提取需带边界（前后非数字非点）：否则 "999.1.1.1" 会截出 "99.1.1.1"（单测实测踩坑）
var ipv4Re = regexp.MustCompile(`(?:^|[^\d.])((?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?:\.(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})(?:$|[^\d.])`)

func (a *Agent) egressIPOf() string {
	a.ipMu.Lock()
	defer a.ipMu.Unlock()
	return a.egressIP
}

// publicIPLoop 周期刷新出口公网 IP（立即一次 + 每 5 分钟）
func (a *Agent) publicIPLoop(ctx context.Context) {
	a.refreshPublicIP(ctx)
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.refreshPublicIP(ctx)
		}
	}
}

func (a *Agent) refreshPublicIP(ctx context.Context) {
	urls := a.currentConfig().IPEchoURLs
	if len(urls) == 0 {
		urls = defaultIPEchoURLs
	}
	for _, u := range urls {
		if ip := fetchIPv4(ctx, u); ip != "" {
			if ip != a.egressIPOf() {
				a.log.Printf("egress public ip: %s (via %s)", ip, u)
			}
			a.ipMu.Lock()
			a.egressIP = ip
			a.ipMu.Unlock()
			return
		}
	}
	a.log.Printf("egress public ip: 全部回显服务失败，保留上次结果 %q", a.egressIPOf())
}

// fetchIPv4 请求回显服务并提取首个合法 IPv4；失败返回空串。
func fetchIPv4(ctx context.Context, url string) string {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "netwatch-agent/1.0")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}} // 直连，不走系统代理
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	return parseIPv4(string(b))
}

// parseIPv4 从回显服务响应中提取首个合法 IPv4（兼容纯文本/"当前 IP：…"/多行格式）
func parseIPv4(body string) string {
	m := ipv4Re.FindStringSubmatch(body)
	if len(m) < 2 || net.ParseIP(m[1]) == nil {
		return ""
	}
	return m[1]
}
