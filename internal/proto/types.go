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
	AgentID   string  `json:"agent_id"`
	Version   string  `json:"version"`
	UptimeS   float64 `json:"uptime_s"`
	MemUsedMB float64 `json:"mem_used_mb"`
	Load1     float64 `json:"load1"`
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
	SkipGateway   bool     `json:"skip_gateway"` // 外网组探针：跳过网关探测（无内网故障域概念）
	SpeedTask     *SpeedTask `json:"speed_task,omitempty"` // 测速任务（中心端串行调度，非空表示该探针需在 RunAt 执行一次）
	Upgrade       *UpgradeTask `json:"upgrade,omitempty"`  // 升级任务（非空表示需升级到指定版本）
}

// UpgradeTask 探针自更新任务
type UpgradeTask struct {
	ReleaseID int64  `json:"release_id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
}

// SpeedTask 中心端下发的单次测速任务（全局同一时刻只有一个探针在测速）
type SpeedTask struct {
	Engine   string   `json:"engine"`               // ookla / http
	RunAt    int64    `json:"run_at"`               // 执行时间（unix 秒）
	HTTPURLs []string `json:"http_urls,omitempty"`  // http 引擎的下载源池
}

// RegisterReq 探针首次注册请求
type RegisterReq struct {
	EnrollToken string `json:"enroll_token"`
	Name        string `json:"name"`    // 探针名称（部署时安装脚本传入，如"探针1"）
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	AgentVer    string `json:"agent_version"`
	SourceIP    string `json:"source_ip"`
	Gateway     string `json:"gateway"`
}

// RegisterResp 注册应答（携带身份凭证与初始配置）
type RegisterResp struct {
	AgentID string      `json:"agent_id"`
	Secret  string      `json:"secret"`
	Config  ProbeConfig `json:"config"`
}
