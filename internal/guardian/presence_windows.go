//go:build windows

package guardian

// LegacyServiceInstalled is an SCM-only presence check. The desktop GUI must
// never depend on, connect to, or wait for the old service's pipe when polling.
func LegacyServiceInstalled() bool {
	service, err := openQueryService()
	if err != nil {
		return false
	}
	service.Close()
	return true
}
