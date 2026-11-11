package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"netwatch/internal/mailfmt"
)

// RelayOptions probe-1 中继参数：通过管理网轮询中心，联通专线出口不可用时
// 用本探针自己的线路向外部 webhook/邮件代发告警。
type RelayOptions struct {
	ServerURL   string
	StateDir    string
	Webhook     string
	WebhookType string // dingtalk / wecom / generic
	LineName    string // 中心所在专线名称（与系统线路名一致，默认"联通专线"）
	Interval    time.Duration
	Timeout     time.Duration

	// 邮件通道（优先于 webhook；专线断时用探针自己的线路发信）
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string
	SMTPTo   []string
}

// relayState 中继状态：记录当前告警类型、是否由中继拥有恢复邮件、已代发的告警 ID。
type relayState struct {
	CenterDown   bool           `json:"center_down"`
	EgressDown   bool           `json:"egress_down"`
	OwnsRecovery bool           `json:"owns_recovery"`
	Kind         string         `json:"kind"` // center / egress
	StartedAt    time.Time      `json:"started_at"`
	SentAlertIDs map[int64]bool `json:"sent_alert_ids"`
	LastSentAt   time.Time      `json:"last_sent_at"`
}

func (s *relayState) reset() {
	*s = relayState{SentAlertIDs: map[int64]bool{}}
}

type relayHealth struct {
	Status          string `json:"status"`
	EgressOK        bool   `json:"egress_ok"`
	EgressCheckedAt int64  `json:"egress_checked_at"`
}

type relayAlert struct {
	ID       int64     `json:"id"`
	Severity string    `json:"severity"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	StartsAt time.Time `json:"starts_at"`
}

type relayAlertsResp struct {
	EgressOK bool         `json:"egress_ok"`
	Alerts   []relayAlert `json:"alerts"`
	Total    int          `json:"total"`
}

// relayMail 与中心告警邮件同模板：对象/状态/级别/时间/探针/消息/详情。
type relayMail struct {
	Probe      string
	Object     string
	Firing     bool
	Severity   string
	Message    string
	Detail     string
	StartedAt  time.Time
	ResolvedAt time.Time
}

func (m relayMail) text() string {
	state, _ := mailfmt.State(m.Firing)
	var b strings.Builder
	fmt.Fprintf(&b, "[NetWatch] [%s] [%s] %s\n", m.Object, state, m.Message)
	fmt.Fprintf(&b, "时间: %s\n", m.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "探针: %s\n", m.Probe)
	fmt.Fprintf(&b, "消息: %s", m.Message)
	if m.Detail != "" {
		fmt.Fprintf(&b, "\n详情: %s", m.Detail)
	}
	if !m.Firing && !m.ResolvedAt.IsZero() {
		fmt.Fprintf(&b, "\n恢复时间: %s", m.ResolvedAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(&b, "\n停机时长: %s", mailfmt.Duration(m.ResolvedAt.Sub(m.StartedAt)))
	}
	return b.String()
}

func centerDownMail(probeID string) relayMail {
	return relayMail{
		Probe: probeID, Object: "中心服务器", Firing: true, Severity: "P0",
		Message:   "中心不可达",
		Detail:    "中心管理网健康检查连续 3 次失败；可能中心服务、电源或管理网故障。",
		StartedAt: time.Now(),
	}
}

func egressDownMail(probeID, lineName string, alerts []relayAlert) relayMail {
	detail := "当前无 firing 告警（仅中心出口自检失败）。"
	if len(alerts) > 0 {
		var b strings.Builder
		b.WriteString("当前活动告警：")
		for _, a := range alerts {
			fmt.Fprintf(&b, "\n- [%s] %s（%s）", a.Severity, a.Title, a.StartsAt.Format("15:04"))
		}
		detail = b.String()
	}
	return relayMail{
		Probe: probeID, Object: lineName, Firing: true, Severity: "P1",
		Message:   "线路出口中断",
		Detail:    detail,
		StartedAt: time.Now(),
	}
}

func recoveryMail(probeID string, st relayState, lineName string) relayMail {
	object, msg := "中心服务器", "中心恢复"
	if st.Kind == "egress" {
		object, msg = lineName, "线路出口恢复"
	}
	start := st.StartedAt
	if start.IsZero() {
		start = time.Now()
	}
	return relayMail{
		Probe: probeID, Object: object, Firing: false, Severity: "P1",
		Message: msg, Detail: "中心健康与出口自检已恢复。",
		StartedAt: start, ResolvedAt: time.Now(),
	}
}

// RunRelay 阻塞运行中继循环，直到 ctx 取消。
func RunRelay(ctx context.Context, opt RelayOptions) error {
	if strings.TrimSpace(opt.ServerURL) == "" {
		return fmt.Errorf("relay: 缺少中心地址")
	}
	if strings.TrimSpace(opt.Webhook) == "" && strings.TrimSpace(opt.SMTPHost) == "" {
		return fmt.Errorf("relay: 需要 webhook 或 SMTP 配置")
	}
	if opt.Interval <= 0 {
		opt.Interval = 15 * time.Second
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Second
	}
	if strings.TrimSpace(opt.LineName) == "" {
		opt.LineName = "联通专线"
	}
	ident, err := ReadIdentity(opt.StateDir)
	if err != nil {
		return fmt.Errorf("relay: 读取探针身份失败: %w", err)
	}
	state := loadRelayState(opt.StateDir)
	var centerFail, egressFail int
	client := &http.Client{Timeout: opt.Timeout}
	base := strings.TrimRight(opt.ServerURL, "/")

	log.Printf("relay: 启动，中心 %s，探针 %s，间隔 %s", base, ident.AgentID, opt.Interval)
	t := time.NewTicker(opt.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("relay: 停止")
			return nil
		case <-t.C:
		}

		ok, health := fetchHealth(ctx, client, base)
		if !ok {
			centerFail++
			egressFail = 0
			if centerFail >= 3 {
				// 仅状态变化或 30 分钟重复时发送，避免每 15s 刷屏
				if !state.CenterDown || time.Since(state.LastSentAt) >= 30*time.Minute {
					m := centerDownMail(ident.AgentID)
					if sendRelayMessage(ctx, client, opt, m) {
						state.CenterDown = true
						state.EgressDown = false
						state.OwnsRecovery = true
						state.Kind = "center"
						state.StartedAt = m.StartedAt
						state.SentAlertIDs = map[int64]bool{}
						state.LastSentAt = time.Now()
						saveRelayState(opt.StateDir, state)
					}
				}
			}
			continue
		}
		centerFail = 0
		if health.EgressCheckedAt == 0 {
			continue // 首次自检未完成，不下结论
		}
		if health.EgressOK {
			egressFail = 0
			if state.CenterDown || state.EgressDown {
				if state.OwnsRecovery {
					m := recoveryMail(ident.AgentID, state, opt.LineName)
					if sendRelayMessage(ctx, client, opt, m) {
						state.reset()
						saveRelayState(opt.StateDir, state)
					}
				} else {
					// 已代发中心真实告警：恢复邮件由中心在出口恢复后发送，中继不重复发
					log.Printf("relay: 恢复邮件由中心发送（中继已代发告警），不重复发送")
					state.reset()
					saveRelayState(opt.StateDir, state)
				}
			}
			continue
		}

		// 中心可达但 egress_ok=false：联通专线出口断，代发 firing 告警
		egressFail++
		if egressFail < 3 {
			continue
		}
		alerts, err := fetchRelayAlerts(ctx, client, base, ident)
		if err != nil {
			log.Printf("relay: 读取中心告警失败: %v", err)
			continue
		}
		var newAlerts []relayAlert
		for _, a := range alerts {
			if !state.SentAlertIDs[a.ID] {
				newAlerts = append(newAlerts, a)
			}
		}
		repeat := state.EgressDown && time.Since(state.LastSentAt) >= 30*time.Minute
		if len(newAlerts) == 0 && state.EgressDown && !repeat {
			continue
		}
		m := egressDownMail(ident.AgentID, opt.LineName, alerts)
		if !sendRelayMessage(ctx, client, opt, m) {
			continue
		}
		if state.SentAlertIDs == nil {
			state.SentAlertIDs = map[int64]bool{}
		}
		for _, a := range newAlerts {
			state.SentAlertIDs[a.ID] = true
		}
		state.EgressDown = true
		state.CenterDown = false
		// 无真实告警时中继自己拥有恢复；有真实告警则由中心发恢复，避免重复
		state.OwnsRecovery = len(alerts) == 0
		state.Kind = "egress"
		state.StartedAt = m.StartedAt
		state.LastSentAt = time.Now()
		saveRelayState(opt.StateDir, state)
	}
}

func fetchHealth(ctx context.Context, client *http.Client, base string) (bool, relayHealth) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/health", nil)
	if err != nil {
		return false, relayHealth{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, relayHealth{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, relayHealth{}
	}
	var h relayHealth
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&h); err != nil {
		return false, relayHealth{}
	}
	return true, h
}

func fetchRelayAlerts(ctx context.Context, client *http.Client, base string, ident Identity) ([]relayAlert, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/agent/relay/alerts", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-NW-Agent", ident.AgentID)
	req.Header.Set("X-NW-Secret", ident.Secret)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out relayAlertsResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Alerts, nil
}

func sendRelayMessage(ctx context.Context, client *http.Client, opt RelayOptions, m relayMail) bool {
	if strings.TrimSpace(opt.SMTPHost) != "" {
		return sendRelayMail(opt, m)
	}
	var payload any
	text := m.text()
	switch strings.ToLower(opt.WebhookType) {
	case "dingtalk", "wecom":
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	default:
		payload = map[string]any{"text": text}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opt.Webhook, bytes.NewReader(b))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("relay: webhook 发送失败: %v", err)
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("relay: webhook HTTP %d", resp.StatusCode)
		return false
	}
	log.Printf("relay: webhook 已发送（%d 字节）", len(b))
	return true
}

// sendRelayMail 用探针自己的线路直接 SMTP 发信，格式与中心告警邮件共用 mailfmt。
func sendRelayMail(opt RelayOptions, m relayMail) bool {
	if len(opt.SMTPTo) == 0 || opt.SMTPFrom == "" {
		log.Printf("relay: SMTP 收件人或发件人为空")
		return false
	}
	port := opt.SMTPPort
	if port <= 0 {
		port = 25
	}
	rows := []mailfmt.Row{
		{Key: "级别", Value: html.EscapeString(m.Severity)},
		{Key: "时间 (" + mailfmt.TimeZone + ")", Value: m.StartedAt.Format("2006-01-02 15:04:05")},
	}
	if !m.Firing && !m.ResolvedAt.IsZero() {
		rows = append(rows,
			mailfmt.Row{Key: "恢复时间 (" + mailfmt.TimeZone + ")", Value: m.ResolvedAt.Format("2006-01-02 15:04:05")},
			mailfmt.Row{Key: "停机时长", Value: mailfmt.Duration(m.ResolvedAt.Sub(m.StartedAt))},
		)
	}
	rows = append(rows,
		mailfmt.Row{Key: "探针", Value: html.EscapeString(m.Probe)},
		mailfmt.Row{Key: "消息", Value: html.EscapeString(m.Message)},
	)
	if m.Detail != "" {
		rows = append(rows, mailfmt.Row{Key: "详情", Value: html.EscapeString(m.Detail)})
	}
	subject := mailfmt.Subject(m.Object, m.Firing, m.Message)
	htmlBody := mailfmt.HTML(m.Object, m.Firing, rows, "")
	raw := mailfmt.BuildMIME(opt.SMTPFrom, opt.SMTPTo, subject, htmlBody)
	auth := smtp.PlainAuth("", opt.SMTPUser, opt.SMTPPass, opt.SMTPHost)
	addr := fmt.Sprintf("%s:%d", opt.SMTPHost, port)
	if err := smtp.SendMail(addr, auth, opt.SMTPFrom, opt.SMTPTo, []byte(raw)); err != nil {
		log.Printf("relay: SMTP 发送失败: %v", err)
		return false
	}
	log.Printf("relay: 邮件已发送 -> %s（%s）", strings.Join(opt.SMTPTo, ","), subject)
	return true
}

func relayStatePath(stateDir string) string {
	return filepath.Join(stateDir, "relay-state.json")
}

func loadRelayState(stateDir string) relayState {
	st := relayState{SentAlertIDs: map[int64]bool{}}
	b, err := os.ReadFile(relayStatePath(stateDir))
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.SentAlertIDs == nil {
		st.SentAlertIDs = map[int64]bool{}
	}
	return st
}

func saveRelayState(stateDir string, st relayState) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := relayStatePath(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("relay: 保存状态失败: %v", err)
		return
	}
	if err := os.Rename(tmp, relayStatePath(stateDir)); err != nil {
		log.Printf("relay: 保存状态失败: %v", err)
	}
}
