package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"guiforcores/internal/vpngate"
)

// The helper replaces only the external core executable. Manager config,
// process ownership, monitoring and shutdown use their production paths. It
// opens loopback listeners and cannot configure TUN, proxy, routes or firewall.
func TestMain(m *testing.M) {
	if os.Getenv("VPNGATE_TEST_CORE") == "1" && len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			if output := os.Getenv("VPNGATE_TEST_CORE_VERSION"); output != "" {
				fmt.Println(output)
			} else {
				fmt.Println("sing-box version 1.14.4 with_openvpn with_gvisor")
			}
			os.Exit(0)
		case "check":
			os.Exit(0)
		case "run":
			runDesktopCoreHelper()
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func runDesktopCoreHelper() {
	if message := os.Getenv("VPNGATE_TEST_CORE_LOG_ERROR"); message != "" {
		fmt.Fprintln(os.Stderr, message)
	}
	var configPath string
	for i, arg := range os.Args {
		if arg == "-c" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
		}
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		panic(err)
	}
	var config struct {
		Inbounds []struct {
			Type string `json:"type"`
			Port int    `json:"listen_port"`
		} `json:"inbounds"`
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		panic(err)
	}
	for _, inbound := range config.Inbounds {
		if inbound.Type != "mixed" {
			panic("test helper forbids non-mixed inbound")
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", inbound.Port))
		if err != nil {
			panic(err)
		}
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		}()
	}
	select {}
}

func desktopWithHelperCore(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("VPNGATE_TEST_CORE", "1")
	core, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Build the loopback-only fixture without the production auto-refresh
	// scheduler. Race-instrumented helper subprocesses can take several seconds;
	// a background refresh must never contact an unrelated local proxy.
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	m.mu.Lock()
	m.desktop, m.corePath, m.settings.CorePath = true, core, core
	m.settings.TUNEnabled, m.settings.SystemProxyEnabled = false, false
	m.settings.RulesAutoUpdate = false
	m.settings.Upstream.Port = 7890
	m.settings.MixedPort, err = freePort()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDesktopConnectAndCloseOwnPortableChildAndProxySnapshot(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	m.resolveOwner = func(context.Context, string, int) ([]string, error) {
		return nil, errors.New("mixed-only mode must not require process owner discovery")
	}
	if _, err := m.Handle(context.Background(), "connect", nil); err != nil {
		t.Fatal(err)
	}
	p := m.process
	if p == nil || m.State().Runtime.Status != "direct" {
		t.Fatal("service-free desktop did not start core")
	}
	restored := false
	m.proxySnapshot = &systemProxySnapshot{}
	m.restoreProxy = func() error { restored = true; m.proxySnapshot = nil; return nil }
	m.Close()
	select {
	case <-p.done:
	default:
		t.Fatal("GUI close left its child running")
	}
	if !restored || m.process != nil || m.State().Runtime.Status != "stopped" {
		t.Fatal("GUI close did not restore owned connection state")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "runtime")); !os.IsNotExist(err) {
		t.Fatalf("GUI close left runtime files: %v", err)
	}
}

func TestDesktopStartupKeepsCoreRunningWithoutNodeAndWithoutElevation(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.TUNEnabled = true
	m.settings.SystemProxyEnabled = true
	m.isAdmin = func() bool { return false }
	m.resolveOwner = func(context.Context, string, int) ([]string, error) {
		t.Fatal("startup attempted TUN owner discovery without elevation")
		return nil, nil
	}
	applied := false
	m.applyProxy = func(port int) error {
		applied = port == m.settings.MixedPort
		return nil
	}
	m.restoreProxy = func() error { return nil }
	if _, err := m.Handle(context.Background(), "runtime.startup", nil); err != nil {
		t.Fatal(err)
	}
	state := m.State()
	if m.process == nil || state.Runtime.Status != "offline" || !state.Runtime.RequiresAdmin || state.Repair.Needed || !state.Settings.TUNEnabled || state.Runtime.TUNEnabled || !state.Runtime.SystemProxyEnabled || !applied {
		t.Fatalf("startup did not run the core with available capture methods: %+v", state)
	}
	// A duplicate lifecycle callback cannot replace a running core.
	first := m.process
	if _, err := m.Handle(context.Background(), "runtime.startup", nil); err != nil || m.process != first {
		t.Fatalf("duplicate startup replaced a running core: %v", err)
	}
}

func TestDesktopConfiguredNodeFailureAndRecoveryKeepTheSameCore(t *testing.T) {
	m := desktopWithHelperCore(t)
	const profile = `client
dev tun
proto tcp
remote 127.0.0.1 443
remote-cert-tls server
<ca>
-----BEGIN CERTIFICATE-----
fixture-ca
-----END CERTIFICATE-----
</ca>
`
	m.settings.SystemProxyEnabled = true
	m.applyProxy = func(int) error { return nil }
	m.restoreProxy = func() error { return nil }
	m.selected = "chosen"
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: m.selected, Config: profile}})
	verify := make(chan struct{}, 1)
	finishCheck := make(chan struct{})
	m.verifyEndpoint = func(ctx context.Context, _ vpngate.Settings) error {
		verify <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finishCheck:
			return errors.New("loopback test: external proxy refused the tunnel")
		}
	}
	if _, err := m.Handle(context.Background(), "runtime.startup", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-verify:
	case <-time.After(3 * time.Second):
		t.Fatal("no initial health check on the running core")
	}
	if pending := m.State(); pending.Runtime.Status != "connecting" || pending.Runtime.ConnectedNodeID != "" || !pending.Runtime.SystemProxyEnabled || pending.Repair.Needed {
		t.Fatalf("initial tunnel check reported a terminal result: %+v", pending.Runtime)
	}
	close(finishCheck)
	process := m.process
	deadline := time.Now().Add(3 * time.Second)
	logged := false
	for !logged && time.Now().Before(deadline) {
		for _, entry := range m.ReadCoreLogs(0) {
			logged = logged || strings.Contains(entry.Message, "external proxy refused the tunnel")
		}
		if !logged {
			time.Sleep(10 * time.Millisecond)
		}
	}
	state := m.State()
	if !logged || process == nil || process.nodeID != "chosen" || state.Runtime.Status != "offline" || !state.Runtime.SystemProxyEnabled || state.Repair.Needed {
		t.Fatalf("configured node did not fail independently of the core and capture: %+v", state)
	}
	m.recordTunnelHealth(process, m.settings.Upstream, nil)
	if recovered := m.State(); m.process != process || recovered.Runtime.Status != "connected" || recovered.Runtime.ConnectedNodeID != "chosen" || recovered.Repair.Needed {
		t.Fatalf("recovery failed to retain the working core: %+v", recovered)
	}
	if _, err := m.Handle(context.Background(), "stop", nil); err != nil {
		t.Fatal(err)
	}
	if stopped := m.State(); stopped.Runtime.Status != "stopped" || stopped.Runtime.SystemProxyEnabled || stopped.Runtime.TUNEnabled {
		t.Fatalf("stop retained effective capture: %+v", stopped)
	}
}

func TestDesktopSwitchAndStopCancelPendingTunnelCheck(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.restoreProxy = func() error { return nil }
	const profile = "client\ndev tun\nproto tcp\nremote 127.0.0.1 443\n<ca>\nfixture-ca\n</ca>\n"
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "first", Config: profile}, {ID: "second", Config: profile}})
	m.selected = "first"
	checks := make(chan context.Context, 2)
	m.verifyEndpoint = func(ctx context.Context, _ vpngate.Settings) error {
		checks <- ctx
		<-ctx.Done()
		return ctx.Err()
	}
	check := func() context.Context {
		t.Helper()
		select {
		case ctx := <-checks:
			return ctx
		case <-time.After(3 * time.Second):
			t.Fatal("no health check for the selected candidate")
			return nil
		}
	}
	assertCanceled := func(ctx context.Context) {
		t.Helper()
		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("old check ended without cancellation: %v", ctx.Err())
			}
		case <-time.After(3 * time.Second):
			t.Fatal("old check remains alive")
		}
	}

	ctx := context.Background()
	if _, err := m.Handle(ctx, "connect", nil); err != nil {
		t.Fatal(err)
	}
	firstCheck, firstProcess := check(), m.process
	if _, err := m.Handle(ctx, "nodes.use", json.RawMessage(`{"id":"second"}`)); err != nil {
		t.Fatal(err)
	}
	secondCheck := check()
	assertCanceled(firstCheck)
	m.recordTunnelHealth(firstProcess, m.settings.Upstream, nil)
	if state := m.State(); state.SelectedNodeID != "second" || state.Runtime.Status != "connecting" || state.Runtime.ConnectedNodeID != "" {
		t.Fatalf("stale check changed the replacement candidate: %+v", state.Runtime)
	}
	if _, err := m.Handle(ctx, "stop", nil); err != nil {
		t.Fatal(err)
	}
	assertCanceled(secondCheck)
	if state := m.State(); state.Runtime.Status != "stopped" || state.Repair.Needed {
		t.Fatalf("canceling the pending check did not preserve explicit stop: %+v", state.Runtime)
	}
}

func TestDesktopInvalidNodeStartsRejectingCoreWithoutRepair(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "invalid", Config: "invalid profile"}})
	m.selected = "invalid"
	if _, err := m.Handle(context.Background(), "connect", nil); err != nil {
		t.Fatalf("invalid node prevented offline core startup: %v", err)
	}
	p := m.process
	state := m.State()
	if p == nil || state.Runtime.Status != "offline" || state.Runtime.Protected || state.Runtime.ConnectedNodeID != "" || state.Repair.Needed {
		t.Fatalf("missing unprotected offline capture: %+v", state)
	}
	raw, err := os.ReadFile(filepath.Join(m.dir, "runtime-offline", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Route struct {
			Rules []struct {
				Action string `json:"action"`
			} `json:"rules"`
		} `json:"route"`
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	reject := false
	for _, rule := range config.Route.Rules {
		reject = reject || rule.Action == "reject"
	}
	if !reject {
		t.Fatal("offline capture allowed VPN branches to fall back")
	}
	logged := false
	for _, entry := range m.ReadCoreLogs(0) {
		logged = logged || strings.Contains(entry.Message, "unsupported OpenVPN directive invalid")
	}
	if !logged {
		t.Fatal("invalid profile was omitted from core diagnostics")
	}
	if _, err = m.Repair(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	default:
		t.Fatal("repair left offline core running")
	}
}

func TestDesktopSettingsChangeAllowsOfflineRoutingWithoutSelectedNode(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "one", Status: "available"}, {ID: "two", Status: "available"}})
	if _, err := m.Handle(context.Background(), "connect", nil); err != nil {
		t.Fatal(err)
	}
	previous := m.process
	s := m.State().Settings
	s.RoutingPreset, s.PoolCapacity = "global", 1
	raw, _ := json.Marshal(s)
	if _, err := m.Handle(context.Background(), "settings.update", raw); err != nil {
		t.Fatalf("global mode without a selected node prevented settings change: %v", err)
	}
	state := m.State()
	if m.process == previous || state.Runtime.Status != "offline" || state.Settings.RoutingPreset != "global" || len(state.Nodes) != 1 || state.Repair.Needed {
		t.Fatalf("settings did not start an offline core in the requested mode: %+v", state)
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved persisted
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Settings.RoutingPreset != "global" || len(saved.Nodes) != 1 {
		t.Fatal("accepted settings or pool pruning were not persisted")
	}
}

func TestDesktopSettingsFailureAfterDetachKeepsOfflineEffectiveSettings(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	if _, err := m.Handle(context.Background(), "connect", nil); err != nil {
		t.Fatal(err)
	}
	previous := m.process
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "invalid", Config: "invalid profile"}})
	m.selected = "invalid"
	s := m.State().Settings
	s.RoutingPreset = "global"
	raw, _ := json.Marshal(s)
	if _, err := m.Handle(context.Background(), "settings.update", raw); err != nil {
		t.Fatalf("invalid node prevented offline core startup: %v", err)
	}
	state := m.State()
	if m.process == nil || m.process == previous || state.Runtime.Status != "offline" || state.Settings.RoutingPreset != "global" || state.Repair.Needed {
		t.Fatalf("offline replacement was labeled with stale settings: %+v", state)
	}
}

func TestDesktopTUNRequiresExternalProxyOwnerBeforeStartingCore(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	m.settings.TUNEnabled = true
	m.isAdmin = func() bool { return true }
	ownerUnavailable := errors.New("external mixed owner unresolved")
	m.resolveOwner = func(ctx context.Context, host string, port int) ([]string, error) {
		if host != "127.0.0.1" || port != 7890 {
			return nil, fmt.Errorf("unexpected upstream %s:%d", host, port)
		}
		return nil, ownerUnavailable
	}
	_, err := m.Handle(context.Background(), "connect", nil)
	if !errors.Is(err, ownerUnavailable) {
		t.Fatalf("desktop TUN skipped external proxy owner preflight: %v", err)
	}
	if m.process != nil {
		t.Fatal("TUN core started without external proxy exclusions")
	}
	if _, err = os.Stat(filepath.Join(m.dir, "runtime")); !os.IsNotExist(err) {
		t.Fatalf("unresolved owner wrote runtime configuration: %v", err)
	}
}

func TestDesktopTUNIncludesDiscoveredExternalProxyInDirectRouting(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	m.settings.TUNEnabled = true
	m.isAdmin = func() bool { return true }
	const savedPath = `C:\OldProxy\proxy.exe`
	const ownerPath = `C:\ExternalProxy\network-core.exe`
	m.settings.Upstream.ProcessPaths = []string{savedPath}
	m.resolveOwner = func(context.Context, string, int) ([]string, error) { return []string{ownerPath}, nil }
	// The helper refuses TUN, so this reaches real config generation but cannot
	// apply a TUN, proxy or route. The emitted process exclusion is the contract.
	if _, err := m.Handle(context.Background(), "connect", nil); err == nil {
		t.Fatal("test helper unexpectedly accepted TUN")
	}
	raw, err := os.ReadFile(filepath.Join(m.dir, "runtime", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Route struct {
			Rules []struct {
				ProcessPaths []string `json:"process_path"`
				Outbound     string   `json:"outbound"`
			} `json:"rules"`
		} `json:"route"`
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range config.Route.Rules {
		if len(rule.ProcessPaths) == 1 && rule.ProcessPaths[0] == ownerPath && rule.Outbound == "direct" {
			found = true
		}
	}
	if !found {
		t.Fatalf("desktop TUN lacks external proxy process exclusion: %s", raw)
	}
	if got := m.State().Settings.Upstream.ProcessPaths; len(got) != 1 || got[0] != savedPath {
		t.Fatalf("derived owner overwrote the saved compatibility field: %v", got)
	}
	const replacementOwner = `C:\ReplacementProxy\network-core.exe`
	m.resolveOwner = func(context.Context, string, int) ([]string, error) { return []string{replacementOwner}, nil }
	if _, err := m.Handle(context.Background(), "connect", nil); err == nil {
		t.Fatal("test helper unexpectedly accepted TUN")
	}
	raw, err = os.ReadFile(filepath.Join(m.dir, "runtime", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	for _, rule := range config.Route.Rules {
		for _, path := range rule.ProcessPaths {
			if path == ownerPath {
				t.Fatal("reconnect retained a stale auto-discovered direct exemption")
			}
			if path == replacementOwner && rule.Outbound == "direct" {
				return
			}
		}
	}
	t.Fatal("reconnect did not discover the replacement upstream owner")
}

func TestDesktopRemoteTUNIgnoresSavedProcessPaths(t *testing.T) {
	for _, host := range []string{"192.0.2.10", "proxy.example.org"} {
		t.Run(host, func(t *testing.T) {
			m := desktopWithHelperCore(t)
			m.settings.RoutingPreset = "global"
			m.settings.TUNEnabled = true
			m.isAdmin = func() bool { return true }
			const savedPath = `C:\OldProxy\proxy.exe`
			m.settings.Upstream.Host = host
			m.settings.Upstream.ProcessPaths = []string{savedPath}
			m.resolveOwner = func(ctx context.Context, gotHost string, port int) ([]string, error) {
				if gotHost != host || port != m.settings.Upstream.Port {
					t.Fatalf("owner resolution got %s:%d, want %s:%d", gotHost, port, host, m.settings.Upstream.Port)
				}
				return nil, nil // The Windows resolver finds no local owner for a remote host.
			}
			// The helper refuses TUN after config generation, so no host route is changed.
			if _, err := m.Handle(context.Background(), "connect", nil); err == nil {
				t.Fatal("test helper unexpectedly accepted TUN")
			}
			raw, err := os.ReadFile(filepath.Join(m.dir, "runtime", "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Route struct {
					AutoDetectInterface bool                         `json:"auto_detect_interface"`
					Rules               []map[string]json.RawMessage `json:"rules"`
				} `json:"route"`
				DNS struct {
					Rules []map[string]json.RawMessage `json:"rules"`
				} `json:"dns"`
			}
			if err := json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			if !config.Route.AutoDetectInterface {
				t.Fatal("remote proxy needs interface-bound sing-box dials under TUN")
			}
			for _, rules := range [][]map[string]json.RawMessage{config.Route.Rules, config.DNS.Rules} {
				for _, rule := range rules {
					if _, ok := rule["process_path"]; ok {
						t.Fatal("remote proxy retained a process exemption")
					}
				}
			}
			if got := m.State().Settings.Upstream.ProcessPaths; len(got) != 1 || got[0] != savedPath {
				t.Fatalf("legacy setting was not preserved: %v", got)
			}
		})
	}
}
