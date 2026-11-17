package main

import (
	"errors"
	"strings"
	"testing"
)

func TestEmergencyCleanupRunsDespiteProxyRestoreFailure(t *testing.T) {
	for _, action := range []string{"recover", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			order := []string{}
			restoreErr := errors.New("corrupt proxy snapshot")
			err := runEmergencyAction(action, func() error { order = append(order, "restore"); return restoreErr }, func() error { order = append(order, "cleanup"); return nil })
			if len(order) != 2 || order[0] != "restore" || order[1] != "cleanup" {
				t.Fatal("owned filter cleanup skipped after failed restore")
			}
			if !errors.Is(err, restoreErr) || !strings.Contains(err.Error(), "manually restore Windows proxy") {
				t.Fatalf("missing actionable restore error: %v", err)
			}
		})
	}
}
func TestEmergencyCleanupAggregatesBothFailures(t *testing.T) {
	restoreErr, cleanupErr := errors.New("hive unavailable"), errors.New("administrator required")
	err := runEmergencyAction("recover", func() error { return restoreErr }, func() error { return cleanupErr })
	if !errors.Is(err, restoreErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("lost failure: %v", err)
	}
}
func TestEmergencyCleanupSuccess(t *testing.T) {
	if e := runEmergencyAction("recover", func() error { return nil }, func() error { return nil }); e != nil {
		t.Fatal(e)
	}
}
