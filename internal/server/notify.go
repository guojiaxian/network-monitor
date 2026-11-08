package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 钉钉/企业微信群机器人推送。均使用官方 webhook 协议，无第三方依赖。

var notifyHTTP = &http.Client{Timeout: 10 * time.Second}

// SendDingtalk 推送钉钉群机器人消息；secret 非空时使用加签（HMAC-SHA256）。
func SendDingtalk(webhook, secret, title, markdown string) error {
	if webhook == "" {
		return fmt.Errorf("钉钉 webhook 未配置")
	}
	if secret != "" {
		ts := time.Now().UnixMilli()
		stringToSign := fmt.Sprintf("%d\n%s", ts, secret)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(stringToSign))
		sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		sep := "?"
		if strings.Contains(webhook, "?") {
			sep = "&"
		}
		webhook = fmt.Sprintf("%s%stimestamp=%d&sign=%s", webhook, sep, ts, sign)
	}
	body := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": title,
			"text":  markdown,
		},
	}
	return postJSON(webhook, body)
}

// SendWecom 推送企业微信群机器人消息（markdown 内容 ≤4096 字节）。
func SendWecom(webhook, markdown string) error {
	if webhook == "" {
		return fmt.Errorf("企业微信 webhook 未配置")
	}
	if len(markdown) > 4000 {
		markdown = markdown[:4000]
	}
	body := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": markdown},
	}
	return postJSON(webhook, body)
}

func postJSON(url string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := notifyHTTP.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err == nil && out.ErrCode != 0 {
		return fmt.Errorf("errcode=%d %s", out.ErrCode, out.ErrMsg)
	}
	return nil
}

// alertMarkdown 告警/恢复的 IM 消息文本
func alertMarkdown(meta AlertMeta, firing bool) string {
	head := "🟢 已恢复"
	if firing {
		switch meta.Severity {
		case "P0":
			head = "🟣 **P0 监控盲区**"
		case "P1":
			head = "🔴 **P1 告警**"
		default:
			head = "🟡 **P2 告警**"
		}
	}
	return fmt.Sprintf("%s\n\n**%s**\n\n线路：%s　楼层：%s\n\n出口 IP：%s\n\n开始时间：%s\n\n—— 物探中心网络监控系统（NetWatch）",
		head, meta.Title, meta.LineName, meta.Floor, meta.PublicIP,
		meta.StartsAt.Format("2006-01-02 15:04:05"))
}
