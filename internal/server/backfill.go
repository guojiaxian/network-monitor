// 样本环重启回填：内存 pingWin 重启即清零，状态卡/总览/大屏的方格心跳会从零
// 重新累计（重启前的分钟全部灰格，最长约 1 小时才补满，用户多次问询）。
// VictoriaMetrics 落盘历史不受重启影响——启动时把近 65 分钟的公网 ping 样本
// （nw_ping_ok，class=public）回填进内存环，重启后心跳立即满格无灰。
// 范围刻意收窄：只回填 pingWin（心跳展示 + 线路判活 lossStats 的数据源）；
// 网关状态、lastTCP/DNS/HTTP、typeFailTargets 等状态机字段不做历史重建，
// 由探针 ≤15s 的心跳与样本自然恢复（用历史数据重建连续失败计数反而易误触发）。
package server

import (
	"context"
	"sort"
	"strconv"
	"time"
)

const ringBackfillWin = 65 * time.Minute // 与 evaluate 的 pingWin 修剪窗口一致

// backfillRing 启动后从 VM 导出近 65 分钟 nw_ping_ok 并合入样本环（异步，不阻塞启动）。
// VM 与 server 同机 systemd 拉起偶尔慢半拍，失败重试一次；两次都失败则本次
// 启动退回原行为（心跳条从零累计），不影响其他功能。
func (e *Engine) backfillRing(ctx context.Context) {
	known := map[string]bool{}
	rows, err := e.store.ListProbes(ctx)
	if err != nil {
		e.log.Printf("ring backfill: 列举探针失败: %v", err)
		return
	}
	for _, r := range rows {
		known[r.ID] = true
	}
	var series []ExportSeries
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(5 * time.Second)
		}
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		series, lastErr = e.tsdb.Export(c, `{__name__="nw_ping_ok"}`,
			time.Now().Add(-ringBackfillWin).Unix(), time.Now().Unix())
		cancel()
		if lastErr == nil {
			n := e.applyRingBackfill(series, known, time.Now().Add(-ringBackfillWin))
			e.log.Printf("ring backfill: %d 个探针回填 %d 条 ping 样本（近 %.0f 分钟）",
				len(series2probes(series, known)), n, ringBackfillWin.Minutes())
			return
		}
	}
	e.log.Printf("ring backfill: VM 导出失败（本次启动心跳条从零累计）: %v", lastErr)
}

// series2probes 统计导出序列涉及的现存探针数（日志用）。
func series2probes(series []ExportSeries, known map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, s := range series {
		if id := s.Metric["probe"]; known[id] {
			out[id] = true
		}
	}
	return out
}

// applyRingBackfill 把导出序列转成 pingRec 合入各探针样本环，返回回填后的样本总数。
// 已删探针（不在 known）跳过，防幽灵聚合；网关样本跳过（与 Ingest 一致）；
// 与在途 Ingest 可能重叠的样本按 (target,ts) 去重；合入后统一按时间排序并修剪窗口。
func (e *Engine) applyRingBackfill(series []ExportSeries, known map[string]bool, cutoff time.Time) int {
	per := map[string][]pingRec{}
	for _, s := range series {
		id := s.Metric["probe"]
		if !known[id] || s.Metric["class"] == "gw" {
			continue
		}
		target := s.Metric["target"]
		for i, ts := range s.Timestamps {
			per[id] = append(per[id], pingRec{target: target, ok: s.Values[i] >= 0.5, ts: time.UnixMilli(ts)})
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	total := 0
	for id, recs := range per {
		agg := e.aggFor(id)
		have := make(map[string]bool, len(agg.pingWin))
		for _, r := range agg.pingWin {
			have[r.target+"\x00"+tsKey(r.ts)] = true
		}
		for _, r := range recs {
			k := r.target + "\x00" + tsKey(r.ts)
			if !have[k] {
				have[k] = true
				agg.pingWin = append(agg.pingWin, r)
			}
		}
		sort.Slice(agg.pingWin, func(i, j int) bool { return agg.pingWin[i].ts.Before(agg.pingWin[j].ts) })
		kept := agg.pingWin[:0]
		for _, r := range agg.pingWin {
			if r.ts.After(cutoff) {
				kept = append(kept, r)
			}
		}
		agg.pingWin = kept
		total += len(kept)
	}
	return total
}

// tsKey 秒级时间戳键（去重用）。
func tsKey(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
