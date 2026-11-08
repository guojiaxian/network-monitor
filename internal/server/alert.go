package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"netwatch/internal/proto"
)

// 告警类别
const (
	KindLineDown      = "line_down"       // P1 线路中断（网关正常、公网全部失败；多探针线路需全部报中断）
	KindFloorDown     = "floor_down"      // P1 楼层中断（楼层网关不可达）
	KindBlind         = "blind"           // P0 监控盲区（探针失联）
	KindDegraded      = "degraded"        // P2 线路降质（丢包/时延超阈值）
	KindSpeedLow      = "speed_low"       // P2 网速慢（一轮均值低于阈值，1 分钟后复测仍低才触发）
	KindConflict      = "probe_conflict"  // P2 探针数据冲突（多探针线路中部分报中断）
	KindMonitorDown   = "monitor_down"    // P2 内网服务不可达（中心内置监控）
	KindProbeRes      = "probe_resource"  // P2 探针资源紧张（探针 VM 自身内存/磁盘 >90%）
	KindIndustryDown  = "industry_down"   // P2 行业网不可达（中心对行业网 IP 仅 ICMP ping）
	KindProbeTypeFail = "probe_type_fail" // P2 探测异常（探针某类 TCP/DNS/HTTP 全部目标连续失败 ≥1min；DNS 百分百失败 5 天无人发现的盲区补防）
)

var kindSeverity = map[string]string{
	KindBlind:         "P0",
	KindLineDown:      "P1",
	KindFloorDown:     "P1",
	KindDegraded:      "P2",
	KindSpeedLow:      "P2",
	KindConflict:      "P2",
	KindMonitorDown:   "P2",
	KindProbeRes:      "P2",
	KindIndustryDown:  "P2",
	KindProbeTypeFail: "P2",
}

var kindTitle = map[string]string{
	KindBlind:         "监控盲区：探针失联",
	KindLineDown:      "宽带线路中断",
	KindFloorDown:     "楼层网络中断（网关不可达）",
	KindDegraded:      "线路质量降质（丢包/时延偏高）",
	KindSpeedLow:      "网速慢",
	KindConflict:      "探针数据冲突（部分探针报线路中断）",
	KindMonitorDown:   "内网服务不可达",
	KindProbeRes:      "探针资源紧张（内存/磁盘使用率超 90%）",
	KindIndustryDown:  "行业网不可达",
	KindProbeTypeFail: "探测异常",
}

// AlertMeta 告警关联元信息（渲染邮件用）
type AlertMeta struct {
	Kind         string
	Severity     string
	Title        string
	LineName     string
	Floor        string
	PublicIP     string
	CIDR         string
	ProbeID      string
	ProbeDisplay string // 探针显示名称（名称或主机名）
	StartsAt     time.Time
	ExtraHTML    string
}

// liveAlert 一个进行中的告警事件
type liveAlert struct {
	rowID  int64
	meta   AlertMeta
	firing time.Time
}

// pingRec 5 分钟滑动窗口内的公网 ping 结果
type pingRec struct {
	target string
	ok     bool
	ts     time.Time
}

// probeAgg 单探针聚合状态
type probeAgg struct {
	version string
	// 心跳
	lastSeen time.Time
	everSeen bool
	// 网关 ping
	gwEver       bool
	gwOK         bool
	gwLast       time.Time
	gwFailStreak int
	// 公网 ping 滑动窗口
	pingWin []pingRec
	// 最近各类探测结果（总览展示用）
	lastTCP    *proto.Sample
	lastDNS    *proto.Sample
	lastHTTPIn *proto.Sample // 最近一次 HTTP 结果（单目标聚合简化展示）
	lastPing   *proto.Sample
	lastSpeed  *proto.Sample
	// 探测类型级失败跟踪（P2 探测异常）：type(tcp/dns/http) → target → 进入连续失败的时刻。
	// 成功样本即清除该 target 记录；evaluate 判定"失败集合覆盖该类型全部配置目标且最早起始 ≥1min"
	typeFailTargets map[string]map[string]time.Time
	// 最近任一公网类型（TCP/DNS/HTTP）成功样本时间——线路判活辅助证据：
	// ICMP 通但 TCP/应用层被拦时线路仍应判活（lastPubOK 45s 内有成功 → pubOK45=true）
	lastPubOK time.Time
	// 探针探测到的出口公网 IP（心跳上报，绑定线路时自动回填 lines.public_ip）
	egressIP string
	// 探针自健康与出口扩展信息（心跳上报）
	ipISP, ipGeo, ipV6                     string
	cpuPct, memPct, diskPct, clockOffsetMs float64
	resFailStreak                          int // 资源紧张（mem/disk >90%）连续心跳次数
}

// ProbeStatus 对外（总览 API）暴露的单探针实时状态
type ProbeStatus struct {
	Status        string    `json:"status"` // online / down / floor_down / degraded / blind / speed_low
	RTTms         float64   `json:"rtt_ms"`
	LossPct       float64   `json:"loss_pct"`
	TCPOk         bool      `json:"tcp_ok"`
	HTTPOk        bool      `json:"http_ok"`
	SpeedDown     float64   `json:"speed_down_mbps,omitempty"` // 最近一次测速下行（Mbps）
	SpeedAt       string    `json:"speed_at,omitempty"`
	LastSeen      string    `json:"last_seen"`
	Version       string    `json:"version"`
	EgressIP      string    `json:"egress_ip,omitempty"` // 探测到的出口公网 IP
	EgressISP     string    `json:"egress_isp,omitempty"`
	EgressGeo     string    `json:"egress_geo,omitempty"`
	EgressIPv6    string    `json:"egress_ipv6,omitempty"`
	CPUPct        float64   `json:"cpu_pct,omitempty"`
	MemPct        float64   `json:"mem_pct,omitempty"`
	DiskPct       float64   `json:"disk_pct,omitempty"`
	ClockOffsetMs float64   `json:"clock_offset_ms,omitempty"` // 探针时钟偏移（正=探针慢）
	Minutes       []MinStat `json:"minutes,omitempty"`         // 方格心跳图：近 60 分钟逐分钟 ping 汇总（时间升序，稀疏）
}

// MinStat 方格心跳图的单分钟汇总（1 格 = 1 分钟）：N=该分钟公网 ping 样本数
// （各目标独立计时，样本级判定），F=其中失败样本数。前端状态判定：
// N=0 不产出格子（灰=暂停探测）/ F=N 红（全部失败）/ M>降级阈值 黄（降级，见下）/
// F>0 橙（有失败已恢复）/ 其余深绿（全部成功）。
// 语义与"只要有一个目标成功线路即联通"一致：目标级偶发失败不再显示为半成功。
type MinStat struct {
	T int64 `json:"t"` // 分钟起点（unix 秒，对齐 60s）
	N int   `json:"n"`
	F int   `json:"f"`
	// M：该分钟按降质告警同款滚动窗口（LossWindowMin，见 minuteStats）计算的
	// 各目标丢包率下中位数（%）。前端黄格判定 = M > loss_thresh（接口下发），
	// 与 P2 降质告警（evaluate 的 lossStats）同 helper 同窗口同阈值——逐点一致：
	// 单目标持续全断时中位数为 0，既不标黄也不告警。
	M float64 `json:"m,omitempty"`
}

// Engine 告警引擎：消费探测样本与心跳，按规则判定并触发邮件。
type Engine struct {
	cfg     Config
	store   *Store
	tsdb    *TSDB
	sched   *SpeedSched // 测速调度器（结果回收用，可后置注入）
	shipper *LogShipper // 日志外发器（syslog → 审计服务器，可后置注入）

	refreshMu  sync.Mutex
	refreshSet map[string]bool // 探针立即刷新出口请求（UI 按钮 → 心跳应答夹带下发）

	mu     sync.Mutex
	probes map[string]*probeAgg
	live   map[string]*liveAlert // fingerprint → 进行中的告警
	// fingerprint → 最近一次中断（线路/楼层）收口时间。中断恢复后一个丢包窗口内
	// 不评估 P2 降质：窗口内残留的中断失败样本会让已恢复线路误报"降质"（同一故障双重告警）。
	outageEnd map[string]time.Time

	metaMu    sync.Mutex
	metaUntil time.Time
	metaMap   map[string]LineMeta

	silMu    sync.Mutex
	silUntil time.Time
	silences []SilenceRow // 生效中的静默窗口（短缓存）

	alsMu     sync.Mutex
	alsCache  AlertSettings // 最近一次 evaluate 加载的告警设置（Snapshot/分钟统计与告警同口径）
	alsLoaded bool

	log *log.Logger
}

// SetSched 注入测速调度器
func (e *Engine) SetSched(s *SpeedSched) { e.sched = s }

// setALS 缓存告警设置（evaluate 每 5s 刷新，Start 预加载）。
// Snapshot/minuteStats 与 evaluate 用同一套阈值，展示与告警不再两套口径。
func (e *Engine) setALS(als AlertSettings) {
	e.alsMu.Lock()
	e.alsCache = als
	e.alsLoaded = true
	e.alsMu.Unlock()
}

// cachedALS 告警设置缓存（未加载过时用默认值）
func (e *Engine) cachedALS() AlertSettings {
	e.alsMu.Lock()
	defer e.alsMu.Unlock()
	if !e.alsLoaded {
		return DefaultAlertSettings()
	}
	return e.alsCache
}

// SetShipper 注入日志外发器
func (e *Engine) SetShipper(s *LogShipper) { e.shipper = s }

// getMeta 线路元信息（10s 缓存）：metrics 上报热路径避免每批次查库
func (e *Engine) getMeta(ctx context.Context) (map[string]LineMeta, error) {
	e.metaMu.Lock()
	defer e.metaMu.Unlock()
	if time.Now().Before(e.metaUntil) && e.metaMap != nil {
		return e.metaMap, nil
	}
	m, err := e.store.ProbeLineMeta(ctx)
	if err != nil {
		if e.metaMap != nil {
			return e.metaMap, nil // 退化用旧缓存
		}
		return nil, err
	}
	e.metaMap = m
	e.metaUntil = time.Now().Add(10 * time.Second)
	return m, nil
}

func NewEngine(cfg Config, store *Store, tsdb *TSDB) *Engine {
	e := &Engine{
		cfg:        cfg,
		store:      store,
		tsdb:       tsdb,
		refreshSet: map[string]bool{},
		probes:     map[string]*probeAgg{},
		live:       map[string]*liveAlert{},
		outageEnd:  map[string]time.Time{},
		log:        log.New(os.Stderr, "alert ", log.LstdFlags|log.Lmsgprefix),
	}
	return e
}

// Start 启动评估循环。重启时先收口遗留的 firing 告警。
func (e *Engine) Start(ctx context.Context) {
	if err := e.store.ResolveAllFiring(ctx); err != nil {
		e.log.Printf("resolve stale firing alerts: %v", err)
	}
	if als, err := e.alertSettings(ctx); err == nil {
		e.setALS(als) // 预加载：首评前的 Snapshot 也有阈值可用
	}
	go e.backfillRing(ctx) // 从 VM 回填样本环：重启后心跳条立即满格，不再从零累计
	go func() {
		// 评估周期 = alert_settings.eval_interval_s（默认 5s，探测配置页可改）：
		// 每轮评估后按缓存配置重置节奏，改周期无需重启即生效
		interval := 5 * time.Second
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.evaluate()
				// 旧 settings 行没有 eval_interval_s（解析为 0）：仅接受正值重置，
				// 否则 t.Reset(0) 会 panic 崩溃循环（存储值合法性由 putSettings 钳位 2~60 保证）
				if want := time.Duration(e.cachedALS().EvalIntervalS) * time.Second; want >= time.Second && want != interval {
					interval = want
					t.Reset(interval)
				}
			}
		}
	}()
}

// Ingest 样本入口：写时序库 + 更新引擎状态（由 agent API 调用）。
func (e *Engine) Ingest(ctx context.Context, agentID string, samples []proto.Sample) {
	// 时钟漂移防护：探针时钟快于服务器超过 2 分钟的样本钳位到服务器当前时间。
	// 未来时间戳会让时序数据"未发生先入库"，使 45s/5min 判定窗口失真、漏报。
	// 只钳位未来偏移（历史回填场景不受影响）；并记日志提示该探针需要校 NTP。
	nowMs := time.Now().UnixMilli()
	skewWarned := false
	for i := range samples {
		if samples[i].TS > nowMs+2*60*1000 {
			if !skewWarned {
				e.log.Printf("clock skew: probe=%s 样本时间快于服务器 >2min，已钳位（请为该探针 VM 配置 NTP）", agentID)
				skewWarned = true
			}
			samples[i].TS = nowMs
		}
	}

	// 线路标签用于时序库；同时取回线路元信息做带宽达标判定
	var line string
	var lm LineMeta
	if m, err := e.getMeta(ctx); err == nil {
		if mm, ok := m[agentID]; ok {
			lm = mm
			if mm.Bound {
				line = mm.LineName
			}
		}
	}
	if err := e.tsdb.WriteBatch(ctx, agentID, line, samples); err != nil {
		e.log.Printf("tsdb write (%s): %v", agentID, err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	agg := e.aggFor(agentID)
	for i := range samples {
		s := samples[i]
		ts := time.UnixMilli(s.TS)
		if s.Type == proto.TypePing {
			if s.Extra["gw"] == 1 {
				agg.gwEver = true
				agg.gwLast = ts
				if s.OK {
					agg.gwOK = true
					agg.gwFailStreak = 0
				} else {
					agg.gwOK = false
					agg.gwFailStreak++
				}
				if agg.lastPing == nil || ts.After(time.UnixMilli(agg.lastPing.TS)) {
					cp := s
					agg.lastPing = &cp
				}
				continue
			}
			agg.pingWin = append(agg.pingWin, pingRec{target: s.Target, ok: s.OK, ts: ts})
			if agg.lastPing == nil || ts.After(time.UnixMilli(agg.lastPing.TS)) {
				cp := s
				agg.lastPing = &cp
			}
		}
		switch s.Type {
		case proto.TypeTCP:
			cp := s
			agg.lastTCP = &cp
			trackTypeFail(agg, "tcp", s.Target, s.OK, ts)
			if s.OK {
				agg.lastPubOK = ts
			}
		case proto.TypeDNS:
			cp := s
			agg.lastDNS = &cp
			trackTypeFail(agg, "dns", s.Target, s.OK, ts)
			if s.OK {
				agg.lastPubOK = ts
			}
		case proto.TypeHTTP:
			cp := s
			agg.lastHTTPIn = &cp
			trackTypeFail(agg, "http", s.Target, s.OK, ts)
			if s.OK {
				agg.lastPubOK = ts // 链路可达语义（含 5xx），线路判活的辅助证据
			}
		case proto.TypeSpeed:
			cp := s
			agg.lastSpeed = &cp
			// 任务回收 + 低速复测判定（并行调度，逐探针独立）：
			// 低速首测排 1 分钟后复测；复测仍低速才触发"网速慢"。
			// 收口直接按"实测达标 + 告警在途"判定，不依赖调度器内存复测标记
			//（标记重启即失，重启前已触发的告警会因 Resolved 不置位而永不收口——实测踩坑）。
			if e.sched != nil {
				v := e.sched.OnResult(agentID, s.Extra["down_mbps"], s.OK)
				fp := KindSpeedLow + ":" + agentID
				down := s.Extra["down_mbps"]
				switch {
				case v.Confirmed && e.live[fp] == nil:
					e.fireLocked(fp, AlertMeta{
						Kind: KindSpeedLow, Severity: kindSeverity[KindSpeedLow],
						Title: kindTitle[KindSpeedLow], ProbeID: agentID,
						LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
						StartsAt: ts,
						ExtraHTML: "首测均值 " + ftoa(v.FirstAvg) + "Mbps，1 分钟后复测均值 " + ftoa(down) +
							"Mbps，仍低于阈值 " + ftoa(v.Threshold) + "Mbps（每轮连测取平均，恢复至阈值以上自动解除）",
					}, agentID, lm)
				case s.OK && down >= v.Threshold && e.live[fp] != nil:
					e.resolveLocked(fp, "测速恢复至 "+ftoa(down)+"Mbps")
				}
				// 日志外发：每轮测速均值一条（事件量 = 探针数 × 测速周期，极低频）
				if e.shipper != nil {
					verdict := "ok"
					switch {
					case v.Confirmed:
						verdict = "low_confirmed"
					case v.Pending:
						verdict = "low_retesting"
					case !s.OK:
						verdict = "failed"
					}
					e.shipper.Ship(speedLogEvent(probeDisplay(agentID, lm), lm.LineName,
						s.Extra["down_mbps"], s.Extra["up_mbps"], int(s.Extra["ok_runs"]), v.Threshold, verdict))
				}
			}
		}
	}
}

// alertSettings 读取告警阈值设置
func (e *Engine) alertSettings(ctx context.Context) (AlertSettings, error) {
	var als AlertSettings
	err := e.store.GetSetting(ctx, "alert_settings", &als)
	return als, err
}

// OnHeartbeat 更新心跳；失联告警随之恢复。
func (e *Engine) OnHeartbeat(agentID string, hb proto.Heartbeat) {
	e.mu.Lock()
	defer e.mu.Unlock()
	agg := e.aggFor(agentID)
	agg.lastSeen = time.Now()
	agg.everSeen = true
	agg.version = hb.Version
	e.resolveLocked(KindBlind+":"+agentID, "心跳恢复")

	// 时钟偏移（探针时间戳 vs 服务器接收时刻；样本钳位机制另在 Ingest 兜底 >2min）
	if hb.AgentTS > 0 {
		agg.clockOffsetMs = float64(time.Now().UnixMilli() - hb.AgentTS)
	}
	agg.cpuPct, agg.memPct, agg.diskPct = hb.CPUPct, hb.MemPct, hb.DiskPct

	// 出口信息对账（egress-identity 拓扑：出口 IP ↔ 线路 ↔ 楼层是唯一识别链）：
	// - 绑定线路未登记出口 IP（新建线）→ 用探针探测值回填（首次接线即完成线路身份登记，留痕）
	// - 绑定线路出口与实际出口一致 → 仅对账运营商/归属地元数据
	// - 不一致 → 两种语义（2026-11-07 用户决议，取代旧"绝不回写、无匹配自动建线"）：
	//   ① 出口命中其他线路身份 → 探针随策略路由调整/接线变更落到别的线路：自动改绑；
	//   ② 出口无任何匹配 → 家宽 PPPoE 重拨等动态换 IP，线路实体未变：自动回写绑定
	//      线路的出口 IP 并在 line_ip_history 留痕（拆线新建会让线路数虚增、楼层关联
	//      与告警历史断链——2026-11-07 凌晨线路11重拨换 IP 实测暴露）。
	needMeta := hb.PublicIP != "" || hb.IPISP != "" || hb.IPGeo != ""
	if !needMeta {
		return
	}
	// PublicIP 空值=探测不到出口（出网中断/回显全挂）：如实清空，与 ipV6 同语义——
	// 保留旧值会让 egress-identity 匹配基于过期信息（实测踩坑）
	agg.egressIP = hb.PublicIP
	if hb.IPISP != "" {
		agg.ipISP = hb.IPISP
	}
	if hb.IPGeo != "" {
		agg.ipGeo = hb.IPGeo
	}
	// IPv6 无"手工兜底"语义，空值=明确无 v6（本机无全局 v6 地址或探测失败），
	// 无条件赋值——否则探针修复/降级后旧值会在引擎里永久残留（实测踩坑）
	agg.ipV6 = hb.IPv6
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := e.getMeta(ctx)
	if err != nil {
		return
	}
	lm, ok := m[agentID]
	if !ok {
		return
	}
	disp := probeDisplay(agentID, lm)

	if !lm.Bound {
		// 未绑定探针：出口 IP 命中某条线路的身份（public_ip 一致）即自动绑定
		// （egress-identity 闭环——探针上线/策略路由调整后自动归位；
		//   未命中不动作不建线，自动建线只发生在零接触开通流程）
		if hb.PublicIP == "" {
			return
		}
		target, err := e.store.FindLineByPublicIP(ctx, hb.PublicIP)
		if err != nil || target == 0 {
			return
		}
		if err := e.store.AssignProbeLine(ctx, agentID, &target); err == nil {
			lname := ""
			if lines, lerr := e.store.ListLines(ctx); lerr == nil {
				for _, l := range lines {
					if l.ID == target {
						lname = l.Name
					}
				}
			}
			e.log.Printf("探针 %s 自动匹配绑定线路「%s」（出口 IP %s 一致；如非预期请检查该探针的策略路由）",
				disp, lname, hb.PublicIP)
			e.store.WriteAudit(ctx, "system", "auto-bind:"+disp,
				fmt.Sprintf("egress=%s line=%d", hb.PublicIP, target))
			if e.shipper != nil {
				e.shipper.Ship(egressLogEvent("探针出口自动绑定线路", [][2]string{
					{"line", lname}, {"probe", disp}, {"new_ip", hb.PublicIP}, {"source", "auto-bind"},
				}))
			}
		}
		return
	}

	if lm.PublicIP == "" {
		// 线路身份未登记：首次接线，回填（留痕——首次登记是 IP 变化轨迹的起点）
		if hb.PublicIP == "" {
			return
		}
		if err := e.store.UpdateLineEgressInfo(ctx, lm.LineID, hb.PublicIP, hb.IPISP, hb.IPGeo); err == nil {
			e.log.Printf("线路 %s 出口公网 IP 首次登记: %s (%s %s) (探针 %s)",
				lm.LineName, hb.PublicIP, hb.IPISP, hb.IPGeo, disp)
			if err := e.store.RecordLineIPChange(ctx, lm.LineID, "", hb.PublicIP, agentID, disp, "egress"); err != nil {
				e.log.Printf("线路 IP 变化留痕失败(首次登记): %v", err)
			}
			if e.shipper != nil {
				e.shipper.Ship(egressLogEvent("线路出口 IP 首次登记", [][2]string{
					{"line", lm.LineName}, {"probe", disp}, {"new_ip", hb.PublicIP},
					{"isp", hb.IPISP}, {"geo", hb.IPGeo}, {"source", "egress"},
				}))
			}
			e.bumpMetaEgress(lm.LineID, hb.PublicIP, hb.IPISP, hb.IPGeo)
		}
		return
	}
	if hb.PublicIP != "" && lm.PublicIP != hb.PublicIP {
		// 出口与绑定线路身份不符：改绑到出口对应的线路；无匹配=动态换 IP，回写绑定线路
		target, err := e.store.FindLineByPublicIP(ctx, hb.PublicIP)
		if err != nil {
			return
		}
		if target != 0 {
			// 语义①：新出口命中其他线路身份 → 探针被策略路由调整/接线变更，改绑过去
			if target == lm.LineID {
				return
			}
			if err := e.store.AssignProbeLine(ctx, agentID, &target); err == nil {
				lname := ""
				if lines, lerr := e.store.ListLines(ctx); lerr == nil {
					for _, l := range lines {
						if l.ID == target {
							lname = l.Name
						}
					}
				}
				e.log.Printf("探针 %s 出口 IP 变更 %s → %s：已自动改绑线路「%s」→「%s」（策略路由变更或接线调整；如非预期请检查防火墙）",
					disp, lm.PublicIP, hb.PublicIP, lm.LineName, lname)
				e.store.WriteAudit(ctx, "system", "egress-rebind:"+disp,
					fmt.Sprintf("%s→%s line:%d→%d", lm.PublicIP, hb.PublicIP, lm.LineID, target))
				if e.shipper != nil {
					e.shipper.Ship(egressLogEvent("探针出口变更自动改绑线路", [][2]string{
						{"line", lname}, {"old_line", lm.LineName}, {"probe", disp},
						{"old_ip", lm.PublicIP}, {"new_ip", hb.PublicIP}, {"source", "egress-rebind"},
					}))
				}
			}
			return
		}
		// 语义②：出口无匹配且探针已绑定线路 → 动态换 IP（家宽重拨等），回写绑定线路
		// 身份并留痕（旧"无匹配自动建线"会让线路数虚增、楼层关联与历史断链）
		if err := e.store.UpdateLineEgressInfo(ctx, lm.LineID, hb.PublicIP, hb.IPISP, hb.IPGeo); err != nil {
			e.log.Printf("线路出口 IP 动态回写失败: %v", err)
			return
		}
		if err := e.store.RecordLineIPChange(ctx, lm.LineID, lm.PublicIP, hb.PublicIP, agentID, disp, "egress"); err != nil {
			e.log.Printf("线路 IP 变化留痕失败: %v", err)
		}
		e.log.Printf("线路 %s 出口 IP 动态变更 %s → %s：已自动回写线路身份（探针 %s；家宽重拨换 IP 或线路调整）",
			lm.LineName, lm.PublicIP, hb.PublicIP, disp)
		e.store.WriteAudit(ctx, "system", "egress-change:"+disp,
			fmt.Sprintf("%s→%s line:%d(%s)", lm.PublicIP, hb.PublicIP, lm.LineID, lm.LineName))
		if e.shipper != nil {
			e.shipper.Ship(egressLogEvent("线路出口 IP 动态变更", [][2]string{
				{"line", lm.LineName}, {"probe", disp},
				{"old_ip", lm.PublicIP}, {"new_ip", hb.PublicIP}, {"source", "egress"},
			}))
		}
		e.bumpMetaEgress(lm.LineID, hb.PublicIP, hb.IPISP, hb.IPGeo)
		return
	}
	// 出口一致：仅对账元数据（IP 是线路身份，不回写）
	if (hb.IPISP != "" && lm.ISP != hb.IPISP) || (hb.IPGeo != "" && lm.Geo != hb.IPGeo) {
		_ = e.store.UpdateLineEgressInfo(ctx, lm.LineID, "", hb.IPISP, hb.IPGeo)
	}
}

// bumpMetaEgress 对账回写线路出口后同步内存 meta 缓存（10s TTL 窗口内同线路探针的
// 后续心跳不再拿旧 IP 重复写库/重复留痕）
func (e *Engine) bumpMetaEgress(lineID int, ip, isp, geo string) {
	e.metaMu.Lock()
	defer e.metaMu.Unlock()
	for id, lm := range e.metaMap {
		if lm.LineID != lineID {
			continue
		}
		if ip != "" {
			lm.PublicIP = ip
		}
		if isp != "" {
			lm.ISP = isp
		}
		if geo != "" {
			lm.Geo = geo
		}
		e.metaMap[id] = lm
	}
}

// RequestEgressRefresh 请求指定探针立即重探出口信息（UI"立即刷新"按钮）。
// 探针无入站通道，指令经心跳应答夹带下发（≤15s 到达探针）。
func (e *Engine) RequestEgressRefresh(ids []string) {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	for _, id := range ids {
		e.refreshSet[id] = true
	}
}

// PopEgressRefresh 心跳处理时取走该探针的刷新请求（一次性）
func (e *Engine) PopEgressRefresh(id string) bool {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	v := e.refreshSet[id]
	delete(e.refreshSet, id)
	return v
}

func (e *Engine) aggFor(id string) *probeAgg {
	a, ok := e.probes[id]
	if !ok {
		a = &probeAgg{}
		e.probes[id] = a
	}
	return a
}

// lossStats 统计滑动窗口内公网 ping：
// 返回（45 秒内是否任一目标成功、各目标丢包率的下中位数、样本量是否足够）。
// 下中位数设计：多数目标劣化才判降质，单个目标持续失败（如个别 DNS 被墙）不误报。
// typeFailWindow 探测异常判定窗口：某类探测全部配置目标连续失败超过该时长才告警
// （alert_settings.type_fail_window_s，默认 60s、钳 30~600；单目标偶发失败不告警，防坏目标噪音）。
func (e *Engine) typeFailWindow() time.Duration {
	secs := e.cachedALS().TypeFailWindowS
	if secs < 30 || secs > 600 {
		secs = 60
	}
	return time.Duration(secs) * time.Second
}

// trackTypeFail 维护类型级失败跟踪：失败样本记 target 进入连续失败的时刻（首次失败时间），
// 成功样本即清除。Ingest 锁内调用。
func trackTypeFail(agg *probeAgg, typ, target string, ok bool, ts time.Time) {
	if agg.typeFailTargets == nil {
		agg.typeFailTargets = map[string]map[string]time.Time{}
	}
	m := agg.typeFailTargets[typ]
	if ok {
		delete(m, target)
		return
	}
	if m == nil {
		m = map[string]time.Time{}
		agg.typeFailTargets[typ] = m
	}
	if _, has := m[target]; !has {
		m[target] = ts
	}
}

// typeFailHit 判定某类型是否满足"全部配置目标连续失败 ≥win"：
// 失败记录恰好覆盖全部目标、且最早的失败起始距今 ≥win。返回是否触发与失败起始时间。
func typeFailHit(m map[string]time.Time, targets []string, now time.Time, win time.Duration) (bool, time.Time) {
	if len(targets) == 0 || len(m) != len(targets) {
		return false, time.Time{}
	}
	earliest := now
	for _, tg := range targets {
		t0, has := m[tg]
		if !has {
			return false, time.Time{}
		}
		if t0.Before(earliest) {
			earliest = t0
		}
	}
	return now.Sub(earliest) >= win, earliest
}

// evalTypeFail 对单探针的 TCP/DNS/HTTP 三类探测做"全部目标连续失败"判定（evaluate 锁内调用）。
// 触发 → P2 探测异常（fingerprint 按探针+类型）；任一目标恢复（失败集合不再覆盖全部目标）→ 收口。
func (e *Engine) evalTypeFail(probeID string, agg *probeAgg, probeCfg *proto.ProbeConfig, lm LineMeta, now time.Time) {
	typeDefs := []struct {
		typ, label string
		targets    []string
	}{
		{"tcp", "TCP", probeCfg.TCPTargets},
		{"dns", "DNS", probeCfg.DNSServers},
		{"http", "HTTP", probeCfg.HTTPTargets},
	}
	win := e.typeFailWindow()
	winTxt := itoa(int(win.Seconds())) + " 秒"
	if win%time.Minute == 0 {
		winTxt = itoa(int(win.Minutes())) + " 分钟"
	}
	for _, d := range typeDefs {
		if len(d.targets) == 0 {
			continue
		}
		m := agg.typeFailTargets[d.typ]
		// 清理已不在配置中的目标：残留记录会阻断"覆盖全部目标"判定
		for tg := range m {
			if !slices.Contains(d.targets, tg) {
				delete(m, tg)
			}
		}
		hit, earliest := typeFailHit(m, d.targets, now, win)
		fp := KindProbeTypeFail + ":" + probeID + ":" + d.typ
		switch {
		case hit && e.live[fp] == nil:
			e.fireLocked(fp, AlertMeta{
				Kind: KindProbeTypeFail, Severity: kindSeverity[KindProbeTypeFail],
				Title: kindTitle[KindProbeTypeFail] + "（" + d.label + "）", ProbeID: probeID,
				LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
				StartsAt:  earliest,
				ExtraHTML: "全部 " + itoa(len(d.targets)) + " 个目标连续失败超过 " + winTxt + "。线路本身正常（ICMP 可达），多为该协议被网络策略拦截或目标集体失效，请检查。",
			}, probeID, lm)
		case !hit && e.live[fp] != nil:
			e.resolveLocked(fp, "探测恢复")
		}
	}
}

// medianLoss 由"目标 → [总数, 失败数]"计算各目标丢包率的下中位数（%）。
// 降质判定与心跳黄格的唯一口径：每目标 ≥10 样本才参与，且所有目标都参与
// （门槛必须同时成立——只统计到部分目标时中位数会退化为单个目标的丢包率，
// 探针刚启动时曾把"唯一凑满样本的坏目标"误判为整条线路降质）。样本不足返回 false，
// 调用方维持原状（告警不触发/黄格不点亮），与"样本不足不下结论"的告警行为一致。
func lossMedian(perTarget map[string][2]int) (float64, bool) {
	var ratios []float64
	for _, p := range perTarget {
		if p[0] >= 10 {
			ratios = append(ratios, float64(p[1])*100/float64(p[0]))
		}
	}
	if len(ratios) == 0 || len(ratios) != len(perTarget) {
		return 0, false
	}
	sort.Float64s(ratios)
	return ratios[(len(ratios)-1)/2], true
}

func (agg *probeAgg) lossStats(now time.Time, win time.Duration) (anyOK45 bool, medianLoss float64, enough bool) {
	perTarget := map[string][2]int{} // target → [总数, 失败数]
	for _, r := range agg.pingWin {
		if now.Sub(r.ts) > win {
			continue
		}
		p := perTarget[r.target]
		p[0]++
		if !r.ok {
			p[1]++
		}
		perTarget[r.target] = p
		if r.ok && now.Sub(r.ts) < 45*time.Second {
			anyOK45 = true
		}
	}
	medianLoss, enough = lossMedian(perTarget)
	return anyOK45, medianLoss, enough
}

// evaluate 周期评估所有探针状态并做告警迁移。
func (e *Engine) evaluate() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	als, err := e.alertSettings(ctx)
	if err != nil {
		e.log.Printf("alert settings: %v", err)
		return
	}
	e.setALS(als) // 供 Snapshot/分钟统计与告警同口径
	meta, err := e.getMeta(ctx)
	if err != nil {
		e.log.Printf("line meta: %v", err)
		return
	}
	var probeCfg proto.ProbeConfig
	_ = e.store.GetSetting(ctx, "agent_config", &probeCfg)

	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()

	lineVotes := map[int]*lineVote{} // lineID → 本轮有效证人投票

	for probeID, agg := range e.probes {
		lm, known := meta[probeID]
		if !known {
			// 探针已在后台删除：清状态、收口其活动告警
			for _, kind := range []string{KindBlind, KindLineDown, KindFloorDown, KindDegraded, KindSpeedLow} {
				e.resolveLocked(kind+":"+probeID, "探针已删除")
			}
			for _, typ := range []string{"tcp", "dns", "http"} {
				e.resolveLocked(KindProbeTypeFail+":"+probeID+":"+typ, "探针已删除")
			}
			delete(e.probes, probeID)
			continue
		}

		// P0 监控盲区：心跳超时
		if agg.everSeen && now.Sub(agg.lastSeen) > time.Duration(als.BlindSecs)*time.Second {
			e.fireLocked(KindBlind+":"+probeID, AlertMeta{
				Kind: KindBlind, Severity: kindSeverity[KindBlind],
				Title: kindTitle[KindBlind], ProbeID: probeID,
				LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
				StartsAt:  agg.lastSeen.Add(time.Duration(als.BlindSecs) * time.Second),
				ExtraHTML: "最后心跳：" + agg.lastSeen.Format("15:04:05"),
			}, probeID, lm)
			continue // 盲区时其他判定无意义
		} else if agg.everSeen {
			e.resolveLocked(KindBlind+":"+probeID, "")
		}

		// 窗口修剪：方格心跳图要展示近 60 分钟（1 格 = 1 分钟），多留 5 分钟余量
		cutoff := now.Add(-65 * time.Minute)
		kept := agg.pingWin[:0]
		for _, r := range agg.pingWin {
			if r.ts.After(cutoff) {
				kept = append(kept, r)
			}
		}
		agg.pingWin = kept

		// P1 楼层中断：网关连续失败（样本本身足够说明）
		if agg.gwEver && now.Sub(agg.gwLast) < 90*time.Second && agg.gwFailStreak >= 3 {
			e.fireLocked(KindFloorDown+":"+probeID, AlertMeta{
				Kind: KindFloorDown, Severity: kindSeverity[KindFloorDown],
				Title: kindTitle[KindFloorDown], ProbeID: probeID,
				LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
				StartsAt:  agg.gwLast,
				ExtraHTML: "楼层网关连续 " + itoa(agg.gwFailStreak) + " 次探测失败",
			}, probeID, lm)
		} else {
			e.resolveLocked(KindFloorDown+":"+probeID, "网关恢复可达")
		}

		// P1 线路中断投票：探针在线 + 网关可达（有效证人）时对"公网是否全断"投票。
		// 多探针线路：全部证人报中断才升 P1；部分报中断 → P2 数据冲突；未绑定线路的探针维持按探针判定。
		probeAlive := agg.everSeen && now.Sub(agg.lastSeen) < 90*time.Second
		gwOKRecent := agg.gwEver && agg.gwOK && now.Sub(agg.gwLast) < 90*time.Second
		pubOK45, _, pingEnough := agg.lossStats(now, 6*time.Minute)
		// 线路判活扩展：TCP/DNS/HTTP 任一目标 45s 内成功同样证明线路可达——
		// ICMP 通但应用层协议被拦/目标失效时，线路不应被误判中断（任一协议通即活）
		if !pubOK45 && !agg.lastPubOK.IsZero() && now.Sub(agg.lastPubOK) < 45*time.Second {
			pubOK45 = true
		}
		if lm.Bound {
			e.resolveLocked(KindLineDown+":"+probeID, "判定已改为线路级")
			lv := lineVotes[lm.LineID]
			if lv == nil {
				lv = &lineVote{meta: lm}
				lineVotes[lm.LineID] = lv
			}
			if probeAlive && gwOKRecent && pingEnough {
				if !pubOK45 && len(probeCfg.ICMPTargets) > 0 {
					lv.down++
					lv.downIDs = append(lv.downIDs, probeID)
				} else {
					lv.up++
					lv.upIDs = append(lv.upIDs, probeID)
				}
			}
		} else {
			lineDownActive := e.live[KindLineDown+":"+probeID] != nil
			if probeAlive && gwOKRecent && pingEnough && !pubOK45 && len(probeCfg.ICMPTargets) > 0 {
				e.fireLocked(KindLineDown+":"+probeID, AlertMeta{
					Kind: KindLineDown, Severity: kindSeverity[KindLineDown],
					Title: kindTitle[KindLineDown], ProbeID: probeID,
					LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
					StartsAt:  now.Add(-time.Duration(als.LineDownSecs) * time.Second),
					ExtraHTML: "楼层网关可达但全部公网探测目标失败（目标数 " + itoa(len(probeCfg.ICMPTargets)) + "）",
				}, probeID, lm)
			} else if !lineDownActive || pubOK45 {
				e.resolveLocked(KindLineDown+":"+probeID, "公网连通恢复")
			}
		}

		// P2 探测异常：某类探测（TCP/DNS/HTTP）全部配置目标连续失败 ≥1min。
		// DNS 曾 100% 失败 5 天无人发现——ping 类告警体系对此完全免疫的盲区补防；
		// 单目标失败不告警（防坏目标噪音），ping 不参与（已有线路中断/降质体系）。
		// 按内网组配置判定：外网组探针 TCP/DNS 目标数为 0 自动跳过（外网组部署时再适配其配置）。
		e.evalTypeFail(probeID, agg, &probeCfg, lm, now)

		// P2 探针资源紧张：探针 VM 自身内存/磁盘 >90% 连续 3 次心跳（≈45s），
		// 回落 <80% 恢复。探针磁盘满会砸掉本地缓存/上报，属预判性告警。
		{
			high := agg.memPct > 90 || agg.diskPct > 90
			low := (agg.memPct == 0 || agg.memPct < 80) && (agg.diskPct == 0 || agg.diskPct < 80)
			fp := KindProbeRes + ":" + probeID
			if high && agg.everSeen {
				agg.resFailStreak++
			}
			if high && agg.everSeen && agg.resFailStreak >= 3 {
				e.fireLocked(fp, AlertMeta{
					Kind: KindProbeRes, Severity: kindSeverity[KindProbeRes],
					Title: kindTitle[KindProbeRes], ProbeID: probeID,
					LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
					StartsAt:  now,
					ExtraHTML: "内存 " + ftoa(agg.memPct) + "% / 磁盘 " + ftoa(agg.diskPct) + "%（连续 " + itoa(agg.resFailStreak) + " 次心跳 >90%）——请检查该探针 VM 资源",
				}, probeID, lm)
			} else if low && agg.resFailStreak > 0 {
				agg.resFailStreak = 0
				e.resolveLocked(fp, "资源使用率已回落")
			}
		}

		// P2 线路降质：窗口内各目标丢包率下中位数超阈值（线路中断时不叠加）
		winMin := time.Duration(als.LossWindowMin) * time.Minute
		_, medianLoss, enough := agg.lossStats(now, winMin)
		if enough {
			fp := KindDegraded + ":" + probeID
			active := e.live[fp] != nil
			// 中断进行中或刚收口一个窗口内：窗口样本被中断污染，不判定降质
			suppressed := e.outageActiveLocked(lm, probeID) ||
				e.recentOutageLocked(lm, probeID, now, winMin)
			if suppressed {
				if active {
					e.resolveLocked(fp, "线路中断（或刚恢复），降质并入中断事件")
				}
			} else if !active && medianLoss > als.LossPctThresh {
				e.fireLocked(fp, AlertMeta{
					Kind: KindDegraded, Severity: kindSeverity[KindDegraded],
					Title: kindTitle[KindDegraded], ProbeID: probeID,
					LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
					StartsAt:  now,
					ExtraHTML: itoa(als.LossWindowMin) + " 分钟窗口目标丢包率中位数 " + ftoa(medianLoss) + "%（阈值 " + ftoa(als.LossPctThresh) + "%）",
				}, probeID, lm)
			} else if active && medianLoss <= als.LossPctThresh*0.7 {
				e.resolveLocked(fp, "丢包率回落至 "+ftoa(medianLoss)+"%")
			}
		}
	}

	// P1 线路中断 / P2 探针数据冲突：按线路汇总证人投票
	for lineID, lv := range lineVotes {
		fireDown, fireConflict := decideLine(lv.down, lv.up)
		fp := KindLineDown + ":line" + itoa(lineID)
		fpC := KindConflict + ":line" + itoa(lineID)
		lm := lv.meta
		if fireDown {
			e.fireLocked(fp, AlertMeta{
				Kind: KindLineDown, Severity: kindSeverity[KindLineDown],
				Title: kindTitle[KindLineDown], ProbeID: strings.Join(lv.downIDs, ","),
				LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
				StartsAt:  now.Add(-time.Duration(als.LineDownSecs) * time.Second),
				ExtraHTML: "全部 " + itoa(lv.down) + " 台有效探针均报公网中断（交叉验证一致）",
			}, strings.Join(lv.downIDs, ","), lm)
			e.resolveLocked(fpC, "探针意见一致")
		} else if fireConflict {
			e.fireLocked(fpC, AlertMeta{
				Kind: KindConflict, Severity: kindSeverity[KindConflict],
				Title: kindTitle[KindConflict], ProbeID: strings.Join(lv.downIDs, ","),
				LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
				StartsAt:  now,
				ExtraHTML: itoa(lv.down) + "/" + itoa(lv.down+lv.up) + " 台探针报中断，其余正常：线路大概率正常，请优先检查异常探针（位置见探针 ID）",
			}, strings.Join(lv.downIDs, ","), lm)
			e.resolveLocked(fp, "存在正常探针，交叉验证不确认中断")
		} else if lv.up > 0 {
			e.resolveLocked(fp, "公网连通恢复")
			e.resolveLocked(fpC, "探针意见一致")
		}
	}
}

// outageActiveLocked 该探针关联线路当前是否已确认 P1 中断（线路级中断 / 楼层网关中断 /
// 未绑定线路的按探针中断）。需在 e.mu 锁内调用。降质告警在确认中断后不再叠加。
func (e *Engine) outageActiveLocked(lm LineMeta, probeID string) bool {
	if lm.Bound {
		return e.live[KindLineDown+":line"+itoa(lm.LineID)] != nil ||
			e.live[KindFloorDown+":"+probeID] != nil
	}
	return e.live[KindLineDown+":"+probeID] != nil ||
		e.live[KindFloorDown+":"+probeID] != nil
}

// recentOutageLocked 该探针/线路是否在最近一个丢包窗口内刚收口过 P1 中断。
// 中断期间的失败样本仍留在 pingWin 中，若立即评估降质会把"刚恢复的正常线路"
// 误报为丢包超阈值（生产演练实测：线路中断 1 分钟，恢复后立刻出现 P2 降质并
// 持续到窗口滚出）。收口后等一个完整窗口再评估，避免同一故障双重告警。
// 需在 e.mu 锁内调用。
func (e *Engine) recentOutageLocked(lm LineMeta, probeID string, now time.Time, win time.Duration) bool {
	keys := []string{KindLineDown + ":" + probeID, KindFloorDown + ":" + probeID}
	if lm.Bound {
		keys = append(keys, KindLineDown+":line"+itoa(lm.LineID))
	}
	for _, k := range keys {
		if t, ok := e.outageEnd[k]; ok && now.Sub(t) < win {
			return true
		}
	}
	return false
}

// lineVote 一条线路在单个评估周期内的有效证人投票
type lineVote struct {
	down, up int
	downIDs  []string
	upIDs    []string
	meta     LineMeta
}

// decideLine 线路级投票判定：全部证人报中断→确认 P1；部分报中断→P2 冲突；无中断→正常。
func decideLine(down, up int) (fireDown, fireConflict bool) {
	switch {
	case down > 0 && up == 0:
		fireDown = true
	case down > 0 && up > 0:
		fireConflict = true
	}
	return
}

// silenced 该线路当前是否处于静默窗口
func (e *Engine) silenced(lineID int) bool {
	e.silMu.Lock()
	defer e.silMu.Unlock()
	if time.Now().After(e.silUntil) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if rows, err := e.store.ActiveSilences(ctx); err == nil {
			e.silences = rows
		}
		cancel()
		e.silUntil = time.Now().Add(30 * time.Second)
	}
	for _, s := range e.silences {
		if s.LineID == nil || lineID == 0 || *s.LineID == lineID {
			return true
		}
	}
	return false
}

// probeDisplay 探针显示名（优先后台名称，回退主机名，再回退 ID 前 8 位）
func probeDisplay(id string, lm LineMeta) string {
	if lm.ProbeName != "" {
		return lm.ProbeName
	}
	if lm.ProbeHostname != "" {
		return lm.ProbeHostname
	}
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// fireLocked 触发（或维持）告警：首次触发持久化 + 发邮件。静默窗口内不触发。
func (e *Engine) fireLocked(fp string, meta AlertMeta, probeID string, lm LineMeta) {
	if _, ok := e.live[fp]; ok {
		return // 进行中，冷却期内不重复通知
	}
	if e.silenced(lm.LineID) {
		return // 静默期内不触发；窗口结束后若故障仍在则自然触发
	}
	meta.Severity = kindSeverity[meta.Kind]
	meta.Title = decorateTitle(meta, lm)
	meta.ProbeDisplay = probeDisplay(probeID, lm)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	row := &AlertRow{
		Fingerprint: fp, Severity: meta.Severity, Kind: meta.Kind, Title: meta.Title,
		ProbeID: probeID, StartsAt: meta.StartsAt,
	}
	if lm.Bound {
		lid := lm.LineID
		row.LineID = &lid
	}
	id, err := e.store.CreateAlert(ctx, row)
	if err != nil {
		e.log.Printf("persist alert %s: %v", fp, err)
	}
	e.live[fp] = &liveAlert{rowID: id, meta: meta, firing: time.Now()}
	e.log.Printf("ALERT %s [%s] %s (probe=%s line=%s)", meta.Severity, meta.Kind, meta.Title, probeID, lm.LineName)
	e.sendMail(meta, true)
}

func (e *Engine) resolveLocked(fp, reason string) {
	la, ok := e.live[fp]
	if !ok {
		return
	}
	// 记录中断收口时刻：降质判定据此跳过"窗口内仍含中断失败样本"的恢复初期
	if strings.HasPrefix(fp, KindLineDown+":") || strings.HasPrefix(fp, KindFloorDown+":") {
		e.outageEnd[fp] = time.Now()
	}
	delete(e.live, fp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if la.rowID > 0 {
		if err := e.store.ResolveAlert(ctx, la.rowID); err != nil {
			e.log.Printf("resolve alert %s: %v", fp, err)
		}
	}
	la.meta.ExtraHTML = reason
	e.log.Printf("RESOLVED [%s] %s", la.meta.Kind, la.meta.Title)
	e.sendMail(la.meta, false)
}

// sendMail 发送告警/恢复通知：SMTP 邮件 + 钉钉/企业微信 webhook（后台可配）。
func (e *Engine) sendMail(meta AlertMeta, firing bool) {
	// 日志外发：告警触发/恢复转发审计服务器（sendMail 是全部 6 条 fire/resolve 路径的
	// 收敛点；静默告警在 fireLocked 更早处被拦，天然不外发——跟随静默语义）
	if e.shipper != nil {
		e.shipper.Ship(alertLogEvent(meta, firing))
	}
	var smtpCfg SMTPSettings
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	subject := "[NetWatch] "
	if firing {
		subject += "【" + meta.Severity + "告警】"
	} else {
		subject += "【已恢复】"
	}
	subject += meta.Title // meta.Title 已含线路名（decorateTitle）

	if err := e.store.GetSetting(ctx, "smtp", &smtpCfg); err == nil && smtpCfg.Valid() {
		als, aerr := e.alertSettings(ctx)
		// 收件组：默认组 → 按级别覆盖
		to := smtpCfg.To
		if aerr == nil && len(als.NotifyTo) > 0 {
			to = als.NotifyTo
		}
		if aerr == nil {
			switch meta.Severity {
			case "P0", "P1":
				if len(als.NotifyToP0P1) > 0 {
					to = als.NotifyToP0P1
				}
			case "P2":
				if len(als.NotifyToP2) > 0 {
					to = als.NotifyToP2
				}
			}
		}
		smtpCfg.To = to
		go func() {
			bctx, bcancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer bcancel()
			_ = bctx
			if err := SendMail(smtpCfg, subject, alertEmailHTML(&e.cfg, meta, firing, meta.ExtraHTML)); err != nil {
				e.log.Printf("send mail: %v", err)
			}
		}()
	}
	e.sendWebhooks(meta, firing, subject)
}

// sendWebhooks IM 通道推送：P0/P1 恒推，P2 按开关。
func (e *Engine) sendWebhooks(meta AlertMeta, firing bool, subject string) {
	if firing && meta.Severity == "P2" {
		var ch NotifyChannels
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.store.GetSetting(ctx, "notify_channels", &ch); err != nil || !ch.SendP2 {
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ch NotifyChannels
	if err := e.store.GetSetting(ctx, "notify_channels", &ch); err != nil || !ch.Enabled {
		return
	}
	md := alertMarkdown(meta, firing)
	go func() {
		if ch.DingtalkWebhook != "" {
			if err := SendDingtalk(ch.DingtalkWebhook, ch.DingtalkSecret, subject, md); err != nil {
				e.log.Printf("dingtalk: %v", err)
			}
		}
		if ch.WecomWebhook != "" {
			if err := SendWecom(ch.WecomWebhook, md); err != nil {
				e.log.Printf("wecom: %v", err)
			}
		}
	}()
}

func decorateTitle(meta AlertMeta, lm LineMeta) string {
	name := lm.LineName
	if name == "" {
		name = "未分配线路(" + meta.ProbeID + ")"
	}
	return name + "：" + meta.Title
}

// minuteStats 把 pingWin 样本按分钟聚合成方格心跳图数据（升序，最多 60 分钟）。
// 各目标独立计时（探针侧每目标独立 ticker），样本级判定即可满足
// "全部目标失败才算失败"的分钟语义，无需重构探测轮次。
// winMin：降质统计窗口（分钟，取自告警设置 LossWindowMin）。每个分钟点额外计算
// M = 以该分钟为终点、向前 winMin 分钟滚动窗口的各目标丢包率下中位数
// （lossMedian 同 helper）——即"告警引擎在该时刻评估会得到的丢包中位数"，
// 前端黄格与 P2 降质告警逐点一致。
func (agg *probeAgg) minuteStats(winMin int) []MinStat {
	if winMin < 1 {
		winMin = 5
	}
	if winMin > 60 { // pingWin 仅保留 65 分钟，滚动窗口以此为限
		winMin = 60
	}
	type acc struct{ n, f int }
	byMin := map[int64]*acc{}
	byMinTarget := map[int64]map[string][2]int{} // 分钟 → 目标 → [总数, 失败数]
	for _, r := range agg.pingWin {
		m := r.ts.Unix() / 60 * 60
		a := byMin[m]
		if a == nil {
			a = &acc{}
			byMin[m] = a
		}
		a.n++
		if !r.ok {
			a.f++
		}
		pt := byMinTarget[m]
		if pt == nil {
			pt = map[string][2]int{}
			byMinTarget[m] = pt
		}
		p := pt[r.target]
		p[0]++
		if !r.ok {
			p[1]++
		}
		pt[r.target] = p
	}
	if len(byMin) == 0 {
		return nil
	}
	out := make([]MinStat, 0, len(byMin))
	for m, a := range byMin {
		out = append(out, MinStat{T: m, N: a.n, F: a.f})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	// 滚动窗口下中位数：窗口 = [m-(winMin-1)分钟, m+1分钟)，与告警 evaluate 的
	// lossStats(now, LossWindowMin) 同 helper 同口径
	lo := int64(winMin-1) * 60
	for i := range out {
		m := out[i].T
		pt := map[string][2]int{}
		for mm, counts := range byMinTarget {
			if mm >= m-lo && mm < m+60 {
				for t, c := range counts {
					p := pt[t]
					p[0] += c[0]
					p[1] += c[1]
					pt[t] = p
				}
			}
		}
		if v, ok := lossMedian(pt); ok {
			out[i].M = v
		}
	}
	if len(out) > 60 {
		out = out[len(out)-60:]
	}
	return out
}

// Snapshot 总览 API 用的实时状态快照
func (e *Engine) Snapshot() map[string]ProbeStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	als := e.cachedALS() // 与 evaluate 同一套阈值（降级判定/丢包率/分钟统计滚动窗口）
	out := map[string]ProbeStatus{}
	for id, agg := range e.probes {
		st := ProbeStatus{
			LastSeen: agg.lastSeen.Format(time.RFC3339),
			Version:  agg.version,
			EgressIP: agg.egressIP, EgressISP: agg.ipISP, EgressGeo: agg.ipGeo, EgressIPv6: agg.ipV6,
			CPUPct: agg.cpuPct, MemPct: agg.memPct, DiskPct: agg.diskPct, ClockOffsetMs: agg.clockOffsetMs,
		}
		// 方格心跳图：近 60 分钟逐分钟 ping 汇总（按时间正序，稀疏——无样本的分钟不产出）
		st.Minutes = agg.minuteStats(als.LossWindowMin)
		switch {
		case agg.everSeen && now.Sub(agg.lastSeen) > 60*time.Second:
			st.Status = "blind"
		case agg.gwEver && now.Sub(agg.gwLast) < 90*time.Second && agg.gwFailStreak >= 3:
			st.Status = "floor_down"
		case agg.gwEver && agg.gwOK && now.Sub(agg.gwLast) < 90*time.Second:
			ok45, medianLoss, enough := agg.lossStats(now, time.Duration(als.LossWindowMin)*time.Minute)
			switch {
			case enough && !ok45:
				st.Status = "down"
			case enough && medianLoss > als.LossPctThresh:
				st.Status = "degraded"
			default:
				st.Status = "online"
			}
			if agg.lastPing != nil {
				st.RTTms = agg.lastPing.RTTms
				st.LossPct = medianLoss
			}
		default:
			st.Status = "unknown"
		}
		// 网速慢（告警在途）：线路在线但测速低于阈值——叠加为独立可见状态
		// （此前 speed_low 仅是告警事件，页面状态从不显示；down/blind/degraded 优先保留）
		if st.Status == "online" && e.live[KindSpeedLow+":"+id] != nil {
			st.Status = "speed_low"
		}
		if agg.lastTCP != nil {
			st.TCPOk = agg.lastTCP.OK
		}
		if agg.lastHTTPIn != nil {
			st.HTTPOk = agg.lastHTTPIn.OK
		}
		if agg.lastSpeed != nil && agg.lastSpeed.OK {
			st.SpeedDown = agg.lastSpeed.Extra["down_mbps"]
			st.SpeedAt = time.UnixMilli(agg.lastSpeed.TS).Format(time.RFC3339)
		}
		out[id] = st
	}
	return out
}

func itoa(n int) string     { return fmtInt(int64(n)) }
func ftoa(f float64) string { return trimFloat(f) }

func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s
}

// FireMonitor 内置监控触发告警（monitor runner 调用）
func (e *Engine) FireMonitor(fp string, it MonitorItem, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.live[fp]; ok {
		return
	}
	meta := AlertMeta{
		Kind: KindMonitorDown, Severity: kindSeverity[KindMonitorDown],
		Title: "内网服务不可达：" + it.Name, ProbeID: "center",
		StartsAt: time.Now(), ExtraHTML: detail,
	}
	if e.silenced(0) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	row := &AlertRow{Fingerprint: fp, Severity: meta.Severity, Kind: meta.Kind,
		Title: "内网监控：" + meta.Title, ProbeID: "center", StartsAt: meta.StartsAt}
	id, err := e.store.CreateAlert(ctx, row)
	if err != nil {
		e.log.Printf("persist alert %s: %v", fp, err)
	}
	e.live[fp] = &liveAlert{rowID: id, meta: meta, firing: time.Now()}
	e.log.Printf("ALERT P2 [monitor_down] %s (%s %s)", it.Name, it.Type, it.Target)
	e.sendMail(meta, true)
}

// ResolveMonitor 内置监控告警解除
func (e *Engine) ResolveMonitor(fp string, it MonitorItem, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	la, ok := e.live[fp]
	if !ok {
		return
	}
	delete(e.live, fp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if la.rowID > 0 {
		if err := e.store.ResolveAlert(ctx, la.rowID); err != nil {
			e.log.Printf("resolve alert %s: %v", fp, err)
		}
	}
	la.meta.ExtraHTML = reason
	e.log.Printf("RESOLVED [monitor_down] %s", it.Name)
	e.sendMail(la.meta, false)
}

// CleanMonitors 内置监控配置变更后：清理已删除目标的进行中告警与状态
func (e *Engine) CleanMonitors(liveNames map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for fp, la := range e.live {
		if la.meta.Kind == KindMonitorDown {
			name := fp[len(KindMonitorDown)+1:]
			if !liveNames[name] {
				delete(e.live, fp)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if la.rowID > 0 {
					if err := e.store.ResolveAlert(ctx, la.rowID); err != nil {
						e.log.Printf("resolve alert %s: %v", fp, err)
					}
				}
				cancel()
				e.log.Printf("RESOLVED [monitor_down] %s（目标已删除）", name)
			}
		}
	}
}

// FireIndustry 行业网监测触发告警（industry runner 调用）
func (e *Engine) FireIndustry(fp string, it IndustryItem, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.live[fp]; ok {
		return
	}
	meta := AlertMeta{
		Kind: KindIndustryDown, Severity: kindSeverity[KindIndustryDown],
		Title: "行业网不可达：" + it.Name, ProbeID: "center",
		StartsAt: time.Now(), ExtraHTML: detail,
	}
	if e.silenced(0) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	row := &AlertRow{Fingerprint: fp, Severity: meta.Severity, Kind: meta.Kind,
		Title: "行业网监测：" + meta.Title, ProbeID: "center", StartsAt: meta.StartsAt}
	id, err := e.store.CreateAlert(ctx, row)
	if err != nil {
		e.log.Printf("persist alert %s: %v", fp, err)
	}
	e.live[fp] = &liveAlert{rowID: id, meta: meta, firing: time.Now()}
	e.log.Printf("ALERT P2 [industry_down] %s (%s)", it.Name, it.Target)
	e.sendMail(meta, true)
}

// ResolveIndustry 行业网监测告警解除
func (e *Engine) ResolveIndustry(fp string, it IndustryItem, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	la, ok := e.live[fp]
	if !ok {
		return
	}
	delete(e.live, fp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if la.rowID > 0 {
		if err := e.store.ResolveAlert(ctx, la.rowID); err != nil {
			e.log.Printf("resolve alert %s: %v", fp, err)
		}
	}
	la.meta.ExtraHTML = reason
	e.log.Printf("RESOLVED [industry_down] %s", it.Name)
	e.sendMail(la.meta, false)
}

// CleanIndustries 行业网监测配置变更后：清理已删除目标的进行中告警与状态
func (e *Engine) CleanIndustries(liveNames map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for fp, la := range e.live {
		if la.meta.Kind == KindIndustryDown {
			name := fp[len(KindIndustryDown)+1:]
			if !liveNames[name] {
				delete(e.live, fp)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if la.rowID > 0 {
					if err := e.store.ResolveAlert(ctx, la.rowID); err != nil {
						e.log.Printf("resolve alert %s: %v", fp, err)
					}
				}
				cancel()
				e.log.Printf("RESOLVED [industry_down] %s（目标已删除）", name)
			}
		}
	}
}
