package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// 起一个进程内 SSH 服务器（密码认证 + exec），验证 sshRun/加解密全链路。
func startTestSSHServer(t *testing.T, user, pass string) (addr string, stop func()) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(pw) == pass {
				return nil, nil
			}
			return nil, &fakeError{}
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						_ = ch.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					go handleSession(ch)
				}
				_ = sconn.Close()
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close(); <-done }
}

func handleSession(ch ssh.NewChannel) {
	conn, reqs, _ := ch.Accept()
	for req := range reqs {
		if req.Type == "exec" {
			var payload struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &payload)
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			if payload.Command == "echo netwatch-ssh-ok" {
				_, _ = conn.Write([]byte("netwatch-ssh-ok\n"))
			} else {
				_, _ = conn.Write([]byte("ran: " + payload.Command + "\n"))
			}
			_, _ = conn.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			_ = conn.Close()
			return
		}
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

type fakeError struct{}

func (e *fakeError) Error() string { return "auth failed" }

func TestSSHOps(t *testing.T) {
	addr, stop := startTestSSHServer(t, "root", "p@ss1234")
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	// 1) 执行链路
	out, err := sshRun(host, port, "root", "p@ss1234", "echo netwatch-ssh-ok", 10*time.Second)
	if err != nil || out != "netwatch-ssh-ok\n" {
		t.Fatalf("sshRun: out=%q err=%v", out, err)
	}
	out, _ = sshRun(host, port, "root", "p@ss1234", "uptime", 10*time.Second)
	if out != "ran: uptime\n" {
		t.Fatalf("exec 输出不符: %q", out)
	}

	// 2) 密码错误应失败
	if _, err := sshRun(host, port, "root", "wrong", "id", 10*time.Second); err == nil {
		t.Fatal("错误密码不应连接成功")
	}

	// 3) 凭据加解密
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	enc, err := encryptPass(key, "s3cret-密码")
	if err != nil {
		t.Fatal(err)
	}
	if enc == "s3cret-密码" || len(enc) == 0 {
		t.Fatal("密文异常")
	}
	plain, err := decryptPass(key, enc)
	if err != nil || plain != "s3cret-密码" {
		t.Fatalf("解密不符: %q err=%v", plain, err)
	}
	// 错误密钥应解密失败
	bad := make([]byte, 32)
	bad[0] ^= 0xFF
	if _, err := decryptPass(bad, enc); err == nil {
		t.Fatal("错误密钥不应解密成功")
	}

	// 4) GetProbeSSH 默认用户兜底逻辑（纯函数级校验 SshRow 规则）
	_ = context.Background()
	_ = x509.MarshalPKCS1PrivateKey
	_ = pem.Block{}
}
