package vpngateruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"guiforcores/internal/guardian"
	"guiforcores/internal/vpngate"
)

const controllerPort = 20123

type nodeConfigError struct{ error }

type coreProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	log     *os.File
	once    sync.Once
	release func()
	err     error
}

func (p *coreProcess) stop() {
	p.once.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		if p.release != nil {
			p.release()
		}
		_ = p.log.Close()
	})
}
func validateCore(ctx context.Context, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("请先安装受管理的 sing-box 内核")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, path, "version")
	hideCommand(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("无法读取 sing-box 版本：%w", err)
	}
	return parseCoreVersion(string(out))
}
func startCore(ctx context.Context, s vpngate.Settings, n *vpngate.Node, opts vpngate.ConfigOptions, dir string) (*coreProcess, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	config, err := vpngate.BuildConfig(s, n, opts)
	if err != nil {
		if n != nil {
			return nil, nodeConfigError{err}
		}
		return nil, err
	}
	configPath := filepath.Join(dir, "config.json")
	if err = atomicWrite(configPath, config); err != nil {
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	check := exec.CommandContext(checkCtx, s.CorePath, "check", "-c", configPath, "-D", dir)
	hideCommand(check)
	out, err := check.CombinedOutput()
	if err != nil {
		configErr := fmt.Errorf("内核配置校验失败：%s", strings.TrimSpace(string(out)))
		var exitErr *exec.ExitError
		if n != nil && checkCtx.Err() == nil && errors.As(err, &exitErr) {
			return nil, nodeConfigError{configErr}
		}
		return nil, fmt.Errorf("内核检查环境不可用：%w", err)
	}
	log, err := os.OpenFile(filepath.Join(dir, "process.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(s.CorePath, "run", "--disable-color", "-c", configPath, "-D", dir)
	hideCommand(cmd)
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	p := &coreProcess{cmd: cmd, done: make(chan struct{}), log: log}
	p.release, err = ownProcess(cmd)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		log.Close()
		return nil, err
	}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
func waitListener(ctx context.Context, p *coreProcess, port int) error {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fmt.Errorf("sing-box 已退出，请查看运行日志")
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 150*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}
func measureRequest(ctx context.Context, client *http.Client, address string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		return 0, fmt.Errorf("测试目标返回 HTTP %d", res.StatusCode)
	}
	if _, err = io.Copy(io.Discard, io.LimitReader(res.Body, 65537)); err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}
func tunnelReady(ctx context.Context, p *coreProcess, port int, address string) (int64, error) {
	start := time.Now()
	client, err := proxyClient(vpngate.Upstream{Host: "127.0.0.1", Port: port, Protocol: "http"})
	if err != nil {
		return 0, err
	}
	defer client.CloseIdleConnections()
	client.Timeout = 4 * time.Second
	var lastErr error
	for {
		if _, err = measureRequest(ctx, client, address); err == nil {
			return time.Since(start).Milliseconds(), nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("隧道连通性验证超时：%w；最近请求失败：%v", ctx.Err(), lastErr)
		case <-p.done:
			return 0, fmt.Errorf("内核在隧道建立期间退出；最近请求失败：%v", lastErr)
		case <-time.After(300 * time.Millisecond):
		}
	}
}
func (m *Manager) connect(ctx context.Context) error {
	if m.guard == nil && !m.desktop {
		return fmt.Errorf("Windows 保护服务未运行")
	}
	m.mu.Lock()
	if m.coreInstallingLocked() {
		m.mu.Unlock()
		return fmt.Errorf("内核正在安装，请完成或取消后重试")
	}
	if m.desktop && (m.closed || m.repairing || m.stopping) {
		m.mu.Unlock()
		return fmt.Errorf("软件正在停止或修复，请稍后重试")
	}
	s := m.settings
	var node *vpngate.Node
	for _, n := range m.pool.Snapshot() {
		if n.ID == m.selected {
			copy := n
			node = &copy
			break
		}
	}
	m.mu.Unlock()
	if m.desktop && s.TUNEnabled && !m.isAdmin() {
		return fmt.Errorf("TUN 模式需要管理员权限；请以管理员身份重新启动 GUI，或关闭 TUN 后连接")
	}
	if err := vpngate.ValidateSettings(s); err != nil {
		return err
	}
	if s.RoutingPreset != "direct" && node == nil {
		return fmt.Errorf("请先选中 VPNGate 节点")
	}
	if s.MixedPort == controllerPort || s.Upstream.Host == "127.0.0.1" && (s.Upstream.Port == s.MixedPort || s.Upstream.Port == controllerPort) {
		return fmt.Errorf("外部上游不能指向本项目的入口或控制端口")
	}
	version, err := validateCore(ctx, s.CorePath)
	if err != nil {
		return err
	}
	// Exclude the external proxy's physical sockets from TUN capture as well as
	// the local mixed listener, or its own outbound traffic can loop into TUN.
	if (!m.desktop || s.TUNEnabled) && len(s.Upstream.ProcessPaths) == 0 {
		paths, resolveErr := m.resolveOwner(ctx, s.Upstream.Host, s.Upstream.Port)
		if resolveErr != nil {
			return resolveErr
		}
		s.Upstream.ProcessPaths = paths
		m.mu.Lock()
		m.settings.Upstream.ProcessPaths = paths
		m.mu.Unlock()
	}
	policy := guardian.Policy{CorePath: s.CorePath, UpstreamHost: s.Upstream.Host, UpstreamPort: s.Upstream.Port, UpstreamProcessPaths: s.Upstream.ProcessPaths, TunInterface: "SingBoxVPNGate"}
	// Acquire and persist protection before removing an existing TUN/connection.
	if m.guard != nil && s.RoutingPreset != "direct" {
		if err = m.guard.Arm(ctx, policy); err != nil {
			return fmt.Errorf("无法启用 Windows 断网保护：%w", err)
		}
	}
	protected := m.guard != nil && m.guard.Status().Protected
	old, err := m.detachForRestart(vpngate.RuntimeState{Status: "starting", Protected: protected, CoreVersion: version, StartedAt: now(), UpstreamOwner: strings.Join(s.Upstream.ProcessPaths, ", ")})
	if err != nil {
		return err
	}
	if old != nil {
		old.stop()
	}
	dir := filepath.Join(m.dir, "runtime")
	opts := vpngate.ConfigOptions{Production: true, MixedPort: s.MixedPort, ControllerPort: controllerPort, CNRulesPath: filepath.Join(m.dir, "rules", "cn.srs"), GFWRulesPath: filepath.Join(m.dir, "rules", "gfw.srs"), LogPath: filepath.Join(dir, "core.log")}
	if s.RoutingPreset == "direct" {
		node = nil
	}
	p, err := startCore(ctx, s, node, opts, dir)
	if err == nil {
		readyCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		err = waitListener(readyCtx, p, s.MixedPort)
		if err == nil && m.guard != nil && s.RoutingPreset != "direct" {
			err = m.guard.Arm(readyCtx, policy)
		}
		// Validate the endpoint directly through the controller, rather than the
		// business mixed inbound where bypass-cn/blacklist could choose direct.
		if err == nil && node != nil {
			err = verifyProductionEndpoint(readyCtx, s)
		}
		cancel()
	}
	if err != nil {
		if p != nil {
			p.stop()
		}
		offline, offlineErr := m.startOffline(ctx, s, opts)
		m.mu.Lock()
		m.process = offline
		m.runtime.Status = "protected"
		if m.desktop {
			m.runtime.Status = "degraded"
			if offline == nil {
				m.runtime.Status = "error"
			}
			m.repair = vpngate.RepairState{Needed: true, Message: "连接失败；可修复软件以停止连接并恢复本应用的网络设置"}
		}
		m.runtime.Message = err.Error()
		if offlineErr != nil {
			m.runtime.Message += "；" + offlineErr.Error()
		}
		m.runtime.ConnectedNodeID = ""
		_ = m.persistLocked()
		m.mu.Unlock()
		if offline != nil {
			go m.watchCore(offline)
		} else if m.desktop {
			if restoreErr := m.restoreProxy(); restoreErr != nil {
				m.markRepair(restoreErr)
			}
		}
		return err
	}
	if s.SystemProxyEnabled {
		if err = m.applyProxy(s.MixedPort); err != nil {
			p.stop()
			if m.desktop {
				_ = m.restoreProxy()
			}
			m.mu.Lock()
			m.runtime.Status = "protected"
			if m.desktop {
				m.runtime.Status = "error"
				m.repair = vpngate.RepairState{Needed: true, Message: "系统代理设置未完成；请修复软件"}
			}
			m.runtime.Message = err.Error()
			_ = m.persistLocked()
			m.mu.Unlock()
			return err
		}
	} else if err = m.restoreProxy(); err != nil {
		p.stop()
		m.mu.Lock()
		m.runtime.Status = "protected"
		if m.desktop {
			m.runtime.Status = "error"
			m.repair = vpngate.RepairState{Needed: true, Message: "系统代理恢复未完成；请修复软件"}
		}
		m.runtime.Message = err.Error()
		_ = m.persistLocked()
		m.mu.Unlock()
		return err
	}
	if m.guard != nil && s.RoutingPreset == "direct" {
		if err = m.guard.Disarm(ctx); err != nil {
			p.stop()
			return err
		}
	}
	m.mu.Lock()
	m.process = p
	m.runtime.Status = "connected"
	if s.RoutingPreset == "direct" {
		m.runtime.Status = "direct"
	}
	if node != nil {
		m.runtime.ConnectedNodeID = node.ID
	}
	m.runtime.Message = ""
	m.runtime.Protected = m.guard != nil && m.guard.Status().Protected
	if m.desktop {
		m.repair = vpngate.RepairState{}
	}
	err = m.persistLocked()
	m.mu.Unlock()
	go m.watchCore(p)
	if node != nil {
		go m.watchTunnel(p, s)
	}
	return err
}
func (m *Manager) watchTunnel(p *coreProcess, s vpngate.Settings) {
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-p.done:
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			err := verifyProductionEndpoint(ctx, s)
			cancel()
			m.recordTunnelHealth(p, err)
		}
	}
}
func (m *Manager) recordTunnelHealth(p *coreProcess, healthErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process != p || m.runtime.ConnectedNodeID == "" {
		return
	}
	status, message := "connected", ""
	if healthErr != nil {
		status = "protected"
		message = "VPNGate 链路检测失败，断网保护仍启用；请检查节点或测试地址"
		if m.desktop {
			status = "degraded"
			message = "VPNGate 链路检测失败；请检查节点或测试地址，或修复软件后重连"
		}
	}
	if m.runtime.Status == status && m.runtime.Message == message {
		return
	}
	m.runtime.Status = status
	m.runtime.Message = message
	_ = m.persistLocked()
}

// Offline capture has explicit rejects for VPN branches. It keeps legitimate
// direct rules usable after a node failure without opening a direct fallback.
func (m *Manager) startOffline(ctx context.Context, s vpngate.Settings, opts vpngate.ConfigOptions) (*coreProcess, error) {
	if s.RoutingPreset == "direct" || (!m.desktop && (m.guard == nil || !m.guard.Status().Protected)) {
		return nil, nil
	}
	dir := filepath.Join(m.dir, "runtime-offline")
	opts.LogPath = filepath.Join(dir, "core.log")
	p, err := startCore(ctx, s, nil, opts, dir)
	if err != nil {
		return nil, err
	}
	ready, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = waitListener(ready, p, s.MixedPort)
	if err == nil && m.guard != nil {
		err = m.guard.Arm(ready, guardian.Policy{CorePath: s.CorePath, UpstreamHost: s.Upstream.Host, UpstreamPort: s.Upstream.Port, UpstreamProcessPaths: s.Upstream.ProcessPaths, TunInterface: "SingBoxVPNGate"})
	}
	if err == nil && s.SystemProxyEnabled {
		err = m.applyProxy(s.MixedPort)
	} else if err == nil {
		err = m.restoreProxy()
	}
	if err != nil {
		p.stop()
		return nil, err
	}
	return p, nil
}
func (m *Manager) detachForRestart(next vpngate.RuntimeState) (*coreProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previousRuntime := m.runtime
	m.runtime = next
	if err := m.persistLocked(); err != nil {
		m.runtime = previousRuntime
		return nil, err
	}
	old := m.process
	m.process = nil
	return old, nil
}
func verifyProductionEndpoint(ctx context.Context, s vpngate.Settings) error {
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/proxies/vpngate/delay?timeout=5000&url=%s", controllerPort, url.QueryEscape(s.TestURL))
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		res, err := client.Do(req)
		if err == nil {
			data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
			res.Body.Close()
			if res.StatusCode == 200 && bytes.Contains(data, []byte("delay")) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("VPNGate 隧道未就绪：%w", ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}
func (m *Manager) watchCore(p *coreProcess) {
	<-p.done
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if m.process != p {
		m.mu.Unlock()
		return
	}
	m.process = nil
	m.runtime.Status = "protected"
	if m.desktop || m.settings.RoutingPreset == "direct" {
		m.runtime.Status = "error"
	}
	m.runtime.ConnectedNodeID = ""
	m.runtime.Message = "sing-box 意外退出；不会自动回退或切换节点"
	if m.desktop {
		m.runtime.Protected = false
		m.repair = vpngate.RepairState{Needed: true, Message: "内核意外退出；请修复软件以清理本应用的运行状态"}
	}
	_ = m.persistLocked()
	m.mu.Unlock()
	p.stop()
	if m.desktop {
		if err := m.restoreProxy(); err != nil {
			m.markRepair(err)
		} else {
			m.mu.Lock()
			_ = m.persistLocked()
			m.mu.Unlock()
		}
	}
}
func (m *Manager) stop(ctx context.Context) error {
	m.mu.Lock()
	for _, c := range m.cancels {
		c()
	}
	p := m.process
	m.process = nil
	m.mu.Unlock()
	if p != nil {
		p.stop()
	}
	if m.desktop {
		m.mu.Lock()
		m.runtime = vpngate.RuntimeState{Status: "stopped"}
		m.mu.Unlock()
	}
	if err := m.restoreProxy(); err != nil {
		if m.desktop {
			m.markRepair(err)
		}
		return err
	}
	if m.guard != nil {
		if err := m.guard.Disarm(ctx); err != nil {
			return err
		}
	}
	if m.desktop {
		if err := m.cleanRuntimeFiles(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtime = vpngate.RuntimeState{Status: "stopped"}
	if m.desktop {
		m.repair = vpngate.RepairState{}
	}
	return m.persistLocked()
}
