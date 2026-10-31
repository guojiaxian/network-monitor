package server

import (
	"context"
	"fmt"
	"log"
	"os"
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
	DailyEnabled  bool   `json:"daily_enabled"`
	DailyAt       string `json:"daily_at"`        // "08:00"
	WeeklyEnabled bool   `json:"weekly_enabled"`
	WeeklyDay     int    `json:"weekly_day"`      // 1=周一 … 7=周日
	WeeklyAt      string `json:"weekly_at"`
}

func DefaultReportSettings() ReportSettings {
	return ReportSettings{DailyEnabled: true, DailyAt: "08:00", WeeklyEnabled: true, WeeklyDay: 1, WeeklyAt: "08:30"}
}

// 报表行（JSON 存库 + 邮件渲染 + CSV 导出）
type reportLine struct {
	Name        string   `json:"name"`
	Floor       string   `json:"floor"`
	AvailPct    *float64 `json:"avail_pct"`    // 公网探测可用率 %
	AvgRTTms    *float64 `json:"avg_rtt_ms"`
	AvgLossPct  *float64 `json:"avg_loss_pct"`
	SpeedMax    *float64 `json:"speed_max_mbps"`
	SpeedAvg    *float64 `json:"speed_avg_mbps"`
	AlertCount  int      `json:"alert_count"`
	HasData     bool     `json:"has_data"`
}

type reportData struct {
	Kind   string       `json:"kind"`
	Start  string       `json:"start"`
	End    string       `json:"end"`
	Lines  []reportLine `json:"lines"`
	Generated string    `json:"generated"`
}

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

func (r *Reporter) tick(ctx context.Context) {
	var rs ReportSettings
	if err := r.store.GetSetting(ctx, "report_settings", &rs); err != nil {
		return
	}
	now := time.Now()
	hhmm := func(t time.Time) string { return t.Format("15:04") }

	// 日报：每 DailyAt 触发，统计前一整天
	if rs.DailyEnabled && hhmm(now) == rs.DailyAt {
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		start := end.Add(-24 * time.Hour)
		r.generateOnce(ctx, "day", start, end)
	}
	// 周报：WeeklyDay 指定星期，统计过去 7 天
	if rs.WeeklyEnabled && int(now.Weekday()) == rs.WeeklyDay && hhmm(now) == rs.WeeklyAt {
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		start := end.Add(-7 * 24 * time.Hour)
		r.generateOnce(ctx, "week", start, end)
	}
}

// Generate 手动生成报表（管理后台"立即生成"）
func (r *Reporter) Generate(ctx context.Context, kind string) (int64, error) {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, now.Location())
	var start time.Time
	if kind == "week" {
		start = end.Add(-7 * 24 * time.Hour)
	} else {
		kind = "day"
		start = end.Add(-24 * time.Hour)
	}
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

func (r *Reporter) generate(ctx context.Context, kind string, start, end time.Time) (int64, error) {
	window := end.Sub(start)
	win := window.String()
	if kind == "day" {
		win = "24h"
	} else {
		win = "7d"
	}

	avail, _ := r.tsdb.QueryByLine(ctx, fmt.Sprintf(`100 * avg by (line) (avg_over_time(nw_ping_ok{class="public"}[%s]))`, win))
	rtt, _ := r.tsdb.QueryByLine(ctx, fmt.Sprintf(`avg by (line) (avg_over_time(nw_ping_rtt_ms{class="public"}[%s]))`, win))
	loss, _ := r.tsdb.QueryByLine(ctx, fmt.Sprintf(`avg by (line) (avg_over_time(nw_ping_loss_pct{class="public"}[%s]))`, win))
	smax, _ := r.tsdb.QueryByLine(ctx, fmt.Sprintf(`max by (line) (max_over_time(nw_speed_down_mbps[%s]))`, win))
	savg, _ := r.tsdb.QueryByLine(ctx, fmt.Sprintf(`avg by (line) (max_over_time(nw_speed_down_mbps[%s]))`, win))

	lines, err := r.store.ListLines(ctx)
	if err != nil {
		return 0, err
	}
	// 各线路周期内告警数
	alertCounts := map[int]int{}
	rows, err := r.store.pool.Query(ctx,
		`SELECT line_id, count(*) FROM alerts WHERE line_id IS NOT NULL AND starts_at BETWEEN $1 AND $2 GROUP BY line_id`, start, end)
	if err == nil {
		for rows.Next() {
			var lid, c int
			if rows.Scan(&lid, &c) == nil {
				alertCounts[lid] = c
			}
		}
		rows.Close()
	}

	data := reportData{Kind: kind, Start: start.Format("2006-01-02 15:04"), End: end.Format("2006-01-02 15:04"),
		Generated: time.Now().Format("2006-01-02 15:04:05")}
	f := func(m map[string]float64, k string) *float64 {
		if v, ok := m[k]; ok {
			return &v
		}
		return nil
	}
	for _, l := range lines {
		rl := reportLine{Name: l.Name, Floor: l.FloorName, AlertCount: alertCounts[l.ID]}
		rl.AvailPct = f(avail, l.Name)
		rl.AvgRTTms = f(rtt, l.Name)
		rl.AvgLossPct = f(loss, l.Name)
		rl.SpeedMax = f(smax, l.Name)
		rl.SpeedAvg = f(savg, l.Name)
		rl.HasData = rl.AvailPct != nil || rl.AvgRTTms != nil
		data.Lines = append(data.Lines, rl)
	}

	id, err := r.store.CreateReport(ctx, kind, start, end, data)
	if err != nil {
		return 0, err
	}
	r.log.Printf("report %s generated: %s ~ %s (%d lines)", kind, data.Start, data.End, len(data.Lines))
	r.mailReport(ctx, kind, &data)
	return id, nil
}

func (r *Reporter) mailReport(ctx context.Context, kind string, d *reportData) {
	var smtpCfg SMTPSettings
	if err := r.store.GetSetting(ctx, "smtp", &smtpCfg); err != nil || !smtpCfg.Valid() {
		return
	}
	title := "日报"
	if kind == "week" {
		title = "周报"
	}
	subject := fmt.Sprintf("[NetWatch] 网络可用率%s（%s ~ %s）", title, d.Start, d.End)

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body style="font-family:'Microsoft YaHei',sans-serif;max-width:720px;margin:0 auto">
<h3>NetWatch 网络可用率` + title + `</h3>
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
	b.WriteString(`</table><p style="color:#999;font-size:12px">本邮件由 NetWatch 自动发送。详情见管理后台「报表」页。</p></body></html>`)

	if r.mail != nil {
		if err := r.mail(subject, b.String()); err != nil {
			r.log.Printf("mail report: %v", err)
		}
	}
}

// CSV 导出（Excel 可直接打开，含 BOM）
func ReportCSV(d *reportData) []byte {
	var b strings.Builder
	b.WriteString("\uFEFF线路,楼层,可用率%,平均时延ms,平均丢包%,测速峰值Mbps,测速均值Mbps,告警次数\n")
	for _, l := range d.Lines {
		row := []string{l.Name, l.Floor, pstr(l.AvailPct, 2), pstr(l.AvgRTTms, 1), pstr(l.AvgLossPct, 2), pstr(l.SpeedMax, 0), pstr(l.SpeedAvg, 0), fmt.Sprint(l.AlertCount)}
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
