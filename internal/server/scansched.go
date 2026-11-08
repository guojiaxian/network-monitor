package server

// 楼层 IP 扫描执行器：手动（后台按钮）与定时（每 N 分钟由中心逐网段扫描）共用。
// 设计要点：
//   - 同一时刻只跑一批（全局 64 并发 ping，不因多楼层/定时叠加而放大骚扰）；
//   - 逐网段串行扫描，每个网段落一条 scan_runs 记录（一网段一记录，方便按网段查看）；
//   - 定时计划存 settings.scan_settings，探测配置页可改启用/间隔/范围，约 30s 内生效；
//   - 是否到点按"最近一次定时扫描时间 + 间隔"判断（落库，中心重启不会重复/漏扫）。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// ScanSettings IP 扫描计划（settings 键 scan_settings）
type ScanSettings struct {
	Enabled     bool  `json:"enabled"`      // 是否启用定时扫描
	IntervalMin int   `json:"interval_min"` // 扫描间隔（分钟）
	TimeoutMs   int   `json:"timeout_ms"`   // 单 IP ping 超时（毫秒，默认 1000；探测配置页可改）
	FloorIDs    []int `json:"floor_ids"`    // 扫描范围（楼层 id）；空 = 全部已配置 CIDR 的楼层
}

func DefaultScanSettings() ScanSettings {
	return ScanSettings{Enabled: true, IntervalMin: 60, TimeoutMs: 1000}
}

// 间隔上下限：太短会持续骚扰楼层网络，太长则在线数失去参考意义。
const (
	scanIntervalMinMin = 5
	scanIntervalMaxMin = 1440 // 24 小时
	scanTimeoutMsMin   = 200
	scanTimeoutMsMax   = 5000
)

// normalize 修正越界值（非法输入回落到默认 60 分钟 / 1000ms）。
func (s *ScanSettings) normalize() {
	if s.IntervalMin < scanIntervalMinMin || s.IntervalMin > scanIntervalMaxMin {
		s.IntervalMin = 60
	}
	if s.TimeoutMs < scanTimeoutMsMin || s.TimeoutMs > scanTimeoutMsMax {
		s.TimeoutMs = 1000
	}
	seen := map[int]bool{}
	out := s.FloorIDs[:0]
	for _, id := range s.FloorIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	s.FloorIDs = out
}

// loadScanSettings 读取扫描计划（缺省/损坏时回落默认值）。手动与定时共用。
func loadScanSettings(ctx context.Context, store *Store) ScanSettings {
	var st ScanSettings
	if err := store.GetSetting(ctx, "scan_settings", &st); err != nil {
		return DefaultScanSettings()
	}
	st.normalize()
	return st
}

// ScanRunner 扫描任务执行器
type ScanRunner struct {
	store   *Store
	log     *log.Logger
	shipper *LogShipper // 日志外发器（每网段完成一条汇总，可后置注入）

	mu     sync.Mutex
	active bool // 有一批扫描正在执行
}

func NewScanRunner(store *Store) *ScanRunner {
	return &ScanRunner{store: store, log: log.New(os.Stderr, "scan ", log.LstdFlags|log.Lmsgprefix)}
}

// SetShipper 注入日志外发器
func (r *ScanRunner) SetShipper(s *LogShipper) { r.shipper = s }

// Start 启动定时扫描循环（每 30s 检查一次是否到点）。
func (r *ScanRunner) Start(ctx context.Context) {
	go r.scheduleLoop(ctx)
}

// StartJob 发起一批扫描（floors 顺序即执行顺序）：先落 N 条 pending 记录，再后台逐网段执行。
// 同一时刻只允许一批，重复发起返回错误（HTTP 层转 409）。
func (r *ScanRunner) StartJob(floors []FloorRow, trigger, by string) (string, error) {
	if len(floors) == 0 {
		return "", fmt.Errorf("没有可扫描的网段")
	}
	r.mu.Lock()
	if r.active {
		r.mu.Unlock()
		return "", fmt.Errorf("已有扫描任务进行中，请等待完成后再发起")
	}
	r.active = true
	r.mu.Unlock()

	batch := newScanBatch()
	ids, err := r.store.CreateScanBatch(context.Background(), batch, floors, trigger, by)
	if err != nil {
		r.mu.Lock()
		r.active = false
		r.mu.Unlock()
		return "", fmt.Errorf("创建扫描记录失败: %w", err)
	}
	go r.runJob(batch, floors, ids, trigger)
	r.log.Printf("批次 %s 启动（%s，%d 个网段，发起人 %s）", batch, trigger, len(floors), by)
	return batch, nil
}

// runJob 逐网段串行扫描；每完成一个网段立即落库（中断也保留已完成部分）。
func (r *ScanRunner) runJob(batch string, floors []FloorRow, ids []int64, trigger string) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Printf("批次 %s 异常退出: %v", batch, p)
		}
		r.mu.Lock()
		r.active = false
		r.mu.Unlock()
	}()
	ctx := context.Background() // 独立于请求 ctx：请求已返回，任务继续执行（同坑 11）
	st := loadScanSettings(ctx, r.store)
	pingTimeout := time.Duration(st.TimeoutMs) * time.Millisecond
	okCnt := 0
	for i, f := range floors {
		if err := r.store.SetScanRunning(ctx, ids[i]); err != nil {
			r.log.Printf("批次 %s 网段「%s」置 running 失败: %v", batch, f.Name, err)
		}
		start := time.Now()
		results, err := scanCIDR(ctx, f.CIDR, pingTimeout)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			r.log.Printf("批次 %s 网段「%s」(%s) 扫描失败: %v", batch, f.Name, f.CIDR, err)
			if err := r.store.FinishScanRun(ctx, ids[i], "error", 0, 0, dur, mustJSON([]scanResult{})); err != nil {
				r.log.Printf("批次 %s 网段「%s」写库失败: %v", batch, f.Name, err)
			}
			continue
		}
		up := 0
		for _, res := range results {
			if res.Online {
				up++
			}
		}
		if err := r.store.FinishScanRun(ctx, ids[i], "done", up, len(results), dur, mustJSON(results)); err != nil {
			r.log.Printf("批次 %s 网段「%s」写库失败: %v", batch, f.Name, err)
			continue
		}
		okCnt++
		r.log.Printf("批次 %s 网段「%s」：在线 %d/%d，耗时 %dms", batch, f.Name, up, len(results), dur)
		if r.shipper != nil {
			r.shipper.Ship(scanLogEvent(f.Name, f.CIDR, up, len(results), dur, trigger))
		}
	}
	status := "完成"
	if okCnt == 0 {
		status = "全部失败"
	}
	r.log.Printf("批次 %s %s（%d/%d 个网段成功）", batch, status, okCnt, len(floors))
}

// scheduleLoop 每 30s 检查计划：启用且距上次定时扫描 ≥ 间隔且当前空闲 → 发起一批。
func (r *ScanRunner) scheduleLoop(ctx context.Context) {
	// 启动后稍等（等探针/网络就绪）；无历史记录时立即跑第一轮，尽快给出在线数
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	r.tick(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

// tick 单次检查并触发（拆出来便于测试）。
func (r *ScanRunner) tick(ctx context.Context) {
	st := r.settings(ctx)
	if !st.Enabled {
		return
	}
	r.mu.Lock()
	busy := r.active
	r.mu.Unlock()
	if busy {
		return // 手动/上一批还在跑：本轮跳过，下个 tick 再看
	}
	last, err := r.store.LastScheduledScanAt(ctx)
	if err != nil {
		r.log.Printf("读取上次定时扫描时间失败: %v", err)
		return
	}
	if !last.IsZero() && time.Since(last) < time.Duration(st.IntervalMin)*time.Minute {
		return
	}
	floors, err := r.targetFloors(ctx, st)
	if err != nil {
		r.log.Printf("读取楼层失败: %v", err)
		return
	}
	if len(floors) == 0 {
		return
	}
	if _, err := r.StartJob(floors, "schedule", "system"); err != nil {
		r.log.Printf("定时扫描未启动: %v", err)
	}
}

// settings 读取扫描计划（缺省/损坏时回落默认值 60 分钟）。
func (r *ScanRunner) settings(ctx context.Context) ScanSettings {
	return loadScanSettings(ctx, r.store)
}

// targetFloors 定时扫描范围：配置了 CIDR 的楼层；settings 里指定了 floor_ids 时只扫这些。
func (r *ScanRunner) targetFloors(ctx context.Context, st ScanSettings) ([]FloorRow, error) {
	all, err := r.store.ListFloors(ctx)
	if err != nil {
		return nil, err
	}
	only := map[int]bool{}
	for _, id := range st.FloorIDs {
		only[id] = true
	}
	out := make([]FloorRow, 0, len(all))
	for _, f := range all {
		if f.CIDR == "" {
			continue
		}
		if len(only) > 0 && !only[f.ID] {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// newScanBatch 批次号（时间戳 + 随机后缀，便于人工辨认与日志对齐）
func newScanBatch() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// mustJSON 序列化（仅用于确定可序列化的本地结构，失败 panic 由任务 recover 兜底）。
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
