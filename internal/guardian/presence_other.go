//go:build !windows

package guardian

func LegacyServiceInstalled() bool { return false }
