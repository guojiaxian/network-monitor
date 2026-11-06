// Package server 实现中心端：探针接入、指标入库、告警引擎、邮件通知与管理 API。
package server

import "os"

// Config 中心端运行配置（环境变量注入；容器内由 docker-compose 提供）
type Config struct {
	Listen      string // HTTP 监听地址
	DBDSN       string // PostgreSQL 连接串
	VMURL       string // VictoriaMetrics 地址（/api/v1/import/prometheus）
	EnrollToken string // 探针注册令牌（settings 可覆盖）
	BaseURL     string // 对外访问基地址（告警邮件里的详情链接）
	WebDir      string // 前端静态文件目录（不存在则不挂载）
	DataDir     string // 数据目录（探针版本发布文件等）
	SSHPubKeyFile string // 中心 SSH 公钥文件（零接触开通时下发到探针，实现免密；空=跳过）
	Version       string // server 版本（main 注入，health API 返回）
}

func LoadConfig() Config {
	return Config{
		Listen:      envOr("NW_LISTEN", ":8080"),
		DBDSN:       envOr("NW_DB", "postgres://netwatch:netwatch@localhost:5432/netwatch?sslmode=disable"),
		VMURL:       envOr("NW_VM_URL", "http://localhost:8428"),
		EnrollToken: envOr("NW_ENROLL_TOKEN", "netwatch-enroll-change-me"),
		BaseURL:     envOr("NW_BASE_URL", ""),
		WebDir:      envOr("NW_WEB_DIR", "./web/dist"),
		DataDir:     envOr("NW_DATA_DIR", "./data"),
		SSHPubKeyFile: envOr("NW_SSH_PUBKEY_FILE", ""),
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
