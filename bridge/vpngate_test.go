package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"guiforcores/internal/vpngate"
)

type fakeDesktopRuntime struct {
	state   vpngate.State
	methods []string
	closed  bool
	err     error
}

func (f *fakeDesktopRuntime) State() vpngate.State { return f.state }
func (f *fakeDesktopRuntime) Handle(_ context.Context, method string, _ json.RawMessage) (any, error) {
	f.methods = append(f.methods, method)
	if f.err != nil {
		return nil, f.err
	}
	if method == "connect" {
		f.state.Runtime.Status = "connected"
	}
	if method == "repair" {
		f.state.Runtime.Status = "stopped"
		f.state.Repair.Message = "repair complete"
	}
	return f.state, nil
}
func (f *fakeDesktopRuntime) Close() { f.closed = true }

func TestDesktopConnectDoesNotRequireInstalledService(t *testing.T) {
	m := &fakeDesktopRuntime{}
	result, err := dispatchDesktop(context.Background(), m, "connect", nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.(vpngate.State).Runtime.Status != "connected" || len(m.methods) != 1 || m.methods[0] != "connect" {
		t.Fatal("desktop request routed through service")
	}
}
func TestLegacyRepairRequiresExplicitElevationBeforeAnyCleanup(t *testing.T) {
	m := &fakeDesktopRuntime{}
	called := 0
	cleanup := func() error { called++; return nil }
	if _, err := dispatchDesktop(context.Background(), m, "repair", nil, true, false, cleanup); err == nil || !strings.Contains(err.Error(), "管理员") {
		t.Fatalf("missing actionable elevation error: %v", err)
	}
	if called != 0 || len(m.methods) != 0 {
		t.Fatal("changed runtime before legacy cleanup authorization")
	}
	if _, err := dispatchDesktop(context.Background(), m, "repair", nil, true, true, cleanup); err != nil {
		t.Fatal(err)
	}
	if called != 1 || len(m.methods) != 1 || m.methods[0] != "repair" {
		t.Fatal("explicit repair did not clean legacy state")
	}
}
func TestLegacyCleanupErrorsAreNotHiddenByLocalRepair(t *testing.T) {
	expected := errors.New("owned legacy cleanup failed")
	_, err := dispatchDesktop(context.Background(), &fakeDesktopRuntime{}, "repair", nil, true, true, func() error { return expected })
	if !errors.Is(err, expected) {
		t.Fatalf("cleanup error hidden: %v", err)
	}
}
func TestLocalRepairFailureDoesNotSkipAuthorizedLegacyCleanup(t *testing.T) {
	localErr := errors.New("local proxy snapshot failed")
	legacyErr := errors.New("legacy cleanup failed")
	called := false
	_, err := dispatchDesktop(context.Background(), &fakeDesktopRuntime{err: localErr}, "repair", nil, true, true, func() error { called = true; return legacyErr })
	if !called || !errors.Is(err, localErr) || !errors.Is(err, legacyErr) {
		t.Fatalf("repair skipped a separately authorized recovery path: %v", err)
	}
}
func TestLegacyStateOffersRepairInsteadOfServiceInstallation(t *testing.T) {
	result, err := dispatchDesktop(context.Background(), &fakeDesktopRuntime{}, "state", nil, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := result.(vpngate.State)
	if !state.Repair.Needed || !state.Repair.RequiresAdmin || state.Runtime.Protected {
		t.Fatal("legacy state does not offer scoped repair")
	}
}
func TestDraftMigrationPreservesSettingsAndPrivateNodes(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "draft.json")
	target := filepath.Join(dir, "desktop", "state.json")
	s := vpngate.DefaultSettings()
	s.Upstream.Port = 10808
	original := struct {
		Settings vpngate.Settings `json:"settings"`
		Nodes    []vpngate.Node   `json:"nodes"`
		Selected string           `json:"selectedNodeId"`
	}{s, []vpngate.Node{{ID: "saved", Config: "private profile", Status: "available"}}, "saved"}
	data, _ := json.Marshal(original)
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateDesktopState(target, []string{source}); err != nil {
		t.Fatal(err)
	}
	migrated, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		Settings vpngate.Settings `json:"settings"`
		Nodes    []vpngate.Node   `json:"nodes"`
	}
	if err = json.Unmarshal(migrated, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Settings.Upstream.Port != 10808 || len(actual.Nodes) != 1 || actual.Nodes[0].Config != "private profile" {
		t.Fatal("migration lost user data")
	}
	unchanged, _ := os.ReadFile(source)
	if string(unchanged) != string(data) {
		t.Fatal("migration changed legacy source")
	}
}
func TestMigrationRecoversLegacyNodesWithoutOverwritingLocalSettings(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "legacy.json")
	target := filepath.Join(dir, "state.json")
	old := vpngate.DefaultSettings()
	old.Upstream.Port = 7890
	local := old
	local.Upstream.Port = 10808
	a, _ := json.Marshal(map[string]any{"settings": old, "nodes": []vpngate.Node{{ID: "old", Config: "profile"}}, "selectedNodeId": "old"})
	b, _ := json.Marshal(map[string]any{"settings": local, "nodes": []vpngate.Node{}})
	os.WriteFile(source, a, 0600)
	os.WriteFile(target, b, 0600)
	if err := migrateDesktopState(target, []string{source}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	var actual struct {
		Settings vpngate.Settings `json:"settings"`
		Nodes    []vpngate.Node   `json:"nodes"`
	}
	json.Unmarshal(data, &actual)
	if actual.Settings.Upstream.Port != 10808 || len(actual.Nodes) != 1 {
		t.Fatal("legacy migration overwrote local preferences or lost nodes")
	}
}
