package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"guiforcores/internal/guardian"
	"guiforcores/internal/vpngateruntime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--install":
			sid := ""
			for i := 2; i+1 < len(os.Args); i++ {
				if os.Args[i] == "--owner-sid" {
					sid = os.Args[i+1]
				}
			}
			if sid != "" {
				return guardian.InstallForUser(sid)
			}
			return guardian.Install()
		case "--recover":
			return runEmergencyAction("recover", func() error { return vpngateruntime.RestoreProxySnapshot(guardian.DataDir()) }, guardian.Recover)
		case "--uninstall":
			return runEmergencyAction("uninstall", func() error { return vpngateruntime.RestoreProxySnapshot(guardian.DataDir()) }, guardian.Uninstall)
		case "--status":
			return json.NewEncoder(os.Stdout).Encode(guardian.ServiceStatus())
		case "--service": // SCM authentication below is mandatory even with this flag.
		default:
			return fmt.Errorf("usage: vpngate-service --install|--recover|--uninstall|--status")
		}
	}
	if !guardian.IsService() {
		return fmt.Errorf("service must be started by Windows Service Control Manager")
	}
	if _, err := guardian.RestrictProcessTree(); err != nil {
		return fmt.Errorf("isolate managed process tree: %w", err)
	}
	guard := guardian.NewProtection()
	manager, err := vpngateruntime.New(guardian.DataDir(), guard)
	if err != nil {
		return err
	}
	defer manager.Close()
	return guardian.RunService(func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
		if method == "service.status" {
			return guardian.ServiceInfo{Installed: true, Running: true, Protected: guard.Status().Protected}, nil
		}
		return manager.Handle(ctx, method, payload)
	})
}

// Proxy snapshot corruption must never disable the separately authorized
// emergency path that removes this application's global blocking filters.
func runEmergencyAction(action string, restore, cleanup func() error) error {
	proxyErr := restore()
	cleanupErr := cleanup()
	if proxyErr != nil {
		proxyErr = fmt.Errorf("system proxy restoration failed; manually restore Windows proxy settings (owned filter cleanup was still attempted): %w", proxyErr)
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("%s owned service/filter cleanup failed: %w", action, cleanupErr)
	}
	return errors.Join(proxyErr, cleanupErr)
}
