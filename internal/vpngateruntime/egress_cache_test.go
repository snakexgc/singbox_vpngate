package vpngateruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"guiforcores/internal/vpngate"
)

func TestDesktopReusesPersistedEgressAfterPortableRelocation(t *testing.T) {
	settings := vpngate.DefaultSettings()
	settings.CorePath = filepath.Join("old-build", "core", "sing-box.exe")
	settings.Upstream.ProcessPaths = []string{"previous-tun-owner.exe"}
	settings.RulesAutoUpdate = false
	checked := time.Now().Add(-time.Hour).Truncate(time.Second)
	node := measuredCacheFixture(settings, vpngate.Node{ID: "cached", Config: "candidate profile", Status: "available"}, checked)
	// Replicate the key stored by older builds, including the old executable path.
	encoded, err := json.Marshal(struct {
		Config   string
		Upstream vpngate.Upstream
		CorePath string
	}{node.Config, settings.Upstream, settings.CorePath})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	node.EgressCacheKey = hex.EncodeToString(digest[:])
	state, err := json.Marshal(persisted{Settings: settings, Nodes: []vpngate.Node{node}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = atomicWrite(filepath.Join(dir, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	manager, err := NewDesktop(dir, filepath.Join("new-build", "core", "sing-box.exe"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	actual := manager.pool.Snapshot()[0]
	if actual.Status != "available" || actual.EgressIP != node.EgressIP || actual.EgressRegion != node.EgressRegion || actual.EgressCheckedAt != node.EgressCheckedAt {
		t.Fatalf("relocation discarded the measured exit: status=%s ip=%q region=%q checked=%q", actual.Status, actual.EgressIP, actual.EgressRegion, actual.EgressCheckedAt)
	}
	if !validEgressCache(manager.settings, actual, checked.Add(egressCacheTTL-time.Second)) {
		t.Fatal("relocated result does not reuse the original seven-day cache")
	}
	if validEgressCache(manager.settings, actual, checked.Add(egressCacheTTL)) {
		t.Fatal("relocation extended the cache lifetime")
	}
}
