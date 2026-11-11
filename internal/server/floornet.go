package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"netwatch/internal/agent"
)

// 楼层网络监测（中心服务器自探）：
//   - 网关：读 floors.gateway（核心交换机 SVI，允许 wifi/104 两段共用一个地址）；
//   - 锚点：读 settings.floor_net_monitors.anchors（楼层汇聚交换机 / AC / 104 汇聚设备）。
//
// 相同 IP 只探测一次，告警与状态按目标 IP 去重；标题列出覆盖楼层段，
// 避免 8L、wifi AC、104 汇聚等共享设备被重复探测、重复告警。
// 仅 ICMP 单包（不重试），连续 fail_n 次失败触发 P1，恢复一次成功即解除。

const (
	floorNetClassGateway = "gateway"
	floorNetClassAnchor  = "anchor"

	floorNetKindGateway     = "gateway"
	floorNetKindSwitch      = "switch"
	floorNetKindAC          = "ac"
	floorNetKindAggregation = "aggregation"

	floorNetMetricOK  = "nw_floor_net_ok"
	floorNetMetricRTT = "nw_floor_net_rtt_ms"
)

// FloorNetAnchor 楼层汇聚/接入设备锚点（管理 IP，仅 ICMP）。
type FloorNetAnchor struct {
	Name   string `json:"name"`   // 设备名，如 2L汇聚交换机
	Target string `json:"target"` // 管理 IP
	Floors string `json:"floors"` // 覆盖楼层段，如 1-3楼 / 8楼-1、8楼-2
	Type   string `json:"type"`   // switch / ac / aggregation
}

// FloorNetSettings 楼层网络监测配置（全局参数 + 汇聚设备锚点）。
type FloorNetSettings struct {
	Enabled   bool             `json:"enabled"`
	IntervalS int              `json:"interval_s"`
	TimeoutS  int              `json:"timeout_s"`
	FailN     int              `json:"fail_n"`
	Anchors   []FloorNetAnchor `json:"anchors"`
}

// DefaultFloorNetSettings 当前生产环境的楼层汇聚设备锚点（网关在 floors.gateway 中逐楼层配置）。
func DefaultFloorNetSettings() FloorNetSettings {
	return FloorNetSettings{
		Enabled:   true,
		IntervalS: 10,
		TimeoutS:  1,
		FailN:     3,
		Anchors: []FloorNetAnchor{
			{Name: "2L汇聚交换机", Target: "10.8.37.1", Floors: "1-3楼", Type: floorNetKindSwitch},
			{Name: "3L汇聚交换机", Target: "10.8.37.2", Floors: "1-3楼", Type: floorNetKindSwitch},
			{Name: "4L汇聚交换机", Target: "10.8.37.3", Floors: "4楼", Type: floorNetKindSwitch},
			{Name: "5L汇聚交换机", Target: "10.8.37.4", Floors: "5楼", Type: floorNetKindSwitch},
			{Name: "6L汇聚交换机", Target: "10.8.37.5", Floors: "6楼", Type: floorNetKindSwitch},
			{Name: "7L汇聚交换机", Target: "10.8.37.6", Floors: "7楼", Type: floorNetKindSwitch},
			{Name: "8L汇聚交换机", Target: "10.8.37.7", Floors: "8楼-1、8楼-2", Type: floorNetKindSwitch},
			{Name: "9L汇聚交换机", Target: "10.8.37.8", Floors: "9楼", Type: floorNetKindSwitch},
			{Name: "10L汇聚交换机", Target: "10.8.37.9", Floors: "10楼", Type: floorNetKindSwitch},
			{Name: "wifi AC控制器", Target: "10.8.36.252", Floors: "wifi-34段、wifi-35段", Type: floorNetKindAC},
			{Name: "104号院汇聚设备", Target: "10.8.37.252", Floors: "104号院-38段、104号院-39段", Type: floorNetKindAggregation},
		},
	}
}

// normalize 参数钳位；锚点字段去空格，非法类型回落 switch。
func (s *FloorNetSettings) normalize() {
	if s.IntervalS < 5 || s.IntervalS > 3600 {
		s.IntervalS = 10
	}
	if s.TimeoutS < 1 || s.TimeoutS > 10 {
		s.TimeoutS = 1
	}
	if s.FailN < 1 || s.FailN > 10 {
		s.FailN = 3
	}
	for i := range s.Anchors {
		a := &s.Anchors[i]
		a.Name = strings.TrimSpace(a.Name)
		a.Target = strings.TrimSpace(a.Target)
		a.Floors = strings.TrimSpace(a.Floors)
		a.Type = strings.TrimSpace(a.Type)
		switch a.Type {
		case floorNetKindSwitch, floorNetKindAC, floorNetKindAggregation:
		default:
			a.Type = floorNetKindSwitch
		}
	}
}

// ValidateFloorNetSettings 保存前校验：名称/IP 必填、IP 合法、同一 IP 不重复配置。
func ValidateFloorNetSettings(st *FloorNetSettings) error {
	st.normalize()
	seen := map[string]bool{}
	for i := range st.Anchors {
		a := &st.Anchors[i]
		if a.Name == "" {
			return fmt.Errorf("汇聚设备名称不能为空")
		}
		if a.Target == "" {
			return fmt.Errorf("%s 的 IP 不能为空", a.Name)
		}
		ip := net.ParseIP(a.Target)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("%s 的 IP 不合法：%s", a.Name, a.Target)
		}
		if seen[a.Target] {
			return fmt.Errorf("汇聚设备 IP 重复：%s", a.Target)
		}
		seen[a.Target] = true
	}
	return nil
}

// FloorNetStatus 单目标实时状态（总览 API）。
type FloorNetStatus struct {
	Name       string  `json:"name"`
	Class      string  `json:"class"` // gateway / anchor
	Kind       string  `json:"kind"`  // gateway / switch / ac / aggregation
	Target     string  `json:"target"`
	Floors     string  `json:"floors"`
	Up         bool    `json:"up"`
	Known      bool    `json:"known"`
	RTTms      float64 `json:"rtt_ms"`
	FailStreak int     `json:"fail_streak"`
	LastTS     string  `json:"last_ts"`
	LastOK     string  `json:"last_ok,omitempty"`
}

// FloorNetTarget 去重后的探测目标（同一 IP 只保留一个）。
type FloorNetTarget struct {
	Key           string // Class + ":" + IP，状态/告警指纹键
	Class         string // gateway / anchor
	Kind          string // gateway / switch / ac / aggregation
	Name          string // 展示名
	Target        string // IP
	Floors        string // 覆盖楼层段
	CIDR          string // 覆盖子网（告警邮件用）
	Title         string // 告警标题
	HistoryTarget string // 探测历史 target 标签（人类可读）
}

// FloorNetRunner 楼层网络监测器。
type FloorNetRunner struct {
	store *Store
	tsdb  *TSDB
	eng   *Engine
	log   *log.Logger

	mu      sync.Mutex
	status  map[string]*FloorNetStatus
	failCnt map[string]int
	lastOK  map[string]time.Time
	version int
}

func NewFloorNetRunner(store *Store, tsdb *TSDB, eng *Engine) *FloorNetRunner {
	return &FloorNetRunner{
		store: store, tsdb: tsdb, eng: eng,
		log:     log.New(os.Stderr, "floornet ", log.LstdFlags|log.Lmsgprefix),
		status:  map[string]*FloorNetStatus{},
		failCnt: map[string]int{},
		lastOK:  map[string]time.Time{},
	}
}

func (r *FloorNetRunner) Start(ctx context.Context) {
	go r.reloadLoop(ctx)
}

// reloadLoop 每 30s 重读楼层与设置；只有配置真正变化时才重建探测循环，
// 避免无条件 cancel 打断在途探测的时序写入（与 monitor/industry 同一教训）。
func (r *FloorNetRunner) reloadLoop(ctx context.Context) {
	var cancel context.CancelFunc
	var lastFP string
	run := func() {
		st := r.settings(ctx)
		floors, err := r.store.ListFloors(ctx)
		if err != nil {
			r.log.Printf("list floors: %v", err)
			return
		}
		targets := buildFloorNetTargets(floors, st)

		if !st.Enabled {
			// 关闭监测：清空状态并收口全部楼层网关/锚点告警，避免页面残留“通”和 firing 告警。
			r.mu.Lock()
			r.status = map[string]*FloorNetStatus{}
			r.failCnt = map[string]int{}
			r.lastOK = map[string]time.Time{}
			r.mu.Unlock()
			r.eng.CleanFloorNet(map[string]bool{})
			if cancel != nil {
				cancel()
				cancel = nil
			}
			lastFP = ""
			return
		}

		liveFP := map[string]bool{}
		liveKey := map[string]bool{}
		for _, t := range targets {
			liveFP[floorNetFingerprint(t)] = true
			liveKey[t.Key] = true
		}
		r.mu.Lock()
		for key := range r.status {
			if !liveKey[key] {
				delete(r.status, key)
				delete(r.failCnt, key)
				delete(r.lastOK, key)
			}
		}
		r.mu.Unlock()
		r.eng.CleanFloorNet(liveFP)
		sig, _ := json.Marshal(struct {
			Settings FloorNetSettings `json:"settings"`
			Floors   []FloorRow       `json:"floors"`
		}{st, floors})
		if string(sig) == lastFP && cancel != nil {
			return
		}
		lastFP = string(sig)
		if cancel != nil {
			cancel()
		}
		var cctx context.Context
		cctx, cancel = context.WithCancel(ctx)
		r.mu.Lock()
		r.version++
		v := r.version
		r.mu.Unlock()
		go r.runAll(cctx, targets, st, v)
	}
	run()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if cancel != nil {
				cancel()
			}
			return
		case <-t.C:
			run()
		}
	}
}

func (r *FloorNetRunner) settings(ctx context.Context) FloorNetSettings {
	var st FloorNetSettings
	if err := r.store.GetSetting(ctx, "floor_net_monitors", &st); err != nil {
		return DefaultFloorNetSettings()
	}
	st.normalize()
	return st
}

func (r *FloorNetRunner) runAll(ctx context.Context, targets []FloorNetTarget, st FloorNetSettings, version int) {
	for _, t := range targets {
		go r.runOne(ctx, t, st, version)
	}
}

func (r *FloorNetRunner) runOne(ctx context.Context, t FloorNetTarget, st FloorNetSettings, version int) {
	interval := time.Duration(st.IntervalS) * time.Second
	timeout := time.Duration(st.TimeoutS) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.mu.Lock()
	cur := r.version
	r.mu.Unlock()
	if cur == version {
		r.probe(ctx, t, st.FailN, timeout)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			cur = r.version
			r.mu.Unlock()
			if cur != version {
				return
			}
			r.probe(ctx, t, st.FailN, timeout)
		}
	}
}

// probe 单次 ICMP：更新状态、写时序库、驱动告警。
// 网络探测使用独立超时，配置重载取消旧 ctx 时不把取消误记为失败。
func (r *FloorNetRunner) probe(ctx context.Context, t FloorNetTarget, failN int, timeout time.Duration) {
	pctx, pcancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer pcancel()
	rtt, ok := agent.ProbePingOnce(pctx, t.Target, timeout)
	now := time.Now()

	r.mu.Lock()
	st, has := r.status[t.Key]
	if !has {
		st = &FloorNetStatus{}
		r.status[t.Key] = st
	}
	st.Name, st.Class, st.Kind = t.Name, t.Class, t.Kind
	st.Target, st.Floors = t.Target, t.Floors
	st.Up, st.Known, st.RTTms = ok, true, rtt
	st.LastTS = now.Format(time.RFC3339)
	if ok {
		r.failCnt[t.Key] = 0
		r.lastOK[t.Key] = now
		st.LastOK = now.Format(time.RFC3339)
	} else {
		r.failCnt[t.Key]++
	}
	failStreak := r.failCnt[t.Key]
	st.FailStreak = failStreak
	lastOK := r.lastOK[t.Key]
	r.mu.Unlock()

	labels := [][2]string{
		{"class", t.Class}, {"kind", t.Kind}, {"floor", t.Floors},
		{"name", t.Name}, {"target", t.HistoryTarget},
	}
	pts := []tsPoint{{
		metric: floorNetMetricOK, labels: labels,
		value: map[bool]float64{true: 1, false: 0}[ok], tsMs: now.UnixMilli(),
	}}
	if ok {
		pts = append(pts, tsPoint{metric: floorNetMetricRTT, labels: labels, value: rtt, tsMs: now.UnixMilli()})
	}
	// 写库不跟随配置重载 ctx：重载取消旧循环时不应把在途样本写成 context canceled。
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := r.tsdb.WritePoints(wctx, pts); err != nil {
		r.log.Printf("tsdb: %v", err)
	}
	wcancel()

	fp := floorNetFingerprint(t)
	if failStreak >= failN {
		detail := fmt.Sprintf("连续 %d 次 ICMP 探测失败（目标 %s）", failStreak, t.Target)
		if !lastOK.IsZero() {
			detail += "；最后成功 " + lastOK.Format("15:04:05")
		}
		r.eng.FireFloorNet(fp, t, detail)
	} else if ok && failStreak == 0 {
		r.eng.ResolveFloorNet(fp, "恢复可达")
	}
}

// Snapshot 总览 API 用（网关在前，同类型按 IP 排序）。
func (r *FloorNetRunner) Snapshot() []FloorNetStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]FloorNetStatus, 0, len(r.status))
	for _, st := range r.status {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class == floorNetClassGateway
		}
		if out[i].Floors != out[j].Floors {
			return out[i].Floors < out[j].Floors
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return ipLess(out[i].Target, out[j].Target)
	})
	return out
}

func floorNetFingerprint(t FloorNetTarget) string {
	kind := KindFloorGWDown
	if t.Class == floorNetClassAnchor {
		kind = KindFloorAnchorDown
	}
	return kind + ":" + t.Target
}

// buildFloorNetTargets 把 floors.gateway 与 settings.anchors 合并成去重目标。
// 同一 IP 被多个楼层段引用时只保留一个目标，Floors/标题列出全部覆盖段。
func buildFloorNetTargets(floors []FloorRow, st FloorNetSettings) []FloorNetTarget {
	floorCIDR := map[string]string{}
	for _, f := range floors {
		if f.CIDR != "" {
			floorCIDR[f.Name] = f.CIDR
		}
	}

	type gwGroup struct {
		ip    string
		names []string
		cidrs []string
	}
	gws := map[string]*gwGroup{}
	for _, f := range floors {
		ip := strings.TrimSpace(f.Gateway)
		if ip == "" || net.ParseIP(ip) == nil {
			continue
		}
		g := gws[ip]
		if g == nil {
			g = &gwGroup{ip: ip}
			gws[ip] = g
		}
		g.names = append(g.names, f.Name)
		if f.CIDR != "" && !containsStr(g.cidrs, f.CIDR) {
			g.cidrs = append(g.cidrs, f.CIDR)
		}
	}

	out := []FloorNetTarget{}
	for _, g := range gws {
		role := "核心网关"
		if len(g.names) > 1 {
			role = "共同网关"
		}
		floorsTxt := strings.Join(g.names, "、")
		out = append(out, FloorNetTarget{
			Key:           floorNetClassGateway + ":" + g.ip,
			Class:         floorNetClassGateway,
			Kind:          floorNetKindGateway,
			Name:          role,
			Target:        g.ip,
			Floors:        floorsTxt,
			CIDR:          strings.Join(g.cidrs, "、"),
			Title:         floorsTxt + "：" + role + "不可达（" + g.ip + "）",
			HistoryTarget: floorsTxt + " " + role + " " + g.ip,
		})
	}

	type anchorGroup struct {
		name   string
		ip     string
		floors []string
		kind   string
	}
	ags := map[string]*anchorGroup{}
	for _, a := range st.Anchors {
		ip := strings.TrimSpace(a.Target)
		if ip == "" || net.ParseIP(ip) == nil {
			continue
		}
		ag := ags[ip]
		if ag == nil {
			ag = &anchorGroup{name: strings.TrimSpace(a.Name), ip: ip, kind: a.Type}
			ags[ip] = ag
		}
		if fs := strings.TrimSpace(a.Floors); fs != "" && !containsStr(ag.floors, fs) {
			ag.floors = append(ag.floors, fs)
		}
	}
	for _, a := range ags {
		floorsTxt := strings.Join(a.floors, "、")
		title := a.name + "不可达（" + a.ip + "）"
		if floorsTxt != "" {
			title = floorsTxt + "：" + a.name + "不可达（" + a.ip + "）"
		}
		hist := a.name + " " + a.ip
		cidrs := []string{}
		for _, f := range a.floors {
			for _, part := range strings.Split(f, "、") {
				if c := floorCIDR[strings.TrimSpace(part)]; c != "" && !containsStr(cidrs, c) {
					cidrs = append(cidrs, c)
				}
			}
		}
		if floorsTxt != "" {
			hist = floorsTxt + " " + hist
		}
		out = append(out, FloorNetTarget{
			Key:           floorNetClassAnchor + ":" + a.ip,
			Class:         floorNetClassAnchor,
			Kind:          a.kind,
			Name:          a.name,
			Target:        a.ip,
			Floors:        floorsTxt,
			CIDR:          strings.Join(cidrs, "、"),
			Title:         title,
			HistoryTarget: hist,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class == floorNetClassGateway
		}
		if out[i].Class == floorNetClassGateway {
			return ipLess(out[i].Target, out[j].Target)
		}
		if out[i].Floors != out[j].Floors {
			return out[i].Floors < out[j].Floors
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return ipLess(out[i].Target, out[j].Target)
	})
	return out
}

func containsStr(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func ipLess(a, b string) bool {
	ia, ib := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if ia == nil || ib == nil {
		return a < b
	}
	for i := 0; i < 4; i++ {
		if ia[i] != ib[i] {
			return ia[i] < ib[i]
		}
	}
	return false
}
