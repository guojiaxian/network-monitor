package server

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/go-chi/chi/v5"
)

// 网页终端（阿里云同款架构）：浏览器 xterm.js ↔ WebSocket ↔ SSH PTY。
// 前端组件：xterm.js（MIT，VS Code 同款终端模拟器）；本文件为服务端桥接。

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// 会话以 Cookie 认证（同源），不依赖 Origin 校验
	CheckOrigin: func(r *http.Request) bool { return true },
}

// sshTerminal 升级为 WebSocket 并桥接到探针的交互式 shell。
// 协议：二进制帧 = 终端数据双向；文本帧 = 控制消息 {"cols":N,"rows":M}（窗口尺寸）。
func (a *AdminAPI) sshTerminal(w http.ResponseWriter, r *http.Request) {
	// WS 握手是 GET：显式校验管理员（观察者不得开终端）
	sess, _ := r.Context().Value(ctxUserKey).(nwSession)
	if sess.role != "admin" {
		httpError(w, 403, "仅管理员可使用网页终端")
		return
	}
	id := chi.URLParam(r, "id")
	host, port, user, pass, err := a.probeSSH(r.Context(), id)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}

	cfg := &ssh.ClientConfig{
		User: user, Auth: []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second,
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(host, itoa(port)), cfg)
	if err != nil {
		httpError(w, 502, "SSH 连接失败: "+err.Error())
		return
	}
	defer client.Close()
	sessSSH, err := client.NewSession()
	if err != nil {
		httpError(w, 502, "会话创建失败: "+err.Error())
		return
	}
	defer sessSSH.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 115200,
		ssh.TTY_OP_OSPEED: 115200,
	}
	if err := sessSSH.RequestPty("xterm-256color", 80, 24, modes); err != nil {
		httpError(w, 502, "PTY 申请失败: "+err.Error())
		return
	}
	stdin, err := sessSSH.StdinPipe()
	if err != nil {
		httpError(w, 502, err.Error())
		return
	}
	stdout, err := sessSSH.StdoutPipe()
	if err != nil {
		httpError(w, 502, err.Error())
		return
	}
	if err := sessSSH.Shell(); err != nil {
		httpError(w, 502, "Shell 启动失败: "+err.Error())
		return
	}
	a.store.WriteAudit(r.Context(), sess.user, "ssh-term:"+id[:min(8, len(id))],
		host+":"+itoa(port))

	ws, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	// stdout/stderr → WebSocket
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				_ = ws.WriteMessage(websocket.BinaryMessage, buf[:n])
			}
			if err != nil {
				_ = ws.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session closed"))
				return
			}
		}
	}()

	// WebSocket → stdin / resize
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			_ = sessSSH.Close()
			return
		}
		switch mt {
		case websocket.BinaryMessage, websocket.TextMessage:
			var resize struct {
				Cols int `json:"cols"`
				Rows int `json:"rows"`
			}
			if mt == websocket.TextMessage && json.Unmarshal(data, &resize) == nil && resize.Cols > 0 && resize.Rows > 0 {
				_ = sessSSH.WindowChange(resize.Rows, resize.Cols)
				continue
			}
			_, _ = stdin.Write(data)
		}
	}
}
