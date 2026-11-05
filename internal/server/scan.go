package server

import (
	"encoding/binary"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"netwatch/internal/agent"

	"github.com/go-chi/chi/v5"
)

// 楼层 IP 扫描：中心服务器对楼层子网 CIDR 内全部主机 IP 做 ICMP ping 在线检测。
// 仅 ping 单包（与行业网监测同样的克制原则）；禁 ping 的主机（如 Windows 默认防火墙）会显示离线。
// 同步接口：并发 64、单 IP 超时 1s，/24 约 4~5 秒完成，处于全局 60s 超时预算内。

// scanMaxHosts 扫描网段主机数上限（/22 = 1022），超过即拒绝，防止误扫超大网段拖垮接口。
const scanMaxHosts = 1022

// scanConcurrency 并发 ping 数；每个 Pinger 占 1 个 fd，64 并发安全且对网络温和。
const scanConcurrency = 64

// scanTargets 解析 CIDR 并枚举待扫主机 IP（跳过网络地址与广播地址；/32、/31 特殊处理）。
func scanTargets(cidr string) ([]string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, errScan("CIDR 格式错误：" + cidr)
	}
	if ipnet.IP.To4() == nil {
		return nil, errScan("暂不支持 IPv6 网段：" + cidr)
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return nil, errScan("暂不支持 IPv6 网段：" + cidr)
	}
	base := binary.BigEndian.Uint32(ipnet.IP.To4())
	var hosts uint32
	switch {
	case ones >= 32:
		hosts = 1
	case ones == 31:
		hosts = 2
	default:
		hosts = 1 << uint32(bits-ones)
	}
	if hosts > scanMaxHosts+2 { // +2 容忍跳过网络/广播地址前的规模判断
		return nil, errScan("网段过大（含 " + strconv.FormatUint(uint64(hosts), 10) + " 个地址），仅支持 /22 及更小范围")
	}
	var out []string
	for i := uint32(0); i < hosts; i++ {
		// 跳过网络地址（i=0）与广播地址（i=hosts-1）；/32、/31 无需跳过
		if ones < 31 && (i == 0 || i == hosts-1) {
			continue
		}
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, base+i)
		out = append(out, ip.String())
	}
	return out, nil
}

type scanError string

func (e scanError) Error() string { return string(e) }

func errScan(msg string) error { return scanError(msg) }

type scanResult struct {
	IP     string  `json:"ip"`
	Online bool    `json:"online"`
	RTTms  float64 `json:"rtt_ms"`
}

// scanFloor POST /api/admin/floors/{id}/scan：对楼层子网做 ping 在线扫描。
func (a *AdminAPI) scanFloor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, 400, "bad id")
		return
	}
	floor, err := a.store.GetFloor(r.Context(), id)
	if err != nil {
		httpError(w, 404, "楼层不存在")
		return
	}
	if floor.CIDR == "" {
		httpError(w, 400, "该楼层未配置子网 CIDR")
		return
	}
	targets, err := scanTargets(floor.CIDR)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}

	results := make([]scanResult, len(targets))
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	start := time.Now()
	for i, ip := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			// pingOnce 以 p.Timeout 兜底（pro-bing 的 Run 不吃 ctx），1s 单包足够
			rtt, ok := agent.ProbePing(r.Context(), ip, 1*time.Second)
			results[i] = scanResult{IP: ip, Online: ok, RTTms: rtt}
		}(i, ip)
	}
	wg.Wait()
	dur := time.Since(start)

	// 按 IP 数值排序（升序展示）
	sort.Slice(results, func(i, j int) bool {
		a, b := net.ParseIP(results[i].IP).To4(), net.ParseIP(results[j].IP).To4()
		return binary.BigEndian.Uint32(a) < binary.BigEndian.Uint32(b)
	})
	up := 0
	for _, r := range results {
		if r.Online {
			up++
		}
	}

	writeJSON(w, map[string]any{
		"id": floor.ID, "name": floor.Name, "cidr": floor.CIDR,
		"total": len(results), "up": up, "down": len(results) - up,
		"duration_ms": dur.Milliseconds(), "results": results,
	})
}
