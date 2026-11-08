package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netwatch/internal/proto"
)

// ErrNotFound 按 ID 更新/读取时目标不存在（handler 映射 404，避免静默成功）。
var ErrNotFound = errors.New("not found")

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

// Ping 数据库连通性检查（health API 用）
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

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
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  role TEXT NOT NULL,
  exp TIMESTAMPTZ NOT NULL
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
-- 探针分组 grp（internal/external）已随外网探针功能删除（2026-11-07）：老库收敛删列
ALTER TABLE probes DROP COLUMN IF EXISTS grp;
DELETE FROM settings WHERE key='agent_config_external';
ALTER TABLE probes ADD COLUMN IF NOT EXISTS sysinfo JSONB NOT NULL DEFAULT '{}';
ALTER TABLE lines ADD COLUMN IF NOT EXISTS geo TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS line_floors (
  line_id  INT NOT NULL REFERENCES lines(id) ON DELETE CASCADE,
  floor_id INT NOT NULL REFERENCES floors(id) ON DELETE CASCADE,
  PRIMARY KEY (line_id, floor_id)
);
INSERT INTO line_floors(line_id, floor_id)
SELECT id, floor_id FROM lines WHERE floor_id IS NOT NULL
ON CONFLICT DO NOTHING;
-- 旧版 scan_runs（2026-11-06 当日结构：一次扫描一行、floors/results 按楼层聚合）
-- 改为"一网段一记录"后的结构不兼容；旧表当天上线即重构、仅含测试数据，直接重建。
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='scan_runs' AND column_name='floors') THEN
    DROP TABLE scan_runs;
  END IF;
END $$;
CREATE TABLE IF NOT EXISTS scan_runs (
  id BIGSERIAL PRIMARY KEY,
  batch TEXT NOT NULL DEFAULT '',            -- 一批扫描共用（同一次手动/定时任务）
  floor_id INT,                              -- 楼层（弱关联：删楼层后历史保留）
  floor_name TEXT NOT NULL DEFAULT '',
  cidr TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',    -- pending/running/done/error/interrupted
  up INT NOT NULL DEFAULT 0,
  down INT NOT NULL DEFAULT 0,
  total INT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  results JSONB,                             -- 逐 IP 明细 [{ip,online,rtt_ms}]
  trigger TEXT NOT NULL DEFAULT 'manual',    -- manual（后台发起）/ schedule（定时任务）
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_scan_runs_created ON scan_runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_scan_runs_floor ON scan_runs(floor_id, created_at DESC);
-- 重启时遗留的未完成行必然已死（扫描任务为内存态），统一收口为中断
UPDATE scan_runs SET status='interrupted' WHERE status IN ('pending','running');
-- 线路出口 IP 变化留痕（2026-11-07 动态 IP 决议：探针出口变化自动回写绑定线路，
-- 每次变化在此留痕，线路详情页展示变化轨迹；删线路级联清理）
CREATE TABLE IF NOT EXISTS line_ip_history (
  id BIGSERIAL PRIMARY KEY,
  line_id INT NOT NULL REFERENCES lines(id) ON DELETE CASCADE,
  old_ip TEXT NOT NULL DEFAULT '',           -- 空=首次登记
  new_ip TEXT NOT NULL,
  probe_id TEXT NOT NULL DEFAULT '',         -- 手工修改时为空
  probe_name TEXT NOT NULL DEFAULT '',       -- 手工修改时存操作账号
  source TEXT NOT NULL DEFAULT 'egress',     -- egress=探针出口对账 / manual=手工修改
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_line_ip_history_line ON line_ip_history(line_id, created_at DESC);`)
	return err
}

// FindLineByPublicIP 按出口公网 IP 查线路（egress-identity 拓扑下探针改绑的匹配依据）
func (s *Store) FindLineByPublicIP(ctx context.Context, ip string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx, `SELECT id FROM lines WHERE public_ip=$1 ORDER BY id LIMIT 1`, ip).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// UpdateLineEgressInfo 线路出口属性自动回填（探针心跳探测）：公网 IP/运营商/归属地。
// 空值不覆盖（如探针尚未探得 IPv6 归属时不回退）。
func (s *Store) UpdateLineEgressInfo(ctx context.Context, lineID int, ip, isp, geo string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE lines SET
  public_ip=CASE WHEN $2<>'' THEN $2 ELSE public_ip END,
  isp=CASE WHEN $3<>'' THEN $3 ELSE isp END,
  geo=CASE WHEN $4<>'' THEN $4 ELSE geo END
WHERE id=$1`, lineID, ip, isp, geo)
	return err
}

// LineIPChange 线路出口 IP 变化留痕（探针出口对账自动回写 / 后台手工修改）
type LineIPChange struct {
	ID        int64     `json:"id"`
	LineID    int       `json:"line_id"`
	OldIP     string    `json:"old_ip"`
	NewIP     string    `json:"new_ip"`
	ProbeID   string    `json:"probe_id"`
	ProbeName string    `json:"probe_name"` // egress 来源=探针显示名；manual 来源=操作账号
	Source    string    `json:"source"`     // egress / manual
	CreatedAt time.Time `json:"created_at"`
}

// RecordLineIPChange 出口 IP 变化留痕。失败由调用方记日志——留痕断链不应静默（坑 24 教训）。
func (s *Store) RecordLineIPChange(ctx context.Context, lineID int, oldIP, newIP, probeID, probeName, source string) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO line_ip_history(line_id, old_ip, new_ip, probe_id, probe_name, source)
VALUES($1,$2,$3,$4,$5,$6)`, lineID, oldIP, newIP, probeID, probeName, source)
	return err
}

// ListLineIPHistory 线路出口 IP 变化轨迹（时间倒序，线路详情页展示）
func (s *Store) ListLineIPHistory(ctx context.Context, lineID, limit int) ([]LineIPChange, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, line_id, old_ip, new_ip, probe_id, probe_name, source, created_at
FROM line_ip_history WHERE line_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`, lineID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineIPChange
	for rows.Next() {
		var h LineIPChange
		if err := rows.Scan(&h.ID, &h.LineID, &h.OldIP, &h.NewIP, &h.ProbeID, &h.ProbeName, &h.Source, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
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
		RetryN:        1,
		RetryPauseMs:  300,
		HeartbeatSec:  15,
		EgressMin:     5,
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

func (s SMTPSettings) Valid() bool {
	return s.Host != "" && s.Port > 0 && len(s.To) > 0 && s.From != ""
}

// AlertSettings 告警阈值（默认值即设计文档 6.1 的默认规则）
type AlertSettings struct {
	BlindSecs       int      `json:"blind_secs"`         // 探针失联判定
	LineDownSecs    int      `json:"line_down_secs"`     // 线路中断判定窗口
	LossPctThresh   float64  `json:"loss_pct_thresh"`    // 降质丢包阈值
	LossWindowMin   int      `json:"loss_window_min"`    // 降质统计窗口（分钟）
	EvalIntervalS   int      `json:"eval_interval_s"`    // 告警引擎评估周期秒（默认 5；探测配置页可改）
	TypeFailWindowS int      `json:"type_fail_window_s"` // 探测异常判定窗口秒（默认 60；探测配置页可改）
	NotifyTo        []string `json:"notify_to"`          // 默认收件组
	NotifyToP0P1    []string `json:"notify_to_p0p1"`     // P0/P1 收件组（为空则用默认）
	NotifyToP2      []string `json:"notify_to_p2"`       // P2 收件组（为空则用默认）
}

func DefaultAlertSettings() AlertSettings {
	return AlertSettings{
		BlindSecs:       60,
		LineDownSecs:    30,
		LossPctThresh:   10,
		LossWindowMin:   5,
		EvalIntervalS:   5,
		TypeFailWindowS: 60,
	}
}

// SeedDefaults 首次启动写入默认设置（不覆盖已有值）
func (s *Store) seedSettings(ctx context.Context) error {
	defaults := map[string]any{
		"agent_config":      DefaultProbeConfig(),
		"smtp":              SMTPSettings{},
		"alert_settings":    DefaultAlertSettings(),
		"speed_settings":    DefaultSpeedSettings(),
		"syslog_settings":   DefaultSyslogSettings(),
		"report_settings":   DefaultReportSettings(),
		"notify_channels":   DefaultNotifyChannels(),
		"snmp_settings":     DefaultSnmpSettings(),
		"internal_monitors": DefaultMonitorSettings(),
		"industry_monitors": DefaultIndustrySettings(),
		"scan_settings":     DefaultScanSettings(),
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

// SetUserPassword 更新用户密码哈希（修改自己密码用）
func (s *Store) SetUserPassword(ctx context.Context, username, hash string) error {
	ct, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE username=$1`, username, hash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("用户 %q 不存在", username)
	}
	return nil
}

func (s *Store) GetUser(ctx context.Context, username string) (id int, hash string, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT id, password_hash FROM users WHERE username=$1`, username).Scan(&id, &hash)
	return
}

// ---------- 静默窗口 ----------

type SilenceRow struct {
	ID        int64     `json:"id"`
	LineID    *int      `json:"line_id"`
	LineName  string    `json:"line_name"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Reason    string    `json:"reason"`
	CreatedBy string    `json:"created_by"`
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
	if s == nil || s.pool == nil { // 空池容错（进程内单测构造的 AdminAPI 无库）
		return
	}
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
INSERT INTO probes(id, secret, name, hostname, os, arch, version, source_ip, gateway, sysinfo, last_seen)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
ON CONFLICT (id) DO UPDATE SET
  secret=EXCLUDED.secret, name=EXCLUDED.name, hostname=EXCLUDED.hostname, os=EXCLUDED.os,
  arch=EXCLUDED.arch, version=EXCLUDED.version, source_ip=EXCLUDED.source_ip,
  gateway=EXCLUDED.gateway, sysinfo=EXCLUDED.sysinfo, last_seen=now()`,
		id, secret, display, p.Hostname, p.OS, p.Arch, p.AgentVer, p.SourceIP, p.Gateway, sysInfoJSON(p.SysInfo))
	return err
}

// sysInfoJSON 资产信息转 JSONB（空 map 存 {}）
func sysInfoJSON(si map[string]string) []byte {
	if len(si) == 0 {
		return []byte(`{}`)
	}
	b, err := json.Marshal(si)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

// UpdateProbeSysInfo 探针资产信息（心跳补报，覆盖式更新）
func (s *Store) UpdateProbeSysInfo(ctx context.Context, probeID string, si map[string]string) {
	_, _ = s.pool.Exec(ctx, `UPDATE probes SET sysinfo=$2 WHERE id=$1`, probeID, sysInfoJSON(si))
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
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Hostname       string            `json:"hostname"`
	OS             string            `json:"os"`
	Arch           string            `json:"arch"`
	Version        string            `json:"version"`
	SourceIP       string            `json:"source_ip"`
	Gateway        string            `json:"gateway"`
	LineID         *int              `json:"line_id"`
	LineName       *string           `json:"line_name"`
	PendingVersion string            `json:"pending_version"`
	SshHost        string            `json:"ssh_host"`
	SshPort        int               `json:"ssh_port"`
	SshUser        string            `json:"ssh_user"`
	SshConfigured  bool              `json:"ssh_configured"`
	SysInfo        map[string]string `json:"sysinfo,omitempty"`
	EnrolledAt     time.Time         `json:"enrolled_at"`
	LastSeen       *time.Time        `json:"last_seen"`
}

func (s *Store) ListProbes(ctx context.Context) ([]ProbeRow, error) {
	rows, err := s.pool.Query(ctx, `
	SELECT p.id, p.name, p.hostname, p.os, p.arch, p.version, p.source_ip, p.gateway,
	       p.line_id, l.name, p.pending_version, p.ssh_host, p.ssh_port, p.ssh_user,
	       (p.ssh_host != '' AND p.ssh_pass_enc != ''), p.enrolled_at, p.last_seen, COALESCE(p.sysinfo,'{}'::jsonb)
FROM probes p LEFT JOIN lines l ON p.line_id = l.id
ORDER BY p.enrolled_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProbeRow{}
	for rows.Next() {
		var r ProbeRow
		var sysInfo []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Hostname, &r.OS, &r.Arch, &r.Version, &r.SourceIP,
			&r.Gateway, &r.LineID, &r.LineName, &r.PendingVersion,
			&r.SshHost, &r.SshPort, &r.SshUser, &r.SshConfigured, &r.EnrolledAt, &r.LastSeen, &sysInfo); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(sysInfo, &r.SysInfo)
		out = append(out, r)
	}
	sortProbesForDisplay(out) // 全站统一：探针按显示名序号 probe-1~12 排列
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
	ISP              string
	Geo              string
	CIDR             string
	ContractDownMbps int
	Bound            bool
	ProbeName        string // 探针显示名称（空则主机名）
	ProbeHostname    string
}

func (s *Store) ProbeLineMeta(ctx context.Context) (map[string]LineMeta, error) {
	rows, err := s.pool.Query(ctx, `
SELECT p.id, COALESCE(l.id,0), COALESCE(l.name,''),
       COALESCE((SELECT string_agg(f.name, '、') FROM line_floors lf JOIN floors f ON f.id=lf.floor_id WHERE lf.line_id=l.id), ''),
       COALESCE(l.public_ip,''), COALESCE(l.isp,''), COALESCE(l.geo,''),
       COALESCE((SELECT string_agg(f.cidr, ',') FROM line_floors lf JOIN floors f ON f.id=lf.floor_id WHERE lf.line_id=l.id), ''),
       COALESCE(l.contract_down_mbps,0), COALESCE(p.name,''), COALESCE(p.hostname,'')
FROM probes p
LEFT JOIN lines l ON p.line_id = l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]LineMeta{}
	for rows.Next() {
		var id string
		var lm LineMeta
		if err := rows.Scan(&id, &lm.LineID, &lm.LineName, &lm.Floor, &lm.PublicIP, &lm.ISP, &lm.Geo, &lm.CIDR, &lm.ContractDownMbps, &lm.ProbeName, &lm.ProbeHostname); err != nil {
			return nil, err
		}
		lm.Bound = lm.LineID > 0
		m[id] = lm
	}
	return m, rows.Err()
}

// ---------- 楼层与线路 ----------

type FloorRow struct {
	ID        int              `json:"id"`
	Name      string           `json:"name"`
	CIDR      string           `json:"cidr"`
	LineCount int              `json:"line_count"`
	Lines     []FloorLineBrief `json:"lines"` // 关联线路（名称+出口公网 IP，楼层配置页展示用）
}

type FloorLineBrief struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	PublicIP string `json:"public_ip"`
}

type FloorBrief struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	CIDR string `json:"cidr"`
}

type LineRow struct {
	ID               int          `json:"id"`
	Name             string       `json:"name"`
	ISP              string       `json:"isp"`
	PublicIP         string       `json:"public_ip"`
	Geo              string       `json:"geo"`
	ContractDownMbps int          `json:"contract_down_mbps"`
	ContractUpMbps   int          `json:"contract_up_mbps"`
	FloorIDs         []int        `json:"floor_ids"`  // 写入用：关联楼层 id 列表（UpsertLine 消费）
	Floors           []FloorBrief `json:"floors"`     // 关联楼层（多对多）
	FloorName        string       `json:"floor_name"` // 聚合展示名（"1-3楼、8楼-1、10楼"）
	CIDR             string       `json:"cidr"`       // 聚合展示（逗号分隔）
	// 在线 IP 设备数：该线路各关联楼层"最近一次成功扫描"的在线数之和；
	// nil = 尚未扫描（前端显示"—"而不是 0）。
	OnlineIPs []FloorOnlineIP `json:"online_ips"` // 逐楼层明细
	OnlineSum *int            `json:"online_sum"` // 合计在线数
	ScanSum   *int            `json:"scan_sum"`   // 合计扫描总数（"42 / 254" 的分母）
	ScannedAt *time.Time      `json:"scanned_at"` // 最近一次扫描时间
}

// FloorOnlineIP 单楼层最近一次扫描的在线数（线路在线 IP 汇总的构成项）。
type FloorOnlineIP struct {
	FloorID   int       `json:"floor_id"`
	FloorName string    `json:"floor_name"`
	Up        int       `json:"up"`
	Total     int       `json:"total"`
	At        time.Time `json:"at"`
}

// ---------- 展示排序（2026-11-07 用户定的全站统一规则） ----------
// 探针：显示名尾部数字升序（probe-1~12），无数字的排后（按显示名、ID）
// 线路：名称含"线路N"的按 N 升序 → 其余线路按 ID → 名含"专线"的最后
// 楼层：按子网 CIDR（网络地址字节序，再按掩码）升序；未配置/非法 CIDR 排最后
// 三个 List* 查询出口统一调用，管理页/总览/大屏/报表/扫描计划全部继承同一顺序。

var lineNumRe = regexp.MustCompile(`线路(\d+)`)

func lineNum(name string) (int, bool) {
	m := lineNumRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func trailingNum(s string) (int, bool) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i >= len(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		return 0, false
	}
	return n, true
}

func probeDisplayName(p ProbeRow) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Hostname
}

func sortProbesForDisplay(ps []ProbeRow) {
	sort.SliceStable(ps, func(i, j int) bool {
		ni, iok := trailingNum(probeDisplayName(ps[i]))
		nj, jok := trailingNum(probeDisplayName(ps[j]))
		if iok != jok {
			return iok // 有编号的在前
		}
		if iok && ni != nj {
			return ni < nj
		}
		di, dj := probeDisplayName(ps[i]), probeDisplayName(ps[j])
		if di != dj {
			return di < dj
		}
		return ps[i].ID < ps[j].ID
	})
}

// lineGroup 0=带"线路N"编号、1=其余线路、2=专线（殿后）
func lineGroup(name string) int {
	if strings.Contains(name, "专线") {
		return 2
	}
	if _, ok := lineNum(name); ok {
		return 0
	}
	return 1
}

func sortLinesForDisplay(ls []LineRow) {
	sort.SliceStable(ls, func(i, j int) bool {
		gi, gj := lineGroup(ls[i].Name), lineGroup(ls[j].Name)
		if gi != gj {
			return gi < gj
		}
		if gi == 0 {
			ni, _ := lineNum(ls[i].Name)
			nj, _ := lineNum(ls[j].Name)
			if ni != nj {
				return ni < nj
			}
		}
		return ls[i].ID < ls[j].ID
	})
}

func sortFloorsForDisplay(fs []FloorRow) {
	sort.SliceStable(fs, func(i, j int) bool {
		pi, e1 := netip.ParsePrefix(strings.TrimSpace(fs[i].CIDR))
		pj, e2 := netip.ParsePrefix(strings.TrimSpace(fs[j].CIDR))
		if e1 != nil || e2 != nil {
			if e1 != nil && e2 != nil {
				return fs[i].ID < fs[j].ID // 都无效：按 ID
			}
			return e2 != nil // 有效的在前
		}
		if c := pi.Addr().Compare(pj.Addr()); c != 0 {
			return c < 0
		}
		return pi.Bits() < pj.Bits()
	})
}

func (s *Store) ListFloors(ctx context.Context) ([]FloorRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT f.id, f.name, f.cidr,
       (SELECT count(*) FROM line_floors lf WHERE lf.floor_id = f.id) AS line_count,
       COALESCE((SELECT json_agg(json_build_object('id',l.id,'name',l.name,'public_ip',l.public_ip) ORDER BY l.id)
                 FROM line_floors lf JOIN lines l ON l.id = lf.line_id WHERE lf.floor_id = f.id), '[]'::json)
FROM floors f ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FloorRow{}
	for rows.Next() {
		var r FloorRow
		var linesJSON []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.CIDR, &r.LineCount, &linesJSON); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(linesJSON, &r.Lines)
		out = append(out, r)
	}
	sortFloorsForDisplay(out) // 全站统一：楼层按子网 CIDR 排列
	return out, rows.Err()
}

// ---------- 会话持久化（重启后会话保留，30 天免登录） ----------

func (s *Store) CreateSession(ctx context.Context, token, username, role string, exp time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions(token, username, role, exp) VALUES($1,$2,$3,$4)
		 ON CONFLICT (token) DO UPDATE SET username=$2, role=$3, exp=$4`, token, username, role, exp)
	return err
}

// GetSession 回源查会话（内存失效/重启后用）；过期行顺带删除并返回 false
func (s *Store) GetSession(ctx context.Context, token string) (username, role string, ok bool) {
	var u, r string
	var exp time.Time
	err := s.pool.QueryRow(ctx, `SELECT username, role, exp FROM sessions WHERE token=$1`, token).
		Scan(&u, &r, &exp)
	if err != nil {
		return "", "", false
	}
	if time.Now().After(exp) {
		_, _ = s.pool.Exec(ctx, `DELETE FROM sessions WHERE token=$1`, token)
		return "", "", false
	}
	return u, r, true
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token=$1`, token)
	return err
}

// DeleteSessionsByUser 删除某用户的全部持久化会话（改密/删除用户时调用——
// 只清内存会话不够：server 重启后内存回源 PG，已删用户的会话会复活）
func (s *Store) DeleteSessionsByUser(ctx context.Context, username string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE username=$1`, username)
	return err
}

// CleanExpiredSessions 清理过期会话行（登录时顺手调用）
func (s *Store) CleanExpiredSessions(ctx context.Context) {
	_, _ = s.pool.Exec(ctx, `DELETE FROM sessions WHERE exp < now()`)
}

// GetFloor 按 ID 取单个楼层（IP 扫描等按楼层操作用）
func (s *Store) GetFloor(ctx context.Context, id int) (FloorRow, error) {
	var f FloorRow
	err := s.pool.QueryRow(ctx, `SELECT id, name, cidr FROM floors WHERE id=$1`, id).
		Scan(&f.ID, &f.Name, &f.CIDR)
	return f, err
}

func (s *Store) UpsertFloor(ctx context.Context, id *int, name, cidr string) (int, error) {
	// 名称唯一性检查与写入必须原子：并发同名创建曾出现多个成功（先查后插竞态）。
	// 历史库可能已有重复名，不新增唯一索引；用事务级 advisory lock 串行化同名写入。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "floors:"+name); err != nil {
		return 0, err
	}
	var dup int
	q := `SELECT count(*) FROM floors WHERE name=$1`
	args := []any{name}
	if id != nil {
		q += ` AND id<>$2`
		args = append(args, *id)
	}
	if err := tx.QueryRow(ctx, q, args...).Scan(&dup); err != nil {
		return 0, err
	}
	if dup > 0 {
		return 0, fmt.Errorf("楼层名 %q 已存在", name)
	}
	if id != nil {
		ct, err := tx.Exec(ctx, `UPDATE floors SET name=$2, cidr=$3 WHERE id=$1`, *id, name, cidr)
		if err != nil {
			return 0, err
		}
		if ct.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, err
		}
		return *id, nil
	}
	var nid int
	if err := tx.QueryRow(ctx,
		`INSERT INTO floors(name, cidr) VALUES($1,$2) RETURNING id`, name, cidr).Scan(&nid); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return nid, nil
}

func (s *Store) DeleteFloor(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM floors WHERE id=$1`, id)
	return err
}

func (s *Store) ListLines(ctx context.Context) ([]LineRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT l.id, l.name, l.isp, l.public_ip, COALESCE(l.geo,''), l.contract_down_mbps, l.contract_up_mbps,
  COALESCE((SELECT json_agg(json_build_object('id',f.id,'name',f.name,'cidr',f.cidr) ORDER BY f.id)
            FROM line_floors lf JOIN floors f ON f.id = lf.floor_id WHERE lf.line_id = l.id), '[]'::json)
FROM lines l ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LineRow{}
	for rows.Next() {
		var r LineRow
		var floorsJSON []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.ISP, &r.PublicIP, &r.Geo, &r.ContractDownMbps,
			&r.ContractUpMbps, &floorsJSON); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(floorsJSON, &r.Floors)
		names, cidrs := make([]string, 0, len(r.Floors)), make([]string, 0, len(r.Floors))
		for _, f := range r.Floors {
			names = append(names, f.Name)
			if f.CIDR != "" {
				cidrs = append(cidrs, f.CIDR)
			}
		}
		r.FloorName = strings.Join(names, "、")
		r.CIDR = strings.Join(cidrs, ", ")
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// 在线 IP 数：各楼层最近一次成功扫描汇总（扫描记录为空时留 nil，前端显示"—"）
	scans, err := s.LatestFloorScans(ctx)
	if err != nil {
		return nil, err
	}
	attachLineOnlineIPs(out, scans)
	sortLinesForDisplay(out) // 全站统一：联通线路N 按序号，其余居中，专线最后
	return out, nil
}

// attachLineOnlineIPs 把楼层扫描摘要汇总到线路上（纯函数，供回归用例覆盖）。
// 一个楼层的在线数计入其关联的全部线路（拓扑上楼层—线路多对多，实际为一一对应）。
func attachLineOnlineIPs(lines []LineRow, scans map[int]FloorScanSummary) {
	for i := range lines {
		var items []FloorOnlineIP
		sum, total := 0, 0
		var at time.Time
		for _, f := range lines[i].Floors {
			sm, ok := scans[f.ID]
			if !ok {
				continue
			}
			items = append(items, FloorOnlineIP{FloorID: f.ID, FloorName: f.Name, Up: sm.Up, Total: sm.Total, At: sm.At})
			sum += sm.Up
			total += sm.Total
			if sm.At.After(at) {
				at = sm.At
			}
		}
		lines[i].OnlineIPs = items
		if len(items) > 0 {
			lines[i].OnlineSum = &sum
			lines[i].ScanSum = &total
			lines[i].ScannedAt = &at
		}
	}
}

// UpsertLine 新增/更新线路。floorIDs 为关联楼层（多对多，空切片=清空关联）。
func (s *Store) UpsertLine(ctx context.Context, l *LineRow, floorIDs []int) (int, error) {
	// 线路名唯一：重名会让"按线路"的曲线/历史/告警统计互相混叠。
	// 与楼层同理，检查+写入放在同一事务并加同名 advisory lock，消除并发重复创建竞态。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "lines:"+l.Name); err != nil {
		return 0, err
	}
	var dup int
	q := `SELECT count(*) FROM lines WHERE name=$1`
	args := []any{l.Name}
	if l.ID != 0 {
		q += ` AND id<>$2`
		args = append(args, l.ID)
	}
	if err := tx.QueryRow(ctx, q, args...).Scan(&dup); err != nil {
		return 0, err
	}
	if dup > 0 {
		return 0, fmt.Errorf("线路名 %q 已存在", l.Name)
	}
	var nid int
	if l.ID != 0 {
		// 自动探测字段（isp/public_ip/geo）空值=不覆盖：管理员改其他字段时
		// 不会把探针自动回填的出口信息误清空
		ct, err := tx.Exec(ctx, `
UPDATE lines SET name=$2,
                 isp=CASE WHEN $3<>'' THEN $3 ELSE isp END,
                 public_ip=CASE WHEN $4<>'' THEN $4 ELSE public_ip END,
                 geo=CASE WHEN $5<>'' THEN $5 ELSE geo END,
                 contract_down_mbps=$6, contract_up_mbps=$7 WHERE id=$1`,
			l.ID, l.Name, l.ISP, l.PublicIP, l.Geo, l.ContractDownMbps, l.ContractUpMbps)
		if err != nil {
			return 0, err
		}
		if ct.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
		nid = l.ID
	} else {
		err := tx.QueryRow(ctx, `
INSERT INTO lines(name, isp, public_ip, geo, contract_down_mbps, contract_up_mbps)
VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
			l.Name, l.ISP, l.PublicIP, l.Geo, l.ContractDownMbps, l.ContractUpMbps).Scan(&nid)
		if err != nil {
			return 0, err
		}
	}
	// 楼层关联多对多：整体替换（校验楼层存在，忽略非法 id）
	if _, err := tx.Exec(ctx, `DELETE FROM line_floors WHERE line_id=$1`, nid); err != nil {
		return 0, err
	}
	for _, fid := range floorIDs {
		if _, err := tx.Exec(ctx, `
INSERT INTO line_floors(line_id, floor_id) SELECT $1, f.id FROM floors f WHERE f.id=$2
ON CONFLICT DO NOTHING`, nid, fid); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return nid, nil
}

func (s *Store) DeleteLine(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM lines WHERE id=$1`, id)
	return err
}

// ---------- 告警 ----------

type AlertRow struct {
	ID          int64      `json:"id"`
	Fingerprint string     `json:"fingerprint"`
	Severity    string     `json:"severity"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	ProbeID     string     `json:"probe_id"`
	LineID      *int       `json:"line_id"`
	StartsAt    time.Time  `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	Status      string     `json:"status"`
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

// ListAlerts 告警中心分页列表：状态/级别/类型/时间窗过滤（时间窗以开始时间为准），
// 返回当页行与过滤后的总数。since 传零值 = 不限时间；参数空或 "all" = 该维度不过滤。
func (s *Store) ListAlerts(ctx context.Context, status, severity, kind string, since time.Time, limit, offset int) ([]AlertRow, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := ` WHERE 1=1`
	var args []any
	if status != "" && status != "all" {
		args = append(args, status)
		where += fmt.Sprintf(` AND status=$%d`, len(args))
	}
	if severity != "" && severity != "all" {
		args = append(args, severity)
		where += fmt.Sprintf(` AND severity=$%d`, len(args))
	}
	if kind != "" && kind != "all" {
		args = append(args, kind)
		where += fmt.Sprintf(` AND kind=$%d`, len(args))
	}
	if !since.IsZero() {
		args = append(args, since)
		where += fmt.Sprintf(` AND starts_at>=$%d`, len(args))
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM alerts`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, fingerprint, severity, kind, title, body, probe_id, line_id, starts_at, ends_at, status
FROM alerts` + where + ` ORDER BY starts_at DESC LIMIT ` + fmt.Sprintf("%d", limit) +
		` OFFSET ` + fmt.Sprintf("%d", offset)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AlertRow{}
	for rows.Next() {
		var r AlertRow
		if err := rows.Scan(&r.ID, &r.Fingerprint, &r.Severity, &r.Kind, &r.Title, &r.Body,
			&r.ProbeID, &r.LineID, &r.StartsAt, &r.EndsAt, &r.Status); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// ResolveAllFiring 中心重启后，将中断的告警 episode 收口（历史保留）。
func (s *Store) ResolveAllFiring(ctx context.Context) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE alerts SET status='resolved', ends_at=now() WHERE status='firing'`)
	return err
}

// ListAlertsRange 统计周期内触发的告警明细（报表导出用，按触发时间倒序，上限 limit）
func (s *Store) ListAlertsRange(ctx context.Context, start, end time.Time, limit int) ([]AlertRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, fingerprint, severity, kind, title, body, probe_id, line_id, starts_at, ends_at, status
FROM alerts WHERE starts_at >= $1 AND starts_at < $2
ORDER BY starts_at DESC LIMIT $3`, start, end, limit)
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

// CountAlertsRange 统计周期内告警按级别计数（报表汇总用，不受明细上限影响）
func (s *Store) CountAlertsRange(ctx context.Context, start, end time.Time) (map[string]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT severity, count(*) FROM alerts WHERE starts_at >= $1 AND starts_at < $2 GROUP BY severity`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			return nil, err
		}
		out[sev] = n
	}
	return out, rows.Err()
}

// ---------- 楼层 IP 扫描记录 ----------

// ScanRunRow 一次网段扫描（一个楼层一行，便于按网段查看）。
// Results 仅 GetScanRun 填充（列表接口不带逐 IP 明细）。
type ScanRunRow struct {
	ID         int64           `json:"id"`
	Batch      string          `json:"batch"` // 同一次手动/定时任务共用，用于进度卡归组
	FloorID    *int            `json:"floor_id"`
	FloorName  string          `json:"floor_name"`
	CIDR       string          `json:"cidr"`
	Status     string          `json:"status"` // pending/running/done/error/interrupted
	Up         int             `json:"up"`
	Down       int             `json:"down"`
	Total      int             `json:"total"`
	DurationMs int64           `json:"duration_ms"`
	Results    json.RawMessage `json:"results,omitempty"`
	Trigger    string          `json:"trigger"` // manual / schedule
	CreatedBy  string          `json:"created_by"`
	CreatedAt  time.Time       `json:"created_at"`
}

// 扫描记录保留策略：细节（results 明细）占用最大，定时扫描每小时每网段一行，
// 7 天约 2.4 千行/35MB；另设 3000 行硬上限防止调小间隔后膨胀。
const (
	scanRunKeepDays = 7
	scanRunMaxRows  = 3000
)

// CreateScanBatch 为一批扫描建记录：每楼层一行（pending），返回与 floors 同序的行 id。
// 顺手执行保留策略（超期/超量清理）。
func (s *Store) CreateScanBatch(ctx context.Context, batch string, floors []FloorRow, trigger, by string) ([]int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	ids := make([]int64, 0, len(floors))
	for _, f := range floors {
		var id int64
		err := tx.QueryRow(ctx, `
INSERT INTO scan_runs(batch, floor_id, floor_name, cidr, status, trigger, created_by)
VALUES($1,$2,$3,$4,'pending',$5,$6) RETURNING id`,
			batch, f.ID, f.Name, f.CIDR, trigger, by).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// 保留策略（失败仅记日志，不影响扫描）。make_interval 显式整型参数，
	// 原写法 `$1 || ' days'` 把 $1 推断成 text，pgx 传 int 编码失败且错误此前被吞（清理从未生效）
	s.pool.Exec(ctx, `DELETE FROM scan_runs WHERE created_at < now() - make_interval(days => $1)`,
		scanRunKeepDays)
	s.pool.Exec(ctx, `DELETE FROM scan_runs WHERE id NOT IN (SELECT id FROM scan_runs ORDER BY id DESC LIMIT $1)`,
		scanRunMaxRows)
	return ids, nil
}

// SetScanRunning 标记某个网段扫描开始。
func (s *Store) SetScanRunning(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE scan_runs SET status='running' WHERE id=$1`, id)
	return err
}

// FinishScanRun 单网段扫描收口：状态 + 计数 + 耗时 + 逐 IP 明细。
// 注意：down 必须由调用方算好后传参——写成 SQL 里的 `$4-$3` 时 PG 无法推断参数类型
// （unknown - unknown），报 42725 操作符不唯一。
func (s *Store) FinishScanRun(ctx context.Context, id int64, status string, up, total int, durMs int64, results []byte) error {
	down := total - up
	_, err := s.pool.Exec(ctx, `
UPDATE scan_runs SET status=$2, up=$3, down=$4, total=$5, duration_ms=$6, results=$7 WHERE id=$1`,
		id, status, up, down, total, durMs, results)
	return err
}

// ListScanRuns 扫描记录列表（不含 results 明细，按时间倒序，分页）。
// days 时间过滤：1=今天（当天零点起）、3/7=近 N 天（含今天，N-1 天前零点起）；其他值不过滤。
// start/end（unix 秒）同时 >0 时优先于 days（探测历史页自定义窗口用）。
// floorID>0 时按楼层过滤（floor_id 为空的"楼层已删除"记录不归入任何楼层，天然不被选中）。
// 返回第二个值为满足过滤条件的总条数（前端算页数用）。
// 顺手把 >15 分钟无进展的未完成行收口为中断（正常一批最长约 4 分钟）。
func (s *Store) ListScanRuns(ctx context.Context, trigger string, days, floorID, limit, offset int, start, end int64) ([]ScanRunRow, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	s.pool.Exec(ctx, `UPDATE scan_runs SET status='interrupted'
WHERE status IN ('pending','running') AND created_at < now() - interval '15 minutes'`)
	var conds []string
	var args []any
	if trigger == "manual" || trigger == "schedule" {
		args = append(args, trigger)
		conds = append(conds, fmt.Sprintf("trigger=$%d", len(args)))
	}
	if floorID > 0 {
		args = append(args, floorID)
		conds = append(conds, fmt.Sprintf("floor_id=$%d", len(args)))
	}
	if start > 0 && end > 0 { // 显式窗口优先于 days（探测历史页时间范围/自定义）
		args = append(args, time.Unix(start, 0), time.Unix(end, 0))
		conds = append(conds, fmt.Sprintf("created_at >= $%d AND created_at < $%d", len(args)-1, len(args)))
	}
	switch days {
	case 1:
		if !(start > 0 && end > 0) {
			conds = append(conds, `created_at >= date_trunc('day', now())`)
		}
	case 3, 7:
		if !(start > 0 && end > 0) {
			args = append(args, days-1)
			// make_interval 显式声明参数为 integer——写成 `$1 || ' days'` 时 PG 会把 $1 推断成 text，
			// pgx 传 int 直接编码失败（OID 25 cannot find encode plan）
			conds = append(conds, fmt.Sprintf("created_at >= date_trunc('day', now()) - make_interval(days => $%d)", len(args)))
		}
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM scan_runs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, batch, floor_id, floor_name, cidr, status, up, down, total, duration_ms, trigger, created_by, created_at
FROM scan_runs` + where + ` ORDER BY id DESC LIMIT ` + fmt.Sprintf("%d", limit) +
		` OFFSET ` + fmt.Sprintf("%d", offset)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []ScanRunRow{}
	for rows.Next() {
		var r ScanRunRow
		if err := rows.Scan(&r.ID, &r.Batch, &r.FloorID, &r.FloorName, &r.CIDR, &r.Status,
			&r.Up, &r.Down, &r.Total, &r.DurationMs, &r.Trigger, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// ScanBatchStat 一次扫描批次（同 batch 的全部网段）的在线汇总——探测历史 IP 扫描页签
// 的趋势/心跳条/汇总 KPI 数据源。
type ScanBatchStat struct {
	T     int64 `json:"t"`     // 批次时间（批内最早 created_at，unix 秒）
	Up    int   `json:"up"`    // 在线 IP 数（批内网段合计）
	Total int   `json:"total"` // 扫描 IP 数（批内网段合计）
	Runs  int   `json:"runs"`  // 批内网段记录数
}

// ScanSummary 按批次聚合窗口内的成功扫描（KPI 汇总与在线趋势图用）。
// floorID>0 时只统计该楼层。批次数钳位 ≤2000（按时间倒序取最近，再反转为升序输出）。
func (s *Store) ScanSummary(ctx context.Context, floorID int, start, end int64) ([]ScanBatchStat, error) {
	conds := []string{"status='done'"}
	var args []any
	if floorID > 0 {
		args = append(args, floorID)
		conds = append(conds, fmt.Sprintf("floor_id=$%d", len(args)))
	}
	if start > 0 && end > 0 {
		args = append(args, time.Unix(start, 0), time.Unix(end, 0))
		conds = append(conds, fmt.Sprintf("created_at >= $%d AND created_at < $%d", len(args)-1, len(args)))
	}
	where := " WHERE " + strings.Join(conds, " AND ")
	rows, err := s.pool.Query(ctx, `
SELECT EXTRACT(EPOCH FROM MIN(created_at))::bigint, COALESCE(SUM(up),0), COALESCE(SUM(total),0), COUNT(*)
FROM scan_runs`+where+` GROUP BY batch ORDER BY 1 DESC LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScanBatchStat{}
	for rows.Next() {
		var b ScanBatchStat
		if err := rows.Scan(&b.T, &b.Up, &b.Total, &b.Runs); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	// DESC 取最近 2000 批，反转为时间升序（图表 x 轴）
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// GetScanRun 单条扫描记录（含逐 IP 明细，行展开时按需拉取）。
func (s *Store) GetScanRun(ctx context.Context, id int64) (*ScanRunRow, error) {
	var r ScanRunRow
	err := s.pool.QueryRow(ctx, `
SELECT id, batch, floor_id, floor_name, cidr, status, up, down, total, duration_ms,
       COALESCE(results,'[]'::jsonb), trigger, created_by, created_at
FROM scan_runs WHERE id=$1`, id).
		Scan(&r.ID, &r.Batch, &r.FloorID, &r.FloorName, &r.CIDR, &r.Status, &r.Up, &r.Down,
			&r.Total, &r.DurationMs, &r.Results, &r.Trigger, &r.CreatedBy, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// FloorScanSummary 楼层最近一次成功扫描的摘要（线路在线 IP 统计用）。
type FloorScanSummary struct {
	Up    int
	Total int
	At    time.Time
}

// LatestFloorScans 各楼层最近一次成功扫描（按楼层取最新一行）。
func (s *Store) LatestFloorScans(ctx context.Context) (map[int]FloorScanSummary, error) {
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT ON (floor_id) floor_id, up, total, created_at
FROM scan_runs WHERE status='done' AND floor_id IS NOT NULL
ORDER BY floor_id, created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]FloorScanSummary{}
	for rows.Next() {
		var fid int
		var sm FloorScanSummary
		if err := rows.Scan(&fid, &sm.Up, &sm.Total, &sm.At); err != nil {
			return nil, err
		}
		out[fid] = sm
	}
	return out, rows.Err()
}

// LastScheduledScanAt 最近一次定时扫描的发起时间（无记录返回零值），调度器按它判断是否到点。
func (s *Store) LastScheduledScanAt(ctx context.Context) (time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx, `
SELECT max(created_at) FROM scan_runs WHERE trigger='schedule'`).Scan(&t)
	if err != nil || t == nil {
		return time.Time{}, err
	}
	return *t, nil
}
