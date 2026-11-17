//go:build windows && (amd64 || arm64)

package guardian

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsSDKLayouts(t *testing.T) {
	for name, pair := range map[string][2]uintptr{"FWP_BYTE_BLOB": {unsafe.Sizeof(wfpBlob{}), 16}, "FWP_VALUE0": {unsafe.Sizeof(wfpValue{}), 16}, "FWPM_FILTER_CONDITION0": {unsafe.Sizeof(wfpCondition{}), 40}, "FWPM_ACTION0": {unsafe.Sizeof(wfpAction{}), 20}, "FWPM_FILTER0": {unsafe.Sizeof(wfpFilter{}), 200}, "FWPM_PROVIDER0": {unsafe.Sizeof(wfpProvider{}), 64}, "FWPM_SUBLAYER0": {unsafe.Sizeof(wfpSublayer{}), 72}, "FWPM_FILTER_ENUM_TEMPLATE0": {unsafe.Sizeof(wfpEnumTemplate{}), 72}} {
		if pair[0] != pair[1] {
			t.Errorf("%s: got %d, SDK x64 ABI requires %d", name, pair[0], pair[1])
		}
	}
	f := wfpFilter{}
	if unsafe.Offsetof(f.Context) != 152 || unsafe.Offsetof(f.ID) != 176 || unsafe.Offsetof(f.Conditions) != 120 {
		t.Fatal("FWPM_FILTER0 union/pointer alignment is wrong")
	}
}
func TestFilterOwnershipRequiresProviderSublayerAndMarker(t *testing.T) {
	f := wfpFilter{Provider: &providerKey, SubLayer: sublayerKey, Data: blob(ownership)}
	if !owned(&f) {
		t.Fatal("owned filter rejected")
	}
	wrong := guid("b297d27e-869a-4cdf-898e-081239b45155")
	f.Provider = &wrong
	if owned(&f) {
		t.Fatal("foreign provider claimed")
	}
	f.Provider = &providerKey
	f.SubLayer = wrong
	if owned(&f) {
		t.Fatal("foreign sublayer claimed")
	}
	f.SubLayer = sublayerKey
	f.Data = blob([]byte("foreign"))
	if owned(&f) {
		t.Fatal("foreign ownership data claimed")
	}
	f.Data = wfpBlob{}
	if owned(&f) || owned(nil) {
		t.Fatal("missing ownership data claimed")
	}
}
func TestRestrictedPipeOwnerCannotCreateServerInstance(t *testing.T) {
	owner := "S-1-5-21-100-200-300-1001"
	sd, e := pipeSD(owner)
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if acl.AceCount != 3 {
		t.Fatalf("unexpected principals: %d", acl.AceCount)
	}
	found := false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, i, &ace); e != nil {
			t.Fatal(e)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.String() == owner {
			found = true
			if ace.Mask&4 != 0 || ace.Mask&windows.GENERIC_WRITE != 0 {
				t.Fatal("GUI can create a server pipe instance")
			}
			if ace.Mask&3 != 3 {
				t.Fatal("GUI lacks client read/write")
			}
		}
	}
	if !found {
		t.Fatal("owner missing")
	}
	if _, e = pipeSD(""); e == nil {
		t.Fatal("empty owner accepted")
	}
}
func TestInstallerPathsUseWindowsKnownFolders(t *testing.T) {
	if !samePath(ManagedCorePath(), installDir()+`\core\sing-box.exe`) {
		t.Fatal("managed core escapes install directory")
	}
	if samePath(ManagedCorePath(), DataDir()+`\sing-box.exe`) {
		t.Fatal("core incorrectly resides in writable state directory")
	}
	if samePath("relative.exe", "relative.exe") {
		t.Fatal("relative trusted executable path accepted")
	}
}

func TestCoreDigestRejectsUnapprovedExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sing-box.exe")
	if e := os.WriteFile(path, []byte("unapproved executable"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := verifyCoreDigest(path); e == nil {
		t.Fatal("unapproved core digest accepted")
	}
}
func TestExplicitUpstreamTrustDoesNotGrantManagedIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstream.exe")
	if e := os.WriteFile(path, []byte("explicit owner-approved app"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := verifyUpstreamExecutable(path); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"relative.exe", ManagedCorePath(), servicePath(), filepath.Join(t.TempDir(), "absent.exe")} {
		if e := verifyUpstreamExecutable(path); e == nil {
			t.Fatalf("invalid upstream identity accepted: %s", path)
		}
	}
	if e := verifyProtectedExecutable(path); e == nil {
		t.Fatal("same user-writable upstream unexpectedly accepted as protected core")
	}
}
