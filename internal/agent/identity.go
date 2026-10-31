package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"netwatch/internal/proto"
)

// Identity 探针身份（注册后持久化到 stateDir/identity.json）
type Identity struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

func (a *Agent) identityPath() string { return filepath.Join(a.stateDir, "identity.json") }

func (a *Agent) hasIdentity() bool {
	a.identMu.RLock()
	defer a.identMu.RUnlock()
	return a.ident.AgentID != "" && a.ident.Secret != ""
}

func (a *Agent) loadIdentity() {
	b, err := os.ReadFile(a.identityPath())
	if err != nil {
		return
	}
	var id Identity
	if json.Unmarshal(b, &id) == nil && id.AgentID != "" && id.Secret != "" {
		a.identMu.Lock()
		a.ident = id
		a.identMu.Unlock()
	}
}

func (a *Agent) clearIdentity() {
	a.identMu.Lock()
	a.ident = Identity{}
	a.identMu.Unlock()
	_ = os.Remove(a.identityPath())
}

func (a *Agent) saveIdentity(id Identity) error {
	b, _ := json.MarshalIndent(id, "", "  ")
	p := a.identityPath()
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	a.identMu.Lock()
	a.ident = id
	a.identMu.Unlock()
	return nil
}

// agentReq 发送携带探针身份的 API 请求。401/403 返回 errReRegister。
func (a *Agent) agentReq(ctx context.Context, method, path string, body, out any) error {
	a.identMu.RLock()
	id := a.ident
	a.identMu.RUnlock()

	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.opts.ServerURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if id.AgentID != "" {
		req.Header.Set("X-NW-Agent", id.AgentID)
		req.Header.Set("X-NW-Secret", id.Secret)
	}
	resp, err := a.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		io.Copy(io.Discard, resp.Body)
		return errReRegister
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// register 向中心端注册并获取初始配置。
func (a *Agent) register(ctx context.Context) error {
	if a.opts.EnrollToken == "" {
		return errors.New("no identity and no enroll token (-token / NW_ENROLL_TOKEN)")
	}
	req := proto.RegisterReq{
		EnrollToken: a.opts.EnrollToken,
		Name:        a.opts.Name,
		Hostname:    hostname(),
		OS:          goos(),
		Arch:        goarch(),
		AgentVer:    a.opts.Version,
		SourceIP:    detectSourceIP(),
		Gateway:     detectGateway(),
	}
	var resp proto.RegisterResp
	if err := a.agentReq(ctx, http.MethodPost, "/api/agent/register", &req, &resp); err != nil {
		return err
	}
	if err := a.saveIdentity(Identity{AgentID: resp.AgentID, Secret: resp.Secret}); err != nil {
		return err
	}
	a.log.Printf("registered as agent %s", resp.AgentID)
	a.applyConfig(resp.Config, false)
	return nil
}

// refreshConfig 拉取最新配置，版本变化时通知探测循环重载。
func (a *Agent) refreshConfig(ctx context.Context) error {
	var cfg proto.ProbeConfig
	if err := a.agentReq(ctx, http.MethodGet, "/api/agent/config", nil, &cfg); err != nil {
		return err
	}
	a.applyConfig(cfg, true)
	return nil
}

// configWatch 每 30s 轮询配置；凭证失效时触发整周期重注册。
func (a *Agent) configWatch(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := a.refreshConfig(ctx); err != nil {
				if errors.Is(err, errReRegister) {
					a.abort(errReRegister)
					return
				}
				a.log.Printf("config poll: %v", err)
				continue
			}
			a.maybeUpgrade(ctx)
		}
	}
}
