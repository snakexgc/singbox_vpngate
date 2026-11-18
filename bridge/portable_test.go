package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsDataPathsStayBesideExecutable(t *testing.T) {
	appDir := filepath.Join(t.TempDir(), "portable app")
	legacyParent := t.TempDir()
	legacy := filepath.Join(legacyParent, "old data")
	if err := os.Mkdir(appDir, 0700); err != nil {
		t.Fatal(err)
	}
	actualAppDir, err := filepath.EvalSymlinks(appDir)
	if err != nil {
		t.Fatal(err)
	}
	actualLegacyParent, err := filepath.EvalSymlinks(legacyParent)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ dir, local, old string }{
		{"", "data", legacy},
		{"profile one", "profile one", ""},
		{filepath.Join(appDir, "profile two"), "profile two", ""},
		{legacy, "data", filepath.Join(actualLegacyParent, "old data")},
		{".", "data", actualAppDir},
	} {
		local, old, err := windowsDataPaths(appDir, tt.dir, legacy)
		if err != nil || local != filepath.Join(actualAppDir, tt.local) || old != tt.old {
			t.Fatalf("windowsDataPaths(%q) = %q, %q, %v", tt.dir, local, old, err)
		}
	}
}

func TestWindowsDataPathRejectsAnEscapingLink(t *testing.T) {
	appDir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(appDir, "data")); err != nil {
		t.Skipf("cannot create test symlink: %v", err)
	}
	if _, _, err := windowsDataPaths(appDir, "", ""); err == nil {
		t.Fatal("accepted a default data directory that resolves outside the app folder")
	}
}

func TestWindowsRuntimeCacheAndChildTempUseLocalProfile(t *testing.T) {
	previous := *Env
	t.Cleanup(func() { *Env = previous })
	Env.OS = "windows"
	Env.BasePath = t.TempDir()
	for _, name := range []string{"WEBVIEW2_USER_DATA_FOLDER", "WEBVIEW2_BROWSER_EXECUTABLE_FOLDER", "TEMP", "TMP", "TMPDIR"} {
		t.Setenv(name, "previous")
	}
	if err := initWindowsDataDir("portable profile"); err != nil {
		t.Fatal(err)
	}
	actualAppDir, err := filepath.EvalSymlinks(Env.BasePath)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(actualAppDir, "portable profile")
	if filepath.FromSlash(Env.DataDir) != data {
		t.Fatalf("unexpected app profile %q", Env.DataDir)
	}
	for name, path := range map[string]string{
		"WEBVIEW2_USER_DATA_FOLDER": WebView2UserDataPath(),
		"TEMP":                      filepath.Join(data, ".cache", "tmp"),
		"TMP":                       filepath.Join(data, ".cache", "tmp"),
		"TMPDIR":                    filepath.Join(data, ".cache", "tmp"),
	} {
		if os.Getenv(name) != path || !strictlyInside(data, path) {
			t.Fatalf("%s did not resolve into the portable profile: %s", name, os.Getenv(name))
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("portable directory %s not created: %v", path, err)
		}
	}
	if _, ok := os.LookupEnv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER"); ok {
		t.Fatal("inherited external WebView2 executable path remains in effect")
	}
}

func TestFixedWebView2UsesExtractedRuntimeBesideExecutable(t *testing.T) {
	previous := *Env
	t.Cleanup(func() { *Env = previous })
	Env.DataDir = filepath.Join(t.TempDir(), "data")
	runtimeDir := filepath.Join(Env.DataDir, "WebView2", "Fixed Runtime x64")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "msedgewebview2.exe"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := processFixedWebView2Runtime(); err != nil {
		t.Fatal(err)
	}
	actualRuntimeDir, err := filepath.EvalSymlinks(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if Env.WebviewPath != actualRuntimeDir {
		t.Fatalf("fixed runtime selected %q, want %q", Env.WebviewPath, actualRuntimeDir)
	}
}

func TestWindowsPortableMigrationPreservesProfileAndDropsOwnership(t *testing.T) {
	previous, dataDir := t.TempDir(), t.TempDir()
	files := map[string]string{
		"user.yaml":          "theme: dark\n",
		"vpngate/state.json": `{"settings":{"mixedPort":12345},"nodes":[{"id":"saved"}],"selectedNodeId":"saved","selectedCore":"cores/v1.14.4-0123456789abcdef01234567/sing-box.exe","armed":true,"proxy":{},"runtimeActive":true}`,
		"vpngate/cores/v1.14.4-0123456789abcdef01234567/sing-box.exe":  "core",
		"vpngate/cores/v1.14.4-0123456789abcdef01234567/libcronet.dll": "dll",
		"vpngate/cores/.staging-temp/download.zip":                     "incomplete download",
		"vpngate/proxy-snapshot.json":                                  "old proxy snapshot",
		"vpngate/runtime/config.json":                                  "old running core configuration",
		".cache/webview2/Cookies":                                      "old application cache",
	}
	for path, data := range files {
		file := filepath.Join(previous, path)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dataDir, "user.yaml"), []byte("theme: light\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateWindowsProfile(dataDir, previous); err != nil {
		t.Fatal(err)
	}
	for path, data := range files {
		if actual, err := os.ReadFile(filepath.Join(previous, path)); err != nil || string(actual) != data {
			t.Fatalf("legacy %s changed: %q, %v", path, actual, err)
		}
	}
	for path, want := range map[string]string{
		"user.yaml": "theme: light\n",
		"vpngate/cores/v1.14.4-0123456789abcdef01234567/sing-box.exe":  "core",
		"vpngate/cores/v1.14.4-0123456789abcdef01234567/libcronet.dll": "dll",
	} {
		if actual, err := os.ReadFile(filepath.Join(dataDir, path)); err != nil || string(actual) != want {
			t.Fatalf("portable %s = %q, %v", path, actual, err)
		}
	}
	for _, path := range []string{"vpngate/proxy-snapshot.json", "vpngate/runtime", "vpngate/cores/.staging-temp", ".cache/webview2"} {
		if _, err := os.Lstat(filepath.Join(dataDir, path)); !os.IsNotExist(err) {
			t.Fatalf("transient legacy file %s was copied: %v", path, err)
		}
	}
	state, err := readProfileState(filepath.Join(dataDir, "vpngate", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(state["selectedNodeId"]) != `"saved"` || string(state["selectedCore"]) != `"cores/v1.14.4-0123456789abcdef01234567/sing-box.exe"` {
		t.Fatalf("portable state lost user selections: %v", state)
	}
	for _, key := range []string{"proxy", "armed", "runtimeActive"} {
		if _, ok := state[key]; ok {
			t.Fatalf("imported old runtime ownership: %s", key)
		}
	}
}
