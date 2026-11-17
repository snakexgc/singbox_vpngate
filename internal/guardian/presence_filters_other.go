//go:build !windows || (!amd64 && !arm64)

package guardian

import "fmt"

func LegacyFilterPresence() (bool, error) {
	return false, fmt.Errorf("legacy WFP inspection is unavailable on this platform")
}
