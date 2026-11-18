package vpngateruntime

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"guiforcores/internal/vpngate"
)

func capturePreferencesOnlyChanged(previous, next vpngate.Settings) bool {
	previous.SystemProxyEnabled = next.SystemProxyEnabled
	previous.TUNEnabled = next.TUNEnabled
	// The GUI represents an absent list of process paths as an empty list.
	if len(previous.Upstream.ProcessPaths) == 0 && len(next.Upstream.ProcessPaths) == 0 {
		previous.Upstream.ProcessPaths, next.Upstream.ProcessPaths = nil, nil
	}
	return reflect.DeepEqual(previous, next)
}

// The desktop's mixed listener already exists. Keep its core and TUN in place
// when only the WinINet preference changes. The caller holds m.op.
func (m *Manager) updateRunningCapturePreferences(settings vpngate.Settings) error {
	m.mu.Lock()
	previouslyApplied := m.runtime.SystemProxyEnabled
	proxyChanged := previouslyApplied != settings.SystemProxyEnabled
	m.mu.Unlock()
	if proxyChanged {
		if err := m.setSystemProxy(settings.SystemProxyEnabled, settings.MixedPort); err != nil {
			if settings.SystemProxyEnabled {
				// Applying a proxy may have changed some values before failing.
				return errors.Join(err, m.rollbackSystemProxy(false, settings.MixedPort))
			}
			return err
		}
	}
	m.mu.Lock()
	previous := m.settings
	m.settings.TUNEnabled = settings.TUNEnabled
	m.settings.SystemProxyEnabled = settings.SystemProxyEnabled
	m.runtime.SystemProxyEnabled = settings.SystemProxyEnabled
	err := m.persistLocked()
	if err != nil {
		m.settings = previous
		m.runtime.SystemProxyEnabled = previouslyApplied
	}
	m.mu.Unlock()
	if err != nil {
		if proxyChanged {
			err = errors.Join(err, m.rollbackSystemProxy(previouslyApplied, settings.MixedPort))
		}
		return fmt.Errorf("保存代理开关设置：%w", err)
	}
	return nil
}

func (m *Manager) setSystemProxy(enabled bool, port int) error {
	if enabled {
		if err := m.applyProxy(port); err != nil {
			return fmt.Errorf("启用系统代理：%w", err)
		}
	} else if err := m.restoreProxy(); err != nil {
		return fmt.Errorf("关闭系统代理：%w", err)
	}
	return nil
}

func (m *Manager) rollbackSystemProxy(enabled bool, port int) error {
	switchErr := m.setSystemProxy(enabled, port)
	m.mu.Lock()
	defer m.mu.Unlock()
	if switchErr != nil {
		// Record pending proxy recovery while preserving the live core's status.
		m.repair = vpngate.RepairState{
			Needed:        true,
			RequiresAdmin: errors.Is(switchErr, os.ErrPermission) && !m.isAdmin(),
			Message:       "系统代理回退未完成：" + switchErr.Error(),
		}
	}
	err := errors.Join(switchErr, m.persistLocked())
	if err != nil {
		return fmt.Errorf("回退系统代理设置：%w", err)
	}
	return nil
}
