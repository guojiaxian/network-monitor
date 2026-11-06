package server

// 探针零接触开通：管理员在后台输入全新探针服务器的 IP/账号/密码（密码只输一次），
// 中心自动完成：SSH 免密配置（公钥下发）→ 探针二进制分发 → systemd 服务安装 →
// 等待注册 → 等待出口公网 IP 探测 → 按出口 IP 匹配宽带线路并绑定（无匹配线路时
// 自动创建，并按楼层 CIDR 自动归属楼层）。
// 匹配规则：一条线路只有一个公网 IP、一个探针只有一个出口 IP → 探针与线路一一对应；
// 同一线路可挂多台探针（交叉验证），一个楼层可有多条线路/多台探针。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
)

// ProvisionJob 一次探针开通任务（内存态；中心重启后任务丢失，重试即可）
type ProvisionJob struct {
	ID          string   `json:"id"`
	IP          string   `json:"ip"`
	Name        string   `json:"name"`
	Status      string   `json:"status"` // running / done / failed
	Steps       []string `json:"steps"`  // 进度日志（带时间戳）
	Err         string   `json:"err,omitempty"`
	ProbeID     string   `json:"probe_id,omitempty"`
	EgressIP    string   `json:"egress_ip,omitempty"`
	LineID      int      `json:"line_id,omitempty"`
	LineName    string   `json:"line_name,omitempty"`
	FloorName   string   `json:"floor_name,omitempty"`
	AutoCreated bool     `json:"auto_created,omitempty"`
	ExpectedLineID int  `json:"expected_line_id,omitempty"` // 管理员预期的线路（装机时校验策略路由接线）
	Warnings    []string `json:"warnings,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (j *ProvisionJob) step(format string, args ...any) {
	j.Steps = append(j.Steps, time.Now().Format("15:04:05")+"  "+fmt.Sprintf(format, args...))
}

// warn 记录警告：进进度日志（⚠ 前缀）+ 独立警告列表（前端醒目展示）。
// 典型场景：出口 IP 与预期线路不符=策略路由接线错误，装机当场发现。
func (j *ProvisionJob) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	j.Steps = append(j.Steps, time.Now().Format("15:04:05")+"  ⚠ "+msg)
	j.Warnings = append(j.Warnings, msg)
}

// provisionStart 创建并异步执行开通任务
func (a *AdminAPI) provisionStart(ip string, port int, user, pass, name string, expectedLineID int) (*ProvisionJob, error) {
	a.provMu.Lock()
	defer a.provMu.Unlock()
	// 同 IP 并发/重复开通防护（含已在跑的任务）
	for _, j := range a.provJobs {
		if j.IP == ip && j.Status == "running" {
			return nil, fmt.Errorf("该 IP 已有进行中的开通任务（%s）", j.ID)
		}
	}
	// 已注册探针占用该 IP 时拒绝（避免重复装机产生孤儿）
	probes, err := a.store.ListProbes(context.Background())
	if err == nil {
		for _, p := range probes {
			if p.SourceIP == ip {
				return nil, fmt.Errorf("该 IP 已有探针注册（%s），如需重装请先在探针管理中删除", probeDisplay(p.ID, LineMeta{ProbeName: p.Name, ProbeHostname: p.Hostname}))
			}
		}
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	job := &ProvisionJob{ID: "prov-" + hex.EncodeToString(b), IP: ip, Name: name, Status: "running", CreatedAt: time.Now()}
	a.provJobs[job.ID] = job
	// 只保留最近 20 个任务
	ids := make([]string, 0, len(a.provJobs))
	for id := range a.provJobs {
		ids = append(ids, id)
	}
	sort.Strings(ids) // id 含时间随机，够用；上限裁剪防泄漏
	for len(ids) > 20 {
		delete(a.provJobs, ids[0])
		ids = ids[1:]
	}

	job.ExpectedLineID = expectedLineID
	go a.provisionRun(job, port, user, pass)
	return job, nil
}

// provisionRun 执行开通全流程（后台 goroutine；进度写 job.Steps）
func (a *AdminAPI) provisionRun(job *ProvisionJob, port int, user, pass string) {
	ctx := context.Background()
	fail := func(format string, args ...any) {
		job.Status = "failed"
		job.Err = fmt.Sprintf(format, args...)
		job.step("❌ 失败：%s", job.Err)
		a.store.WriteAudit(ctx, "system", "provision-fail:"+job.IP, job.Err)
	}

	// 0. 探针名称默认值：probe-<IP 尾段>（英文+数字主机名，符合命名规范）
	name := job.Name
	if name == "" {
		lastOctet := job.IP[strings.LastIndex(job.IP, ".")+1:]
		name = "probe-" + lastOctet
	}
	safe := safeHostname(name)
	job.step("目标 %s（用户 %s）→ 名称 %s / 主机名 %s", job.IP, user, name, safe)

	// 1. SSH 连通性（密码认证）
	job.step("测试 SSH 连接…")
	if out, err := sshRun(job.IP, port, user, pass, "echo ok && uname -m", 15*time.Second); err != nil {
		fail("SSH 连接失败：%v", err)
		return
	} else if arch := strings.TrimSpace(out); strings.Contains(arch, "aarch64") {
		fail("探针机为 arm64 架构，当前交付二进制为 amd64，请先构建 arm64 版本")
		return
	}

	// 2. 中心公钥下发（免密 SSH；NW_SSH_PUBKEY_FILE 未配置时跳过，密码仍会加密留存供后台运维）
	pub := a.sshCenterPubKey()
	if pub != "" {
		job.step("下发中心公钥（免密 SSH）…")
		cmd := fmt.Sprintf(`mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && grep -qxF '%s' ~/.ssh/authorized_keys || echo '%s' >> ~/.ssh/authorized_keys`, pub, pub)
		if _, err := sshRun(job.IP, port, user, pass, cmd, 15*time.Second); err != nil {
			job.step("⚠ 公钥下发失败（%v），继续用密码完成安装", err)
		} else {
			job.step("✓ 免密 SSH 已配置（中心 root 可直接 ssh 本机）")
		}
	} else {
		job.step("⚠ 未配置 NW_SSH_PUBKEY_FILE，跳过免密配置（后台远程运维仍可用，凭据已加密留存）")
	}

	// 3. 主机名
	job.step("设置 OS 主机名 %s…", safe)
	if _, err := sshRun(job.IP, port, user, pass, "hostnamectl set-hostname "+safe, 15*time.Second); err != nil {
		job.step("⚠ 主机名设置失败（%v），继续", err)
	}

	// 4. 上传探针二进制（cat > 经 SSH stdin，免 SFTP 依赖）
	bin, err := agentBinaryPath()
	if err != nil {
		fail("%v", err)
		return
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		fail("读取探针二进制失败: %v", err)
		return
	}
	job.step("上传探针二进制（%d MB）…", len(data)>>20)
	if err := sshUpload(job.IP, port, user, pass, "/tmp/.nw-agent", data, 120*time.Second); err != nil {
		fail("二进制上传失败: %v", err)
		return
	}
	// 上传完整性校验（实测踩过静默装到旧构建/传输损坏的坑：校验不过立即失败，绝不带病安装）
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	out, err := sshRun(job.IP, port, user, pass, "sha256sum /tmp/.nw-agent", 30*time.Second)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), want) {
		fail("二进制上传校验失败（远端 sha256 不匹配，want %s…）——请重试", want[:12])
		return
	}
	job.step("✓ 二进制校验一致（sha256 %s…）", want[:12])
	if _, err := sshRun(job.IP, port, user, pass, "install -m 0755 /tmp/.nw-agent /usr/local/bin/netwatch-agent && rm -f /tmp/.nw-agent && mkdir -p /var/lib/netwatch-agent", 15*time.Second); err != nil {
		fail("安装二进制失败: %v", err)
		return
	}

	// 5. 写 systemd 服务并启动
	token := a.enrollTokenSetting()
	serverURL := a.centerSelfURL()
	unit := fmt.Sprintf(`[Unit]
Description=NetWatch network probe agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/netwatch-agent -server %s -token %s -name "%s"
Restart=always
RestartSec=5
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/netwatch-agent /usr/local/bin
PrivateTmp=yes

[Install]
WantedBy=multi-user.target`, serverURL, token, name)
	job.step("安装 systemd 服务并启动…")
	cmd := fmt.Sprintf(`cat > /etc/systemd/system/netwatch-agent.service <<'NWUNIT'
%s
NWUNIT
systemctl daemon-reload && systemctl enable --now netwatch-agent`, unit)
	if _, err := sshRun(job.IP, port, user, pass, cmd, 30*time.Second); err != nil {
		fail("服务安装失败: %v", err)
		return
	}

	// 6. 等待注册（按源 IP 匹配新探针行）
	job.step("等待探针注册（≤60s）…")
	var probeID string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		ps, err := a.store.ListProbes(ctx)
		if err != nil {
			continue
		}
		for _, p := range ps {
			if p.SourceIP == job.IP && p.LastSeen != nil && time.Since(*p.LastSeen) < 2*time.Minute {
				probeID = p.ID
			}
		}
		if probeID != "" {
			break
		}
	}
	if probeID == "" {
		fail("60s 内未收到探针注册（检查探针到中心 %s 的网络与注册令牌）", serverURL)
		return
	}
	job.ProbeID = probeID
	job.step("✓ 注册成功（agent %s）", probeID[:8])

	// 7. 写入 SSH 运维凭据（密码 AES 加密留存，供后台网页终端/远程运维）+ 名称兜底
	if key, err := a.sshMasterKey(); err == nil {
		if enc, err := encryptPass(key, pass); err == nil {
			_ = a.store.UpdateProbeMeta(ctx, probeID, &name, &job.IP, &port, &user, &enc)
		}
	} else {
		job.step("⚠ SSH 凭据加密存储失败（后台远程运维不可用，可稍后手工补填）")
	}

	// 8. 等待出口公网 IP 自动探测（探针启动即探，≤90s）
	job.step("等待出口公网 IP 自动探测（≤90s）…")
	deadline = time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		if st, ok := a.eng.Snapshot()[probeID]; ok && st.EgressIP != "" {
			job.EgressIP = st.EgressIP
			break
		}
	}
	if job.EgressIP == "" {
		fail("90s 内未探测到出口公网 IP（检查探针出网与 IP 回显服务可达性）")
		return
	}
	eg := a.eng.Snapshot()[probeID]
	job.step("✓ 出口公网 IP：%s（%s %s）", eg.EgressIP, eg.EgressISP, eg.EgressGeo)

	// 9. 按出口 IP 匹配宽带线路；无匹配自动创建（含按楼层 CIDR 归属楼层）
	var lineID int
	var lineName string
	auto := false
	lines, err := a.store.ListLines(ctx)
	if err != nil {
		fail("读取线路失败: %v", err)
		return
	}
	for _, l := range lines {
		if l.PublicIP == job.EgressIP {
			lineID, lineName = l.ID, l.Name
			break
		}
	}
	if lineID == 0 && job.ExpectedLineID != 0 {
		// 预期线路存在且出口 IP 尚未登记：把探测到的出口登记为该线路身份并绑定
		// （12 台逐一开通的主路径——管理员指定预期线路，开通即完成线路出口登记）
		for _, l := range lines {
			if l.ID == job.ExpectedLineID && l.PublicIP == "" {
				if err := a.store.UpdateLineEgressInfo(ctx, l.ID, job.EgressIP, eg.EgressISP, eg.EgressGeo); err == nil {
					lineID, lineName = l.ID, l.Name
					job.step("✓ 出口 IP %s 首次登记到预期线路「%s」", job.EgressIP, l.Name)
				}
				break
			}
		}
	}
	if lineID == 0 {
		// 自动创建：名称=运营商-出口IP。楼层不在此猜测——探针全部位于管理网段
		// （防火墙按源 IP 策略路由分配出口），楼层只能随线路（线路配置页维护）
		auto = true
		isp := eg.EgressISP
		if isp == "" {
			isp = "线路"
		}
		newName := isp + "-" + job.EgressIP
		lr := &LineRow{Name: newName, ISP: eg.EgressISP, PublicIP: job.EgressIP, Geo: eg.EgressGeo}
		nid, err := a.store.UpsertLine(ctx, lr, nil)
		if err != nil {
			// 线路名冲突兜底：追加时间戳
			lr.Name = fmt.Sprintf("%s-%d", newName, time.Now().Unix()%1000)
			if nid, err = a.store.UpsertLine(ctx, lr, nil); err != nil {
				fail("自动创建线路失败: %v", err)
				return
			}
		}
		lineID, lineName = nid, lr.Name
		job.step("✓ 未匹配到已有线路，自动创建线路「%s」（出口 %s）", lineName, job.EgressIP)
		if job.ExpectedLineID != 0 {
			ename := a.lineNameByID(ctx, job.ExpectedLineID)
			job.warn("实际出口未匹配到预期线路「%s」——请检查该探针的策略路由是否指向预期线路的出口", ename)
		}
	} else {
		job.step("✓ 匹配到宽带线路「%s」（出口 IP 一致）", lineName)
		if job.ExpectedLineID != 0 {
			if job.ExpectedLineID == lineID {
				job.step("✓ 与预期线路一致")
			} else {
				ename := a.lineNameByID(ctx, job.ExpectedLineID)
				job.warn("出口 IP 与预期线路不符：预期「%s」，实际匹配「%s」——请检查该探针的策略路由是否指向预期线路的出口；若以实际出口为准则无需处理", ename, lineName)
			}
		}
	}
	if err := a.store.AssignProbeLine(ctx, probeID, &lineID); err != nil {
		fail("绑定线路失败: %v", err)
		return
	}
	job.LineID, job.LineName, job.AutoCreated = lineID, lineName, auto
	// 同线路已有其他探针：提示（双探针交叉验证属正常；单探针/线路拓扑下出现=策略路由疑似指错）
	if ps, err := a.store.ListProbes(ctx); err == nil {
		var others []string
		for _, p := range ps {
			if p.LineID != nil && *p.LineID == lineID && p.ID != probeID {
				others = append(others, probeDisplay(p.ID, LineMeta{ProbeName: p.Name, ProbeHostname: p.Hostname}))
			}
		}
		if len(others) > 0 {
			job.warn("线路「%s」已绑定探针 %s——同线路多探针=交叉验证（正常）；若各探针应独占一条线路，请检查策略路由", lineName, strings.Join(others, "、"))
		}
	}
	// 楼层名（展示用）
	if lines2, err := a.store.ListLines(ctx); err == nil {
		for _, l := range lines2 {
			if l.ID == lineID {
				job.FloorName = l.FloorName
			}
		}
	}
	job.step("✓ 已绑定线路「%s」", lineName)
	if job.FloorName != "" {
		job.step("✓ 所在楼层：%s", job.FloorName)
	}

	job.Status = "done"
	job.step("🎉 开通完成")
	a.store.WriteAudit(ctx, "system", "provision:"+job.IP,
		fmt.Sprintf("probe=%s line=%d auto=%v egress=%s", probeID[:8], lineID, auto, job.EgressIP))
}

// sshCenterPubKey 读取中心公钥（NW_SSH_PUBKEY_FILE 指向的文件内容；未配置/不可读返回空）
func (a *AdminAPI) sshCenterPubKey() string {
	if a.cfg.SSHPubKeyFile == "" {
		return ""
	}
	b, err := os.ReadFile(a.cfg.SSHPubKeyFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// enrollTokenSetting 注册令牌：settings 优先，回退 env
func (a *AdminAPI) enrollTokenSetting() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var tok string
	if err := a.store.GetSetting(ctx, "agent.enroll_token", &tok); err == nil && tok != "" {
		return tok
	}
	return a.cfg.EnrollToken
}

// centerSelfURL 中心基地址（邮件链接同源；BaseURL 未配置时回退监听地址）
func (a *AdminAPI) centerSelfURL() string {
	if a.cfg.BaseURL != "" {
		return a.cfg.BaseURL
	}
	return "http://" + net.JoinHostPort(localIP(), portOf(a.cfg.Listen))
}

// lineNameByID 线路名（警告文案用；不存在返回 "未知"）
func (a *AdminAPI) lineNameByID(ctx context.Context, lineID int) string {
	lines, err := a.store.ListLines(ctx)
	if err != nil {
		return "未知"
	}
	for _, l := range lines {
		if l.ID == lineID {
			return l.Name
		}
	}
	return "未知"
}

// safeHostname 名称 → 合法主机名：非 ASCII 字符替换为 -，数字开头加 probe- 前缀
// （与 install-agent.sh 同规则；"探针-201"→"probe-201"）
func safeHostname(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "netwatch-probe"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "probe-" + s
	}
	return s
}

// agentBinaryPath 探针二进制：与中心 server 同目录（交付布局 bin/ 同级）
func agentBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), "netwatch-agent")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("未找到探针二进制 %s（请将 netwatch-agent 与 netwatch-server 放同目录）", p)
	}
	return p, nil
}

// sshUpload 经 SSH stdin 上传文件（cat >，无 SFTP 依赖）
func sshUpload(host string, port int, user, pass, remotePath string, data []byte, timeout time.Duration) error {
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(port)), cfg)
	if err != nil {
		return fmt.Errorf("连接失败: %v", err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	if err := sess.Start("cat > " + remotePath); err != nil {
		return err
	}
	// 同步写（ssh 层自带流控，LAN 内 7MB 毫秒级）；整体超时由下方 Wait 守护
	if _, err := stdin.Write(data); err != nil {
		return err
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = sess.Signal(ssh.SIGKILL)
		return fmt.Errorf("上传超时")
	}
}

// localIP 本机出口 IP（_centerSelfURL 兜底用）
func localIP() string {
	conn, err := net.DialTimeout("udp", "223.5.5.5:53", 2*time.Second)
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return ua.IP.String()
	}
	return "127.0.0.1"
}

// portOf 从监听地址提取端口
func portOf(listen string) string {
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		return listen[i+1:]
	}
	return listen
}

// ---------- HTTP handlers ----------

type provisionReq struct {
	IP             string `json:"ip"`
	SshPort        int    `json:"ssh_port"`
	SshUser        string `json:"ssh_user"`
	SshPass        string `json:"ssh_pass"`
	Name           string `json:"name"`
	ExpectedLineID int    `json:"expected_line_id"` // 可选：预期线路（开通时校验策略路由接线）
}

// provisionNew POST /probes/provision：发起零接触开通（立即返回任务 ID，前端轮询进度）
func (a *AdminAPI) provisionNew(w http.ResponseWriter, r *http.Request) {
	var req provisionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	req.IP = strings.TrimSpace(req.IP)
	if net.ParseIP(req.IP) == nil {
		httpError(w, 400, "服务器 IP 无效")
		return
	}
	if req.SshPass == "" {
		httpError(w, 400, "SSH 密码必填（输入一次，自动配置免密并加密留存）")
		return
	}
	if req.SshPort <= 0 || req.SshPort > 65535 {
		req.SshPort = 22
	}
	if req.SshUser == "" {
		req.SshUser = "root"
	}
	job, err := a.provisionStart(req.IP, req.SshPort, req.SshUser, req.SshPass, strings.TrimSpace(req.Name), req.ExpectedLineID)
	if err != nil {
		httpError(w, 409, err.Error())
		return
	}
	a.store.WriteAudit(r.Context(), "system", "provision-start:"+req.IP, "job="+job.ID+" name="+req.Name)
	writeJSON(w, map[string]any{"ok": true, "job_id": job.ID})
}

// provisionStatus GET /probes/provision/{job}：开通任务进度
func (a *AdminAPI) provisionStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "job")
	a.provMu.Lock()
	job := a.provJobs[id]
	a.provMu.Unlock()
	if job == nil {
		httpError(w, 404, "任务不存在（中心可能已重启，请重新发起）")
		return
	}
	writeJSON(w, job)
}

// refreshProbes POST /probes/refresh：立即刷新探针信息。
// 探针无入站端口，指令经心跳应答下发：agent 收到后立即重探出口 IP/运营商/归属地
// 并马上回报（≤15s + 数秒，相比 5 分钟周期大幅提前）。probe_ids 空=全部探针。
func (a *AdminAPI) refreshProbes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProbeIDs []string `json:"probe_ids"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req)
	ids := req.ProbeIDs
	if len(ids) == 0 {
		ps, err := a.store.ListProbes(r.Context())
		if err != nil {
			httpError(w, 500, "db")
			return
		}
		for _, p := range ps {
			ids = append(ids, p.ID)
		}
	}
	a.eng.RequestEgressRefresh(ids)
	a.store.WriteAudit(r.Context(), "system", "probes-refresh",
		fmt.Sprintf("count=%d", len(ids)))
	writeJSON(w, map[string]any{"ok": true, "count": len(ids)})
}
