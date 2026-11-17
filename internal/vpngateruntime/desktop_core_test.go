package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

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

func TestDesktopNodeFailureKeepsRejectingOfflineCoreUntilExplicitRepair(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "invalid", Config: "invalid profile"}})
	m.selected = "invalid"
	if _, err := m.Handle(context.Background(), "connect", nil); err == nil {
		t.Fatal("invalid node unexpectedly connected")
	}
	p := m.process
	state := m.State()
	if p == nil || state.Runtime.Status != "degraded" || state.Runtime.Protected || state.Runtime.ConnectedNodeID != "" || !state.Repair.Needed {
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
	if _, err = m.Repair(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	default:
		t.Fatal("repair left offline core running")
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
	const ownerPath = `C:\ExternalProxy\network-core.exe`
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
	if got := m.State().Settings.Upstream.ProcessPaths; len(got) != 1 || got[0] != ownerPath {
		t.Fatalf("discovered owner not retained in settings: %v", got)
	}
}
