package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"guiforcores/internal/vpngate"
)

// Startup runs after the Wails single-instance check. If the compatible core
// has yet to be installed, the first successful install or selection resumes
// startup, unless the user has since stopped the runtime.
func (m *Manager) startDesktop(ctx context.Context, afterCoreReady bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if ctx.Err() != nil || m.closed || m.repairing || m.stopping || m.repair.Needed || m.process != nil ||
		(afterCoreReady && !m.startupPendingCore) || (!afterCoreReady && m.startupAttempted) {
		m.mu.Unlock()
		return nil
	}
	m.startupAttempted = true
	if m.coreInstallingLocked() {
		m.startupPendingCore = true
		m.mu.Unlock()
		return nil
	}
	s := m.settings
	if s.CorePath == "" {
		m.startupPendingCore = true
		m.mu.Unlock()
		return nil
	}
	info, statErr := os.Stat(s.CorePath)
	m.startupPendingCore = os.IsNotExist(statErr)
	if m.startupPendingCore {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	err := statErr
	if err == nil {
		if !info.Mode().IsRegular() {
			err = fmt.Errorf("sing-box 内核路径不是文件")
		} else {
			err = m.connectWithSettings(ctx, s, true)
		}
	}
	if err != nil {
		m.mu.Lock()
		if !m.closed && !m.repairing && !m.stopping && m.process == nil && m.runtime.Status != "error" {
			m.runtime = vpngate.RuntimeState{Status: "error", Message: err.Error()}
		}
		m.mu.Unlock()
		m.coreLogs.Add(time.Now(), "error", "无法启动 sing-box："+redactUpstream(s.Upstream)(err.Error()))
	}
	return err
}

func (m *Manager) startAfterCoreReady() {
	ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
	defer cancel()
	_ = m.startDesktop(ctx, true)
}

func (m *Manager) inspectDesktopRecovery() error {
	data, err := os.ReadFile(filepath.Join(m.dir, "proxy-snapshot.json"))
	if err == nil {
		var snapshot systemProxySnapshot
		if err = json.Unmarshal(data, &snapshot); err != nil {
			return fmt.Errorf("系统代理恢复快照损坏：%w", err)
		}
		m.proxySnapshot = &snapshot
	} else if !os.IsNotExist(err) {
		return err
	}
	if m.proxySnapshot != nil {
		m.repair = vpngate.RepairState{Needed: true, Message: "检测到本应用的系统代理快照；请修复软件以恢复网络设置"}
	}
	for _, name := range []string{"runtime", "runtime-offline", "probes"} {
		entries, e := os.ReadDir(filepath.Join(m.dir, name))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if len(entries) > 0 {
			m.repair.Needed = true
			m.repair.Message = "检测到上次运行的临时文件；请修复软件以清理本应用的运行状态"
		}
	}
	return nil
}

func (m *Manager) cancelAndWaitJobs() {
	m.mu.Lock()
	waits := make([]chan struct{}, 0, len(m.jobDone))
	for _, c := range m.cancels {
		c()
	}
	for _, done := range m.jobDone {
		waits = append(waits, done)
	}
	m.mu.Unlock()
	for _, done := range waits {
		<-done
	}
}

// cleanRuntimeFiles removes only directories reserved for owned core/probe data.
// The fixed child names exclude settings, node profiles, rules and other data.
func (m *Manager) cleanRuntimeFiles() error {
	root, err := filepath.Abs(m.dir)
	if err != nil {
		return err
	}
	for _, name := range []string{"runtime", "runtime-offline", "probes"} {
		target := filepath.Join(root, name)
		if filepath.Dir(target) != root {
			return fmt.Errorf("运行文件路径超出应用目录")
		}
		if err = os.RemoveAll(target); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) markRepair(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repair = vpngate.RepairState{Needed: true, RequiresAdmin: m.desktop && errors.Is(err, os.ErrPermission) && !m.isAdmin(), Message: "修复未完成：" + err.Error()}
	if m.repair.RequiresAdmin {
		m.repair.Message += "；请以管理员身份重新启动 GUI 后再次修复"
	}
	m.runtime.Status, m.runtime.Message = "error", err.Error()
	_ = m.persistLocked()
}

func (m *Manager) stopDesktop(ctx context.Context) (vpngate.State, error) {
	m.mu.Lock()
	if m.closed || m.repairing || m.stopping {
		m.mu.Unlock()
		return m.State(), fmt.Errorf("软件正在停止或修复，请稍后重试")
	}
	m.stopping = true
	m.startupAttempted = true
	m.startupPendingCore = false
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.stopping = false; m.mu.Unlock() }()
	// A canceled rules job may need op to finish; never wait while holding it.
	m.cancelAndWaitJobs()
	m.op.Lock()
	defer m.op.Unlock()
	err := m.stop(ctx)
	return m.State(), err
}

// Repair stops owned work and restores this application's network changes while
// preserving user settings, the selected node, private profiles and valid rules.
func (m *Manager) Repair(ctx context.Context) (vpngate.State, error) {
	m.mu.Lock()
	if m.closed || m.repairing || m.stopping {
		m.mu.Unlock()
		return m.State(), fmt.Errorf("软件正在停止或修复，请稍后重试")
	}
	m.repairing = true
	m.startupAttempted = true
	m.startupPendingCore = false
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.repairing = false; m.mu.Unlock() }()
	// Wait before taking op: a canceled rule job may need op to finish.
	m.cancelAndWaitJobs()
	m.op.Lock()
	defer m.op.Unlock()
	err := m.stop(ctx)
	if err == nil {
		err = m.cleanRuntimeFiles()
	}
	if err != nil {
		m.markRepair(err)
		return m.State(), err
	}
	m.mu.Lock()
	m.runtime = vpngate.RuntimeState{Status: "stopped"}
	m.repair = vpngate.RepairState{Message: "修复完成：已停止连接并恢复本应用的网络设置，节点和设置已保留"}
	err = m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		m.markRepair(err)
	}
	return m.State(), err
}
