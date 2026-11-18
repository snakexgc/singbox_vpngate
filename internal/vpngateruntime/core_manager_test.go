package vpngateruntime

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"guiforcores/internal/vpngate"
)

func TestCoreStatusReportsLocalSelectedExecutable(t *testing.T) {
	m := desktopWithHelperCore(t)
	result, err := m.Handle(context.Background(), "core.status", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var status struct {
		Path, Version, Error string
		Installed            bool
	}
	if err = json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.Version != "1.14.4" || status.Path != m.corePath || status.Error != "" {
		t.Fatalf("bad local status: %s", raw)
	}
}

func TestValidateCoreAcceptsCompatibleSemanticVersions(t *testing.T) {
	t.Setenv("VPNGATE_TEST_CORE", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.14.0", "1.14.5", "1.15.0-alpha.2"} {
		t.Setenv("VPNGATE_TEST_CORE_VERSION", "sing-box version "+version+"\nTags: with_openvpn,with_gvisor")
		got, err := validateCore(context.Background(), executable)
		if err != nil || got != version {
			t.Fatalf("compatible %s rejected: %q %v", version, got, err)
		}
	}
}

func TestValidateCoreRejectsOldMalformedAndSubstringBuildTags(t *testing.T) {
	t.Setenv("VPNGATE_TEST_CORE", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"sing-box version 1.13.99 with_openvpn with_gvisor", "sing-box version 1.14.0-alpha.1 with_openvpn with_gvisor", "sing-box version 1.14.4evil with_openvpn with_gvisor", "sing-box version 1.14.4 without_openvpn with_gvisor", "sing-box version 1.14.4 with_openvpn_disabled with_gvisor"} {
		t.Setenv("VPNGATE_TEST_CORE_VERSION", output)
		if _, err := validateCore(context.Background(), executable); err == nil {
			t.Fatalf("invalid core accepted: %s", output)
		}
	}
}

func TestCoreBuildTagsFollowNativeStackVersion(t *testing.T) {
	for _, test := range []struct {
		name, output string
		valid        bool
	}{
		{"stable-needs-gvisor", "sing-box version 1.14.4\nTags: with_openvpn", false},
		{"stable-with-gvisor", "sing-box version 1.14.4\nTags: with_openvpn,with_gvisor", true},
		{"official-alpha-native-stack", "sing-box version 1.15.0-alpha.13\nTags: with_quic,with_openvpn", true},
		{"native-stack-stable", "sing-box version 1.15.0\nTags: with_openvpn", true},
		{"native-stack-still-needs-openvpn", "sing-box version 1.15.0-alpha.13\nTags: with_quic", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCoreVersion(test.output)
			if (err == nil) != test.valid {
				t.Fatalf("core build compatibility: valid=%t err=%v", test.valid, err)
			}
		})
	}
}

func TestCoreJobExcludesMainAndOtherJobs(t *testing.T) {
	m := desktopWithHelperCore(t)
	ctx, job, err := m.beginJob("core")
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishJob(job, ctx.Err())
	for _, kind := range []string{"test", "refresh", "rules", "core"} {
		if ctx2, j, err := m.beginJob(kind); err == nil {
			m.finishJob(j, ctx2.Err())
			t.Fatalf("%s overlapped installation", kind)
		}
	}
	m.settings.RoutingPreset = "direct"
	if _, err = m.Handle(context.Background(), "connect", nil); err == nil || !strings.Contains(err.Error(), "内核") {
		t.Fatalf("main work not blocked: %v", err)
	}
	s := m.settings
	s.MixedPort++
	raw, _ := json.Marshal(s)
	if _, err = m.Handle(context.Background(), "settings.update", raw); err == nil {
		t.Fatal("settings mutated during core installation")
	}
	if m.settings.MixedPort != s.MixedPort-1 {
		t.Fatal("failed update changed settings")
	}
}

func TestCoreInstallJobRequiresStoppedIdleManager(t *testing.T) {
	m := desktopWithHelperCore(t)
	for _, status := range []string{"starting", "connecting", "connected", "direct", "offline", "protected", "degraded"} {
		m.runtime.Status = status
		if ctx, j, err := m.beginJob("core"); err == nil {
			m.finishJob(j, ctx.Err())
			t.Fatalf("install accepted while %s", status)
		}
	}
	m.runtime.Status = "stopped"
	ctx, j, err := m.beginJob("rules")
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishJob(j, ctx.Err())
	if ctx2, j2, err := m.beginJob("core"); err == nil {
		m.finishJob(j2, ctx2.Err())
		t.Fatal("install overlapped existing rules job")
	}
}

func TestDownloadedSelectionOverridesBundleAndIgnoresEditorPath(t *testing.T) {
	dir := t.TempDir()
	rel := "cores/v1.14.4-0123456789abcdef01234567/sing-box.exe"
	expected := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(expected), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expected, []byte("selected downloaded executable"), 0600); err != nil {
		t.Fatal(err)
	}
	s := vpngate.DefaultSettings()
	s.CorePath = "untrusted editor path"
	raw, _ := json.Marshal(map[string]any{"settings": s, "selectedCore": rel})
	if err := os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewDesktop(dir, "bundled-fallback.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.State().Settings.CorePath != expected || m.corePath != expected {
		t.Fatalf("downloaded selection lost: %s", m.State().Settings.CorePath)
	}
	s = m.settings
	s.CorePath = "another arbitrary.exe"
	raw, _ = json.Marshal(s)
	if _, err = m.Handle(context.Background(), "settings.update", raw); err != nil {
		t.Fatal(err)
	}
	if m.settings.CorePath != expected {
		t.Fatal("editor overrode managed selection")
	}
}

func coreFixtureProxy(t *testing.T, m *Manager, handler http.HandlerFunc) *atomic.Int32 {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || (r.Host != "api.github.com:443" && r.Host != "github.com:443") {
			http.Error(w, "unexpected proxy target", 500)
			return
		}
		requests.Add(1)
		upstream, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		_, _ = io.WriteString(connection, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() { defer upstream.Close(); defer connection.Close(); _, _ = io.Copy(upstream, connection) }()
		go func() { defer upstream.Close(); defer connection.Close(); _, _ = io.Copy(connection, upstream) }()
	}))
	t.Cleanup(proxy.Close)
	host, port, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	number, _ := strconv.Atoi(port)
	m.mu.Lock()
	m.settings.Upstream = vpngate.Upstream{Host: host, Port: number, Protocol: "http"}
	m.mu.Unlock()
	m.coreHTTPClient = func(upstream vpngate.Upstream) (*http.Client, error) {
		client, err := proxyClient(upstream)
		if err != nil {
			return nil, err
		}
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Trust only this loopback fixture's self-signed TLS.
		return client, nil
	}
	return &requests
}

func fixtureCoreZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, data := range files {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func helperCoreZIP(t *testing.T) []byte {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureCoreZIP(t, map[string][]byte{"sing-box-1.14.4-windows-amd64/sing-box.exe": data, "sing-box-1.14.4-windows-amd64/libcronet.dll": []byte("fixture DLL payload"), "sing-box-1.14.4-windows-amd64/LICENSE": []byte("fixture license")})
}

func serveCoreRelease(archive []byte, digest string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/SagerNet/sing-box/releases/tags/v1.14.4":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.14.4", "assets": []any{map[string]any{"name": "sing-box-1.14.4-windows-amd64.zip", "size": len(archive), "digest": digest, "browser_download_url": "https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-windows-amd64.zip"}}})
		case "/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-windows-amd64.zip":
			_, _ = w.Write(archive)
		default:
			http.Error(w, "unexpected official endpoint", http.StatusNotFound)
		}
	}
}

func waitCoreJob(t *testing.T, m *Manager, job vpngate.Job) vpngate.Job {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		m.mu.Lock()
		current := *m.jobs[job.ID]
		done := m.jobDone[job.ID]
		m.mu.Unlock()
		if current.Status != "running" {
			return current
		}
		select {
		case <-done:
		case <-deadline:
			t.Fatal("core job timeout")
		}
	}
}

func TestCoreReleasesListsStableAndPrereleaseThroughSavedProxy(t *testing.T) {
	m := desktopWithHelperCore(t)
	requests := coreFixtureProxy(t, m, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/SagerNet/sing-box/releases" || r.URL.Query().Get("per_page") != "100" {
			http.Error(w, "unexpected release list endpoint", 500)
			return
		}
		_, _ = io.WriteString(w, `[{"tag_name":"v1.14.4","prerelease":false,"published_at":"2026-01-01T00:00:00Z"},{"tag_name":"v1.15.0-alpha.2","prerelease":true,"published_at":"2026-02-01T00:00:00Z"},{"tag_name":"v1.13.99"}]`)
	})
	result, err := m.Handle(context.Background(), "core.releases", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var list struct {
		Releases []struct {
			Version     string
			Prerelease  bool
			PublishedAt string
		}
	}
	if err = json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Releases) != 2 || list.Releases[0].Version != "v1.14.4" || list.Releases[0].Prerelease || !list.Releases[1].Prerelease || list.Releases[1].PublishedAt != "2026-02-01T00:00:00Z" || requests.Load() == 0 {
		t.Fatalf("bad official releases: %s proxy=%d", raw, requests.Load())
	}
}

func TestCoreInstallVerifiesExtractsSelectsAndReloadsWithoutBundle(t *testing.T) {
	m := desktopWithHelperCore(t)
	archive := helperCoreZIP(t)
	sum := sha256.Sum256(archive)
	requests := coreFixtureProxy(t, m, serveCoreRelease(archive, "sha256:"+hex.EncodeToString(sum[:])))
	latency := int64(20)
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "saved", Config: "private profile", Status: "available", LatencyMs: &latency, PathFingerprint: "old", LastTested: now()}})
	m.selected = "saved"
	original := m.corePath
	result, err := m.Handle(context.Background(), "core.install", json.RawMessage(`{"version":"1.14.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	job := waitCoreJob(t, m, result.(vpngate.Job))
	if job.Status != "completed" {
		t.Fatalf("install failed: %+v", job)
	}
	if m.corePath == original || m.settings.CorePath != m.corePath || !strings.HasPrefix(m.selectedCore, "cores/v1.14.4-") || requests.Load() < 2 {
		t.Fatal("selected path not committed through proxy")
	}
	for _, name := range []string{"sing-box.exe", "libcronet.dll", "LICENSE"} {
		if _, err = os.Stat(filepath.Join(filepath.Dir(m.corePath), name)); err != nil {
			t.Fatal(err)
		}
	}
	node := m.pool.Snapshot()[0]
	if node.Config != "private profile" || node.Status != "untested" || node.LatencyMs != nil || node.PathFingerprint != "" || m.selected != "saved" {
		t.Fatalf("core switch damaged node or retained stale measurements: %+v", node)
	}
	chosen := m.corePath
	if _, err = m.Repair(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(chosen); err != nil {
		t.Fatalf("repair removed installed core: %v", err)
	}
	m.Close()
	reloaded, err := NewDesktop(m.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if reloaded.corePath != chosen || reloaded.settings.CorePath != chosen {
		t.Fatal("restart lost selected core")
	}
	result, err = reloaded.Handle(context.Background(), "core.status", nil)
	if err != nil {
		t.Fatal(err)
	}
	status := result.(coreStatus)
	if !status.Installed || len(status.InstalledVersions) != 1 || !status.InstalledVersions[0].Selected {
		t.Fatalf("installed status lost: %+v", status)
	}
}

func TestCoreInstallInvalidDigestDoesNotRunOrReplaceOriginal(t *testing.T) {
	m := desktopWithHelperCore(t)
	original := m.corePath
	// This is deliberately not an executable: the digest must stop it before execution.
	archive := fixtureCoreZIP(t, map[string][]byte{"sing-box-1.14.4-windows-amd64/sing-box.exe": []byte("untrusted executable")})
	coreFixtureProxy(t, m, serveCoreRelease(archive, "sha256:"+strings.Repeat("0", 64)))
	result, err := m.Handle(context.Background(), "core.install", json.RawMessage(`{"version":"v1.14.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	job := waitCoreJob(t, m, result.(vpngate.Job))
	if job.Status != "failed" || !strings.Contains(job.Message, "SHA-256") {
		t.Fatalf("digest verification skipped: %+v", job)
	}
	if m.corePath != original || m.selectedCore != "" {
		t.Fatal("hash-invalid download replaced original")
	}
	entries, _ := os.ReadDir(filepath.Join(m.dir, "cores"))
	if len(entries) != 0 {
		t.Fatalf("failed install left staging artifacts: %v", entries)
	}
}

func TestCoreExtractionRejectsTraversalMissingPayloadAndOversizedEntries(t *testing.T) {
	for name, files := range map[string]map[string][]byte{
		"traversal":             {"../escaped.exe": []byte("unsafe")},
		"nested traversal":      {"sing-box-1.14.4-windows-amd64/../../escaped.exe": []byte("unsafe")},
		"missing payload":       {"sing-box-1.14.4-windows-amd64/LICENSE": []byte("license")},
		"unapproved executable": {"sing-box-1.14.4-windows-amd64/sing-box.exe": []byte("main"), "sing-box-1.14.4-windows-amd64/helper.exe": []byte("unexpected")},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "archive.zip")
			if err := os.WriteFile(archive, fixtureCoreZIP(t, files), 0600); err != nil {
				t.Fatal(err)
			}
			if err := extractCoreArchive(context.Background(), archive, filepath.Join(dir, "payload"), "sing-box-1.14.4-windows-amd64"); err == nil {
				t.Fatal("unsafe or incomplete archive accepted")
			}
		})
	}
}

func TestCoreInstallCancelRetainsOriginalSelection(t *testing.T) {
	m := desktopWithHelperCore(t)
	original := m.corePath
	started := make(chan struct{})
	canceled := make(chan struct{})
	coreFixtureProxy(t, m, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) })
	result, err := m.Handle(context.Background(), "core.install", json.RawMessage(`{"version":"v1.14.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	job := result.(vpngate.Job)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	if _, err = m.Handle(context.Background(), "jobs.cancel", json.RawMessage(fmt.Sprintf(`{"id":%q}`, job.ID))); err != nil {
		t.Fatal(err)
	}
	job = waitCoreJob(t, m, job)
	if job.Status != "canceled" || m.corePath != original {
		t.Fatalf("cancel changed original: %+v", job)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("network request did not cancel")
	}
}

func TestCoreSelectionWriteFailureRollsBackSettingsAndHealth(t *testing.T) {
	m := desktopWithHelperCore(t)
	original := m.corePath
	selection := "cores/v1.14.4-0123456789abcdef01234567/sing-box.exe"
	latency := int64(23)
	previousNode := measuredCacheFixture(m.settings, vpngate.Node{ID: "preserved", Config: "private", Status: "available", LatencyMs: &latency}, time.Now().Add(-time.Hour))
	m.pool = vpngate.NewPool([]vpngate.Node{previousNode})
	if err := os.Mkdir(filepath.Join(m.dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.activateCore(context.Background(), selection); err == nil {
		t.Fatal("selection committed despite state write failure")
	}
	if m.corePath != original || m.settings.CorePath != original || m.selectedCore != "" || m.pool.Snapshot()[0].Status != "available" || *m.pool.Snapshot()[0].LatencyMs != 23 {
		t.Fatal("selection failure damaged previous state")
	}
	if actual := m.pool.Snapshot()[0]; actual.EgressIP != previousNode.EgressIP || actual.EgressCheckedAt != previousNode.EgressCheckedAt {
		t.Fatal("selection write failure discarded original core's egress cache")
	}
}

func TestCoreSelectionPreservesEgressCache(t *testing.T) {
	m := desktopWithHelperCore(t)
	selection := "cores/v1.14.4-0123456789abcdef01234567/sing-box.exe"
	n := measuredCacheFixture(m.settings, vpngate.Node{ID: "cached", Config: "private", Status: "available"}, time.Now().Add(-time.Hour))
	m.pool = vpngate.NewPool([]vpngate.Node{n})
	for _, scenario := range []string{"new core", "same core"} {
		if err := m.activateCore(context.Background(), selection); err != nil {
			t.Fatal(err)
		}
		if actual := m.pool.Snapshot()[0]; actual.Status != "untested" || actual.EgressIP != n.EgressIP || actual.EgressCheckedAt != n.EgressCheckedAt || !validEgressCache(m.settings, actual, time.Now()) {
			t.Fatalf("%s selection must reset health and preserve the dated cache", scenario)
		}
	}
}

func TestCoreUseSelectsPreviouslyInstalledVersionOffline(t *testing.T) {
	m := desktopWithHelperCore(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	selection := "cores/v1.14.4-0123456789abcdef01234567/sing-box.exe"
	installed := filepath.Join(m.dir, filepath.FromSlash(selection))
	if err = os.MkdirAll(filepath.Dir(installed), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(installed, content, 0700); err != nil {
		t.Fatal(err)
	}
	m.coreHTTPClient = func(vpngate.Upstream) (*http.Client, error) {
		t.Error("offline selection attempted network access")
		return nil, fmt.Errorf("offline")
	}
	if _, err = m.Handle(context.Background(), "core.use", json.RawMessage(`{"version":"1.14.4"}`)); err != nil {
		t.Fatal(err)
	}
	if m.corePath != installed || m.selectedCore != selection {
		t.Fatal("offline version selection failed")
	}
	if _, err = m.Handle(context.Background(), "core.use", json.RawMessage(`{"version":"1.14.5"}`)); err == nil {
		t.Fatal("uninstalled version selected")
	}
	if m.corePath != installed {
		t.Fatal("failed offline selection lost current core")
	}
}

func TestDesktopStartsAfterSelectingMissingCoreUnlessExplicitlyStopped(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopped=%t", stopped), func(t *testing.T) {
			m := desktopWithHelperCore(t)
			m.restoreProxy = func() error { return nil }
			m.settings.RoutingPreset = "direct"
			helper := m.corePath
			m.corePath = filepath.Join(m.dir, "missing.exe")
			m.settings.CorePath = m.corePath
			if _, err := m.Handle(context.Background(), "runtime.startup", nil); err != nil {
				t.Fatal(err)
			}
			if !m.startupPendingCore || m.State().Runtime.Status != "stopped" {
				t.Fatal("startup did not await core installation")
			}
			if stopped {
				if _, err := m.Handle(context.Background(), "stop", nil); err != nil {
					t.Fatal(err)
				}
			}
			const selection = "cores/v1.14.4-0123456789abcdef01234567/sing-box.exe"
			target := filepath.Join(m.dir, filepath.FromSlash(selection))
			content, err := os.ReadFile(helper)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(target, content, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err = m.Handle(context.Background(), "core.use", json.RawMessage(`{"version":"1.14.4"}`)); err != nil {
				t.Fatal(err)
			}
			if stopped {
				// Serialize with the async hook and examine the same startup path.
				m.startAfterCoreReady()
				if m.State().Runtime.Status != "stopped" {
					t.Fatal("explicit stop was overridden after selecting a core")
				}
				return
			}
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for m.State().Runtime.Status != "direct" {
				select {
				case <-timer.C:
					t.Fatalf("selecting a core failed to resume pending startup: %+v", m.State().Runtime)
				case <-ticker.C:
				}
			}
		})
	}
}

func TestCoreCandidateRejectsVersionMismatchAndInvalidGeneratedConfig(t *testing.T) {
	m := desktopWithHelperCore(t)
	t.Setenv("VPNGATE_TEST_CORE_VERSION", "sing-box version 1.14.5 with_openvpn with_gvisor")
	if err := m.checkCoreCandidate(context.Background(), m.corePath, "v1.14.4", t.TempDir()); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("reported version mismatch accepted: %v", err)
	}
	t.Setenv("VPNGATE_TEST_CORE_VERSION", "sing-box version 1.14.4 with_openvpn with_gvisor")
	m.mu.Lock()
	m.settings.MixedPort = 0
	m.mu.Unlock()
	if err := m.checkCoreCandidate(context.Background(), m.corePath, "v1.14.4", t.TempDir()); err == nil {
		t.Fatal("invalid generated configuration accepted")
	}
}

func TestCoreInstallMissingChecksumNeverDownloadsPayload(t *testing.T) {
	m := desktopWithHelperCore(t)
	var downloads atomic.Int32
	metadata := serveCoreRelease([]byte("archive"), "")
	coreFixtureProxy(t, m, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/download/") {
			downloads.Add(1)
		}
		metadata(w, r)
	})
	result, err := m.Handle(context.Background(), "core.install", json.RawMessage(`{"version":"1.14.4"}`))
	if err != nil {
		t.Fatal(err)
	}
	job := waitCoreJob(t, m, result.(vpngate.Job))
	if job.Status != "failed" || !strings.Contains(job.Message, "SHA-256") || downloads.Load() != 0 {
		t.Fatalf("unverified payload downloaded: %+v downloads=%d", job, downloads.Load())
	}
}

func TestCoreCandidateGeneratedCheckWorksWithCachedOfficialCore(t *testing.T) {
	executable, err := filepath.Abs(filepath.Join("..", "..", "build", "bin", "vpngate-cache", "sing-box-1.14.4-windows-amd64", "sing-box.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(executable); os.IsNotExist(err) {
		t.Skip("official native fixture not cached")
	} else if err != nil {
		t.Fatal(err)
	}
	m := desktopWithHelperCore(t)
	if err = m.checkCoreCandidate(context.Background(), executable, "v1.14.4", t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestCoreStatusMissingCoreDoesNotRequireUpstream(t *testing.T) {
	m, err := NewDesktop(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.settings.Upstream.Port = 0
	result, err := m.Handle(context.Background(), "core.status", nil)
	if err != nil {
		t.Fatal(err)
	}
	status := result.(coreStatus)
	if status.Installed || status.Version != "" || status.Error == "" {
		t.Fatalf("missing core status unusable: %+v", status)
	}
}

func TestCoreVersionRejectsLeadingZeroPrereleaseIdentifiers(t *testing.T) {
	t.Setenv("VPNGATE_TEST_CORE", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VPNGATE_TEST_CORE_VERSION", "sing-box version 1.15.0-alpha.01 with_openvpn with_gvisor")
	if _, err = validateCore(context.Background(), executable); err == nil {
		t.Fatal("invalid semantic version accepted")
	}
}

func TestAutomaticJobsWaitForInstalledCore(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "automatic request before core installation", 500)
	}))
	defer proxy.Close()
	host, port, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	number, _ := strconv.Atoi(port)
	m, err := NewDesktop(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.mu.Lock()
	m.settings.Upstream = vpngate.Upstream{Host: host, Port: number, Protocol: "http"}
	m.mu.Unlock()
	time.Sleep(2300 * time.Millisecond)
	if requests.Load() != 0 || len(m.State().Jobs) != 0 {
		t.Fatalf("missing core started automatic network work: %d jobs=%+v", requests.Load(), m.State().Jobs)
	}
}

func TestCoreReleasesAllowsBoundedFullOfficialAssetMetadata(t *testing.T) {
	m := desktopWithHelperCore(t)
	coreFixtureProxy(t, m, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"tag_name":"v1.14.4"}]`+strings.Repeat(" ", 5<<20))
	})
	result, err := m.Handle(context.Background(), "core.releases", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !bytes.Contains(raw, []byte(`"v1.14.4"`)) {
		t.Fatalf("full release metadata omitted compatible version: %s", raw)
	}
}
