package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"netwatch/internal/proto"
)

// 告警类别
const (
	KindLineDown   = "line_down"      // P1 线路中断（网关正常、公网全部失败；多探针线路需全部报中断）
	KindFloorDown  = "floor_down"     // P1 楼层中断（楼层网关不可达）
	KindBlind      = "blind"          // P0 监控盲区（探针失联）
	KindDegraded   = "degraded"       // P2 线路降质（丢包/时延超阈值）
	KindSpeedLow   = "speed_low"      // P2 带宽不达标（测速 < 契约 70%，连续 2 次）
	KindConflict   = "probe_conflict" // P2 探针数据冲突（多探针线路中部分报中断）
	KindSiteDown   = "site_down"      // P1 网站从外网不可达（外网组全部探针 HTTP 失败）
	KindSiteConf   = "site_conflict"  // P2 外网探针数据冲突（部分探针报网站不可达）
	KindCertExpire = "cert_expiring"  // P2 HTTPS 证书即将过期
	KindMonitorDown = "monitor_down"  // P2 内网服务不可达（中心内置监控）
)

var kindSeverity = map[string]string{
	KindBlind:      "P0",
	KindLineDown:   "P1",
	KindFloorDown:  "P1",
	KindDegraded:   "P2",
	KindSpeedLow:   "P2",
	KindConflict:   "P2",
	KindSiteDown:   "P1",
	KindSiteConf:   "P2",
	KindCertExpire: "P2",
	KindMonitorDown: "P2",
}

var kindTitle = map[string]string{
	KindBlind:      "监控盲区：探针失联",
	KindLineDown:   "宽带线路中断",
	KindFloorDown:  "楼层网络中断（网关不可达）",
	KindDegraded:   "线路质量降质（丢包/时延偏高）",
	KindSpeedLow:   "带宽不达标（测速低于契约 70%）",
	KindConflict:   "探针数据冲突（部分探针报线路中断）",
	KindSiteDown:   "网站从外网不可达",
	KindSiteConf:   "外网探针数据冲突（部分探针报网站不可达）",
	KindCertExpire: "HTTPS 证书即将过期",
	KindMonitorDown: "内网服务不可达",
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
	gwEver      bool
	gwOK        bool
	gwLast      time.Time
	gwFailStreak int
	// 公网 ping 滑动窗口
	pingWin []pingRec
	// 最近各类探测结果（总览展示用）
	lastTCP    *proto.Sample
	lastDNS    *proto.Sample
	lastHTTP   map[string]*proto.Sample // 外网组按 target 记录最近 HTTP 结果
	lastHTTPIn *proto.Sample            // 内网组（单目标聚合简化展示）
	lastPing   *proto.Sample
	lastSpeed  *proto.Sample
	// 带宽不达标连续次数
	speedLowStreak int
	// 分组（internal/external，evaluate 时从库刷新）
	group string
}

// httpStats 外网组按 target 统计最近 HTTP 结果（90 秒内视为新鲜）
func (agg *probeAgg) httpStats(now time.Time) (anyOK, allFail bool, fresh int) {
	for _, s := range agg.lastHTTP {
		if s == nil || now.Sub(time.UnixMilli(s.TS)) > 90*time.Second {
			continue
		}
		fresh++
		if s.OK {
			anyOK = true
		} else {
			allFail = !anyOK // 全部失败才保持 true
		}
	}
	if fresh == 0 {
		return false, false, 0
	}
	return anyOK, !anyOK && fresh > 0, fresh
}

// ProbeStatus 对外（总览 API）暴露的单探针实时状态
type ProbeStatus struct {
	Status     string  `json:"status"` // online / down / floor_down / degraded / blind / speed_low
	RTTms      float64 `json:"rtt_ms"`
	LossPct    float64 `json:"loss_pct"`
	TCPOk      bool    `json:"tcp_ok"`
	HTTPOk     bool    `json:"http_ok"`
	SpeedDown  float64 `json:"speed_down_mbps,omitempty"` // 最近一次测速下行（Mbps）
	SpeedAt    string  `json:"speed_at,omitempty"`
	LastSeen   string  `json:"last_seen"`
	Version    string  `json:"version"`
	Recent     []bool  `json:"recent,omitempty"` // 最近 30 次公网 ping 成败（心跳条）
}

// Engine 告警引擎：消费探测样本与心跳，按规则判定并触发邮件。
type Engine struct {
	cfg  Config
	store *Store
	tsdb *TSDB
	sched *SpeedSched // 测速调度器（结果回收用，可后置注入）

	mu     sync.Mutex
	probes map[string]*probeAgg
	live   map[string]*liveAlert // fingerprint → 进行中的告警

	silMu    sync.Mutex
	silUntil time.Time
	silences []SilenceRow // 生效中的静默窗口（短缓存）

	log *log.Logger
}

// SetSched 注入测速调度器
func (e *Engine) SetSched(s *SpeedSched) { e.sched = s }

func NewEngine(cfg Config, store *Store, tsdb *TSDB) *Engine {
	e := &Engine{
		cfg:    cfg,
		store:  store,
		tsdb:   tsdb,
		probes: map[string]*probeAgg{},
		live:   map[string]*liveAlert{},
		log:    log.New(os.Stderr, "alert ", log.LstdFlags|log.Lmsgprefix),
	}
	return e
}

// Start 启动评估循环。重启时先收口遗留的 firing 告警。
func (e *Engine) Start(ctx context.Context) {
	if err := e.store.ResolveAllFiring(ctx); err != nil {
		e.log.Printf("resolve stale firing alerts: %v", err)
	}
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.evaluate()
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
	if m, err := e.store.ProbeLineMeta(ctx); err == nil {
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
		case proto.TypeDNS:
			cp := s
			agg.lastDNS = &cp
		case proto.TypeHTTP:
			cp := s
			if agg.lastHTTP == nil {
				agg.lastHTTP = map[string]*proto.Sample{}
			}
			agg.lastHTTP[s.Target] = &cp
			agg.lastHTTPIn = &cp
			// P2 证书即将过期（<15 天触发一次；恢复逻辑在 evaluate 中 >20 天解除）
			if days, hasCert := s.Extra["cert_days"]; hasCert && days > 0 && days < 15 && lm.Group == "external" {
				e.fireLocked(KindCertExpire+":"+s.Target, AlertMeta{
					Kind: KindCertExpire, Severity: kindSeverity[KindCertExpire],
					Title: kindTitle[KindCertExpire] + "：" + s.Target, ProbeID: agentID,
					StartsAt: ts,
					ExtraHTML: "证书剩余 " + ftoa(days) + " 天，请尽快安排更换（更换后告警将在剩余天数 >20 天时自动解除）",
				}, agentID, lm)
			}
		case proto.TypeSpeed:
			cp := s
			agg.lastSpeed = &cp
			// 任务回收（无论成败）：排定下一轮
			if e.sched != nil {
				e.sched.OnResult(agentID)
			}
			// P2 带宽不达标：测速 < 契约 70% 连续 2 次（恢复阈值 85%，带迟滞）
			if s.OK && lm.Bound && lm.ContractDownMbps > 0 {
				fp := KindSpeedLow + ":" + agentID
				down := s.Extra["down_mbps"]
				lowAt := 0.7 * float64(lm.ContractDownMbps)
				highAt := 0.85 * float64(lm.ContractDownMbps)
				switch {
				case down < lowAt:
					agg.speedLowStreak++
				case down >= highAt:
					agg.speedLowStreak = 0
					e.resolveLocked(fp, "测速恢复至 "+ftoa(down)+"Mbps")
				}
				if agg.speedLowStreak >= 2 && e.live[fp] == nil {
					e.fireLocked(fp, AlertMeta{
						Kind: KindSpeedLow, Severity: kindSeverity[KindSpeedLow],
						Title: kindTitle[KindSpeedLow], ProbeID: agentID,
						LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
						StartsAt: ts,
						ExtraHTML: "本次测速下行 " + ftoa(down) + "Mbps，契约 " + itoa(lm.ContractDownMbps) + "Mbps（阈值 " + ftoa(lowAt) + "Mbps）",
					}, agentID, lm)
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
func (e *Engine) OnHeartbeat(agentID, version string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	agg := e.aggFor(agentID)
	agg.lastSeen = time.Now()
	agg.everSeen = true
	agg.version = version
	e.resolveLocked(KindBlind+":"+agentID, "心跳恢复")
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
func (agg *probeAgg) lossStats(now time.Time, win time.Duration) (anyOK45 bool, medianLoss float64, enough bool) {
	perTarget := map[string]*[2]int{} // target → [总数, 失败数]
	for _, r := range agg.pingWin {
		if now.Sub(r.ts) > win {
			continue
		}
		p, ok := perTarget[r.target]
		if !ok {
			p = &[2]int{}
			perTarget[r.target] = p
		}
		p[0]++
		if !r.ok {
			p[1]++
		}
		if r.ok && now.Sub(r.ts) < 45*time.Second {
			anyOK45 = true
		}
	}
	var ratios []float64
	for _, p := range perTarget {
		if p[0] >= 10 { // 每目标窗口内样本量足够才参与统计
			ratios = append(ratios, float64(p[1])*100/float64(p[0]))
		}
	}
	if len(ratios) == 0 {
		return anyOK45, 0, false
	}
	sort.Float64s(ratios)
	return anyOK45, ratios[(len(ratios)-1)/2], true
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
	meta, err := e.store.ProbeLineMeta(ctx)
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
	siteVote := lineVote{}           // 外网组网站可用性投票

	for probeID, agg := range e.probes {
		agg.group = meta[probeID].Group
		lm, known := meta[probeID]
		if !known {
			// 探针已在后台删除：清状态、收口其活动告警
			for _, kind := range []string{KindBlind, KindLineDown, KindFloorDown, KindDegraded, KindSpeedLow} {
				e.resolveLocked(kind+":"+probeID, "探针已删除")
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
				StartsAt: agg.lastSeen.Add(time.Duration(als.BlindSecs) * time.Second),
				ExtraHTML: "最后心跳：" + agg.lastSeen.Format("15:04:05"),
			}, probeID, lm)
			continue // 盲区时其他判定无意义
		} else if agg.everSeen {
			e.resolveLocked(KindBlind+":"+probeID, "")
		}

		// 窗口修剪
		cutoff := now.Add(-6 * time.Minute)
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
				StartsAt: agg.gwLast,
				ExtraHTML: "楼层网关连续 " + itoa(agg.gwFailStreak) + " 次探测失败",
			}, probeID, lm)
		} else {
			e.resolveLocked(KindFloorDown+":"+probeID, "网关恢复可达")
		}

		// P1 线路中断投票：探针在线 + 网关可达（有效证人）时对"公网是否全断"投票。
		// 多探针线路：全部证人报中断才升 P1；部分报中断 → P2 数据冲突；未绑定线路的探针维持按探针判定。
		// 外网组（external）：不参与线路判定，改为"网站可用性投票"（HTTP 是否可达官网）。
		probeAlive := agg.everSeen && now.Sub(agg.lastSeen) < 90*time.Second
		gwOKRecent := agg.gwEver && agg.gwOK && now.Sub(agg.gwLast) < 90*time.Second
		pubOK45, _, pingEnough := agg.lossStats(now, 6*time.Minute)
		if lm.Group == "external" {
			e.resolveLocked(KindLineDown+":"+probeID, "外网组不适用线路判定")
			e.resolveLocked(KindFloorDown+":"+probeID, "外网组不适用楼层判定")
			if probeAlive {
				anyOK, allFail, fresh := agg.httpStats(now)
				if fresh > 0 {
					sv := siteVote
					if allFail && !anyOK {
						sv.down++
						sv.downIDs = append(sv.downIDs, probeID)
					} else {
						sv.up++
						sv.upIDs = append(sv.upIDs, probeID)
					}
					siteVote = sv
				}
			}
			// 证书过期恢复：>20 天自动解除
			for tgt, s := range agg.lastHTTP {
				if s != nil && s.Extra["cert_days"] > 20 {
					e.resolveLocked(KindCertExpire+":"+tgt, "证书剩余 "+ftoa(s.Extra["cert_days"])+" 天")
				}
			}
		} else if lm.Bound {
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
					StartsAt: now.Add(-time.Duration(als.LineDownSecs) * time.Second),
					ExtraHTML: "楼层网关可达但全部公网探测目标失败（目标数 " + itoa(len(probeCfg.ICMPTargets)) + "）",
				}, probeID, lm)
			} else if !lineDownActive || pubOK45 {
				e.resolveLocked(KindLineDown+":"+probeID, "公网连通恢复")
			}
		}

		// P2 线路降质：窗口内各目标丢包率下中位数超阈值（线路中断时不叠加）
		winMin := time.Duration(als.LossWindowMin) * time.Minute
		_, medianLoss, enough := agg.lossStats(now, winMin)
		if enough {
			fp := KindDegraded + ":" + probeID
			active := e.live[fp] != nil
			if !active && medianLoss > als.LossPctThresh {
				e.fireLocked(fp, AlertMeta{
					Kind: KindDegraded, Severity: kindSeverity[KindDegraded],
					Title: kindTitle[KindDegraded], ProbeID: probeID,
					LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
					StartsAt: now,
					ExtraHTML: itoa(als.LossWindowMin) + " 分钟窗口目标丢包率中位数 " + ftoa(medianLoss) + "%（阈值 " + ftoa(als.LossPctThresh) + "%）",
				}, probeID, lm)
			} else if active && medianLoss <= als.LossPctThresh*0.7 {
				e.resolveLocked(fp, "丢包率回落至 "+ftoa(medianLoss)+"%")
			}
		}
	}

	// 外网组网站可用性投票：全部探针 HTTP 失败 → P1 网站从外网不可达；部分 → P2 冲突
	{
		anyMeta := LineMeta{Group: "external"}
		for _, lm := range meta {
			if lm.Group == "external" {
				anyMeta = lm
				break
			}
		}
		fireDown, fireConflict := decideLine(siteVote.down, siteVote.up)
		fpD := KindSiteDown + ":external"
		fpC := KindSiteConf + ":external"
		if fireDown {
			e.fireLocked(fpD, AlertMeta{
				Kind: KindSiteDown, Severity: kindSeverity[KindSiteDown],
				Title: kindTitle[KindSiteDown], ProbeID: strings.Join(siteVote.downIDs, ","),
				StartsAt: now.Add(-90 * time.Second),
				ExtraHTML: "全部 " + itoa(siteVote.down) + " 台外网探针（阿里云等）均无法访问目标网站（HTTP 探测失败）——对互联网用户网站大概率不可达，属重大事件",
			}, strings.Join(siteVote.downIDs, ","), anyMeta)
			e.resolveLocked(fpC, "外网探针意见一致")
		} else if fireConflict {
			e.fireLocked(fpC, AlertMeta{
				Kind: KindSiteConf, Severity: kindSeverity[KindSiteConf],
				Title: kindTitle[KindSiteConf], ProbeID: strings.Join(siteVote.downIDs, ","),
				StartsAt: now,
				ExtraHTML: itoa(siteVote.down) + "/" + itoa(siteVote.down+siteVote.up) + " 台外网探针报网站不可达，其余正常：请优先检查异常探针服务器自身网络",
			}, strings.Join(siteVote.downIDs, ","), anyMeta)
			e.resolveLocked(fpD, "存在正常探针，不确认网站故障")
		} else if siteVote.up > 0 {
			e.resolveLocked(fpD, "网站从外网恢复可达")
			e.resolveLocked(fpC, "外网探针意见一致")
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
				StartsAt: now.Add(-time.Duration(als.LineDownSecs) * time.Second),
				ExtraHTML: "全部 " + itoa(lv.down) + " 台有效探针均报公网中断（交叉验证一致）",
			}, strings.Join(lv.downIDs, ","), lm)
			e.resolveLocked(fpC, "探针意见一致")
		} else if fireConflict {
			e.fireLocked(fpC, AlertMeta{
				Kind: KindConflict, Severity: kindSeverity[KindConflict],
				Title: kindTitle[KindConflict], ProbeID: strings.Join(lv.downIDs, ","),
				LineName: lm.LineName, Floor: lm.Floor, PublicIP: lm.PublicIP, CIDR: lm.CIDR,
				StartsAt: now,
				ExtraHTML: itoa(lv.down) + "/" + itoa(lv.down+lv.up) + " 台探针报中断，其余正常：线路大概率正常，请优先检查异常探针（位置见探针 ID）",
			}, strings.Join(lv.downIDs, ","), lm)
			e.resolveLocked(fp, "存在正常探针，交叉验证不确认中断")
		} else if lv.up > 0 {
			e.resolveLocked(fp, "公网连通恢复")
			e.resolveLocked(fpC, "探针意见一致")
		}
	}
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
	if lm.Group == "external" {
		name = "外网探测"
	}
	if name == "" {
		name = "未分配线路(" + meta.ProbeID + ")"
	}
	return name + "：" + meta.Title
}

// Snapshot 总览 API 用的实时状态快照
func (e *Engine) Snapshot() map[string]ProbeStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	out := map[string]ProbeStatus{}
	for id, agg := range e.probes {
		st := ProbeStatus{
			LastSeen: agg.lastSeen.Format(time.RFC3339),
			Version:  agg.version,
		}
		// 心跳条：最近 30 次公网 ping 成败（按时间正序）
		n := len(agg.pingWin)
		start := 0
		if n > 30 {
			start = n - 30
		}
		for _, r := range agg.pingWin[start:] {
			st.Recent = append(st.Recent, r.ok)
		}
		switch {
		case agg.everSeen && now.Sub(agg.lastSeen) > 60*time.Second:
			st.Status = "blind"
		case agg.group == "external":
			// 外网组：以 HTTP 是否可达官网为主要状态
			anyOK, allFail, fresh := agg.httpStats(now)
			switch {
			case fresh > 0 && allFail && !anyOK:
				st.Status = "down"
			case anyOK:
				if _, medianLoss, enough := agg.lossStats(now, 5*time.Minute); enough && medianLoss > 10 {
					st.Status = "degraded"
				} else {
					st.Status = "online"
				}
			default:
				st.Status = "unknown"
			}
			if agg.lastPing != nil {
				st.RTTms = agg.lastPing.RTTms
			}
		case agg.gwEver && now.Sub(agg.gwLast) < 90*time.Second && agg.gwFailStreak >= 3:
			st.Status = "floor_down"
		case agg.gwEver && agg.gwOK && now.Sub(agg.gwLast) < 90*time.Second:
			ok45, medianLoss, enough := agg.lossStats(now, 5*time.Minute)
			switch {
			case enough && !ok45:
				st.Status = "down"
			case enough && medianLoss > 10:
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
