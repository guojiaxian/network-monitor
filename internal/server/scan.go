package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
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
// 单楼层同步接口：并发 64、单 IP 超时 1s，/24 约 4~5 秒完成，处于全局 60s 超时预算内。
// 多楼层/定时扫描走后台任务（每次扫一个网段、落一条记录，便于按网段查看）；
// 任务由 ScanRunner 统一执行，手动与定时共用同一把"同一时刻只跑一批"的闸。

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

// scanCIDR 对单个网段做并发 ping 在线扫描（同步完成；/24 约 4~5 秒，/22 上限约 16 秒）。
// timeout 为单 IP ping 超时，来自 scan_settings.timeout_ms（默认 1s）。
func scanCIDR(ctx context.Context, cidr string, timeout time.Duration) ([]scanResult, error) {
	targets, err := scanTargets(cidr)
	if err != nil {
		return nil, err
	}
	results := make([]scanResult, len(targets))
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for i, ip := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			// pingOnce 以 p.Timeout 兜底（pro-bing 的 Run 不吃 ctx）；1s 单包足够；
			// 用无重试版本：扫描重在线量与速度，逐 IP 重试会使扫描时长翻倍
			rtt, ok := agent.ProbePingOnce(ctx, ip, timeout)
			results[i] = scanResult{IP: ip, Online: ok, RTTms: rtt}
		}(i, ip)
	}
	wg.Wait()

	// 按 IP 数值排序（升序展示）
	sort.Slice(results, func(i, j int) bool {
		a, b := net.ParseIP(results[i].IP).To4(), net.ParseIP(results[j].IP).To4()
		return binary.BigEndian.Uint32(a) < binary.BigEndian.Uint32(b)
	})
	return results, nil
}

// scanFloor POST /api/admin/floors/{id}/scan：单楼层同步扫描（保留旧接口；页面统一走后台任务）。
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
	start := time.Now()
	st := loadScanSettings(r.Context(), a.store)
	results, err := scanCIDR(r.Context(), floor.CIDR, time.Duration(st.TimeoutMs)*time.Millisecond)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	up := 0
	for _, res := range results {
		if res.Online {
			up++
		}
	}
	writeJSON(w, map[string]any{
		"id": floor.ID, "name": floor.Name, "cidr": floor.CIDR,
		"total": len(results), "up": up, "down": len(results) - up,
		"duration_ms": time.Since(start).Milliseconds(), "results": results,
	})
}

// pickScanFloors 按入参顺序去重选出楼层；空选择或含已删除楼层时报错（纯函数，回归用例覆盖）。
func pickScanFloors(all []FloorRow, ids []int) ([]FloorRow, error) {
	if len(ids) == 0 {
		return nil, errScan("请至少选择一个楼层")
	}
	byID := make(map[int]FloorRow, len(all))
	for _, f := range all {
		byID[f.ID] = f
	}
	seen := make(map[int]bool, len(ids))
	out := make([]FloorRow, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		f, ok := byID[id]
		if !ok {
			return nil, errScan(fmt.Sprintf("楼层不存在或已删除（id=%d），请刷新页面后重试", id))
		}
		out = append(out, f)
	}
	return out, nil
}

// startScan POST /api/admin/scan：对多个楼层发起 IP 扫描任务（后台逐网段执行，秒回批次号）。
// 记录按网段逐条入库（一次任务 N 个楼层 = N 条记录）。
func (a *AdminAPI) startScan(w http.ResponseWriter, r *http.Request) {
	sess, _ := r.Context().Value(ctxUserKey).(nwSession)
	var req struct {
		FloorIDs []int `json:"floor_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpError(w, 400, "请求体格式错误")
		return
	}
	all, err := a.store.ListFloors(r.Context())
	if err != nil {
		httpError(w, 500, "读取楼层失败: "+err.Error())
		return
	}
	floors, err := pickScanFloors(all, req.FloorIDs)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	batch, err := a.scan.StartJob(floors, "manual", sess.user)
	if err != nil {
		httpError(w, 409, err.Error())
		return
	}
	writeJSON(w, map[string]any{"batch": batch, "count": len(floors), "floors": floors})
}

// listScanRuns GET /api/admin/scan/runs?trigger=&days=&floor_id=&limit=&offset=&start=&end=：扫描记录
// （每网段一行，不含逐 IP 明细，分页返回 {total, rows}）。days=1 今天 / 3 近三天 / 7 近七天，默认今天；
// start/end（unix 秒）同时给出时优先于 days（探测历史页时间范围/自定义窗口）。
func (a *AdminAPI) listScanRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days != 3 && days != 7 {
		days = 1 // 默认只看今天
	}
	floorID, _ := strconv.Atoi(r.URL.Query().Get("floor_id"))
	start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
	end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
	rows, total, err := a.store.ListScanRuns(r.Context(), r.URL.Query().Get("trigger"), days, floorID, limit, offset, start, end)
	if err != nil {
		httpError(w, 500, "读取扫描记录失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"total": total, "rows": rows})
}

// scanSummary GET /api/admin/scan/summary?floor_id=&start=&end=：按批次聚合窗口内的成功扫描
// （探测历史「IP 扫描」页签的 KPI 汇总/批次心跳条/在线趋势数据源）。
func (a *AdminAPI) scanSummary(w http.ResponseWriter, r *http.Request) {
	floorID, _ := strconv.Atoi(r.URL.Query().Get("floor_id"))
	start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
	end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
	batches, err := a.store.ScanSummary(r.Context(), floorID, start, end)
	if err != nil {
		httpError(w, 500, "读取扫描汇总失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"batches": batches})
}

// getScanRun GET /api/admin/scan/runs/{id}：单条记录（含该网段逐 IP 明细）。
func (a *AdminAPI) getScanRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpError(w, 400, "bad id")
		return
	}
	run, err := a.store.GetScanRun(r.Context(), id)
	if err != nil {
		httpError(w, 404, "扫描记录不存在")
		return
	}
	writeJSON(w, run)
}
