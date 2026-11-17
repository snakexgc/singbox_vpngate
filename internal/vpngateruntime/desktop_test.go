package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"guiforcores/internal/vpngate"
)

func TestDesktopPinsPortableCoreAndConnectHasNoServiceGate(t *testing.T) {
	dir := t.TempDir()
	old, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	old.settings.CorePath = "legacy-installed-core.exe"
	old.mu.Lock()
	err = old.persistLocked()
	old.mu.Unlock()
	old.Close()
	if err != nil {
		t.Fatal(err)
	}
	portable := filepath.Join(dir, "portable", "sing-box.exe")
	m, err := NewDesktop(dir, portable)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := m.State().Settings
	s.TUNEnabled, s.SystemProxyEnabled = false, false
	s.RoutingPreset, s.Upstream.Port, s.CorePath = "direct", 7890, "editor-overridden.exe"
	raw, _ := json.Marshal(s)
	if _, err = m.Handle(context.Background(), "settings.update", raw); err != nil {
		t.Fatal(err)
	}
	if got := m.State().Settings.CorePath; got != portable {
		t.Fatalf("portable core replaced: %q", got)
	}
	_, err = m.Handle(context.Background(), "connect", nil)
	if err == nil || !strings.Contains(err.Error(), "版本") || strings.Contains(err.Error(), "保护服务") {
		t.Fatalf("desktop connect did not reach portable core validation: %v", err)
	}
	if m.State().Service.Installed || m.State().Runtime.Protected {
		t.Fatal("desktop claimed a service or firewall protection")
	}
}

func TestDesktopTUNPreflightRequiresElevatedGUIBeforeCoreOrNetworking(t *testing.T) {
	m, err := NewDesktop(t.TempDir(), "missing-core.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.isAdmin = func() bool { return false }
	m.settings.Upstream.Port = 7890
	m.settings.RoutingPreset = "direct"
	if !m.State().Runtime.RequiresAdmin {
		t.Fatal("TUN did not advertise elevation")
	}
	_, err = m.Handle(context.Background(), "connect", nil)
	if err == nil || !strings.Contains(err.Error(), "管理员") || !strings.Contains(err.Error(), "TUN") {
		t.Fatalf("wrong privilege preflight: %v", err)
	}
	if _, err = os.Stat(filepath.Join(m.dir, "runtime")); !os.IsNotExist(err) {
		t.Fatalf("preflight touched runtime: %v", err)
	}
	if m.process != nil || m.proxySnapshot != nil {
		t.Fatal("privilege rejection changed networking ownership")
	}
}

func TestDesktopRepairPreservesUserDataAndRemovesOnlyOwnedRuntime(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"runtime/config.json", "runtime-offline/process.log", "probes/one/config.json", "unrelated/keep.txt"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep-or-clean"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := NewDesktop(dir, filepath.Join(dir, "portable.exe"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if !m.State().Repair.Needed {
		t.Fatal("interrupted runtime was not reported")
	}
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "saved", Config: "PRIVATE PROFILE", Status: "available"}})
	m.selected = "saved"
	m.settings.MixedPort = 23456
	m.runtime.Status, m.runtime.Message = "error", "old failure"
	rulePath := filepath.Join(dir, "rules", "cn.srs")
	ruleBefore, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	m.proxySnapshot = &systemProxySnapshot{}
	m.restoreProxy = func() error { restored = true; m.proxySnapshot = nil; return nil }
	result, err := m.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !restored || result.Runtime.Status != "stopped" || result.Runtime.Message != "" || result.Repair.Needed || result.Repair.Message == "" {
		t.Fatalf("incomplete repair: %+v", result)
	}
	for _, name := range []string{"runtime", "runtime-offline", "probes"} {
		if _, err = os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("left owned %s: %v", name, err)
		}
	}
	if _, err = os.Stat(filepath.Join(dir, "unrelated", "keep.txt")); err != nil {
		t.Fatal(err)
	}
	ruleAfter, _ := os.ReadFile(rulePath)
	if string(ruleAfter) != string(ruleBefore) {
		t.Fatal("repair changed valid rules")
	}
	m.Close()
	reloaded, err := NewDesktop(dir, filepath.Join(dir, "portable.exe"))
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if reloaded.settings.MixedPort != 23456 || reloaded.selected != "saved" || len(reloaded.pool.Snapshot()) != 1 || reloaded.pool.Snapshot()[0].Config != "PRIVATE PROFILE" {
		t.Fatal("repair lost settings, selection or private node data")
	}
}

func TestDesktopRepairWaitsForCanceledJobsBeforeCleanup(t *testing.T) {
	m, err := NewDesktop(t.TempDir(), "portable.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, job, err := m.beginJob("test")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	go func() { <-ctx.Done(); <-release; m.finishJob(job, ctx.Err()) }()
	done := make(chan error, 1)
	go func() { _, e := m.Repair(context.Background()); done <- e }()
	<-ctx.Done()
	select {
	case err = <-done:
		t.Fatalf("repair returned before job cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if m.State().Jobs[0].Status != "canceled" {
		t.Fatal("job not finished before repair")
	}
}

func TestDesktopCoreExitRestoresProxyAndPreservesReplacementIdentity(t *testing.T) {
	m, err := NewDesktop(t.TempDir(), "portable.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	log, err := os.CreateTemp(t.TempDir(), "core-log")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	p := &coreProcess{cmd: &exec.Cmd{}, done: done, log: log}
	m.process = p
	m.runtime = vpngate.RuntimeState{Status: "connected", ConnectedNodeID: "chosen"}
	m.proxySnapshot = &systemProxySnapshot{}
	restored := 0
	m.restoreProxy = func() error { restored++; m.proxySnapshot = nil; return nil }
	m.watchCore(p)
	state := m.State()
	if restored != 1 || state.Runtime.Status != "error" || state.Runtime.Protected || !state.Repair.Needed || state.Runtime.ConnectedNodeID != "" {
		t.Fatalf("crash falsely retained protection or ownership: %+v", state)
	}
	replacement := &coreProcess{}
	m.process = replacement
	m.runtime = vpngate.RuntimeState{Status: "connected", ConnectedNodeID: "replacement"}
	m.watchCore(p)
	if restored != 1 || m.process != replacement || m.runtime.ConnectedNodeID != "replacement" {
		t.Fatal("stale core exit touched replacement")
	}
	m.process = nil
}

func TestDesktopRepairFailureRetainsSnapshotButClearsStoppedConnectionIdentity(t *testing.T) {
	m, err := NewDesktop(t.TempDir(), "portable.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.runtime = vpngate.RuntimeState{Status: "connected", ConnectedNodeID: "no-longer-running"}
	m.proxySnapshot = &systemProxySnapshot{}
	m.restoreProxy = func() error { return errors.New("proxy access denied") }
	state, err := m.Repair(context.Background())
	if err == nil || !state.Repair.Needed || state.Runtime.Status != "error" || state.Runtime.ConnectedNodeID != "" || m.proxySnapshot == nil {
		t.Fatalf("repair failure reported stale connection or discarded recovery: state=%+v err=%v", state, err)
	}
	m.restoreProxy = func() error { m.proxySnapshot = nil; return nil }
	if state, err = m.Repair(context.Background()); err != nil || state.Repair.Needed || state.Runtime.Status != "stopped" {
		t.Fatalf("repair retry failed: %+v %v", state, err)
	}
}

func TestDesktopRepairPermissionFailureOffersElevation(t *testing.T) {
	m, err := NewDesktop(t.TempDir(), "portable.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.isAdmin = func() bool { return false }
	m.restoreProxy = func() error { return os.ErrPermission }
	state, err := m.Repair(context.Background())
	if !errors.Is(err, os.ErrPermission) || !state.Repair.Needed || !state.Repair.RequiresAdmin {
		t.Fatalf("permission error lacks repair elevation action: %+v %v", state.Repair, err)
	}
	m.restoreProxy = func() error { return nil }
}

func TestDesktopStopWaitsForProbeCleanupAndBlocksNewJobs(t *testing.T) {
	m, err := NewDesktop(t.TempDir(), "portable.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	probeDir := filepath.Join(m.dir, "probes", "running")
	if err = os.MkdirAll(probeDir, 0700); err != nil {
		t.Fatal(err)
	}
	probeFile := filepath.Join(probeDir, "process.log")
	if err = os.WriteFile(probeFile, []byte("owned active probe"), 0600); err != nil {
		t.Fatal(err)
	}
	jobCtx, job, err := m.beginJob("test")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	opAvailable := make(chan bool, 1)
	go func() {
		<-jobCtx.Done()
		<-release
		// Rules completion can acquire op; Stop must wait outside that lock.
		available := m.op.TryLock()
		if available {
			m.op.Unlock()
		}
		opAvailable <- available
		m.finishJob(job, jobCtx.Err())
	}()
	done := make(chan error, 1)
	go func() { _, e := m.Handle(context.Background(), "stop", nil); done <- e }()
	<-jobCtx.Done()
	select {
	case err = <-done:
		t.Fatalf("stop returned before probe cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err = os.Stat(probeFile); err != nil {
		t.Fatalf("stop removed probe files while job owns them: %v", err)
	}
	if ctx, admitted, e := m.beginJob("rules"); e == nil {
		m.finishJob(admitted, context.Canceled)
		_ = ctx
		t.Fatal("stop admitted a new job during cleanup")
	}
	close(release)
	if !<-opAvailable {
		t.Error("stop held operation lock while waiting for canceled job")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(m.dir, "probes")); !os.IsNotExist(err) {
		t.Fatalf("stop retained completed probe files: %v", err)
	}
}
