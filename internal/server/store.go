package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"netwatch/internal/proto"
)

// Store 业务库（PostgreSQL）：探针、楼层/线路、告警、设置、用户。
// 时序指标不入此库（走 VictoriaMetrics）。
type Store struct {
	pool *pgxpool.Pool
	sec  secretCache // 探针凭证缓存
}

func NewStore(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := s.seedSettings(ctx); err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS floors (
  id SERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  cidr TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS lines (
  id SERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  isp TEXT NOT NULL DEFAULT '联通',
  public_ip TEXT NOT NULL DEFAULT '',
  contract_down_mbps INT NOT NULL DEFAULT 0,
  contract_up_mbps INT NOT NULL DEFAULT 0,
  floor_id INT REFERENCES floors(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS probes (
  id TEXT PRIMARY KEY,
  secret TEXT NOT NULL,
  hostname TEXT NOT NULL DEFAULT '',
  os TEXT NOT NULL DEFAULT '',
  arch TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  source_ip TEXT NOT NULL DEFAULT '',
  gateway TEXT NOT NULL DEFAULT '',
  line_id INT REFERENCES lines(id) ON DELETE SET NULL,
  enrolled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS alerts (
  id BIGSERIAL PRIMARY KEY,
  fingerprint TEXT NOT NULL,
  severity TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  probe_id TEXT NOT NULL DEFAULT '',
  line_id INT,
  starts_at TIMESTAMPTZ NOT NULL,
  ends_at TIMESTAMPTZ,
  status TEXT NOT NULL DEFAULT 'firing'
);
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status, starts_at DESC);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS users (
  id SERIAL PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'admin',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS reports (
  id BIGSERIAL PRIMARY KEY,
  kind TEXT NOT NULL,               -- day / week
  period_start TIMESTAMPTZ NOT NULL,
  period_end TIMESTAMPTZ NOT NULL,
  data JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS silences (
  id BIGSERIAL PRIMARY KEY,
  line_id INT REFERENCES lines(id) ON DELETE CASCADE,  -- NULL = 全部线路
  starts_at TIMESTAMPTZ NOT NULL,
  ends_at TIMESTAMPTZ NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS audit (
  id BIGSERIAL PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  username TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS agent_releases (
  id BIGSERIAL PRIMARY KEY,
  version TEXT NOT NULL,
  os TEXT NOT NULL DEFAULT 'linux',
  arch TEXT NOT NULL DEFAULT 'amd64',
  sha256 TEXT NOT NULL,
  filename TEXT NOT NULL,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE probes ADD COLUMN IF NOT EXISTS pending_version TEXT NOT NULL DEFAULT '';
ALTER TABLE probes ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
ALTER TABLE probes ADD COLUMN IF NOT EXISTS ssh_host TEXT NOT NULL DEFAULT '';
ALTER TABLE probes ADD COLUMN IF NOT EXISTS ssh_port INT NOT NULL DEFAULT 22;
ALTER TABLE probes ADD COLUMN IF NOT EXISTS ssh_user TEXT NOT NULL DEFAULT 'root';
ALTER TABLE probes ADD COLUMN IF NOT EXISTS ssh_pass_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE probes ADD COLUMN IF NOT EXISTS grp TEXT NOT NULL DEFAULT 'internal';`)
	return err
}

// UpdateLinePublicIP 线路出口公网 IP 自动回填（探针心跳探测）
func (s *Store) UpdateLinePublicIP(ctx context.Context, lineID int, ip string) error {
	_, err := s.pool.Exec(ctx, `UPDATE lines SET public_ip=$2 WHERE id=$1`, lineID, ip)
	return err
}

// ---------- 设置 ----------

func DefaultProbeConfig() proto.ProbeConfig {
	return proto.ProbeConfig{
		Version:       1,
		IntervalPingS: 10,
		IntervalTCPS:  30,
		IntervalDNSS:  60,
		IntervalHTTPS: 60,
		TimeoutS:      3,
		ICMPTargets:   []string{"223.5.5.5", "119.29.29.29", "114.114.114.114"},
		TCPTargets:    []string{"www.qq.com:443", "www.baidu.com:443"},
		DNSServers:    []string{"223.5.5.5", "114.114.114.114"},
		DNSDomain:     "www.baidu.com",
		HTTPTargets:   []string{"http://www.qq.com/generate_204", "https://www.baidu.com"},
		SourceIP:      "auto",
		IPEchoURLs:    []string{"http://ip.3322.net", "http://members.3322.org/dyndns/getip", "https://v4.ident.me", "https://myip.ipip.net"},
	}
}

// DefaultExternalProbeConfig 外网组（阿里云等）默认配置：探测单位官网，
// 目标由管理员在后台填入（域名/URL），跳过网关探测。
func DefaultExternalProbeConfig() proto.ProbeConfig {
	return proto.ProbeConfig{
		Version:       1,
		IntervalPingS: 10,
		IntervalTCPS:  0,
		IntervalDNSS:  0,
		IntervalHTTPS: 30,
		TimeoutS:      5,
		DNSDomain:     "",
		SourceIP:      "auto",
		SkipGateway:   true,
		IPEchoURLs:    []string{"http://ip.3322.net", "http://members.3322.org/dyndns/getip", "https://v4.ident.me", "https://myip.ipip.net"},
	}
}

// SMTPSettings 邮件服务器配置（后台填写）
type SMTPSettings struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	UseSSL   bool     `json:"use_ssl"`
	To       []string `json:"to"`
}

func (s SMTPSettings) Valid() bool { return s.Host != "" && s.Port > 0 && len(s.To) > 0 && s.From != "" }

// AlertSettings 告警阈值（默认值即设计文档 6.1 的默认规则）
type AlertSettings struct {
	BlindSecs      int     `json:"blind_secs"`        // 探针失联判定
	CooldownSecs   int     `json:"cooldown_secs"`     // 重复告警冷却
	LineDownSecs   int     `json:"line_down_secs"`    // 线路中断判定窗口
	LossPctThresh  float64 `json:"loss_pct_thresh"`   // 降质丢包阈值
	LossWindowMin  int     `json:"loss_window_min"`   // 降质统计窗口（分钟）
	LatencyMsWarn  float64 `json:"latency_ms_warn"`   // 时延提示阈值（日报用）
	NotifyTo       []string `json:"notify_to"`        // 默认收件组
	NotifyToP0P1   []string `json:"notify_to_p0p1"`   // P0/P1 收件组（为空则用默认）
	NotifyToP2     []string `json:"notify_to_p2"`     // P2 收件组（为空则用默认）
}

func DefaultAlertSettings() AlertSettings {
	return AlertSettings{
		BlindSecs:     60,
		CooldownSecs:  300,
		LineDownSecs:  30,
		LossPctThresh: 10,
		LossWindowMin: 5,
		LatencyMsWarn: 100,
	}
}

// SeedDefaults 首次启动写入默认设置（不覆盖已有值）
func (s *Store) seedSettings(ctx context.Context) error {
	defaults := map[string]any{
		"agent_config":           DefaultProbeConfig(),
		"agent_config_external":  DefaultExternalProbeConfig(),
		"smtp":                   SMTPSettings{},
		"alert_settings":         DefaultAlertSettings(),
		"speed_settings":         DefaultSpeedSettings(),
		"report_settings":        DefaultReportSettings(),
		"notify_channels":        DefaultNotifyChannels(),
		"snmp_settings":          DefaultSnmpSettings(),
		"internal_monitors":      DefaultMonitorSettings(),
	}
	for k, v := range defaults {
		b, _ := json.Marshal(v)
		_, err := s.pool.Exec(ctx,
			`INSERT INTO settings(key, value) VALUES($1, $2) ON CONFLICT (key) DO NOTHING`, k, b)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetSetting(ctx context.Context, key string, out any) error {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (s *Store) PutSetting(ctx context.Context, key string, val any) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO settings(key, value, updated_at) VALUES($1, $2, now())
ON CONFLICT (key) DO UPDATE SET value=$2, updated_at=now()`, key, b)
	return err
}

func (s *Store) ListSettings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v json.RawMessage
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// ---------- 用户 ----------

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, username, hash string) error {
	ct, err := s.pool.Exec(ctx,
		`INSERT INTO users(username, password_hash) VALUES($1,$2) ON CONFLICT (username) DO NOTHING`,
		username, hash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("用户名 %q 已存在", username)
	}
	return nil
}

func (s *Store) SetUserRole(ctx context.Context, username, role string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET role=$2 WHERE username=$1`, username, role)
	return err
}

func (s *Store) GetUser(ctx context.Context, username string) (id int, hash string, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT id, password_hash FROM users WHERE username=$1`, username).Scan(&id, &hash)
	return
}

// ---------- 静默窗口 ----------

type SilenceRow struct {
	ID        int64      `json:"id"`
	LineID    *int       `json:"line_id"`
	LineName  string     `json:"line_name"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    time.Time  `json:"ends_at"`
	Reason    string     `json:"reason"`
	CreatedBy string     `json:"created_by"`
}

func (s *Store) CreateSilence(ctx context.Context, lineID *int, startsAt, endsAt time.Time, reason, by string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO silences(line_id, starts_at, ends_at, reason, created_by)
VALUES($1,$2,$3,$4,$5) RETURNING id`, lineID, startsAt, endsAt, reason, by).Scan(&id)
	return id, err
}

func (s *Store) ListSilences(ctx context.Context) ([]SilenceRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT si.id, si.line_id, COALESCE(l.name,'全局'), si.starts_at, si.ends_at, si.reason, si.created_by
FROM silences si LEFT JOIN lines l ON si.line_id = l.id
ORDER BY si.starts_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SilenceRow{}
	for rows.Next() {
		var r SilenceRow
		if err := rows.Scan(&r.ID, &r.LineID, &r.LineName, &r.StartsAt, &r.EndsAt, &r.Reason, &r.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSilence(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM silences WHERE id=$1`, id)
	return err
}

// ActiveSilences 当前生效的静默（引擎评估用；调用方自行做短缓存）
func (s *Store) ActiveSilences(ctx context.Context) ([]SilenceRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT si.id, si.line_id, COALESCE(l.name,''), si.starts_at, si.ends_at, si.reason, si.created_by
FROM silences si LEFT JOIN lines l ON si.line_id = l.id
WHERE now() BETWEEN si.starts_at AND si.ends_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SilenceRow{}
	for rows.Next() {
		var r SilenceRow
		if err := rows.Scan(&r.ID, &r.LineID, &r.LineName, &r.StartsAt, &r.EndsAt, &r.Reason, &r.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- 报表 ----------

type ReportRow struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) CreateReport(ctx context.Context, kind string, start, end time.Time, data any) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO reports(kind, period_start, period_end, data) VALUES($1,$2,$3,$4) RETURNING id`,
		kind, start, end, data).Scan(&id)
	return id, err
}

func (s *Store) ListReports(ctx context.Context, kind string, limit int) ([]ReportRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, kind, period_start, period_end, created_at FROM reports`
	var args []any
	if kind != "" && kind != "all" {
		q += ` WHERE kind=$1`
		args = append(args, kind)
	}
	q += ` ORDER BY period_end DESC LIMIT ` + fmt.Sprintf("%d", limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportRow{}
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.PeriodStart, &r.PeriodEnd, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReportExists 判断某周期报表是否已生成（防重复推送）
func (s *Store) ReportExists(ctx context.Context, kind string, end time.Time) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM reports WHERE kind=$1 AND period_end=$2`, kind, end).Scan(&n)
	return n > 0, err
}

func (s *Store) GetReport(ctx context.Context, id int64) (kind string, start, end time.Time, data []byte, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT kind, period_start, period_end, data FROM reports WHERE id=$1`, id).
		Scan(&kind, &start, &end, &data)
	return
}

// ---------- 审计 ----------

func (s *Store) WriteAudit(ctx context.Context, username, action, detail string) {
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO audit(username, action, detail) VALUES($1,$2,$3)`, username, action, detail)
}

type AuditRow struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, created_at, username, action, detail FROM audit ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.Username, &r.Action, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- 探针版本升级 ----------

type ReleaseRow struct {
	ID        int64     `json:"id"`
	Version   string    `json:"version"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	SHA256    string    `json:"sha256"`
	Filename  string    `json:"filename"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateRelease(ctx context.Context, r *ReleaseRow) error {
	return s.pool.QueryRow(ctx, `
INSERT INTO agent_releases(version, os, arch, sha256, filename, size_bytes)
VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		r.Version, r.OS, r.Arch, r.SHA256, r.Filename, r.SizeBytes).Scan(&r.ID)
}

func (s *Store) ListReleases(ctx context.Context) ([]ReleaseRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, version, os, arch, sha256, size_bytes, created_at
FROM agent_releases ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReleaseRow{}
	for rows.Next() {
		var r ReleaseRow
		if err := rows.Scan(&r.ID, &r.Version, &r.OS, &r.Arch, &r.SHA256, &r.SizeBytes, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRelease(ctx context.Context, id int64) (*ReleaseRow, error) {
	r := &ReleaseRow{}
	err := s.pool.QueryRow(ctx, `
SELECT id, version, os, arch, sha256, filename, size_bytes, created_at
FROM agent_releases WHERE id=$1`, id).
		Scan(&r.ID, &r.Version, &r.OS, &r.Arch, &r.SHA256, &r.Filename, &r.SizeBytes, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GetReleaseByVersion 按版本号取发布（探针 pending_version 存版本号）
func (s *Store) GetReleaseByVersion(ctx context.Context, version string) (*ReleaseRow, error) {
	r := &ReleaseRow{}
	err := s.pool.QueryRow(ctx, `
SELECT id, version, os, arch, sha256, filename, size_bytes, created_at
FROM agent_releases WHERE version=$1 ORDER BY id DESC LIMIT 1`, version).
		Scan(&r.ID, &r.Version, &r.OS, &r.Arch, &r.SHA256, &r.Filename, &r.SizeBytes, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) SetProbePendingVersion(ctx context.Context, probeID, version string) error {
	_, err := s.pool.Exec(ctx, `UPDATE probes SET pending_version=$2 WHERE id=$1`, probeID, version)
	return err
}

func (s *Store) GetProbePendingVersion(ctx context.Context, probeID string) string {
	var v string
	_ = s.pool.QueryRow(ctx, `SELECT pending_version FROM probes WHERE id=$1`, probeID).Scan(&v)
	return v
}

// ClearProbePending 升级完成后清除待升级标记
func (s *Store) ClearProbePendingIfMatch(ctx context.Context, probeID, version string) {
	_, _ = s.pool.Exec(ctx,
		`UPDATE probes SET pending_version='' WHERE id=$1 AND pending_version=$2`, probeID, version)
}

// GetUserRole 查询用户角色（admin / viewer）
func (s *Store) GetUserRole(ctx context.Context, username string) string {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM users WHERE username=$1`, username).Scan(&role)
	if err != nil || role == "" {
		return "viewer"
	}
	return role
}

type UserRow struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, username, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserRow{}
	for rows.Next() {
		var r UserRow
		if err := rows.Scan(&r.ID, &r.Username, &r.Role, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteUser(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	return err
}

// CountAdmins 用于防止删除最后一个管理员
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='admin'`).Scan(&n)
	return n, err
}

// ---------- 探针 ----------

func (s *Store) UpsertProbe(ctx context.Context, p *proto.RegisterReq, id, secret string) error {
	// name：注册时携带的部署名称（install-agent.sh 第 4 参数）；为空则回退主机名。
	// 注意：重新注册（身份丢失/库重置）会以部署名覆盖——管理员改名后若探针重注册，
	// 请同步更新安装参数或注册后在后台再改。
	display := p.Name
	if display == "" {
		display = p.Hostname
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO probes(id, secret, name, hostname, os, arch, version, source_ip, gateway, last_seen)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
ON CONFLICT (id) DO UPDATE SET
  secret=EXCLUDED.secret, name=EXCLUDED.name, hostname=EXCLUDED.hostname, os=EXCLUDED.os,
  arch=EXCLUDED.arch, version=EXCLUDED.version, source_ip=EXCLUDED.source_ip,
  gateway=EXCLUDED.gateway, last_seen=now()`,
		id, secret, display, p.Hostname, p.OS, p.Arch, p.AgentVer, p.SourceIP, p.Gateway)
	return err
}

// probeSecrets 缓存探针凭证，避免每次上报查库
type secretCache struct {
	mu    sync.RWMutex
	m     map[string]string
	until time.Time
}

func (s *Store) AuthProbe(ctx context.Context, id, secret string) bool {
	s.sec.mu.RLock()
	if time.Now().Before(s.sec.until) {
		v, ok := s.sec.m[id]
		s.sec.mu.RUnlock()
		if ok {
			return v == secret
		}
		// 缓存未见该探针（刚注册/缓存过期前注册的）：直接查库，避免新探针 15s 窗口内被 401
		var dbSec string
		err := s.pool.QueryRow(ctx, `SELECT secret FROM probes WHERE id=$1`, id).Scan(&dbSec)
		return err == nil && dbSec == secret
	}
	s.sec.mu.RUnlock()

	s.sec.mu.Lock()
	defer s.sec.mu.Unlock()
	if time.Now().Before(s.sec.until) { // 其他 goroutine 已刷新
		v, ok := s.sec.m[id]
		return ok && v == secret
	}
	m := map[string]string{}
	rows, err := s.pool.Query(ctx, `SELECT id, secret FROM probes`)
	if err == nil {
		for rows.Next() {
			var pid, sec string
			if rows.Scan(&pid, &sec) == nil {
				m[pid] = sec
			}
		}
		rows.Close()
	}
	s.sec.m = m
	s.sec.until = time.Now().Add(15 * time.Second)
	v, ok := s.sec.m[id]
	return ok && v == secret
}

func (s *Store) TouchProbe(ctx context.Context, id, version string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE probes SET last_seen=now(), version=$2 WHERE id=$1`, id, version)
	return err
}

type ProbeRow struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Hostname       string     `json:"hostname"`
	Group          string     `json:"group"`
	OS             string     `json:"os"`
	Arch           string     `json:"arch"`
	Version        string     `json:"version"`
	SourceIP       string     `json:"source_ip"`
	Gateway        string     `json:"gateway"`
	LineID         *int       `json:"line_id"`
	LineName       *string    `json:"line_name"`
	PendingVersion string     `json:"pending_version"`
	SshHost        string     `json:"ssh_host"`
	SshPort        int        `json:"ssh_port"`
	SshUser        string     `json:"ssh_user"`
	SshConfigured  bool       `json:"ssh_configured"`
	EnrolledAt     time.Time  `json:"enrolled_at"`
	LastSeen       *time.Time `json:"last_seen"`
}

func (s *Store) ListProbes(ctx context.Context) ([]ProbeRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT p.id, p.name, p.hostname, COALESCE(p.grp,'internal'), p.os, p.arch, p.version, p.source_ip, p.gateway,
       p.line_id, l.name, p.pending_version, p.ssh_host, p.ssh_port, p.ssh_user,
       (p.ssh_host != '' AND p.ssh_pass_enc != ''), p.enrolled_at, p.last_seen
FROM probes p LEFT JOIN lines l ON p.line_id = l.id
ORDER BY p.enrolled_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProbeRow{}
	for rows.Next() {
		var r ProbeRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Hostname, &r.Group, &r.OS, &r.Arch, &r.Version, &r.SourceIP,
			&r.Gateway, &r.LineID, &r.LineName, &r.PendingVersion,
			&r.SshHost, &r.SshPort, &r.SshUser, &r.SshConfigured, &r.EnrolledAt, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SshRow 探针 SSH 运维凭据（含加密密码，仅内部使用）
type SshRow struct {
	Host    string
	Port    int
	User    string
	PassEnc string
}

func (s *Store) GetProbeSSH(ctx context.Context, probeID string) (SshRow, error) {
	var r SshRow
	err := s.pool.QueryRow(ctx,
		`SELECT ssh_host, ssh_port, ssh_user, ssh_pass_enc FROM probes WHERE id=$1`, probeID).
		Scan(&r.Host, &r.Port, &r.User, &r.PassEnc)
	if r.User == "" {
		r.User = "root"
	}
	return r, err
}

// UpdateProbeMeta 更新探针名称与 SSH 运维信息（passEnc 为空表示不改动密码）
func (s *Store) UpdateProbeMeta(ctx context.Context, probeID string, name *string,
	host *string, port *int, user *string, passEnc *string) error {
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if utf8.RuneCountInString(trimmed) > 64 {
			return errors.New("探针名称过长（≤64 字符）")
		}
		if _, err := s.pool.Exec(ctx, `UPDATE probes SET name=$2 WHERE id=$1`, probeID, trimmed); err != nil {
			return err
		}
	}
	if host != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE probes SET ssh_host=$2 WHERE id=$1`, probeID, *host); err != nil {
			return err
		}
	}
	if port != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE probes SET ssh_port=$2 WHERE id=$1`, probeID, *port); err != nil {
			return err
		}
	}
	if user != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE probes SET ssh_user=$2 WHERE id=$1`, probeID, *user); err != nil {
			return err
		}
	}
	if passEnc != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE probes SET ssh_pass_enc=$2 WHERE id=$1`, probeID, *passEnc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AssignProbeLine(ctx context.Context, probeID string, lineID *int) error {
	_, err := s.pool.Exec(ctx, `UPDATE probes SET line_id=$2 WHERE id=$1`, probeID, lineID)
	return err
}

func (s *Store) DeleteProbe(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM probes WHERE id=$1`, id)
	return err
}

// LineMeta 告警引擎使用的线路元信息（probeID → 线路）
type LineMeta struct {
	LineID           int
	LineName         string
	Floor            string
	PublicIP         string
	CIDR             string
	ContractDownMbps int
	Bound            bool
	Group            string // internal / external
	ProbeName        string // 探针显示名称（空则主机名）
	ProbeHostname    string
}

func (s *Store) ProbeLineMeta(ctx context.Context) (map[string]LineMeta, error) {
	rows, err := s.pool.Query(ctx, `
SELECT p.id, COALESCE(l.id,0), COALESCE(l.name,''), COALESCE(f.name,''),
       COALESCE(l.public_ip,''), COALESCE(f.cidr,''), COALESCE(l.contract_down_mbps,0),
       COALESCE(p.grp,'internal'), COALESCE(p.name,''), COALESCE(p.hostname,'')
FROM probes p
LEFT JOIN lines l ON p.line_id = l.id
LEFT JOIN floors f ON l.floor_id = f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]LineMeta{}
	for rows.Next() {
		var id string
		var lm LineMeta
		if err := rows.Scan(&id, &lm.LineID, &lm.LineName, &lm.Floor, &lm.PublicIP, &lm.CIDR, &lm.ContractDownMbps, &lm.Group, &lm.ProbeName, &lm.ProbeHostname); err != nil {
			return nil, err
		}
		if lm.Group == "" {
			lm.Group = "internal"
		}
		lm.Bound = lm.LineID > 0
		m[id] = lm
	}
	return m, rows.Err()
}

// SetProbeGroup 探针切换分组（internal=楼层宽带内网组 / external=外网服务器组）
func (s *Store) SetProbeGroup(ctx context.Context, probeID, group string) error {
	if group != "internal" && group != "external" {
		group = "internal"
	}
	_, err := s.pool.Exec(ctx, `UPDATE probes SET grp=$2, line_id=NULL WHERE id=$1`, probeID, group)
	return err
}

// ---------- 楼层与线路 ----------

type FloorRow struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	LineCount int    `json:"line_count"`
}

type LineRow struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	ISP              string `json:"isp"`
	PublicIP         string `json:"public_ip"`
	ContractDownMbps int    `json:"contract_down_mbps"`
	ContractUpMbps   int    `json:"contract_up_mbps"`
	FloorID          *int   `json:"floor_id"`
	FloorName        string `json:"floor_name"`
	CIDR             string `json:"cidr"`
}

func (s *Store) ListFloors(ctx context.Context) ([]FloorRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT f.id, f.name, f.cidr,
       (SELECT count(*) FROM lines l WHERE l.floor_id = f.id) AS line_count
FROM floors f ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FloorRow{}
	for rows.Next() {
		var r FloorRow
		if err := rows.Scan(&r.ID, &r.Name, &r.CIDR, &r.LineCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertFloor(ctx context.Context, id *int, name, cidr string) (int, error) {
	var dup int
	q := `SELECT count(*) FROM floors WHERE name=$1`
	args := []any{name}
	if id != nil {
		q += ` AND id<>$2`
		args = append(args, *id)
	}
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&dup); err == nil && dup > 0 {
		return 0, fmt.Errorf("楼层名 %q 已存在", name)
	}
	if id != nil {
		_, err := s.pool.Exec(ctx, `UPDATE floors SET name=$2, cidr=$3 WHERE id=$1`, *id, name, cidr)
		return *id, err
	}
	var nid int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO floors(name, cidr) VALUES($1,$2) RETURNING id`, name, cidr).Scan(&nid)
	return nid, err
}

func (s *Store) DeleteFloor(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM floors WHERE id=$1`, id)
	return err
}

func (s *Store) ListLines(ctx context.Context) ([]LineRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT l.id, l.name, l.isp, l.public_ip, l.contract_down_mbps, l.contract_up_mbps,
       l.floor_id, COALESCE(f.name,''), COALESCE(f.cidr,'')
FROM lines l LEFT JOIN floors f ON l.floor_id = f.id
ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LineRow{}
	for rows.Next() {
		var r LineRow
		if err := rows.Scan(&r.ID, &r.Name, &r.ISP, &r.PublicIP, &r.ContractDownMbps,
			&r.ContractUpMbps, &r.FloorID, &r.FloorName, &r.CIDR); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertLine(ctx context.Context, l *LineRow) (int, error) {
	// 线路名唯一：重名会让"按线路"的曲线/历史/告警统计互相混叠
	var dup int
	q := `SELECT count(*) FROM lines WHERE name=$1`
	args := []any{l.Name}
	if l.ID != 0 {
		q += ` AND id<>$2`
		args = append(args, l.ID)
	}
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&dup); err == nil && dup > 0 {
		return 0, fmt.Errorf("线路名 %q 已存在", l.Name)
	}
	if l.ID != 0 {
		_, err := s.pool.Exec(ctx, `
UPDATE lines SET name=$2, isp=$3, public_ip=$4, contract_down_mbps=$5,
                 contract_up_mbps=$6, floor_id=$7 WHERE id=$1`,
			l.ID, l.Name, l.ISP, l.PublicIP, l.ContractDownMbps, l.ContractUpMbps, l.FloorID)
		return l.ID, err
	}
	var nid int
	err := s.pool.QueryRow(ctx, `
INSERT INTO lines(name, isp, public_ip, contract_down_mbps, contract_up_mbps, floor_id)
VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		l.Name, l.ISP, l.PublicIP, l.ContractDownMbps, l.ContractUpMbps, l.FloorID).Scan(&nid)
	return nid, err
}

func (s *Store) DeleteLine(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM lines WHERE id=$1`, id)
	return err
}

// ---------- 告警 ----------

type AlertRow struct {
	ID        int64      `json:"id"`
	Fingerprint string   `json:"fingerprint"`
	Severity  string     `json:"severity"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	ProbeID   string     `json:"probe_id"`
	LineID    *int       `json:"line_id"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	Status    string     `json:"status"`
}

func (s *Store) CreateAlert(ctx context.Context, a *AlertRow) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO alerts(fingerprint, severity, kind, title, body, probe_id, line_id, starts_at, status)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,'firing') RETURNING id`,
		a.Fingerprint, a.Severity, a.Kind, a.Title, a.Body, a.ProbeID, a.LineID, a.StartsAt).Scan(&id)
	return id, err
}

func (s *Store) ResolveAlert(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE alerts SET status='resolved', ends_at=now() WHERE id=$1`, id)
	return err
}

func (s *Store) ListAlerts(ctx context.Context, status string, limit int) ([]AlertRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT id, fingerprint, severity, kind, title, body, probe_id, line_id, starts_at, ends_at, status
FROM alerts`
	var args []any
	if status != "" && status != "all" {
		q += ` WHERE status=$1`
		args = append(args, status)
	}
	q += ` ORDER BY starts_at DESC LIMIT ` + fmt.Sprintf("%d", limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRow{}
	for rows.Next() {
		var r AlertRow
		if err := rows.Scan(&r.ID, &r.Fingerprint, &r.Severity, &r.Kind, &r.Title, &r.Body,
			&r.ProbeID, &r.LineID, &r.StartsAt, &r.EndsAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveAllFiring 中心重启后，将中断的告警 episode 收口（历史保留）。
func (s *Store) ResolveAllFiring(ctx context.Context) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE alerts SET status='resolved', ends_at=now() WHERE status='firing'`)
	return err
}
