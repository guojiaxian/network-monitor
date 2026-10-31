// sshmockd 测试用 SSH 服务器（密码认证 + exec 回显），仅用于验证 NetWatch 远程运维链路。
// 用法：sshmockd <端口> <用户> <密码>
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
)

func main() {
	port, user, pass := "2222", "root", "p@ss1234"
	if len(os.Args) > 3 {
		port, user, pass = os.Args[1], os.Args[2], os.Args[3]
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, _ := ssh.NewSignerFromKey(key)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(pw) == pass {
				return nil, nil
			}
			return nil, fmt.Errorf("auth failed")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(1)
	}
	fmt.Println("sshmockd on 127.0.0.1:" + port)
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
				cc, creqs, _ := ch.Accept()
				go handleRequests(cc, creqs)
			}
			_ = sconn.Close()
		}(conn)
	}
}

// handleRequests 支持 exec 与 PTY+shell（模拟交互 shell）
func handleRequests(cc ssh.Channel, creqs <-chan *ssh.Request) {
	for req := range creqs {
		switch req.Type {
		case "pty-req", "window-change":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "shell":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			go fakeShell(cc)
			return
		case "exec":
			var payload struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &payload)
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			fmt.Fprintf(cc, "mock-ok: %s\n", payload.Command)
			_, _ = cc.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			_ = cc.Close()
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// fakeShell 极简交互 shell：提示符 + 按行应答
func fakeShell(cc ssh.Channel) {
	fmt.Fprint(cc, "\r\nmock-shell ready（测试用）\r\n$ ")
	buf := make([]byte, 1)
	line := make([]byte, 0, 256)
	for {
		n, err := cc.Read(buf)
		if err != nil || n == 0 {
			return
		}
		b := buf[0]
		if b == '\r' || b == '\n' {
			cmd := string(line)
			line = line[:0]
			switch {
			case cmd == "":
			case cmd == "echo hello":
				fmt.Fprint(cc, "\r\nhello\r\n")
			case cmd == "whoami":
				fmt.Fprint(cc, "\r\nroot\r\n")
			case cmd == "exit":
				fmt.Fprint(cc, "\r\nlogout\r\n")
				_, _ = cc.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				_ = cc.Close()
				return
			default:
				fmt.Fprintf(cc, "\r\nunknown command: %s\r\n", cmd)
			}
			fmt.Fprint(cc, "$ ")
		} else if b == 0x7F {
			if len(line) > 0 {
				line = line[:len(line)-1]
			}
		} else {
			line = append(line, b)
		}
	}
}
