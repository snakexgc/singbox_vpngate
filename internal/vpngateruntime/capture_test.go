package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopSavesCapturePreferencesIndependentlyWhileStopped(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.isAdmin = func() bool { return false }
	m.applyProxy = func(int) error { t.Fatal("saved preference applied system proxy while stopped"); return nil }
	m.restoreProxy = func() error { return nil }
	ctx := context.Background()
	for _, tun := range []bool{false, true} {
		for _, proxy := range []bool{false, true} {
			s := m.State().Settings
			s.TUNEnabled, s.SystemProxyEnabled = tun, proxy
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Handle(ctx, "settings.update", raw); err != nil {
				t.Fatal(err)
			}
			state := m.State()
			if state.Settings.TUNEnabled != tun || state.Settings.SystemProxyEnabled != proxy || state.Runtime.RequiresAdmin != tun || m.process != nil {
				t.Fatalf("capture settings or elevation for TUN=%t, proxy=%t are wrong: %+v", tun, proxy, state)
			}
		}
	}
}

func TestDesktopSystemProxySwitchKeepsRunningCore(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	ctx := context.Background()
	if _, err := m.Handle(ctx, "connect", nil); err != nil {
		t.Fatal(err)
	}
	process := m.process
	var appliedPort, restores int
	m.applyProxy = func(port int) error { appliedPort = port; return nil }
	m.restoreProxy = func() error { restores++; return nil }
	for _, enabled := range []bool{true, false} {
		s := m.State().Settings
		s.SystemProxyEnabled = enabled
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Handle(ctx, "settings.update", raw); err != nil {
			t.Fatal(err)
		}
		if m.process != process || m.State().Runtime.Status != "direct" {
			t.Fatal("system proxy switch replaced the running core")
		}
		if current := m.State().Settings; current.SystemProxyEnabled != enabled || current.TUNEnabled {
			t.Fatalf("runtime settings do not reflect the independent proxy switch: %+v", current)
		}
		data, err := os.ReadFile(filepath.Join(m.dir, "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		var saved persisted
		if err = json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Settings.SystemProxyEnabled != enabled || saved.Settings.TUNEnabled {
			t.Fatalf("persisted settings do not reflect the independent proxy switch: %+v", saved.Settings)
		}
	}
	if appliedPort != m.settings.MixedPort || restores != 1 {
		t.Fatalf("system proxy operations did not use the live mixed listener: port=%d restores=%d", appliedPort, restores)
	}
}

func TestFailedDesktopSystemProxySwitchPreservesRunningCoreAndPreference(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	ctx := context.Background()
	if _, err := m.Handle(ctx, "connect", nil); err != nil {
		t.Fatal(err)
	}
	process := m.process
	denied := errors.New("proxy write denied")
	restored := false
	m.applyProxy = func(int) error { return denied }
	m.restoreProxy = func() error { restored = true; return nil }
	s := m.State().Settings
	s.SystemProxyEnabled = true
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Handle(ctx, "settings.update", raw); !errors.Is(err, denied) {
		t.Fatalf("expected proxy failure, got %v", err)
	}
	state := m.State()
	if !restored || m.process != process || state.Settings.SystemProxyEnabled || state.Runtime.Status != "direct" {
		t.Fatalf("failed switch lost running core or preference: %+v", state)
	}
}

func TestDesktopSwitchToTUNWithoutElevationPersistsRequestWithoutChangingLiveProxy(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	m.settings.SystemProxyEnabled = true
	m.isAdmin = func() bool { return false }
	var applied int
	m.applyProxy = func(int) error { applied++; return nil }
	m.restoreProxy = func() error { return nil }
	ctx := context.Background()
	if _, err := m.Handle(ctx, "connect", nil); err != nil {
		t.Fatal(err)
	}
	process := m.process
	s := m.State().Settings
	s.TUNEnabled = true
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Handle(ctx, "settings.update", raw); err != nil {
		t.Fatalf("TUN preference save rejected despite retaining the current process: %v", err)
	}
	state := m.State()
	if m.process != process || applied != 1 || !state.Runtime.SystemProxyEnabled || !state.Settings.TUNEnabled || state.Runtime.TUNEnabled || !state.Runtime.RequiresAdmin {
		t.Fatalf("pending TUN request changed the live proxy or effective capture: %+v", state)
	}
}
