//go:build !windows

package guardian

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type unavailableBackend struct{}

func newBackend() backend                                       { return unavailableBackend{} }
func (unavailableBackend) validate(Policy) error                { return ErrUnavailable }
func (unavailableBackend) interfaceLUID(string) (uint64, error) { return 0, ErrUnavailable }
func (unavailableBackend) servicePath() string                  { return "" }
func (unavailableBackend) apply(context.Context, []rule) error  { return ErrUnavailable }
func (unavailableBackend) remove(context.Context) error         { return ErrUnavailable }
func (unavailableBackend) status() ProtectionStatus {
	return ProtectionStatus{Message: ErrUnavailable.Error()}
}
func DataDir() string         { return filepath.Join(os.TempDir(), ServiceName) }
func ManagedCorePath() string { return "" }
func OwnerSID() string        { return "" }
func Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, ErrUnavailable
}
func Serve(context.Context, Handler) error { return ErrUnavailable }
func RunService(Handler) error             { return ErrUnavailable }
func Install() error                       { return ErrUnavailable }
func InstallForUser(string) error          { return ErrUnavailable }
func Uninstall() error                     { return ErrUnavailable }
func Recover() error                       { return ErrUnavailable }
func ServiceStatus() ServiceInfo {
	return ServiceInfo{RequiresAdmin: true, Message: ErrUnavailable.Error()}
}
func Elevate(string) error                 { return ErrUnavailable }
func IsService() bool                      { return false }
func RestrictProcessTree() (func(), error) { return func() {}, ErrUnavailable }
