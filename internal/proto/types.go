// Package proto 定义探针（agent）与中心端（server）之间的线上协议结构。
// 两端共享本包，字段变更需同步升级两端；探针通过配置版本号感知变更。
package proto

// 探测类型常量
const (
	TypePing  = "ping"  // ICMP，target 为 IP；网关探测在 Extra 中带 gw=1
	TypeTCP   = "tcp"   // TCP connect，target 为 host:port
	TypeDNS   = "dns"   // DNS 解析，target 为 DNS 服务器地址
	TypeHTTP  = "http"  // HTTP GET，target 为 URL
	TypeSpeed = "speed" // 测速（二期）
)

// Sample 单次探测结果
type Sample struct {
	Type    string             `json:"type"`
	Target  string             `json:"target"`
	TS      int64              `json:"ts_ms"` // 采样时间，UnixMilli
	OK      bool               `json:"ok"`
	RTTms   float64            `json:"rtt_ms,omitempty"`
	LossPct float64            `json:"loss_pct,omitempty"` // ping 轮次丢包率（0/100）
	Extra   map[string]float64 `json:"extra,omitempty"`    // 附加指标：gw=1、code=200 等
}

// MetricsBatch 探测结果批量上报
type MetricsBatch struct {
	AgentID string   `json:"agent_id"`
	Samples []Sample `json:"samples"`
}

// Heartbeat 探针心跳（同时驱动"监控盲区"判定）
type Heartbeat struct {
	AgentID   string            `json:"agent_id"`
	Version   string            `json:"version"`
	UptimeS   float64           `json:"uptime_s"`
	MemUsedMB float64           `json:"mem_used_mb"`
	Load1     float64           `json:"load1"`
	PublicIP  string            `json:"public_ip,omitempty"` // 探测到的出口公网 IP（绑定线路时自动回填 lines.public_ip）
	IPISP     string            `json:"ip_isp,omitempty"`    // 出口 IP 归属运营商（自动回填 lines.isp）
	IPGeo     string            `json:"ip_geo,omitempty"`    // 出口 IP 归属地（自动回填 lines.geo）
	IPv6      string            `json:"ipv6,omitempty"`      // 出口 IPv6 地址（无双栈为空）
	CPUPct    float64           `json:"cpu_pct,omitempty"`   // 探针自健康：CPU 使用率 %
	MemPct    float64           `json:"mem_pct,omitempty"`   // 内存使用率 %
	DiskPct   float64           `json:"disk_pct,omitempty"`  // 身份目录所在分区使用率 %
	AgentTS   int64             `json:"agent_ts,omitempty"`  // 探针发送时刻 UnixMilli（服务器算时钟偏移）
	SysInfo   map[string]string `json:"sysinfo,omitempty"`   // 资产信息（每周期首采，心跳补报——不重注册也能刷新）
}

// ProbeConfig 中心端下发的探测配置（版本号递增生效）
type ProbeConfig struct {
	Version       int      `json:"version"`
	IntervalPingS int      `json:"interval_ping_s"`
	IntervalTCPS  int      `json:"interval_tcp_s"`
	IntervalDNSS  int      `json:"interval_dns_s"`
	IntervalHTTPS int      `json:"interval_http_s"`
	TimeoutS      int      `json:"timeout_s"`
	ICMPTargets   []string `json:"icmp_targets"` // 公网 ICMP 目标（IP）
	TCPTargets    []string `json:"tcp_targets"`  // host:port
	DNSServers    []string `json:"dns_servers"`  // DNS 服务器
	DNSDomain     string   `json:"dns_domain"`   // 解析测试用域名
	HTTPTargets   []string `json:"http_targets"` // URL
	SourceIP      string   `json:"source_ip"`    // auto = 自动探测出口源 IP
	IPEchoURLs    []string `json:"ip_echo_urls"` // 出口公网 IP 回显服务地址池（空=用探针内置默认）
	// 以下 4 字段只增兼容（2026-11-08）：旧中心缺省=0，探针按 <=0 回退内置默认
	RetryN       int          `json:"retry_n,omitempty"`        // 每轮探测失败后的重试次数（默认 1）
	RetryPauseMs int          `json:"retry_pause_ms,omitempty"` // 重试前等待毫秒（默认 300）
	HeartbeatSec int          `json:"heartbeat_sec,omitempty"`  // 心跳间隔秒（默认 15）
	EgressMin    int          `json:"egress_min,omitempty"`     // 出口 IP 识别间隔分钟（默认 5）
	SpeedTask    *SpeedTask   `json:"speed_task,omitempty"`     // 测速任务（每探针独立并行调度，非空表示该探针需在 RunAt 执行一轮）
	Upgrade      *UpgradeTask `json:"upgrade,omitempty"`        // 升级任务（非空表示需升级到指定版本）
}

// UpgradeTask 探针自更新任务
type UpgradeTask struct {
	ReleaseID int64  `json:"release_id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
}

// SpeedTask 中心端下发的一轮测速任务（每探针独立并行，多探针可同时各测各的线路）
type SpeedTask struct {
	Engine   string   `json:"engine"`              // ookla / http
	RunAt    int64    `json:"run_at"`              // 执行时间（unix 秒）
	Times    int      `json:"times,omitempty"`     // 一轮连测次数取平均（旧中心=0 时按 1 次执行，字段只增兼容）
	HTTPURLs []string `json:"http_urls,omitempty"` // http 引擎的下载源池
}

// RegisterReq 探针首次注册请求
type RegisterReq struct {
	EnrollToken string            `json:"enroll_token"`
	Name        string            `json:"name"` // 探针名称（部署时安装脚本传入，如"探针1"）
	Hostname    string            `json:"hostname"`
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	AgentVer    string            `json:"agent_version"`
	SourceIP    string            `json:"source_ip"`
	Gateway     string            `json:"gateway"`
	SysInfo     map[string]string `json:"sysinfo,omitempty"` // 资产信息（CPU/内存/磁盘/内核/虚拟化/MAC，注册时一次采集）
}

// RegisterResp 注册应答（携带身份凭证与初始配置）
type RegisterResp struct {
	AgentID string      `json:"agent_id"`
	Secret  string      `json:"secret"`
	Config  ProbeConfig `json:"config"`
}
