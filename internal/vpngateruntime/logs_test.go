package vpngateruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedLogLevelPersistsAndAppliesOnNextCoreStart(t *testing.T) {
	m := desktopWithHelperCore(t)
	m.settings.RoutingPreset = "direct"
	t.Setenv("VPNGATE_TEST_CORE_LOG_ERROR", "+0800 2026-11-18 12:00:33 WARN [test] core output")
	ctx := context.Background()
	if _, err := m.Handle(ctx, "connect", nil); err != nil {
		t.Fatal(err)
	}
	before := m.process
	result, err := m.Handle(ctx, "logs.level", json.RawMessage(`{"level":"debug"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.process != before || m.State().Runtime.LogLevel != "info" || m.State().LogLevel != "debug" {
		t.Fatalf("setting changed the running core or failed to stage debug: %#v", result)
	}
	if _, err := m.Handle(ctx, "logs.level", json.RawMessage(`{"level":"invalid"}`)); err == nil {
		t.Fatal("accepted an unsupported level")
	}
	if _, err := m.Handle(ctx, "stop", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Handle(ctx, "connect", nil); err != nil {
		t.Fatal(err)
	}
	if got := m.State().Runtime.LogLevel; got != "debug" {
		t.Fatalf("restarted core level = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "runtime", "config.json"))
	if err != nil || !strings.Contains(string(data), `"level": "debug"`) {
		t.Fatalf("core config did not receive debug level: %v", err)
	}
	logs := m.ReadCoreLogs(0)
	if len(logs) != 2 || logs[0].Level != "warn" || logs[0].Message != "[test] core output" {
		t.Fatalf("core output should survive stop and restart: %+v", logs)
	}
	if logs[0].Time != "2026-11-18T04:00:33Z" {
		t.Fatalf("log timestamp = %s", logs[0].Time)
	}
	if logs := m.ReadCoreLogs(logs[0].ID); len(logs) != 1 {
		t.Fatalf("cursor did not skip previously read entries: %+v", logs)
	}
	saved, err := New(m.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.State().LogLevel; got != "debug" {
		t.Fatalf("migrated persisted log level = %q", got)
	}
	saved.Close()
}
