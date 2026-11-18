package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"guiforcores/internal/vpngate"
)

func TestUnprivilegedDraftCannotStartTakeover(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Handle(context.Background(), "connect", nil); err == nil || !strings.Contains(err.Error(), "保护服务") {
		t.Fatalf("unguarded start accepted: %v", err)
	}
	if m.State().Runtime.Protected {
		t.Fatal("draft claimed firewall protection")
	}
}
func TestStateOmitsPrivateOpenVPNMaterialButPersistenceRetainsIt(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "one", Config: "PRIVATE CLIENT KEY", Status: "untested"}})
	m.mu.Lock()
	err = m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if m.State().Nodes[0].Config != "" {
		t.Fatal("private configuration exposed in GUI snapshot")
	}
	m.Close()
	restored, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.pool.Snapshot()[0].Config != "PRIVATE CLIENT KEY" {
		t.Fatal("lost private config during state serialization")
	}
}
func TestCanceledProbeJobAndRefreshCannotRunConcurrently(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, j, err := m.beginJob("test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.beginJob("refresh"); err == nil {
		t.Fatal("refresh raced test pool mutations")
	}
	if _, err = m.Handle(context.Background(), "jobs.cancel", json.RawMessage(`{"id":"`+j.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	m.finishJob(j, ctx.Err())
	if m.State().Jobs[0].Status != "canceled" {
		t.Fatal("cancellation marked as node failure")
	}
}
func TestMalformedPersistedStateIsNotSilentlyReset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if m, err := New(dir, nil); err == nil {
		m.Close()
		t.Fatal("corrupt persisted state silently discarded")
	}
}
func TestFullAutomaticRefreshContactsConfiguredProxyWhenScheduled(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.settings.PoolCapacity = 1
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "existing", Status: "untested"}})
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodConnect || r.Host != "www.vpngate.net:443" {
			t.Errorf("unexpected refresh route: %s %s", r.Method, r.Host)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()
	m.settings.Upstream = diagnosticUpstream(t, upstream.URL)
	if err = m.refresh(context.Background(), &vpngate.Job{}, false); err == nil || requests.Load() != 1 {
		t.Fatalf("full pool skipped the scheduled feed refresh: requests=%d, error=%v", requests.Load(), err)
	}
	if len(m.pool.Snapshot()) != 1 || m.pool.Snapshot()[0].ID != "existing" {
		t.Fatal("failed automatic refresh replaced the existing full pool")
	}
}
func TestRestartPersistenceFailureRetainsProcessOwnership(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	blocker := filepath.Join(m.dir, "blocker")
	if err = os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	m.dir = blocker
	previous := &coreProcess{}
	m.process = previous
	m.runtime = vpngate.RuntimeState{Status: "connected", ConnectedNodeID: "working"}
	_, err = m.detachForRestart(vpngate.RuntimeState{Status: "starting"}, m.settings)
	if err == nil || m.process != previous || m.runtime.Status != "connected" {
		t.Fatalf("lost core ownership after write failure: %v", err)
	}
	m.process = nil
}
func TestRefreshProbesNeverMixOldAndNewConfigForStableIdentity(t *testing.T) {
	old := vpngate.Node{ID: "same", ConfigHash: "old", Status: "untested"}
	updated := old
	updated.ConfigHash = "new"
	targets := refreshProbeTargets([]vpngate.Node{updated}, []vpngate.Node{old}, vpngate.DefaultSettings())
	if len(targets) != 1 || targets[0].ConfigHash != "new" {
		t.Fatalf("mixed profiles in one test job: %+v", targets)
	}
}
func TestChangedCandidateProbeDoesNotClearPreviousProfileHealth(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	latency := int64(25)
	old := vpngate.Node{ID: "same", ConfigHash: "old", Status: "available", LatencyMs: &latency}
	m.pool = vpngate.NewPool([]vpngate.Node{old})
	candidate := old
	candidate.ConfigHash = "new"
	candidate.Config = "invalid profile"
	s := vpngate.DefaultSettings()
	s.TestConcurrency = 1
	_, _ = m.probeBatch(context.Background(), &vpngate.Job{}, s, []vpngate.Node{candidate})
	actual := m.pool.Snapshot()[0]
	if actual.Status != "available" || actual.LatencyMs == nil || *actual.LatencyMs != 25 {
		t.Fatalf("changed candidate poisoned old health: %+v", actual)
	}
}
func TestInvalidNodeProfileIsNotACommonEnvironmentFailure(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := vpngate.DefaultSettings()
	s.Upstream.Port = 7890
	result := m.probe(context.Background(), s, vpngate.Node{ID: "bad", Config: "invalid profile"})
	if result.Status != "failed" || result.FailureKind != "node" {
		t.Fatalf("bad node aborted environment: %+v", result)
	}
}
func TestTunnelHealthReportsFailureWithoutChangingExitOrTouchingReplacement(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p := &coreProcess{nodeID: "chosen"}
	m.process = p
	m.runtime = vpngate.RuntimeState{Status: "connected", ConnectedNodeID: "chosen"}
	m.recordTunnelHealth(p, vpngate.Upstream{}, errors.New("upstream lost"))
	if m.runtime.Status != "protected" || m.runtime.ConnectedNodeID != "chosen" {
		t.Fatal("failure silently changed selected exit")
	}
	m.recordTunnelHealth(&coreProcess{}, vpngate.Upstream{}, nil)
	if m.runtime.Status != "protected" {
		t.Fatal("old session health changed replacement state")
	}
	m.recordTunnelHealth(p, vpngate.Upstream{}, nil)
	if m.runtime.Status != "connected" {
		t.Fatal("same selected tunnel recovery not reflected")
	}
	m.process = nil
}
func TestBulkDeletePreservesNodeWhileItsConnectionIsStarting(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "connecting", Status: "failed"}})
	m.selected = "connecting"
	m.runtime.Status = "starting"
	if _, err = m.Handle(context.Background(), "nodes.deleteFailed", nil); err != nil {
		t.Fatal(err)
	}
	if len(m.pool.Snapshot()) != 1 {
		t.Fatal("node removed while its connection was starting")
	}
}
