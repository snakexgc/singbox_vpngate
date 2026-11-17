package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"guiforcores/internal/guardian"
	"guiforcores/internal/vpngate"
	"guiforcores/internal/vpngateruntime"
)

type desktopRuntime interface {
	State() vpngate.State
	Handle(context.Context, string, json.RawMessage) (any, error)
	Close()
}

var vpnDesktop struct {
	sync.Mutex
	manager desktopRuntime
	closing bool
}

// The GUI owns its runtime. No installed service or service IPC is required.
func (a *App) VPNGateRequest(method, payload string) FlagResult {
	if method == "runtime.elevate" {
		if !Env.IsPrivileged {
			result := a.RestartApp(true)
			if !result.Flag {
				return result
			}
		}
		return FlagResult{true, `{"restarting":true}`}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if payload == "" {
		payload = "{}"
	}
	if !json.Valid([]byte(payload)) {
		return FlagResult{false, "无效的 VPNGate 请求"}
	}
	result, err := vpnRequest(ctx, method, json.RawMessage(payload))
	if err != nil {
		return FlagResult{false, err.Error()}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return FlagResult{false, err.Error()}
	}
	return FlagResult{true, string(data)}
}

func vpnRequest(ctx context.Context, method string, payload json.RawMessage) (any, error) {
	m, err := localVPNGateRuntime()
	if err != nil {
		if method == "repair" {
			// Network recovery remains available when the main state cannot be loaded.
			restoreErr := vpngateruntime.RestoreProxySnapshot(filepath.Join(Env.DataDir, "vpngate"))
			var legacyErr error
			if Env.IsPrivileged {
				legacyErr = repairLegacyProtection()
			}
			return nil, errors.Join(fmt.Errorf("配置仍无法读取，已尝试恢复网络且保留原始文件：%w", err), restoreErr, legacyErr)
		}
		return nil, err
	}
	legacy := guardian.LegacyServiceInstalled()
	return dispatchDesktop(ctx, m, method, payload, legacy, Env.IsPrivileged, repairLegacyProtection)
}

func dispatchDesktop(ctx context.Context, m desktopRuntime, method string, payload json.RawMessage, legacy, admin bool, cleanup func() error) (any, error) {
	if method == "state" {
		state := m.State()
		if legacy {
			state.Repair.Needed = true
			state.Repair.RequiresAdmin = !admin
			state.Repair.Message = "检测到旧版后台保护组件，请点击修复软件清理；无需重新安装服务"
		}
		return state, nil
	}
	if method == "connect" && legacy {
		return nil, fmt.Errorf("检测到旧版后台保护组件，请先点击修复软件清理，再连接")
	}
	if method == "repair" {
		if legacy && !admin {
			return nil, fmt.Errorf("清理旧版保护残留需要管理员权限，请先以管理员身份重启软件，再点击修复")
		}
		result, err := m.Handle(ctx, "repair", nil)
		localErr := err
		var cleanupErr error
		if admin && cleanup != nil {
			cleanupErr = cleanup()
		}
		if err = errors.Join(localErr, cleanupErr); err != nil {
			return nil, fmt.Errorf("修复未完全完成，已尝试所有获授权的恢复步骤：%w", err)
		}
		return result, nil
	}
	return m.Handle(ctx, method, payload)
}

func localVPNGateRuntime() (desktopRuntime, error) {
	vpnDesktop.Lock()
	defer vpnDesktop.Unlock()
	if vpnDesktop.closing {
		return nil, fmt.Errorf("软件正在退出")
	}
	if vpnDesktop.manager != nil {
		return vpnDesktop.manager, nil
	}
	dir := filepath.Join(Env.DataDir, "vpngate")
	draft := filepath.Join(Env.DataDir, "vpngate-draft", "state.json")
	sources := []string{draft}
	// Only profiles used by the old GUI are eligible for the machine-wide legacy
	// import. A fresh/custom profile never silently imports another profile.
	if _, err := os.Stat(draft); err == nil {
		sources = append([]string{filepath.Join(guardian.DataDir(), "state.json")}, sources...)
	}
	if err := migrateDesktopState(filepath.Join(dir, "state.json"), sources); err != nil {
		return nil, err
	}
	m, err := vpngateruntime.NewDesktop(dir, portableCorePath())
	if err != nil {
		return nil, err
	}
	vpnDesktop.manager = m
	return m, nil
}

func portableCorePath() string {
	name := "sing-box"
	if Env.OS == "windows" {
		name += ".exe"
	}
	candidates := []string{filepath.Join(Env.BasePath, name), filepath.Join(Env.BasePath, "core", name), filepath.Join(Env.DataDir, "sing-box", name)}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return candidates[0]
}

// Called on normal GUI exit/restart. No unrelated sing-box process is touched.
func CloseVPNGateRuntime() {
	vpnDesktop.Lock()
	m := vpnDesktop.manager
	vpnDesktop.manager = nil
	vpnDesktop.closing = true
	vpnDesktop.Unlock()
	if m != nil {
		m.Close()
	}
}

func repairLegacyProtection() error {
	// This function is reached only from an explicit administrator Repair action.
	// Always attempt owned-filter removal even when a legacy snapshot is corrupt.
	restoreErr := vpngateruntime.RestoreProxySnapshot(guardian.DataDir())
	// A clean desktop installation has no legacy WFP objects to remove. Avoid
	// mutating (or requiring) the filtering platform for an ordinary repair.
	if !guardian.LegacyServiceInstalled() {
		present, inspectErr := guardian.LegacyFilterPresence()
		if inspectErr != nil {
			return errors.Join(restoreErr, fmt.Errorf("无法确认旧版保护残留，未将检查失败当作已清理：%w", inspectErr))
		}
		if !present {
			return restoreErr
		}
	}
	cleanupErr := guardian.Uninstall()
	return errors.Join(restoreErr, cleanupErr)
}

// Copy valid legacy data; retain all original files as recovery backups. Once a
// desktop pool exists, older snapshots cannot overwrite it or current settings.
func migrateDesktopState(target string, sources []string) error {
	var current map[string]json.RawMessage
	changed := false
	data, err := os.ReadFile(target)
	if err == nil {
		if err = json.Unmarshal(data, &current); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, source := range sources {
		raw, readErr := os.ReadFile(source)
		if readErr != nil {
			continue
		}
		var old map[string]json.RawMessage
		if json.Unmarshal(raw, &old) != nil {
			continue
		}
		if current == nil {
			current = old
			changed = true
			continue
		}
		var nodes, oldNodes []vpngate.Node
		_ = json.Unmarshal(current["nodes"], &nodes)
		_ = json.Unmarshal(old["nodes"], &oldNodes)
		if len(nodes) == 0 && len(oldNodes) > 0 {
			current["nodes"] = old["nodes"]
			var selected string
			_ = json.Unmarshal(current["selectedNodeId"], &selected)
			if selected == "" {
				current["selectedNodeId"] = old["selectedNodeId"]
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	output, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".migration-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(output)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, target)
}
