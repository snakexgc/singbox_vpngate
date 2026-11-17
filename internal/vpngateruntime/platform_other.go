//go:build !windows

package vpngateruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func resolveUpstreamOwner(context.Context, string, int) ([]string, error) {
	return nil, fmt.Errorf("VPNGate 接管仅支持 Windows")
}

type systemProxySnapshot struct{}

func desktopElevated() bool { return false }

func hideCommand(*exec.Cmd)                   {}
func ownProcess(*exec.Cmd) (func(), error)    { return func() {}, nil }
func replaceFile(from, to string) error       { return os.Rename(from, to) }
func (m *Manager) applySystemProxy(int) error { return fmt.Errorf("VPNGate 接管仅支持 Windows") }
func (m *Manager) restoreSystemProxy() error  { return nil }
