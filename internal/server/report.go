package server

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NotifyChannels IM 告警通道（钉钉/企业微信群机器人 webhook）
type NotifyChannels struct {
	Enabled         bool   `json:"enabled"`
	DingtalkWebhook string `json:"dingtalk_webhook"` // 钉钉机器人 webhook 地址
	DingtalkSecret  string `json:"dingtalk_secret"`  // 加签密钥（留空不加签）
	WecomWebhook    string `json:"wecom_webhook"`    // 企业微信群机器人 webhook 地址
	SendP2          bool   `json:"send_p2"`          // P2 降质/带宽告警是否也推送（P0/P1 恒推送）
}

func DefaultNotifyChannels() NotifyChannels { return NotifyChannels{} }

// ReportSettings 报表推送设置（后台可改）
type ReportSettings struct {
	DailyEnabled   bool   `json:"daily_enabled"`
	DailyAt        string `json:"daily_at"` // "08:00"
	WeeklyEnabled  bool   `json:"weekly_enabled"`
	WeeklyDay      int    `json:"weekly_day"` // 1=周一 … 7=周日
	WeeklyAt       string `json:"weekly_at"`
	MonthlyEnabled bool   `json:"monthly_enabled"`
	MonthlyDay     int    `json:"monthly_day"` // 每月几号（1~28），出上一自然月的报告
	MonthlyAt      string `json:"monthly_at"`
	OrgName        string `json:"org_name"` // 单位名称（报告模板 {{单位名称}} 变量取值）
}

func DefaultReportSettings() ReportSettings {
	return ReportSettings{DailyEnabled: true, DailyAt: "08:00", WeeklyEnabled: true, WeeklyDay: 1, WeeklyAt: "08:30",
		MonthlyEnabled: true, MonthlyDay: 1, MonthlyAt: "08:00"}
}

// repSummary 报告汇总（总体概况表与占位符取值来源）
type repSummary struct {
	LineTotal  int      `json:"line_total"`
	FloorTotal int      `json:"floor_total"`
	AvgAvail   *float64 `json:"avg_avail"` // 全部有数据线路等权平均
	AvgRTT     *float64 `json:"avg_rtt"`
	AvgLoss    *float64 `json:"avg_loss"`
	AvgSpeed   *float64 `json:"avg_speed"`
	Samples    int64    `json:"samples"` // 公网连通性样本总数
	AlertTotal int      `json:"alert_total"`
	AlertP0    int      `json:"alert_p0"`
	AlertP1    int      `json:"alert_p1"`
	AlertP2    int      `json:"alert_p2"`
	WorstLine  string   `json:"worst_line"`  // 可用率最低的线路名
	WorstBrief string   `json:"worst_brief"` // 异常线路简述句
	Overall    string   `json:"overall"`     // 总体评价句
	Conclusion string   `json:"conclusion"`  // 结论与建议段落
}

// 报表行（JSON 存库 + 邮件渲染 + CSV 导出）
type reportLine struct {
	Name       string   `json:"name"`
	Floor      string   `json:"floor"`
	AvailPct   *float64 `json:"avail_pct"` // 公网探测可用率 %
	AvgRTTms   *float64 `json:"avg_rtt_ms"`
	AvgLossPct *float64 `json:"avg_loss_pct"`
	SpeedMax   *float64 `json:"speed_max_mbps"`
	SpeedAvg   *float64 `json:"speed_avg_mbps"`
	AlertCount int      `json:"alert_count"`
	HasData    bool     `json:"has_data"`
}

// reportFloor 楼层网段（在线 IP 取最近一次成功扫描）
type reportFloor struct {
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	Lines     string `json:"lines"`      // 关联线路名（顿号连接）
	OnlineIPs *int   `json:"online_ips"` // nil=尚未扫描
	ScanTotal int    `json:"scan_total"`
	ScannedAt string `json:"scanned_at"`
}

// reportAlert 告警明细行
type reportAlert struct {
	Time     string `json:"time"`
	Level    string `json:"level"` // P0/P1/P2
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Duration string `json:"duration"`
	Status   string `json:"status"` // 已恢复/处理中
}

// reportSpeed 线路最近一次测速
type reportSpeed struct {
	Line     string   `json:"line"`
	Probe    string   `json:"probe"`
	Time     string   `json:"time"`
	Down     *float64 `json:"down_mbps"`
	Up       *float64 `json:"up_mbps"`
	Contract int      `json:"contract_mbps"` // 契约下行带宽（0=未配置）
}

// reportItem 通用监测项统计（公网目标/内置监控/行业网）
type reportItem struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Target   string   `json:"target"`
	AvailPct *float64 `json:"avail_pct"`
	AvgRTT   *float64 `json:"avg_rtt_ms"`
}

type reportData struct {
	Kind      string       `json:"kind"`
	Start     string       `json:"start"`
	End       string       `json:"end"`
	Lines     []reportLine `json:"lines"`
	Generated string       `json:"generated"`
	// 以下字段 2026-11-07 增强；旧报表数据缺省为 nil/空，导出兼容（占位符显示 —）
	Summary    *repSummary   `json:"summary,omitempty"`
	Floors     []reportFloor `json:"floors,omitempty"`
	Alerts     []reportAlert `json:"alerts,omitempty"`
	Speeds     []reportSpeed `json:"speeds,omitempty"`
	Targets    []reportItem  `json:"targets,omitempty"`    // 公网 TCP/HTTP 探测目标
	Monitors   []reportItem  `json:"monitors,omitempty"`   // 内→内内置监控
	Industries []reportItem  `json:"industries,omitempty"` // 行业网监测
}

const repAlertLimit = 200 // 报告告警明细上限（超出部分表格尾注说明）

// Reporter 报表生成与推送调度
type Reporter struct {
	store *Store
	tsdb  *TSDB
	cfg   Config
	mail  func(subject, html string) error // 由 engine 提供的 SMTP 发送封装
	log   *log.Logger
}

func NewReporter(store *Store, tsdb *TSDB, cfg Config) *Reporter {
	return &Reporter{store: store, tsdb: tsdb, cfg: cfg,
		log: log.New(os.Stderr, "report ", log.LstdFlags|log.Lmsgprefix)}
}

// SetMailFunc 注入邮件发送封装（main 装配时提供）
func (r *Reporter) SetMailFunc(f func(subject, html string) error) { r.mail = f }

func (r *Reporter) Start(ctx context.Context) {
	go func() {
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
	}()
}

// reportPeriod 报表统计周期（自然周期口径）：日报=昨日、周报=上一个自然周（周一~周日）、月报=上一个自然月
func reportPeriod(kind string, now time.Time) (time.Time, time.Time) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch kind {
	case "week":
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		thisMonday := today.AddDate(0, 0, -(wd - 1))
		return thisMonday.AddDate(0, 0, -7), thisMonday
	case "month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return first.AddDate(0, -1, 0), first
	default: // day
		return today.AddDate(0, 0, -1), today
	}
}

func (r *Reporter) tick(ctx context.Context) {
	var rs ReportSettings
	if err := r.store.GetSetting(ctx, "report_settings", &rs); err != nil {
		return
	}
	now := time.Now()
	hhmm := func(t time.Time) string { return t.Format("15:04") }

	// 日报：每 DailyAt 触发，统计昨日自然日
	if rs.DailyEnabled && hhmm(now) == rs.DailyAt {
		start, end := reportPeriod("day", now)
		r.generateOnce(ctx, "day", start, end)
	}
	// 周报：WeeklyDay 指定星期，统计上一个自然周
	if rs.WeeklyEnabled && int(now.Weekday()) == rs.WeeklyDay && hhmm(now) == rs.WeeklyAt {
		start, end := reportPeriod("week", now)
		r.generateOnce(ctx, "week", start, end)
	}
	// 月报：每月 MonthlyDay 日 MonthlyAt 触发，统计上一个自然月
	if rs.MonthlyEnabled && now.Day() == rs.MonthlyDay && hhmm(now) == rs.MonthlyAt {
		start, end := reportPeriod("month", now)
		r.generateOnce(ctx, "month", start, end)
	}
}

// Generate 手动生成报表（管理后台"立即生成"）
func (r *Reporter) Generate(ctx context.Context, kind string) (int64, error) {
	if kind != "day" && kind != "week" && kind != "month" {
		return 0, fmt.Errorf("报表类型须为 day/week/month")
	}
	start, end := reportPeriod(kind, time.Now())
	return r.generate(ctx, kind, start, end)
}

func (r *Reporter) generateOnce(ctx context.Context, kind string, start, end time.Time) {
	exists, err := r.store.ReportExists(ctx, kind, end)
	if err == nil && exists {
		return
	}
	if _, err := r.generate(ctx, kind, start, end); err != nil {
		r.log.Printf("generate %s report: %v", kind, err)
	}
}

// seriesAgg 一条 VM 序列在统计窗口内的二次聚合
type seriesAgg struct {
	Avg, Max, Min, Sum float64
	Count              int
}

// aggSeries 把 query_range 返回的若干序列按 label 标签值归并聚合（窗口统计）
func aggSeries(ss []ExportSeries, label string) map[string]seriesAgg {
	out := map[string]seriesAgg{}
	for _, s := range ss {
		k := s.Metric[label]
		if k == "" {
			continue
		}
		var a seriesAgg
		a.Min = math.MaxFloat64
		for _, v := range s.Values {
			a.Sum += v
			a.Count++
			if v > a.Max {
				a.Max = v
			}
			if v < a.Min {
				a.Min = v
			}
		}
		if a.Count == 0 {
			continue
		}
		a.Avg = a.Sum / float64(a.Count)
		out[k] = a
	}
	return out
}

// ptrF 值拷贝转指针（聚合结果写入 reportItem/reportLine）
func ptrF(v float64) *float64 { return &v }

func (r *Reporter) generate(ctx context.Context, kind string, start, end time.Time) (int64, error) {
	// ---- VM 聚合：连通性/时延/丢包按 5 分钟桶精确统计（样本 10s 一轮，分层均值对等间隔样本无偏） ----
	qr := func(q string) []ExportSeries {
		ss, err := r.tsdb.QueryRange(ctx, q, start.Unix(), end.Unix(), 300)
		if err != nil {
			r.log.Printf("report query: %v", err)
		}
		return ss
	}
	avail := aggSeries(qr(`avg by (line) (avg_over_time(nw_ping_ok{class="public"}[5m]))`), "line")
	rtt := aggSeries(qr(`avg by (line) (avg_over_time(nw_ping_rtt_ms{class="public"}[5m]))`), "line")
	loss := aggSeries(qr(`avg by (line) (avg_over_time(nw_ping_loss_pct{class="public"}[5m]))`), "line")
	samp := aggSeries(qr(`sum by (line) (count_over_time(nw_ping_ok{class="public"}[5m]))`), "line")
	// 测速样本稀疏（默认每小时一轮）：1h 子窗口聚合 + 30 分钟步长，
	// 序列 max=窗口峰值（精确），序列 avg≈窗口内各测速轮均值（每样本被 1h 窗覆盖约 2 步，权重均匀）
	smaxAgg := aggSeries(qr(`max by (line) (max_over_time(nw_speed_down_mbps[1h]))`), "line")
	savgAgg := aggSeries(qr(`avg by (line) (avg_over_time(nw_speed_down_mbps[1h]))`), "line")
	// 公网目标（TCP/HTTP）、内置监控、行业网
	tcpOK := aggSeries(qr(`avg by (target) (avg_over_time(nw_tcp_ok[5m]))`), "target")
	tcpRTT := aggSeries(qr(`avg by (target) (avg_over_time(nw_tcp_rtt_ms[5m]))`), "target")
	httpOK := aggSeries(qr(`avg by (target) (avg_over_time(nw_http_ok[5m]))`), "target")
	httpTTFB := aggSeries(qr(`avg by (target) (avg_over_time(nw_http_ttfb_ms[5m]))`), "target")
	monOK := aggSeries(qr(`avg by (monitor) (avg_over_time(nw_monitor_ok[5m]))`), "monitor")
	monRTT := aggSeries(qr(`avg by (monitor) (avg_over_time(nw_monitor_rtt_ms[5m]))`), "monitor")
	indOK := aggSeries(qr(`avg by (industry) (avg_over_time(nw_industry_ok[5m]))`), "industry")
	indRTT := aggSeries(qr(`avg by (industry) (avg_over_time(nw_industry_rtt_ms[5m]))`), "industry")

	lines, err := r.store.ListLines(ctx)
	if err != nil {
		return 0, err
	}

	// ---- 告警：分级计数（精确）+ 明细（上限 repAlertLimit）----
	sevCounts, _ := r.store.CountAlertsRange(ctx, start, end)
	alertRows, _ := r.store.ListAlertsRange(ctx, start, end, repAlertLimit)
	alertCounts := map[int]int{}
	var repAlerts []reportAlert
	for _, a := range alertRows {
		if a.LineID != nil {
			alertCounts[*a.LineID]++
		}
		dur := "进行中"
		if a.EndsAt != nil {
			dur = repDuration(a.EndsAt.Sub(a.StartsAt))
		}
		status := "已恢复"
		if a.Status == "firing" {
			status = "处理中"
		}
		repAlerts = append(repAlerts, reportAlert{
			Time: a.StartsAt.Format("01-02 15:04"), Level: a.Severity,
			Kind: a.Kind, Title: a.Title, Duration: dur, Status: status})
	}

	data := reportData{Kind: kind,
		Start: start.Format("2006-01-02 15:04"), End: end.Format("2006-01-02 15:04"),
		Generated: time.Now().Format("2006-01-02 15:04:05")}
	data.Alerts = repAlerts

	// ---- 线路明细 ----
	for _, l := range lines {
		rl := reportLine{Name: l.Name, Floor: l.FloorName, AlertCount: alertCounts[l.ID]}
		if a, ok := avail[l.Name]; ok && a.Count > 0 {
			rl.AvailPct = ptrF(a.Avg * 100)
		}
		if a, ok := rtt[l.Name]; ok && a.Count > 0 {
			rl.AvgRTTms = ptrF(a.Avg)
		}
		if a, ok := loss[l.Name]; ok && a.Count > 0 {
			rl.AvgLossPct = ptrF(a.Avg)
		}
		if a, ok := smaxAgg[l.Name]; ok && a.Count > 0 {
			rl.SpeedMax = ptrF(a.Max)
		}
		if a, ok := savgAgg[l.Name]; ok && a.Count > 0 {
			rl.SpeedAvg = ptrF(a.Avg)
		}
		rl.HasData = rl.AvailPct != nil || rl.AvgRTTms != nil
		data.Lines = append(data.Lines, rl)
	}

	// ---- 楼层与在线终端 ----
	floors, _ := r.store.ListFloors(ctx)
	scans, _ := r.store.LatestFloorScans(ctx)
	for _, fl := range floors {
		rf := reportFloor{Name: fl.Name, CIDR: fl.CIDR}
		var names []string
		for _, lb := range fl.Lines {
			names = append(names, lb.Name)
		}
		rf.Lines = strings.Join(names, "、")
		if sm, ok := scans[fl.ID]; ok {
			up := sm.Up
			rf.OnlineIPs = &up
			rf.ScanTotal = sm.Total
			rf.ScannedAt = sm.At.Format("01-02 15:04")
		}
		data.Floors = append(data.Floors, rf)
	}

	// ---- 测速明细：各线路最近一次（原始样本用 export 拉取，取每序列末点） ----
	probeNames := map[string]string{}
	if probes, err := r.store.ListProbes(ctx); err == nil {
		for _, p := range probes {
			probeNames[p.ID] = p.Name
		}
	}
	type spPoint struct {
		ts int64
		v  float64
	}
	collectSpeed := func(metric string) map[string]map[string]spPoint { // line -> probe -> last
		m := map[string]map[string]spPoint{}
		ss, err := r.tsdb.Export(ctx, metric, start.Unix(), end.Unix())
		if err != nil {
			r.log.Printf("report export %s: %v", metric, err)
			return m
		}
		for _, s := range ss {
			line, probe := s.Metric["line"], s.Metric["probe"]
			if line == "" || len(s.Values) == 0 {
				continue
			}
			if m[line] == nil {
				m[line] = map[string]spPoint{}
			}
			if cur, ok := m[line][probe]; !ok || s.Timestamps[len(s.Timestamps)-1] >= cur.ts {
				// VM export 返回毫秒时间戳，报表按秒使用
				m[line][probe] = spPoint{s.Timestamps[len(s.Timestamps)-1] / 1000, s.Values[len(s.Values)-1]}
			}
		}
		return m
	}
	lastDown := collectSpeed(`nw_speed_down_mbps`)
	lastUp := collectSpeed(`nw_speed_up_mbps`)
	for _, l := range lines {
		for probe, dp := range lastDown[l.Name] {
			rs := reportSpeed{Line: l.Name, Probe: probeNames[probe],
				Time: time.Unix(dp.ts, 0).Format("01-02 15:04"), Down: ptrF(dp.v), Contract: l.ContractDownMbps}
			if up, ok := lastUp[l.Name][probe]; ok {
				rs.Up = ptrF(up.v)
			}
			data.Speeds = append(data.Speeds, rs)
		}
	}

	// ---- 公网目标 / 内置监控 / 行业网 ----
	itemFrom := func(name, typ, target string, ok, rttM map[string]seriesAgg) reportItem {
		it := reportItem{Name: name, Type: typ, Target: target}
		if a, ok2 := ok[name]; ok2 && a.Count > 0 {
			it.AvailPct = ptrF(a.Avg * 100)
		}
		if a, ok2 := rttM[name]; ok2 && a.Count > 0 {
			it.AvgRTT = ptrF(a.Avg)
		}
		return it
	}
	for tgt := range tcpOK {
		data.Targets = append(data.Targets, itemFrom(tgt, "TCP", tgt, tcpOK, tcpRTT))
	}
	for tgt := range httpOK {
		data.Targets = append(data.Targets, itemFrom(tgt, "HTTP", tgt, httpOK, httpTTFB))
	}
	sort.Slice(data.Targets, func(i, j int) bool { return data.Targets[i].Target < data.Targets[j].Target })
	var ms MonitorSettings
	if err := r.store.GetSetting(ctx, "internal_monitors", &ms); err == nil && ms.Enabled {
		for _, it := range ms.Items {
			data.Monitors = append(data.Monitors, itemFrom(it.Name, strings.ToUpper(it.Type), it.Target, monOK, monRTT))
		}
	}
	var ims IndustrySettings
	if err := r.store.GetSetting(ctx, "industry_monitors", &ims); err == nil && ims.Enabled {
		for _, it := range ims.Items {
			data.Industries = append(data.Industries, itemFrom(it.Name, "ICMP", it.Target, indOK, indRTT))
		}
	}

	// ---- 汇总与自动文案 ----
	data.Summary = buildSummary(&data, sevCounts, samp)

	id, err := r.store.CreateReport(ctx, kind, start, end, data)
	if err != nil {
		return 0, err
	}
	r.log.Printf("report %s generated: %s ~ %s (%d lines, %d alerts)",
		kind, data.Start, data.End, len(data.Lines), data.Summary.AlertTotal)
	r.mailReport(ctx, kind, &data)
	return id, nil
}

// buildSummary 基于线路明细与告警计数计算汇总与自动评价/结论文案
func buildSummary(d *reportData, sevCounts map[string]int, samp map[string]seriesAgg) *repSummary {
	s := &repSummary{LineTotal: len(d.Lines), FloorTotal: len(d.Floors),
		AlertP0: sevCounts["P0"], AlertP1: sevCounts["P1"], AlertP2: sevCounts["P2"]}
	s.AlertTotal = s.AlertP0 + s.AlertP1 + s.AlertP2
	for _, a := range samp {
		s.Samples += int64(a.Sum)
	}

	var sumAvail, sumRTT, sumLoss, sumSpeed float64
	var nAvail, nRTT, nLoss, nSpeed int
	minAvail := 101.0
	for _, l := range d.Lines {
		if l.AvailPct != nil {
			sumAvail += *l.AvailPct
			nAvail++
			if *l.AvailPct < minAvail {
				minAvail = *l.AvailPct
				s.WorstLine = l.Name
			}
		}
		if l.AvgRTTms != nil {
			sumRTT += *l.AvgRTTms
			nRTT++
		}
		if l.AvgLossPct != nil {
			sumLoss += *l.AvgLossPct
			nLoss++
		}
		if l.SpeedAvg != nil {
			sumSpeed += *l.SpeedAvg
			nSpeed++
		}
	}
	avg := func(sum float64, n int) *float64 {
		if n == 0 {
			return nil
		}
		return ptrF(sum / float64(n))
	}
	s.AvgAvail = avg(sumAvail, nAvail)
	s.AvgRTT = avg(sumRTT, nRTT)
	s.AvgLoss = avg(sumLoss, nLoss)
	s.AvgSpeed = avg(sumSpeed, nSpeed)

	p01 := s.AlertP0 + s.AlertP1
	switch {
	case nAvail == 0:
		s.Overall = "本报告期无有效探测数据，无法给出有效评价"
		s.WorstBrief = "本报告期无有效探测数据。"
	case p01 > 0:
		s.Overall = fmt.Sprintf("期间发生 P0/P1 级告警 %d 次，最差线路可用率 %.2f%%，详见告警明细", p01, minAvail)
		s.WorstBrief = fmt.Sprintf("其中可用率最低的线路为「%s」（%.2f%%），详见告警明细。", s.WorstLine, minAvail)
	case minAvail < 99:
		s.Overall = fmt.Sprintf("整体运行基本平稳，未发生 P0/P1 级网络中断，但线路「%s」可用率偏低", s.WorstLine)
		s.WorstBrief = fmt.Sprintf("其中可用率最低的线路为「%s」（%.2f%%），建议关注。", s.WorstLine, minAvail)
	default:
		s.Overall = "整体运行平稳，全部线路探测正常，未发生 P0/P1 级网络中断"
		s.WorstBrief = "全部线路可用率正常。"
	}

	var cb strings.Builder
	if nAvail > 0 {
		cb.WriteString(fmt.Sprintf("本报告期网络整体平均可用率 %s%%，平均时延 %s 毫秒，平均测速 %sMbps，触发告警 %d 次（P0 级 %d 次、P1 级 %d 次、P2 级 %d 次）。",
			repNum(s.AvgAvail, 2), repNum(s.AvgRTT, 1), repNum(s.AvgSpeed, 0), s.AlertTotal, s.AlertP0, s.AlertP1, s.AlertP2))
	} else {
		cb.WriteString("本报告期监测数据不足，请检查探针运行状态。")
	}
	if p01 > 0 {
		cb.WriteString(fmt.Sprintf("P0/P1 级告警共 %d 次，恢复情况详见告警明细（第四章）。", p01))
	}
	switch {
	case minAvail < 99 && s.WorstLine != "":
		cb.WriteString(fmt.Sprintf("建议持续关注线路「%s」的连通质量，必要时与运营商核实链路状况。", s.WorstLine))
	case p01 == 0 && nAvail > 0:
		cb.WriteString("整体网络运行良好，建议继续保持日常监测与定期报告制度。")
	}
	s.Conclusion = cb.String()
	return s
}

// repDuration 告警持续时长文案
func repDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分 %d 秒", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时 %d 分", int(d.Hours()), int(d.Minutes())%60)
	default:
		h := int(d.Hours())
		return fmt.Sprintf("%d 天 %d 小时", h/24, h%24)
	}
}

func (r *Reporter) mailReport(ctx context.Context, kind string, d *reportData) {
	var smtpCfg SMTPSettings
	if err := r.store.GetSetting(ctx, "smtp", &smtpCfg); err != nil || !smtpCfg.Valid() {
		return
	}
	title := map[string]string{"day": "日报", "week": "周报", "month": "月报"}[kind]
	if title == "" {
		title = "日报"
	}
	subject := fmt.Sprintf("[NetWatch] 网络可用率%s（%s ~ %s）", title, d.Start, d.End)

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body style="font-family:'Microsoft YaHei',sans-serif;max-width:720px;margin:0 auto">
<h3>物探中心网络监控系统 · 网络可用率` + title + `</h3>
<p style="color:#666">统计周期：` + d.Start + ` ~ ` + d.End + `　生成时间：` + d.Generated + `</p>
<table border="1" cellpadding="8" cellspacing="0" style="border-collapse:collapse;font-size:14px">
<tr style="background:#f0f0f0"><th>线路</th><th>楼层</th><th>可用率</th><th>平均时延</th><th>平均丢包</th><th>测速峰值</th><th>告警次数</th></tr>`)
	for _, l := range d.Lines {
		cell := func(v *float64, unit string, digits int) string {
			if v == nil {
				return `<td style="color:#aaa">无数据</td>`
			}
			return fmt.Sprintf("<td>%."+fmt.Sprint(digits)+"f%s</td>", *v, unit)
		}
		color := "#0a7a3d"
		if l.AvailPct != nil && *l.AvailPct < 99 {
			color = "#c0392b"
		}
		b.WriteString("<tr><td>" + l.Name + "</td><td>" + l.Floor + "</td>")
		if l.AvailPct != nil {
			b.WriteString(fmt.Sprintf(`<td style="color:%s;font-weight:bold">%.2f%%</td>`, color, *l.AvailPct))
		} else {
			b.WriteString(`<td style="color:#aaa">无数据</td>`)
		}
		b.WriteString(cell(l.AvgRTTms, "ms", 1))
		b.WriteString(cell(l.AvgLossPct, "%", 2))
		b.WriteString(cell(l.SpeedMax, "Mbps", 0))
		b.WriteString(fmt.Sprintf("<td>%d</td></tr>", l.AlertCount))
	}
	b.WriteString(`</table><p style="color:#999;font-size:12px">本邮件由物探中心网络监控系统（NetWatch）自动发送。详情见管理后台「报表」页。</p></body></html>`)

	if r.mail != nil {
		if err := r.mail(subject, b.String()); err != nil {
			r.log.Printf("mail report: %v", err)
		}
	}
}

// ReportCSV 导出线路明细 CSV（Excel 可直接打开，含 BOM）
func ReportCSV(d *reportData) []byte {
	var b strings.Builder
	b.WriteString("\uFEFF线路,楼层,可用率%,平均时延ms,平均丢包%,测速峰值Mbps,测速均值Mbps,告警次数\n")
	for _, l := range d.Lines {
		row := []string{l.Name, l.Floor, pstr(l.AvailPct, 2), pstr(l.AvgRTTms, 1), pstr(l.AvgLossPct, 2), pstr(l.SpeedMax, 0), pstr(l.SpeedAvg, 0), strconv.Itoa(l.AlertCount)}
		for i, c := range row {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(c)
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func pstr(v *float64, digits int) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.*f", digits, *v)
}
