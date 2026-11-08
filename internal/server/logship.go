package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 日志外发：把监测事件整理成标准 syslog 格式（CEF / KV）转发到外部日志审计服务器
//（如奇安信日志审计/态势感知平台）。UDP/TCP 可配，端口默认 514。
// 事件量极低（告警偶发、测速每线路每小时一轮、扫描按计划、出口变化罕见），
// 被动发送即可，无需后台循环；发送 goroutine 异步，失败仅记日志不阻塞热路径。

// SyslogSettings 日志外发设置（settings 键 syslog_settings，管理后台可改）
type SyslogSettings struct {
	Enabled    bool   `json:"enabled"`
	Host       string `json:"host"`        // 审计服务器地址（IP 或域名）
	Port       int    `json:"port"`        // 默认 514
	Proto      string `json:"proto"`       // udp（默认）| tcp
	Format     string `json:"format"`      // cef（默认）| kv
	AppName    string `json:"app_name"`    // syslog APP-NAME，默认 NetWatch
	TimeoutS   int    `json:"timeout_s"`   // 发送（建连+写）超时秒，默认 5；通知与推送页可改
	SendAlerts bool   `json:"send_alerts"` // 告警触发/恢复
	SendSpeed  bool   `json:"send_speed"`  // 测速结果
	SendScan   bool   `json:"send_scan"`   // IP 扫描网段汇总
	SendEgress bool   `json:"send_egress"` // 线路出口 IP 变化
}

func DefaultSyslogSettings() SyslogSettings {
	return SyslogSettings{
		Port:       514,
		Proto:      "udp",
		Format:     "cef",
		AppName:    "NetWatch",
		TimeoutS:   5,
		SendAlerts: true,
		SendSpeed:  true,
		SendScan:   true,
		SendEgress: true,
	}
}

// normalize 修正越界/缺省值（脏数据兜底；与 speedsched.go 同模式）。
func (s *SyslogSettings) normalize() {
	s.Host = strings.TrimSpace(s.Host)
	s.AppName = strings.TrimSpace(s.AppName)
	if s.AppName == "" {
		s.AppName = "NetWatch"
	}
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = 514
	}
	if s.Proto != "tcp" {
		s.Proto = "udp"
	}
	if s.Format != "kv" {
		s.Format = "cef"
	}
	if s.TimeoutS <= 0 || s.TimeoutS > 15 {
		s.TimeoutS = 5
	}
}

// LogEvent 一条待外发的监测事件（CEF/KV 两种格式共用的中间结构）
type LogEvent struct {
	Type     string // alert / speed / scan / egress（CEF 签名 ID、KV event=）
	Kind     string // 细分类型（alert 时为告警 kind，如 line_down）
	Time     time.Time
	Severity string      // P0/P1/P2（alert 类）；其他事件留空按 notice
	Status   string      // firing / resolved（alert 类）
	Title    string      // 事件名（CEF name / KV title=）
	Msg      string      // 人读摘要（CEF msg= / KV msg=，KV 中恒排最后）
	Fields   [][2]string // 有序结构化字段（line/floor/probe/cidr/...，空值跳过）
}

// LogShipper 日志外发器：读 syslog_settings（30s 缓存）→ 按类型开关过滤 → 拼报文 → 发送。
type LogShipper struct {
	store *Store
	log   *log.Logger
	host  string // syslog HOST 字段（构造时取一次）
	ver   string // CEF 设备版本

	settingsFn func() SyslogSettings // 测试注入用（nil 时读 settings 表）

	setMu sync.Mutex
	set   SyslogSettings
	setAt time.Time // 零值=尚未成功读过
}

// NewLogShipper 构造日志外发器（cfg 仅取 Version 用于 CEF 设备版本字段）。
func NewLogShipper(cfg Config, store *Store) *LogShipper {
	return &LogShipper{
		store: store,
		log:   log.New(os.Stderr, "logship ", log.LstdFlags|log.Lmsgprefix),
		host:  localHostname(),
		ver:   cfg.Version,
	}
}

func localHostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "netwatch-server"
}

// settings 惰性读取（30s 缓存）；读失败时沿用上次配置，从未读过则禁用。
func (s *LogShipper) settings(ctx context.Context) SyslogSettings {
	if s.settingsFn != nil {
		st := s.settingsFn()
		st.normalize()
		return st
	}
	s.setMu.Lock()
	defer s.setMu.Unlock()
	now := time.Now()
	if now.Before(s.setAt) {
		return s.set
	}
	var st SyslogSettings
	if err := s.store.GetSetting(ctx, "syslog_settings", &st); err != nil {
		if s.setAt.IsZero() {
			return SyslogSettings{} // Enabled=false，静默跳过
		}
		return s.set
	}
	st.normalize()
	s.set, s.setAt = st, now.Add(30*time.Second)
	return st
}

// Ship 外发一条事件：未启用/未配地址/类型开关关闭即静默跳过。
// 全程异步（设置读取+发送都在 goroutine 里），挂接点在引擎锁内，不得阻塞热路径；失败仅记日志。
func (s *LogShipper) Ship(evt LogEvent) {
	if evt.Time.IsZero() {
		evt.Time = time.Now()
	}
	typ := evt.Type
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st := s.settings(ctx)
		if !st.Enabled || st.Host == "" {
			return
		}
		switch typ {
		case "alert":
			if !st.SendAlerts {
				return
			}
		case "speed":
			if !st.SendSpeed {
				return
			}
		case "scan":
			if !st.SendScan {
				return
			}
		case "egress":
			if !st.SendEgress {
				return
			}
		default:
			return
		}
		msg := buildSyslogMessage(s.ver, s.host, st, evt)
		if err := sendSyslog(st, msg); err != nil {
			s.log.Printf("外发 %s 事件失败: %v", typ, err)
		}
	}()
}

// SendTestLog 按给定配置同步发送一条样例告警事件（管理后台"测试发送"用，不经开关过滤）。
func SendTestLog(cfg Config, st SyslogSettings) error {
	st.normalize()
	if st.Host == "" {
		return fmt.Errorf("审计服务器地址未配置")
	}
	evt := LogEvent{
		Type: "alert", Kind: "test", Time: time.Now(), Severity: "P2", Status: "firing",
		Title: "NetWatch 测试事件",
		Msg:   "这是一条 NetWatch 日志外发测试消息，收到即表示链路畅通",
		Fields: [][2]string{
			{"line", "测试线路"}, {"floor", "测试楼层"}, {"probe", "测试探针"},
			{"cidr", "10.8.14.0/23"}, {"public_ip", "203.0.113.1"},
		},
	}
	return sendSyslog(st, buildSyslogMessage(cfg.Version, localHostname(), st, evt))
}

// sendSyslog 发送单条 syslog 报文（per-send 建连：事件量低，简单稳健；UDP 天然无连接）。
// 建连与写超时统一取 st.TimeoutS（默认 5s，通知与推送页可改）。
func sendSyslog(st SyslogSettings, msg string) error {
	if len(msg) > 2048 { // 防超长（RFC5424 单条报文建议 ≤2048；本系统事件拼接后远小于此）
		msg = msg[:2048]
	}
	timeout := time.Duration(st.TimeoutS) * time.Second
	addr := net.JoinHostPort(st.Host, strconv.Itoa(st.Port))
	if st.Proto == "tcp" {
		conn, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetWriteDeadline(time.Now().Add(timeout))
		_, err = conn.Write([]byte(msg + "\n")) // 传统 BSD 分帧：换行分隔（兼容大多数接收端）
		return err
	}
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	_, err = conn.Write([]byte(msg))
	return err
}

// severityFor 事件级别 → (syslog PRI = facility local0(14)×8 + severity, CEF severity 0~10)。
// P0→crit(2)/10，P1→err(3)/7，P2→warning(4)/4，其他→notice(5)/5。
func severityFor(sev string) (pri, cefSev int) {
	switch sev {
	case "P0":
		return 14*8 + 2, 10
	case "P1":
		return 14*8 + 3, 7
	case "P2":
		return 14*8 + 4, 4
	}
	return 14*8 + 5, 5
}

// buildSyslogMessage 组装完整 syslog 报文：
// CEF = RFC5424 头（<PRI>1 TIMESTAMP HOST APP-NAME - - - MSG）+ CEF:0 载荷；
// KV  = RFC3164 头（<PRI>Mmm dd hh:mm:ss HOST APP-NAME:）+ key=value 正文。
func buildSyslogMessage(version, host string, st SyslogSettings, evt LogEvent) string {
	pri, cefSev := severityFor(evt.Severity)
	if st.Format == "kv" {
		return fmt.Sprintf("<%d>%s %s %s: %s", pri,
			evt.Time.Format("Jan  2 15:04:05"), host, st.AppName, buildKVBody(evt))
	}
	ts := evt.Time.Format("2006-01-02T15:04:05.000Z07:00")
	return fmt.Sprintf("<%d>1 %s %s %s - - - %s", pri, ts, host, st.AppName,
		buildCEFBody(version, cefSev, evt))
}

// cefSlots CEF 自定义槽位映射表：扩展字段按此顺序占用 cs1..csN（csNLabel 标注含义），
// 平台侧可按固定槽位做白名单映射。字段名即 LogEvent.Fields 的 key。
var cefSlots = []string{
	"status", "line", "floor", "probe", "cidr", "public_ip", "old_ip", "new_ip", "source",
	"old_line", "new_line", "isp", "geo", "down_mbps", "up_mbps", "ok_runs", "threshold",
	"verdict", "online", "total", "duration_ms", "trigger",
}

// buildCEFBody 生成 CEF 载荷：CEF:0|厂商|产品|版本|签名ID|名称|严重性|扩展
// 签名 ID 用告警 kind（如 line_down）；标准扩展 msg 携带摘要；业务字段走 cs1..csN
// 自定义槽位（csNLabel 标注含义，CEF 规范的自定义字段写法，任意 CEF 解析器均可识别）。
func buildCEFBody(version string, sevNum int, evt LogEvent) string {
	kind := evt.Kind
	if kind == "" {
		kind = evt.Type
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CEF:0|NetWatch|NetWatch|%s|%s|%s|%d|",
		cefEscape(version), cefEscape(kind), cefEscape(evt.Title), sevNum)

	vals, order := map[string]string{}, []string{}
	fields := evt.Fields
	if evt.Status != "" { // status 是一等字段，自动并入扩展（告警 firing/resolved）
		fields = append([][2]string{{"status", evt.Status}}, fields...)
	}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if _, dup := vals[f[0]]; !dup {
			order = append(order, f[0])
		}
		vals[f[0]] = f[1]
	}
	exts := make([][2]string, 0, len(evt.Fields)+2)
	if evt.Msg != "" {
		exts = append(exts, [2]string{"msg", evt.Msg})
	}
	inSlot := map[string]bool{}
	for i, key := range cefSlots {
		if val, ok := vals[key]; ok {
			exts = append(exts, [2]string{fmt.Sprintf("cs%dLabel", i+1), key},
				[2]string{fmt.Sprintf("cs%d", i+1), val})
			inSlot[key] = true
		}
	}
	// 槽位表未覆盖的字段按原 key 输出（兜底，避免信息丢失；均非 CEF 保留字）
	for _, key := range order {
		if !inSlot[key] {
			exts = append(exts, [2]string{key, vals[key]})
		}
	}
	for i, e := range exts {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s=%s", e[0], cefEscape(e[1]))
	}
	return b.String()
}

// buildKVBody 生成 key=value 正文：event/status/severity 在前，业务字段居中，msg 恒排最后。
// 值中的空白字符替换为下划线（KV 按空格分词解析的接收端不会截断）。
func buildKVBody(evt LogEvent) string {
	kind := evt.Kind
	if kind == "" {
		kind = evt.Type
	}
	parts := []string{"event=" + kvEscape(kind), "title=" + kvEscape(evt.Title)}
	if evt.Status != "" {
		parts = append(parts, "status="+evt.Status)
	}
	if evt.Severity != "" {
		parts = append(parts, "severity="+evt.Severity)
	}
	for _, f := range evt.Fields {
		if f[1] != "" {
			parts = append(parts, f[0]+"="+kvEscape(f[1]))
		}
	}
	if evt.Msg != "" {
		parts = append(parts, "msg="+kvEscape(evt.Msg))
	}
	return strings.Join(parts, " ")
}

// cefEscape CEF 值转义：\ = | 与换行（CEF 规范要求）。
func cefEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `=`, `\=`, `|`, `\|`, "\n", `\n`, "\r", ``)
	return r.Replace(s)
}

// kvEscape KV 值转义：空白替换为下划线。
func kvEscape(s string) string {
	return strings.NewReplacer(" ", "_", "\t", "_", "\n", "_", "\r", "_").Replace(s)
}

// ---- 事件构造（各挂接点调用）----

// alertLogEvent 告警/恢复 → 外发事件（静默告警不会走到 sendMail，天然跟随静默）。
func alertLogEvent(meta AlertMeta, firing bool) LogEvent {
	status := "resolved"
	if firing {
		status = "firing"
	}
	return LogEvent{
		Type: "alert", Kind: meta.Kind, Time: time.Now(),
		Severity: meta.Severity, Status: status,
		Title: meta.Title,
		Msg:   meta.Title,
		Fields: [][2]string{
			{"line", meta.LineName}, {"floor", meta.Floor}, {"probe", meta.ProbeDisplay},
			{"cidr", meta.CIDR}, {"public_ip", meta.PublicIP},
		},
	}
}

// speedLogEvent 测速结果（每轮均值一条）→ 外发事件。
func speedLogEvent(probe, line string, down, up float64, okRuns int, threshold float64, verdict string) LogEvent {
	return LogEvent{
		Type: "speed", Time: time.Now(),
		Title: "线路测速完成",
		Msg:   fmt.Sprintf("%s 测速均值 %.1f Mbps（%d 连测，判定 %s）", line, down, okRuns, verdict),
		Fields: [][2]string{
			{"probe", probe}, {"line", line},
			{"down_mbps", ftoa(down)}, {"up_mbps", ftoa(up)},
			{"ok_runs", strconv.Itoa(okRuns)}, {"threshold", ftoa(threshold)},
			{"verdict", verdict},
		},
	}
}

// scanLogEvent IP 扫描网段完成 → 外发事件。
func scanLogEvent(floor, cidr string, online, total int, durMs int64, trigger string) LogEvent {
	return LogEvent{
		Type: "scan", Time: time.Now(),
		Title: "IP 网段扫描完成",
		Msg:   fmt.Sprintf("%s（%s）在线 %d/%d，耗时 %dms", floor, cidr, online, total, durMs),
		Fields: [][2]string{
			{"floor", floor}, {"cidr", cidr},
			{"online", strconv.Itoa(online)}, {"total", strconv.Itoa(total)},
			{"duration_ms", strconv.FormatInt(durMs, 10)}, {"trigger", trigger},
		},
	}
}

// egressLogEvent 线路出口 IP 变化 → 外发事件（与 line_ip_history 留痕同源）。
func egressLogEvent(title string, fields [][2]string) LogEvent {
	return LogEvent{Type: "egress", Time: time.Now(), Title: title, Fields: fields}
}
