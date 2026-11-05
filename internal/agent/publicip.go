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
	"strings"
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

// egressExtras 出口附加信息（低频探测缓存）
func (a *Agent) egressExtras() (isp, geo, ipv6 string) {
	a.ipMu.Lock()
	defer a.ipMu.Unlock()
	return a.ipISP, a.ipGeo, a.ipV6
}

// publicIPLoop 周期刷新出口信息（立即一次 + 每 5 分钟）：
// 公网 IPv4（心跳回填线路）→ 归属运营商/地区（ipip 全文解析）→ IPv6（尽力探测）
func (a *Agent) publicIPLoop(ctx context.Context) {
	a.refreshEgress(ctx)
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.refreshEgress(ctx)
		}
	}
}

func (a *Agent) refreshEgress(ctx context.Context) {
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
			break
		}
	}
	a.refreshGeo(ctx)
	a.refreshIPv6(ctx)
}

// refreshGeo 归属运营商/地区：myip.ipip.net 全文（"当前 IP：x.x.x.x  来自于：中国 河南 郑州 联通"）。
// 尽力而为：失败保留旧值（ISP/geo 仅展示与线路自动归类用）。
func (a *Agent) refreshGeo(ctx context.Context) {
	body := fetchBody(ctx, "https://myip.ipip.net")
	if body == "" {
		return
	}
	i := strings.Index(body, "来自于：")
	if i < 0 {
		return
	}
	f := strings.Fields(body[i+len("来自于："):])
	if len(f) == 0 {
		return
	}
	isp := f[len(f)-1]
	geo := strings.Join(f[:len(f)-1], " ")
	a.ipMu.Lock()
	a.ipISP, a.ipGeo = isp, geo
	a.ipMu.Unlock()
}

// refreshIPv6 出口 IPv6：先确认本机有全局 IPv6 地址（零网络依赖，纯 v4 内网
// 直接判"不支持"），有才向 v6-only 回显服务探测出口地址。
// 教训：不能直接信回显服务——api6/api64 端点在无 v6 网络会返回 v4 地址或被
// DNS 污染返回 A 记录，造成"v6=v4 地址"误报（实测踩坑）。
func (a *Agent) refreshIPv6(ctx context.Context) {
	v6 := ""
	if hasGlobalIPv6() {
		for _, u := range []string{"https://api6.ipify.org", "https://v6.ident.me"} {
			if s := fetchBody(ctx, u); s != "" {
				if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil && ip.To4() == nil {
					v6 = ip.String()
					break
				}
			}
		}
	}
	a.ipMu.Lock()
	a.ipV6 = v6
	a.ipMu.Unlock()
}

// hasGlobalIPv6 本机是否有全局单播 IPv6 地址（2000::/3，排除链路本地）
func hasGlobalIPv6() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip6 := ipn.IP.To16(); ip6 != nil && ip6.To4() == nil && ip6[0]&0xE0 == 0x20 {
					return true
				}
			}
		}
	}
	return false
}

// fetchBody 拉取回显服务响应文本（限 4KB，8s 超时，不走系统代理）
func fetchBody(ctx context.Context, url string) string {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "netwatch-agent/1.0")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
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
	return string(b)
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
