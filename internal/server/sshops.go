package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

// 探针远程运维（SSH 通道）：
// 目标是探针 VM 装机后运维不再逐台登录——名称/SSH 凭据在后台维护（密码 AES-256-GCM 加密落库），
// 中心端提供测试连接、远程重启探针服务、远程执行命令（全程审计）。
// SSH 主要作为救援通道（探针 agent 挂掉时自更新不可用，靠 SSH 拉起）。

// sshMasterKey 加载/生成 SSH 凭据加密主密钥（DataDir/ssh-master.key，0600）。
func (a *AdminAPI) sshMasterKey() ([]byte, error) {
	path := filepath.Join(a.cfg.DataDir, "ssh-master.key")
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func encryptPass(key []byte, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func decryptPass(key []byte, enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// sshRun 在探针上以密码认证执行命令，返回合并输出。
func sshRun(host string, port int, user, pass, cmd string, timeout time.Duration) (string, error) {
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 内网运维通道，不做主机指纹校验
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return "", fmt.Errorf("连接失败: %v", err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("会话失败: %v", err)
	}
	defer sess.Close()

	done := make(chan struct{})
	var output []byte
	go func() { output, _ = sess.CombinedOutput(cmd); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = sess.Signal(ssh.SIGKILL)
		return string(output) + "\n[命令超时被终止]", nil
	}
	return string(output), nil
}

// probeSSH 解析某探针的 SSH 连接参数
func (a *AdminAPI) probeSSH(ctx context.Context, probeID string) (host string, port int, user, pass string, err error) {
	var p SshRow
	p, err = a.store.GetProbeSSH(ctx, probeID)
	if err != nil {
		return
	}
	if p.Host == "" {
		err = errors.New("该探针未配置 SSH（在探针管理中填写管理 IP 与密码）")
		return
	}
	key, kerr := a.sshMasterKey()
	if kerr != nil {
		err = kerr
		return
	}
	pass, err = decryptPass(key, p.PassEnc)
	return p.Host, p.Port, p.User, pass, err
}
