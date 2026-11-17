//go:build windows && (amd64 || arm64)

package guardian

import (
	"fmt"
	"golang.org/x/sys/windows"
)

// LegacyFilterPresence distinguishes absence from an inspection error. It is
// read-only and is used only during an explicit administrator repair action.
func LegacyFilterPresence() (bool, error) {
	h, err := openWFP()
	if err != nil {
		return false, err
	}
	defer closeWFP(h)
	var provider *wfpProvider
	code := wfpCode("FwpmProviderGetByKey0", uintptr(h), ptr(&providerKey), ptr(&provider))
	if code == uint32(windows.FWP_E_PROVIDER_NOT_FOUND) {
		return false, nil
	}
	if code != 0 {
		return false, fmt.Errorf("inspect legacy WFP provider: 0x%08x", code)
	}
	if provider != nil {
		defer wfpCall("FwpmFreeMemory0", ptr(&provider))
	}
	filters, err := ownedFilters(h)
	if err != nil {
		return false, err
	}
	return len(filters) > 0, nil
}
