package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
)

// 探针自更新：中心端下发 Upgrade 任务（版本 + SHA256），
// 探针下载新二进制 → 校验 → 原子替换自身 → exec 重启（systemd 亦可拉起兜底）。

type upgradeState struct {
	LastReleaseID int64 `json:"last_release_id"`
}

func (a *Agent) upgradeStatePath() string { return filepath.Join(a.stateDir, "upgrade.json") }

func (a *Agent) loadUpgradeState() upgradeState {
	var s upgradeState
	if b, err := os.ReadFile(a.upgradeStatePath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (a *Agent) saveUpgradeState(s upgradeState) {
	b, _ := json.Marshal(s)
	_ = os.WriteFile(a.upgradeStatePath(), b, 0o600)
}

// maybeUpgrade 在每次配置刷新后调用；无需升级时零开销返回。
func (a *Agent) maybeUpgrade(ctx context.Context) {
	up := a.currentConfig().Upgrade
	if up == nil || up.Version == "" || up.Version == a.opts.Version {
		return
	}
	st := a.loadUpgradeState()
	if st.LastReleaseID == up.ReleaseID {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		a.log.Printf("upgrade: locate self: %v", err)
		return
	}
	exePath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		exePath = exe
	}
	tmp := exePath + ".new"

	a.log.Printf("upgrade: downloading release %d (version %s)", up.ReleaseID, up.Version)
	sha, err := a.downloadBinary(ctx, up.ReleaseID, tmp)
	if err != nil {
		a.log.Printf("upgrade: download failed: %v", err)
		_ = os.Remove(tmp)
		return
	}
	if up.SHA256 != "" && sha != up.SHA256 {
		a.log.Printf("upgrade: sha256 mismatch (got %s)", sha)
		_ = os.Remove(tmp)
		return
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		a.log.Printf("upgrade: chmod: %v", err)
		return
	}
	if err := os.Rename(tmp, exePath); err != nil {
		a.log.Printf("upgrade: replace failed: %v", err)
		return
	}
	a.saveUpgradeState(upgradeState{LastReleaseID: up.ReleaseID})
	a.log.Printf("upgrade: switched to version %s, restarting", up.Version)
	// 用新二进制重启自身；若 exec 失败则退出交给 systemd 拉起
	if err := syscall.Exec(exePath, os.Args, os.Environ()); err != nil {
		a.log.Printf("upgrade: exec failed (%v), exiting for systemd restart", err)
		os.Exit(1)
	}
}

// downloadBinary 流式下载并计算 SHA256
func (a *Agent) downloadBinary(ctx context.Context, releaseID int64, dst string) (string, error) {
	a.identMu.RLock()
	id := a.ident
	a.identMu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/api/agent/download/%d", a.opts.ServerURL, releaseID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-NW-Agent", id.AgentID)
	req.Header.Set("X-NW-Secret", id.Secret)
	resp, err := a.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
