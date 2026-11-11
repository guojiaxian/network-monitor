package server

// 监测说明数据接口：GET /api/admin/monitor-doc（登录即可读，观察者放行）。
//
// ★ 维护约定（自 2026-11-08 起从 web/src/components/MonitorDocModal.vue 移交至此）：
//   本文件是「监测说明」弹窗的数据**唯一来源**——周期/超时/启用状态全部实时取自 settings，
//   前端弹窗只负责渲染。今后任何探测/监测机制的周期、阈值、开关变更，必须同步更新本文件的行定义，
//   不再维护前端硬编码文案。
//
// 行结构：name/desc 展示名与补充说明；period/timeout 为实时配置值（零值回退默认）；
// status: on=启用 off=未启用 fixed=内置固定不可配；source 标注配置入口页面；
// items 用于"一项一配置"的监测（内→内/行业网逐项列出间隔与超时）。

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"netwatch/internal/agent"
	"netwatch/internal/proto"
)

type DocRow struct {
	Name    string    `json:"name"`
	Desc    string    `json:"desc,omitempty"`
	Period  string    `json:"period"`
	Timeout string    `json:"timeout,omitempty"`
	Status  string    `json:"status"` // on / off / fixed
	Source  string    `json:"source,omitempty"`
	Items   []DocItem `json:"items,omitempty"`
}

type DocItem struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	Period  string `json:"period"`
	Timeout string `json:"timeout"`
}

type DocGroup struct {
	Title string   `json:"title"`
	Note  string   `json:"note,omitempty"`
	Rows  []DocRow `json:"rows"`
}

type DocAlert struct {
	Level    string `json:"level"`
	Name     string `json:"name"`
	Trigger  string `json:"trigger"`
	Recovery string `json:"recovery"`
}

// DocColor 心跳格颜色图例（前端直接渲染色块）
type DocColor struct {
	Color string `json:"color"` // 十六进制色值，与 web/src/style.css .hbg-* 一致
	Name  string `json:"name"`
	Rule  string `json:"rule"`
}

// DocHeartbeat 心跳格说明：粒度、窗口、公共注记与颜色表
type DocHeartbeat struct {
	Cell    string     `json:"cell"`
	Windows string     `json:"windows"`
	Note    string     `json:"note,omitempty"`
	Colors  []DocColor `json:"colors"`
}

// ---- 取值小工具 ----

// dInt 零值回退默认（新字段旧数据缺省=0 的统一兜底）
func dInt(v, dft int) int {
	if v <= 0 {
		return dft
	}
	return v
}

// dStr 空串回退默认
func dStr(v, dft string) string {
	if v == "" {
		return dft
	}
	return v
}

// clampInt 越界回退默认（与消费端钳位同口径，防其他写入路径绕过 putSettings）
func clampInt(v, lo, hi, dft int) int {
	if v < lo || v > hi {
		return dft
	}
	return v
}

func secs(n int) string { return fmt.Sprintf("%ds", n) }
func mins(n int) string { return fmt.Sprintf("%dmin", n) }

// monClampInterval 监控项间隔钳位（与 monitor.go/industry.go settings 读取同口径）
func monClampInterval(v int) int {
	if v < 10 {
		return 30
	}
	return v
}

// monClampTimeout 监控项超时钳位（默认 5s，上限 60s）
func monClampTimeout(v int) int {
	if v <= 0 || v > 60 {
		return 5
	}
	return v
}

func (a *AdminAPI) monitorDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeJSON(w, monitorDocData(func(key string, v any) error {
		return a.store.GetSetting(ctx, key, v)
	}))
}

// monitorDocData 组装监测说明数据；getSetting 抽象 settings 读取（单测注入用）。
func monitorDocData(getSetting func(string, any) error) map[string]any {
	def := DefaultProbeConfig()
	var cfg proto.ProbeConfig
	_ = getSetting("agent_config", &cfg)

	pingIv := dInt(cfg.IntervalPingS, def.IntervalPingS)
	tcpIv := dInt(cfg.IntervalTCPS, def.IntervalTCPS)
	dnsIv := dInt(cfg.IntervalDNSS, def.IntervalDNSS)
	httpIv := dInt(cfg.IntervalHTTPS, def.IntervalHTTPS)
	tmo := dInt(cfg.TimeoutS, def.TimeoutS)
	retryN := dInt(cfg.RetryN, def.RetryN)
	retryMs := dInt(cfg.RetryPauseMs, def.RetryPauseMs)
	hbSec := clampInt(cfg.HeartbeatSec, 5, 60, def.HeartbeatSec)
	egMin := clampInt(cfg.EgressMin, 1, 60, def.EgressMin)
	icmpN, tcpN, dnsN, httpN := len(cfg.ICMPTargets), len(cfg.TCPTargets), len(cfg.DNSServers), len(cfg.HTTPTargets)
	if icmpN == 0 {
		icmpN = len(def.ICMPTargets)
	}
	if tcpN == 0 {
		tcpN = len(def.TCPTargets)
	}
	if dnsN == 0 {
		dnsN = len(def.DNSServers)
	}
	if httpN == 0 {
		httpN = len(def.HTTPTargets)
	}
	targetTxt := func(cur, dft []string) string {
		s := strings.Join(cur, " / ")
		if s == "" {
			s = strings.Join(dft, " / ")
		}
		return s
	}
	icmpTxt := targetTxt(cfg.ICMPTargets, def.ICMPTargets)
	tcpTxt := targetTxt(cfg.TCPTargets, def.TCPTargets)
	dnsTxt := targetTxt(cfg.DNSServers, def.DNSServers)
	httpTxt := targetTxt(cfg.HTTPTargets, def.HTTPTargets)

	// ---- 测速 ----
	var sp SpeedSettings
	if err := getSetting("speed_settings", &sp); err != nil {
		sp = DefaultSpeedSettings()
	}
	sp.normalize()
	// 并发口径随 max_concurrent 配置走：1=逐条串行（2026-11-10 实测共享上行带宽下串行更准，见 AGENTS.md），N=同时最多 N 条
	concTxt := "逐条串行"
	if sp.MaxConcurrent > 1 {
		concTxt = fmt.Sprintf("同时最多 %d 条并行", sp.MaxConcurrent)
	}
	speedRow := DocRow{
		Name:    "定时测速（" + dStr(sp.Engine, "http") + " 引擎，" + concTxt + "）",
		Desc:    fmt.Sprintf("%d 连接 CDN 采样 %s ×%d 次取平均（代码固定）", agent.SpeedHTTPConns, secs(int(agent.SpeedHTTPWindow.Seconds())), sp.Runs),
		Period:  mins(sp.IntervalMin),
		Timeout: "单轮 " + mins(sp.TimeoutMin),
		Status:  onOff(sp.Enabled),
		Source:  "探测配置页 · 定时测速",
	}

	// ---- IP 扫描 ----
	var scan ScanSettings
	if err := getSetting("scan_settings", &scan); err != nil {
		scan = DefaultScanSettings()
	}
	scan.normalize()
	scanRow := DocRow{
		Name:    "楼层 IP 扫描",
		Desc:    fmt.Sprintf("逐 IP ping 单包；并发 %d，单网段上限 /22（%d 个地址）；手动与定时共用一把闸；禁 ping 主机显示离线", scanConcurrency, scanMaxHosts),
		Period:  mins(scan.IntervalMin),
		Timeout: fmt.Sprintf("%dms/IP", scan.TimeoutMs),
		Status:  onOff(scan.Enabled),
		Source:  "探测配置页 · IP 扫描计划",
	}

	// ---- 内→内监控 ----
	var im MonitorSettings
	_ = getSetting("internal_monitors", &im)
	monRow := DocRow{
		Name:    "内→内服务监控（中心自探）",
		Desc:    "复用探针探测原语（带 1 次重试）；连续 3 次失败报 P2，恢复即解除",
		Period:  "各项独立",
		Timeout: "各项独立",
		Status:  onOff(im.Enabled),
		Source:  "探测配置页 · 中心监测",
	}
	for _, it := range im.Items {
		monRow.Items = append(monRow.Items, DocItem{
			Name: it.Name + "（" + it.Type + "）", Target: it.Target,
			Period: secs(monClampInterval(it.IntervalS)), Timeout: secs(monClampTimeout(it.TimeoutS)),
		})
	}

	// ---- 行业网监测 ----
	var ind IndustrySettings
	_ = getSetting("industry_monitors", &ind)
	indRow := DocRow{
		Name:    "行业网监测（仅 ICMP）",
		Desc:    "连续 3 次失败报 P2，恢复即解除",
		Period:  "各项独立",
		Timeout: "各项独立",
		Status:  onOff(ind.Enabled),
		Source:  "探测配置页 · 中心监测",
	}
	for _, it := range ind.Items {
		indRow.Items = append(indRow.Items, DocItem{
			Name: it.Name, Target: it.Target,
			Period: secs(monClampInterval(it.IntervalS)), Timeout: secs(monClampTimeout(it.TimeoutS)),
		})
	}

	// ---- 楼层网关 / 汇聚设备监测 ----
	var fn FloorNetSettings
	_ = getSetting("floor_net_monitors", &fn)
	fn.normalize()
	floorNetRow := DocRow{
		Name:    "楼层网关与汇聚设备（中心自探）",
		Desc:    "网关读楼层配置的 gateway；汇聚/AC/104 汇聚读锚点管理 IP；相同 IP 只探测一次，连续 " + itoa(fn.FailN) + " 次失败报 P1",
		Period:  secs(fn.IntervalS) + " ±10%",
		Timeout: secs(fn.TimeoutS),
		Status:  onOff(fn.Enabled),
		Source:  "楼层配置页 · 探测配置页",
	}
	for _, a := range fn.Anchors {
		floorNetRow.Items = append(floorNetRow.Items, DocItem{
			Name: a.Name + "（" + a.Floors + "）", Target: a.Target,
			Period: secs(fn.IntervalS) + " ±10%", Timeout: secs(fn.TimeoutS),
		})
	}

	// ---- SNMP 采集 ----
	var sn SnmpSettings
	_ = getSetting("snmp_settings", &sn)
	snInterval := sn.IntervalS
	if snInterval < 10 {
		snInterval = 30
	}
	snTimeout := sn.TimeoutS
	if snTimeout <= 0 || snTimeout > 30 {
		snTimeout = 5
	}
	snmpRow := DocRow{
		Name:    "SNMP 流量采集（防火墙 WAN 口）",
		Desc:    "只读轮询字节计数器差分出每线真实上下行速率，写入时序库",
		Period:  secs(snInterval),
		Timeout: secs(snTimeout),
		Status:  onOff(sn.Enabled),
		Source:  "采集与测速页",
	}

	// ---- 日志外发 ----
	var sl SyslogSettings
	_ = getSetting("syslog_settings", &sl)
	sl.normalize()
	syslogRow := DocRow{
		Name:    "日志外发（syslog）",
		Desc:    "告警 / 测速 / IP 扫描 / 出口变化四类事件；CEF 或 KV、UDP 或 TCP；静默告警不外发",
		Period:  "事件驱动",
		Timeout: secs(sl.TimeoutS),
		Status:  onOff(sl.Enabled),
		Source:  "通知渠道页",
	}

	// ---- 报表 ----
	var rs ReportSettings
	_ = getSetting("report_settings", &rs)
	dr := DefaultReportSettings()
	repTxt, repOn := reportScheduleText(rs, dr)
	reportRow := DocRow{
		Name:   "日报 / 周报 / 月报",
		Desc:   "按配置时刻生成上一个自然周期并邮件推送",
		Period: repTxt,
		Status: onOff(repOn),
		Source: "报表推送页",
	}

	// ---- 告警设置（实时值，供第一~三组与告警表引用）----
	var als AlertSettings
	if err := getSetting("alert_settings", &als); err != nil {
		als = DefaultAlertSettings()
	}
	blind := dInt(als.BlindSecs, 60)
	lossWin := dInt(als.LossWindowMin, 3)
	lossPct := als.LossPctThresh
	if lossPct <= 0 {
		lossPct = 10
	}
	evalIv := dInt(als.EvalIntervalS, 5)
	tfWin := dInt(als.TypeFailWindowS, 60)
	tfTxt := fmt.Sprintf("%d 秒", tfWin)
	if tfWin%60 == 0 {
		tfTxt = fmt.Sprintf("%d 分钟", tfWin/60)
	}

	groups := []DocGroup{
		{
			Title: "一、楼层探针",
			Note:  "目标/周期/超时/重试在「探测配置」页修改；间隔带 ±10% 抖动（代码固定）",
			Rows: []DocRow{
				{Name: "网关探测（区分楼层 / 线路故障域）", Desc: "区分楼层故障 / 线路故障", Period: secs(pingIv) + " ±10%", Timeout: secs(tmo), Status: "on", Source: "探测配置页"},
				{Name: fmt.Sprintf("公网 ICMP（当前 %d 目标投票）", icmpN), Desc: icmpTxt, Period: secs(pingIv) + " ±10%", Timeout: secs(tmo), Status: "on", Source: "探测配置页"},
				{Name: fmt.Sprintf("TCP 连接（当前 %d 目标）", tcpN), Desc: tcpTxt, Period: secs(tcpIv) + " ±10%", Timeout: secs(tmo), Status: "on", Source: "探测配置页"},
				{Name: fmt.Sprintf("DNS 解析（%d 个服务器）+ HTTP 访问（%d 个目标）", dnsN, httpN), Desc: dnsTxt + " → " + dStr(cfg.DNSDomain, def.DNSDomain) + "；" + httpTxt, Period: "DNS " + secs(dnsIv) + " / HTTP " + secs(httpIv), Timeout: secs(tmo), Status: "on", Source: "探测配置页"},
				{Name: "探测重试（每轮失败后）", Desc: "任一次成功即成功；IP 扫描不重试", Period: "重试 " + itoa(retryN) + " 次", Timeout: "间隔 " + itoa(retryMs) + "ms", Status: "on", Source: "探测配置页"},
			},
		},
		{
			Title: "二、探针 → 中心上报",
			Rows: []DocRow{
				{Name: "心跳 + 探针资源（CPU / 内存 / 磁盘）", Desc: "CPU/内存/磁盘 >90% 连续 3 次心跳报 P2", Period: secs(hbSec), Status: "on", Source: "探测配置页 · 上报与重试"},
				{Name: "出口 IP / 运营商 / 归属地识别", Desc: "出口变化自动对账/改绑/回写并留痕", Period: mins(egMin), Timeout: "8s（代码固定）", Status: "on", Source: "探测配置页 · 上报与重试"},
				{Name: "配置轮询", Desc: "探测配置修改后的生效延迟", Period: secs(int(agent.ConfigPollInterval.Seconds())), Status: "fixed", Source: "内置固定"},
				speedRow,
			},
		},
		{
			Title: "三、中心服务器",
			Note:  "告警评估对全部探测数据判定；触发与恢复条件见下表",
			Rows: []DocRow{
				{Name: "告警评估", Period: secs(evalIv), Status: "on", Source: "探测配置页 · 中心引擎"},
				scanRow,
				monRow,
				indRow,
				floorNetRow,
				snmpRow,
				syslogRow,
				reportRow,
			},
		},
	}

	alerts := []DocAlert{
		{Level: "P0", Name: "监控盲区", Trigger: "探针心跳缺失 > " + secs(blind) + "〔告警策略页可配〕", Recovery: "心跳恢复"},
		{Level: "P1", Name: "楼层中断", Trigger: "网关连续 3 次 ping 失败", Recovery: "网关恢复可达"},
		{Level: "P1", Name: "楼层网关不可达", Trigger: "核心/共同网关连续 " + itoa(fn.FailN) + " 次 ICMP 失败（单包，中心自探）", Recovery: "网关恢复可达"},
		{Level: "P1", Name: "楼层汇聚设备不可达", Trigger: "汇聚交换机/AC/104 汇聚管理 IP 连续 " + itoa(fn.FailN) + " 次 ICMP 失败", Recovery: "设备恢复可达"},
		{Level: "P1", Name: "线路中断", Trigger: "网关可达且探针心跳/网关样本在 " + secs(int(aliveWindow.Seconds())) + " 内，任一协议 " + secs(int(aliveWindow.Seconds())) + " 内无成功；全部有效探针一致（分歧报 P2 冲突）", Recovery: "任一公网目标恢复"},
		{Level: "P2", Name: "探针数据冲突", Trigger: "多探针线路中部分探针报中断、其余正常；线路大概率正常，优先检查异常探针", Recovery: "探针意见一致或恢复"},
		{Level: "P2", Name: "线路降质", Trigger: fmt.Sprintf("连续 %d 分钟每分钟两个目标丢包率下中位数 >%g%%〔告警策略页可配〕（每目标 ≥%d 轮）", lossWin, lossPct, lossMinuteMinRounds), Recovery: fmt.Sprintf("连续 %d 分钟回落到阈值 %d%%", lossWin, int(lossResolveRatio*100))},
		{Level: "P2", Name: "短时严重丢包", Trigger: fmt.Sprintf("单分钟两个目标丢包率下中位数 ≥%d%%（每目标 ≥%d 轮）", severeLossPct, lossMinuteMinRounds), Recovery: fmt.Sprintf("回落到 <%g%%", severeLossPct*severeResolveRatio)},
		{Level: "P2", Name: "网速慢", Trigger: fmt.Sprintf("一轮均值 <%gMbps〔探测配置页可配〕→ %d 分钟后复测一轮仍低（等待固定）", sp.SlowMinMbps, int(retestWaitMin.Minutes())), Recovery: "任一轮实测达标"},
		{Level: "P2", Name: "服务 / 行业网不可达", Trigger: "连续 3 次探测失败", Recovery: "恢复即解除"},
		{Level: "P2", Name: "探针资源紧张", Trigger: "内存或磁盘 >90% 连续 3 次心跳（≈" + secs(hbSec*3) + "）", Recovery: "回落 <80%"},
		{Level: "P2", Name: "探测异常（TCP/DNS/HTTP）", Trigger: "某类探测全部目标连续失败 ≥ " + tfTxt + "〔探测配置页可配〕（单目标失败不告警）", Recovery: "任一目标恢复"},
	}

	heartbeat := DocHeartbeat{
		Cell:    "1 格 = 1 分钟",
		Windows: fmt.Sprintf("状态页/总览/大屏最近 60 分钟；线路/楼层详情最近 %d 小时", int(ringHistory.Hours())),
		Note:    fmt.Sprintf("线路/楼层多探针取最差口径；格子只反映单分钟实际降质，常规 P2 需连续 %d 分钟、短时严重 P2 单分钟 ≥%d%% 才报（见下表）", lossWin, severeLossPct),
		Colors: []DocColor{
			{Color: "#05c96b", Name: "绿", Rule: "该分钟无丢包"},
			{Color: "#f0883e", Name: "橙", Rule: "有丢包，但该分钟各目标丢包率下中位数 ≤ 阈值"},
			{Color: "#a78bfa", Name: "紫", Rule: fmt.Sprintf("该分钟各目标丢包率下中位数 > %g%%（每目标 ≥%d 轮；单目标全断不标紫）", lossPct, lossMinuteMinRounds)},
			{Color: "#dc3545", Name: "红", Rule: fmt.Sprintf("该分钟所有轮次 %d 包全丢", agent.PingBurstCount)},
			{Color: "#adb5bd", Name: "灰", Rule: "该分钟无样本（停探/盲区）"},
		},
	}

	notes := []string{
		"告警触发/恢复自动通知：邮件格式与 Uptime Kuma 一致（[对象] [🔴 Down/✅ Up] 消息），默认仅报 P0/P1，P2 需在告警设置页开启「P2 告警也发邮件」；钉钉/企微 P0P1 恒推，P2 需在通知渠道页开启；支持按线路静默。",
		"中心重启时未恢复告警自动收口；删除线路/探针/监控目标时对应告警自动收口。",
		fmt.Sprintf("探测样本每 %s 或满 %d 条批量上报；断网本地缓存 ≤%dMB，恢复自动补传。", secs(int(agent.ReporterFlushInterval.Seconds())), agent.ReporterFlushSize, agent.ReporterCacheMaxBytes>>20),
		fmt.Sprintf("探针样本时间快于服务器 >%d 分钟时按服务器时间钳位（请配置 NTP）。", int(clockSkewClamp.Minutes())),
		fmt.Sprintf("IP 扫描记录保留 %d 天且最多 %d 条；告警、审计、报表不自动清理。", scanRunKeepDays, scanRunMaxRows),
	}

	return map[string]any{
		"generated_at": time.Now().Format(time.RFC3339),
		"groups":       groups,
		"heartbeat":    heartbeat,
		"notes":        notes,
		"alerts":       alerts,
	}
}

// onOff bool → 状态徽章值
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// reportScheduleText 报表计划的实时文案；返回 (文案, 是否有任一报表启用)。
// 生成时刻与发送时刻独立：发送时刻留空表示生成后立即发送。
func reportScheduleText(rs, def ReportSettings) (string, bool) {
	weekday := map[int]string{0: "周日", 1: "周一", 2: "周二", 3: "周三", 4: "周四", 5: "周五", 6: "周六"}
	timing := func(gen, send string, dflt string) string {
		s := dStr(gen, dflt) + " 生成"
		if strings.TrimSpace(send) == "" {
			return s + "后立即发送"
		}
		return s + "、" + strings.TrimSpace(send) + " 发送"
	}
	var parts []string
	if rs.DailyEnabled {
		parts = append(parts, "日报 每日 "+timing(rs.DailyAt, rs.DailySendAt, def.DailyAt))
	}
	if rs.WeeklyEnabled {
		day := rs.WeeklyDay
		if day < 0 || day > 6 {
			day = def.WeeklyDay
		}
		parts = append(parts, "周报 每周"+weekday[day]+" "+timing(rs.WeeklyAt, rs.WeeklySendAt, def.WeeklyAt))
	}
	if rs.MonthlyEnabled {
		mday := rs.MonthlyDay
		if mday < 1 || mday > 28 {
			mday = def.MonthlyDay
		}
		parts = append(parts, "月报 每月 "+itoa(mday)+" 日 "+timing(rs.MonthlyAt, rs.MonthlySendAt, def.MonthlyAt))
	}
	if len(parts) == 0 {
		return "未启用任何报表", false
	}
	txt := parts[0]
	for _, p := range parts[1:] {
		txt += " · " + p
	}
	return txt, true
}
