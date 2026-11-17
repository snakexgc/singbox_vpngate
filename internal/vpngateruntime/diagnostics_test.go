package vpngateruntime

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"guiforcores/internal/vpngate"
)

func TestTunnelReadyKeepsLastTargetFailureAtDeadline(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	u := diagnosticUpstream(t, proxy.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := tunnelReady(ctx, &coreProcess{done: make(chan struct{})}, u.Port, "http://target.invalid/health")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("target failure lost behind generic readiness timeout: %v", err)
	}
}

func TestTunnelReadyKeepsLastTargetFailureWhenCoreExits(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	u := diagnosticUpstream(t, proxy.URL)
	done := make(chan struct{})
	close(done)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := tunnelReady(ctx, &coreProcess{done: done}, u.Port, "http://target.invalid/health")
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "退出") {
		t.Fatalf("core exit hid the most recent target failure: %v", err)
	}
}

func TestProbeKeepsRedactedCoreFailureAfterTemporaryCleanup(t *testing.T) {
	t.Setenv("VPNGATE_TEST_CORE_LOG_ERROR", "ERROR endpoint/openvpn-client[vpngate]: diagnostic-core-marker authentication failed for diagnostic-user:diagnostic-secret")
	m := desktopWithHelperCore(t)
	s := m.settings
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept only the proxy control exchange; the helper core never forwards
		// a packet or opens a system interface.
		if r.Method != http.MethodConnect || r.Host != "192.0.2.10:443" {
			t.Errorf("unexpected upstream diagnostic request: %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	s.Upstream = diagnosticUpstream(t, upstream.URL)
	s.Upstream.Username, s.Upstream.Password = "diagnostic-user", "diagnostic-secret"
	// The race-instrumented child has its own bounded shutdown/check overhead.
	s.TestTimeoutSeconds = 3
	node := vpngate.Node{ID: "diagnostic", Config: "client\ndev tun\nproto tcp\nremote 192.0.2.10 443\n<ca>\nfixture-ca\n</ca>\n"}
	result := m.probe(context.Background(), s, node)
	if result.Status != "failed" || !strings.Contains(result.Error, "diagnostic-core-marker") {
		t.Fatalf("probe discarded core failure evidence: %+v", result)
	}
	if strings.Contains(result.Error, "diagnostic-user") || strings.Contains(result.Error, "diagnostic-secret") {
		t.Fatalf("probe exposed proxy credentials: %s", result.Error)
	}
	entries, err := os.ReadDir(filepath.Join(m.dir, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe left generated profiles behind: files=%d error=%v", len(entries), err)
	}
}

func TestCoreFailureDiagnosticBoundsTailAndRedactsCredentials(t *testing.T) {
	dir := t.TempDir()
	u := vpngate.Upstream{Username: "diagnostic-user", Password: "secret@password"}
	log := strings.Repeat("INFO irrelevant certificate-like contents\n", 2000) +
		"INFO discarded-older-error\nWARN first-useful-error\nERROR final-useful-error secret%40password\n"
	if err := os.WriteFile(filepath.Join(dir, "core.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(u.Username + ":" + u.Password))
	got := coreFailureDiagnostic(errors.New("HTTP proxy failure Basic "+encoded+" "+url.QueryEscape(u.Password)), dir, u)
	for _, want := range []string{"HTTP proxy failure", "first-useful-error", "final-useful-error"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost relevant failure %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{u.Username, u.Password, url.QueryEscape(u.Password), encoded, "certificate-like", "discarded-older-error"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("diagnostic retained sensitive or irrelevant content %q", forbidden)
		}
	}
	got = coreFailureDiagnostic(errors.New(strings.Repeat("failure ", 10000)), dir, u)
	if len([]rune(got)) > 4097 || !strings.Contains(got, "final-useful-error") {
		t.Fatalf("diagnostic was unbounded or lost its core evidence: length=%d", len([]rune(got)))
	}
}

func TestCoreFailureDiagnosticKeepsInitialCauseDespiteReadinessNoise(t *testing.T) {
	dir := t.TempDir()
	log := "ERROR endpoint/openvpn-client[vpngate]: initial-proxy-cause\n" +
		strings.Repeat("ERROR router: endpoint is not ready yet\n", 2000) +
		"ERROR router: final-readiness-failure\n"
	if err := os.WriteFile(filepath.Join(dir, "core.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	got := coreFailureDiagnostic(errors.New("tunnel timeout"), dir, vpngate.Upstream{})
	if !strings.Contains(got, "initial-proxy-cause") || !strings.Contains(got, "final-readiness-failure") {
		t.Fatalf("readiness noise hid the original failure: %s", got)
	}
}
