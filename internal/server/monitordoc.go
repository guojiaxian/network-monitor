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
	speedRow := DocRow{
		Name:    "定时测速（" + dStr(sp.Engine, "http") + " 引擎，全部探针并行）",
		Desc:    "16 连接 CDN 采样 15s ×" + itoa(sp.Runs) + " 次取平均；低于阈值 1 分钟后自动复测一轮（固定），仍低报「网速慢」；「立即刷新」按钮即触发一轮",
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
		Name:    "楼层 IP 扫描（逐 IP ping 单包，并发 64〔内置固定〕）",
		Desc:    "手动与定时共用同一把闸（同一时刻只跑一批）；禁 ping 的主机会显示离线",
		Period:  mins(scan.IntervalMin),
		Timeout: fmt.Sprintf("%dms/IP", scan.TimeoutMs),
		Status:  onOff(scan.Enabled),
		Source:  "探测配置页 · IP 扫描计划",
	}

	// ---- 内→内监控 ----
	var im MonitorSettings
	_ = getSetting("internal_monitors", &im)
	monRow := DocRow{
		Name:    "内→内服务监控（中心自探，Uptime Kuma 模式）",
		Desc:    "复用探针同一套探测原语（带 1 次重试）；连续 3 次失败触发 P2，恢复即解除",
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
		Name:    "行业网监测（仅 ICMP，不做其他探测以免被封）",
		Desc:    "复用探针 ping 原语（带 1 次重试）；连续 3 次失败触发 P2，恢复即解除",
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
		Name:    "SNMP 真实流量采集（防火墙 WAN 口）",
		Desc:    "只读轮询字节计数器差分出每线真实上下行速率，写入时序库供线路详情查看",
		Period:  secs(snInterval),
		Timeout: secs(snTimeout),
		Status:  onOff(sn.Enabled),
		Source:  "采集与测速页",
	}

	// ---- 日志外发 ----
	var sl SyslogSettings
	_ = getSetting("syslog_settings", &sl)
	sl.normalize()
	slProto := "UDP"
	if sl.Proto == "tcp" {
		slProto = "TCP"
	}
	syslogRow := DocRow{
		Name:    "日志外发（syslog → 日志审计服务器）",
		Desc:    "告警 / 测速 / IP 扫描 / 出口 IP 变化四类事件，CEF 或 KV 格式，" + slProto + " 可选；被静默的告警不外发",
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
		Name:   "日报 / 周报 / 月报（自动生成并邮件推送）",
		Desc:   "按报表推送页配置的时刻命中触发，统计上一个自然周期",
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
	lossWin := dInt(als.LossWindowMin, 5)
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

	summary := DocGroup{
		Title: "当前生效配置速览",
		Note:  "数值实时取自系统配置；详细机制见下方分组",
		Rows: []DocRow{
			{
				Name:    "探测周期与目标",
				Desc:    "ICMP " + icmpTxt + "；TCP " + tcpTxt + "；DNS " + dnsTxt + " → " + dStr(cfg.DNSDomain, def.DNSDomain) + "；HTTP " + httpTxt,
				Period:  fmt.Sprintf("ICMP %s / TCP %s / DNS %s / HTTP %s", secs(pingIv), secs(tcpIv), secs(dnsIv), secs(httpIv)),
				Timeout: secs(tmo), Status: "on", Source: "探测配置页",
			},
			{
				Name:   "探测重试 / 心跳 / 出口识别",
				Desc:   fmt.Sprintf("失败后重试 %d 次（间隔 %dms），任一次成功即成功；出口识别超时 8s", retryN, retryMs),
				Period: fmt.Sprintf("心跳 %s / 出口 %s", secs(hbSec), mins(egMin)),
				Status: "on", Source: "探测配置页 · 上报与重试",
			},
			{
				Name:   "告警判定",
				Desc:   fmt.Sprintf("盲区 >%s；线路中断 45s 无成功；楼层网关连续 3 次失败；降质 %dmin 丢包中位数 >%g%%；探测异常 %s；评估 %s", secs(blind), lossWin, lossPct, tfTxt, secs(evalIv)),
				Period: secs(evalIv), Status: "on", Source: "告警策略页 / 探测配置页",
			},
			{
				Name:   "心跳展示",
				Desc:   "1 格 = 1 分钟；红=中断、紫=降质、橙=有失败已恢复、灰=无数据、绿=正常；状态/总览/大屏最近 60 分钟，详情页最近 3 小时",
				Period: "60min / 3h", Status: "fixed", Source: "内置固定（内存环 3h）",
			},
		},
	}
	groups := []DocGroup{
		summary,
		{
			Title: "一、自动探测 / 监测项 · 楼层探针",
			Note:  "目标与间隔在「探测配置」页可改（约 30s 下发生效）；间隔带 ±10% 随机抖动防同步风暴〔内置固定〕；每轮探测失败自动重试，任一次成功即成功；线路判活 = 任一协议 45s 内有成功即可达〔内置固定〕",
			Rows: []DocRow{
				{Name: "网关探测（区分楼层 / 线路故障域）", Period: secs(pingIv) + " ±10%", Timeout: secs(tmo), Status: "on", Source: "探测配置页 · 探测周期与超时"},
				{Name: fmt.Sprintf("公网 ICMP（当前 %d 目标投票）", icmpN), Period: secs(pingIv) + " ±10%", Timeout: secs(tmo), Status: "on", Source: "探测配置页 · 探测目标"},
				{Name: fmt.Sprintf("TCP 连接（当前 %d 目标）", tcpN), Period: secs(tcpIv) + " ±10%", Timeout: secs(tmo), Status: "on", Source: "探测配置页 · 探测目标"},
				{Name: fmt.Sprintf("DNS 解析（%d 个服务器）+ HTTP 访问（%d 个目标）", dnsN, httpN), Period: "DNS " + secs(dnsIv) + " / HTTP " + secs(httpIv) + "，均 ±10%", Timeout: secs(tmo), Status: "on", Source: "探测配置页 · 探测目标"},
				{Name: "探测重试（每轮失败后）", Desc: "任一次成功即成功，全部失败才判断连；IP 扫描不重试以保扫描速度", Period: "重试 " + itoa(retryN) + " 次", Timeout: "间隔 " + itoa(retryMs) + "ms", Status: "on", Source: "探测配置页 · 探测周期与超时"},
			},
		},
		{
			Title: "二、自动探测 / 监测项 · 探针 → 中心上报",
			Note:  "样本批量上报由独立通道驱动（每 3s 冲刷〔内置固定〕）；探针断网样本本地缓存 ≤48MB，恢复自动补传",
			Rows: []DocRow{
				{Name: "心跳 + 探针资源（CPU / 内存 / 磁盘）", Desc: "资源配置告警 = >90% 连续 3 次心跳（≈" + secs(hbSec*3) + "）", Period: secs(hbSec), Status: "on", Source: "探测配置页 · 上报与重试"},
				{Name: "出口 IP / 运营商 / 归属地识别", Desc: "出口变化自动对账：命中其他线路=改绑；无匹配=自动回写绑定线路，线路详情页留痕", Period: mins(egMin), Timeout: "8s〔固定〕", Status: "on", Source: "探测配置页 · 上报与重试"},
				{Name: "配置轮询（探测配置下发生效）", Period: secs(30), Status: "fixed", Source: "内置固定"},
				speedRow,
			},
		},
		{
			Title: "三、自动探测 / 监测项 · 中心服务器",
			Note:  "告警评估对全部探测数据判定（触发与恢复条件见下表）；各项配置入口见行尾标注",
			Rows: []DocRow{
				{Name: "告警评估（对全部探测数据判定，见下表）", Period: secs(evalIv), Status: "on", Source: "探测配置页 · 中心引擎"},
				scanRow,
				monRow,
				indRow,
				snmpRow,
				syslogRow,
				reportRow,
			},
		},
	}

	alerts := []DocAlert{
		{Level: "P0", Name: "监控盲区", Trigger: "探针心跳缺失 > " + secs(blind) + "〔告警策略页可配〕", Recovery: "心跳恢复"},
		{Level: "P1", Name: "楼层中断", Trigger: "网关连续 3 次 ping 失败", Recovery: "网关恢复可达"},
		{Level: "P1", Name: "线路中断", Trigger: "网关可达且探针心跳/网关样本在 45s 内，任一协议 45s 内无成功；全部有效探针一致（分歧报 P2 冲突）", Recovery: "任一公网目标恢复"},
		{Level: "P2", Name: "探针数据冲突", Trigger: "多探针线路中部分探针报中断、其余正常；线路大概率正常，优先检查异常探针", Recovery: "探针意见一致或恢复"},
		{Level: "P2", Name: "线路降质", Trigger: fmt.Sprintf("%dmin 窗口各目标丢包率下中位数 >%g%%〔告警策略页可配〕（每目标 ≥10 样本同时成立；线路/楼层中断恢复后一个窗口内不评估，避免同一故障双重告警）", lossWin, lossPct), Recovery: "丢包回落"},
		{Level: "P2", Name: "网速慢", Trigger: fmt.Sprintf("一轮均值 <%gMbps〔探测配置页可配〕→ 1 分钟后复测一轮仍低（等待固定）", sp.SlowMinMbps), Recovery: "任一轮实测达标"},
		{Level: "P2", Name: "服务 / 行业网不可达", Trigger: "连续 3 次探测失败", Recovery: "恢复即解除"},
		{Level: "P2", Name: "探针资源紧张", Trigger: "内存或磁盘 >90% 连续 3 次心跳（≈" + secs(hbSec*3) + "）", Recovery: "回落 <80%"},
		{Level: "P2", Name: "探测异常（TCP/DNS/HTTP）", Trigger: "某类探测全部目标连续失败 ≥ " + tfTxt + "〔探测配置页可配〕（单目标失败不告警）", Recovery: "任一目标恢复"},
	}

	return map[string]any{
		"generated_at": time.Now().Format(time.RFC3339),
		"groups":       groups,
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

// reportScheduleText 报表计划的实时文案；返回 (文案, 是否有任一报表启用)
func reportScheduleText(rs, def ReportSettings) (string, bool) {
	weekday := map[int]string{0: "周日", 1: "周一", 2: "周二", 3: "周三", 4: "周四", 5: "周五", 6: "周六"}
	var parts []string
	if rs.DailyEnabled {
		parts = append(parts, "日报 每日 "+dStr(rs.DailyAt, def.DailyAt))
	}
	if rs.WeeklyEnabled {
		day := rs.WeeklyDay
		if day < 0 || day > 6 {
			day = def.WeeklyDay
		}
		parts = append(parts, "周报 每周"+weekday[day]+" "+dStr(rs.WeeklyAt, def.WeeklyAt))
	}
	if rs.MonthlyEnabled {
		mday := rs.MonthlyDay
		if mday < 1 || mday > 28 {
			mday = def.MonthlyDay
		}
		parts = append(parts, "月报 每月 "+itoa(mday)+" 日 "+dStr(rs.MonthlyAt, def.MonthlyAt))
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
