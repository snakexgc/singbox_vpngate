//go:build windows && !amd64 && !arm64

package guardian

import "context"

type unsupportedBackend struct{}

func newBackend() backend                                       { return unsupportedBackend{} }
func (unsupportedBackend) validate(Policy) error                { return ErrUnavailable }
func (unsupportedBackend) interfaceLUID(string) (uint64, error) { return 0, ErrUnavailable }
func (unsupportedBackend) servicePath() string                  { return servicePath() }
func (unsupportedBackend) apply(context.Context, []rule) error  { return ErrUnavailable }
func (unsupportedBackend) remove(context.Context) error         { return ErrUnavailable }
func (unsupportedBackend) status() ProtectionStatus {
	return ProtectionStatus{Message: "64-bit Windows is required for WFP protection"}
}
