package server

// 一次性实弹测试：用真实 SMTP 凭据发送一封 Kuma 格式样例告警邮件。
// 仅在 NW_LIVE_SMTP=1 时运行，凭据全部来自环境变量；跑完即删本文件。

import (
	"os"
	"testing"
	"time"
)

func TestLiveSMTPSend(t *testing.T) {
	if os.Getenv("NW_LIVE_SMTP") != "1" {
		t.Skip("live smtp test disabled")
	}
	cfg := SMTPSettings{
		Host:     os.Getenv("NW_SMTP_HOST"),
		Port:     25,
		Username: os.Getenv("NW_SMTP_USER"),
		Password: os.Getenv("NW_SMTP_PASS"),
		From:     os.Getenv("NW_SMTP_FROM"),
		To:       []string{os.Getenv("NW_SMTP_TO")},
	}
	meta := mkMailMeta(KindLineDown, "联通线路2：宽带线路中断", "联通线路2")
	meta.StartsAt = time.Now()
	meta.ExtraHTML = ""
	subject := mailSubject(meta, true)
	if err := SendMail(cfg, subject, alertEmailHTML(&Config{}, meta, true, "")); err != nil {
		t.Fatalf("send: %v", err)
	}
	t.Logf("sent: %s", subject)
}
