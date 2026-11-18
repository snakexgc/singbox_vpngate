package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const egressSuccessBody = `{"status":"success","query":"8.8.4.4","country":"美国","countryCode":"US","regionName":"加利福尼亚州","city":"山景城"}`

// This subprocess fixture replaces only the external executable. It serves the
// generated candidate mixed listener; all manager routing and process ownership
// remain real, while no packet leaves loopback or changes system networking.
func init() {
	if os.Getenv("VPNGATE_TEST_EGRESS_CORE") != "1" || len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "version":
		fmt.Println("sing-box version 1.14.4 with_openvpn with_gvisor")
		os.Exit(0)
	case "check":
		os.Exit(0)
	case "run":
		if err := serveEgressCoreFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func serveEgressCoreFixture() error {
	var path string
	for i, arg := range os.Args {
		if arg == "-c" && i+1 < len(os.Args) {
			path = os.Args[i+1]
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config struct {
		Inbounds []struct {
			Type string `json:"type"`
			Port int    `json:"listen_port"`
		} `json:"inbounds"`
		Endpoints []struct {
			Server string `json:"server"`
			Detour string `json:"detour"`
		} `json:"endpoints"`
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return err
	}
	if len(config.Inbounds) != 1 || config.Inbounds[0].Type != "mixed" || len(config.Endpoints) != 1 || config.Endpoints[0].Server != "192.0.2.10" || config.Endpoints[0].Detour != "upstream" || config.Route.Final != "vpngate" {
		return fmt.Errorf("fixture requires the candidate-only mixed configuration")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.Inbounds[0].Port))
	if err != nil {
		return err
	}
	return http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() == "http://latency.invalid/generate_204" {
			delay, _ := time.ParseDuration(os.Getenv("VPNGATE_TEST_LATENCY_DELAY"))
			time.Sleep(delay)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Host != "ip-api.com" || r.URL.Path != "/json/" || r.URL.Query().Get("fields") != "status,message,query,country,countryCode,regionName,city" || r.URL.Query().Get("lang") != "zh-CN" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if path := os.Getenv("VPNGATE_TEST_EGRESS_REQUEST"); path != "" {
			if file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
				_, _ = file.WriteString(r.URL.String() + "\n")
				_ = file.Close()
			}
		}
		if os.Getenv("VPNGATE_TEST_EGRESS_BLOCK") == "1" {
			<-r.Context().Done()
			return
		}
		delay, _ := time.ParseDuration(os.Getenv("VPNGATE_TEST_EGRESS_DELAY"))
		time.Sleep(delay)
		if status, _ := strconv.Atoi(os.Getenv("VPNGATE_TEST_EGRESS_STATUS")); status != 0 {
			w.Header().Set("X-Rl", "0")
			w.Header().Set("X-Ttl", "42")
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(os.Getenv("VPNGATE_TEST_EGRESS_BODY")))
	}))
}

func egressProbeFixture(t *testing.T, body string, status int) (*Manager, vpngate.Settings, vpngate.Node) {
	t.Helper()
	t.Setenv("VPNGATE_TEST_EGRESS_CORE", "1")
	t.Setenv("VPNGATE_TEST_EGRESS_BODY", body)
	t.Setenv("VPNGATE_TEST_EGRESS_STATUS", strconv.Itoa(status))
	t.Setenv("VPNGATE_TEST_EGRESS_BLOCK", "0")
	t.Setenv("VPNGATE_TEST_LATENCY_DELAY", "")
	t.Setenv("VPNGATE_TEST_EGRESS_DELAY", "")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "192.0.2.10:443" {
			t.Errorf("egress lookup bypassed candidate via upstream: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	s := m.settings
	s.Upstream = diagnosticUpstream(t, upstream.URL)
	s.CorePath, err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.TestURL, s.TestTimeoutSeconds = "http://latency.invalid/generate_204", 20
	n := vpngate.Node{ID: "candidate", Config: "client\ndev tun\nproto tcp\nremote 192.0.2.10 443\n<ca>\nfixture-ca\n</ca>\n"}
	return m, s, n
}

func egressJSONFields(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestProbeEnrichesEgressThroughCandidateTunnel(t *testing.T) {
	m, s, n := egressProbeFixture(t, egressSuccessBody, 0)
	requestPath := filepath.Join(t.TempDir(), "egress-request")
	t.Setenv("VPNGATE_TEST_EGRESS_REQUEST", requestPath)
	r := m.probe(context.Background(), s, n)
	if r.Status != "available" || r.LatencyMs == nil || r.HandshakeMs == nil {
		t.Fatalf("connectivity samples did not complete: %+v", r)
	}
	fields := egressJSONFields(t, r)
	for key, want := range map[string]string{"egressIP": "8.8.4.4", "egressCountry": "美国", "egressCountryCode": "US", "egressRegion": "加利福尼亚州", "egressCity": "山景城", "egressError": ""} {
		if fields[key] != want {
			t.Errorf("lookup did not populate %s: got %v want %q", key, fields[key], want)
		}
	}
	if _, err := os.ReadFile(requestPath); err != nil {
		t.Fatalf("candidate mixed listener never received lookup: %v", err)
	}
}

func TestProbeEgressHasABudgetSeparateFromLatency(t *testing.T) {
	m, s, n := egressProbeFixture(t, egressSuccessBody, 0)
	s.TestTimeoutSeconds = 4
	// Handshake plus three latency reads consume at least 600 ms, so the
	// lookup cannot finish within the remaining connectivity budget.
	t.Setenv("VPNGATE_TEST_LATENCY_DELAY", "150ms")
	t.Setenv("VPNGATE_TEST_EGRESS_DELAY", "3500ms")
	r := m.probe(context.Background(), s, n)
	if r.Status != "available" || r.LatencyMs == nil || r.EgressIP != "8.8.4.4" || r.EgressError != "" {
		t.Fatalf("healthy candidate lost a valid location to the latency deadline: %+v", r)
	}
}

func TestManualRefreshProbeBackfillsRecentAvailableNodeWithoutEgress(t *testing.T) {
	m, s, n := egressProbeFixture(t, egressSuccessBody, 0)
	n.Status, n.LastTested, n.PathFingerprint = "available", now(), fingerprint(s)
	m.settings = s
	m.pool = vpngate.NewPool([]vpngate.Node{n})
	targets := refreshProbeTargets(nil, m.pool.Snapshot(), s)
	if _, err := m.probeBatch(context.Background(), &vpngate.Job{}, s, targets); err != nil {
		t.Fatal(err)
	}
	updated := m.State().Nodes[0]
	if updated.Status != "available" || updated.EgressIP != "8.8.4.4" || updated.EgressCity != "山景城" || updated.EgressError != "" {
		t.Fatalf("manual refresh left an available node without an egress outcome: %+v", updated)
	}
}

func TestProbeReusesSevenDayEgressCacheAcrossRetestsAndRestart(t *testing.T) {
	m, s, n := egressProbeFixture(t, egressSuccessBody, 0)
	requestPath := filepath.Join(t.TempDir(), "egress-requests")
	t.Setenv("VPNGATE_TEST_EGRESS_REQUEST", requestPath)
	first := m.probe(context.Background(), s, n)
	firstFields := egressJSONFields(t, first)
	if first.Status != "available" || firstFields["egressCheckedAt"] == nil || firstFields["egressCheckedAt"] == "" || firstFields["egressLanguage"] != "zh-CN" {
		t.Fatalf("successful lookup did not record a durable Chinese cache: %+v", firstFields)
	}
	applyToNode(&n, first)
	m.settings = s
	m.pool = vpngate.NewPool([]vpngate.Node{n})
	m.pool.UpdateProbe(vpngate.ProbeResult{NodeID: n.ID, Status: "testing"})
	second := m.probe(context.Background(), s, m.pool.Snapshot()[0])
	if second.Status != "available" || egressJSONFields(t, second)["egressCheckedAt"] != firstFields["egressCheckedAt"] {
		t.Fatalf("retest lost or extended the existing cache: %+v", second)
	}
	m.pool.UpdateProbe(second)
	m.mu.Lock()
	err := m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := New(m.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	third := loaded.probe(context.Background(), loaded.settings, loaded.pool.Snapshot()[0])
	if third.Status != "available" || egressJSONFields(t, third)["egressCheckedAt"] != firstFields["egressCheckedAt"] {
		t.Fatalf("restart lost or extended the existing cache: %+v", third)
	}
	requests, err := os.ReadFile(requestPath)
	if err != nil || strings.Count(string(requests), "\n") != 1 {
		t.Fatalf("cached retests repeated the provider request: requests=%q error=%v", requests, err)
	}
}

func measuredCacheFixture(s vpngate.Settings, n vpngate.Node, checked time.Time) vpngate.Node {
	n.EgressIP, n.EgressCountry, n.EgressCountryCode = "8.8.4.4", "美国", "US"
	n.EgressRegion, n.EgressCity = "加利福尼亚州", "山景城"
	n.EgressCheckedAt, n.EgressLanguage, n.EgressCacheKey = checked.UTC().Format(time.RFC3339), "zh-CN", egressCacheKey(s, n)
	return n
}

func TestEgressCacheExpiresAtExactlySevenDaysAndRequiresMatchingPath(t *testing.T) {
	at := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		age    time.Duration
		change func(*vpngate.Settings, *vpngate.Node)
		valid  bool
	}{
		{name: "just-before-expiry", age: 7*24*time.Hour - time.Second, valid: true},
		{name: "exact-expiry", age: 7 * 24 * time.Hour},
		{name: "older-than-seven-days", age: 8 * 24 * time.Hour},
		{name: "future-measurement", age: -time.Second},
		{name: "missing-timestamp", change: func(s *vpngate.Settings, n *vpngate.Node) { n.EgressCheckedAt = "" }},
		{name: "invalid-timestamp", change: func(s *vpngate.Settings, n *vpngate.Node) { n.EgressCheckedAt = "not-a-time" }},
		{name: "legacy-language", change: func(s *vpngate.Settings, n *vpngate.Node) { n.EgressLanguage = "" }},
		{name: "english-language", change: func(s *vpngate.Settings, n *vpngate.Node) { n.EgressLanguage = "en" }},
		{name: "legacy-key", change: func(s *vpngate.Settings, n *vpngate.Node) { n.EgressCacheKey = "" }},
		{name: "changed-profile", change: func(s *vpngate.Settings, n *vpngate.Node) { n.Config += "new certificate" }},
		{name: "changed-upstream", change: func(s *vpngate.Settings, n *vpngate.Node) { s.Upstream.Port++ }},
		{name: "changed-upstream-auth", change: func(s *vpngate.Settings, n *vpngate.Node) { s.Upstream.Password = "new-password" }},
		{name: "changed-core-path", change: func(s *vpngate.Settings, n *vpngate.Node) { s.CorePath = "new-core" }, valid: true},
		{name: "changed-tun-process-paths", change: func(s *vpngate.Settings, n *vpngate.Node) { s.Upstream.ProcessPaths = []string{"proxy.exe"} }, valid: true},
		{name: "changed-latency-target", change: func(s *vpngate.Settings, n *vpngate.Node) { s.TestURL = "https://different.example/204" }, valid: true},
		{name: "changed-business-preset", change: func(s *vpngate.Settings, n *vpngate.Node) { s.RoutingPreset = "global" }, valid: true},
		{name: "last-refresh-error", change: func(s *vpngate.Settings, n *vpngate.Node) { n.EgressError = "previous failed refresh" }, valid: true},
		{name: "invalid-cached-ip", change: func(s *vpngate.Settings, n *vpngate.Node) { n.EgressIP = "192.168.1.1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := vpngate.DefaultSettings()
			n := measuredCacheFixture(s, vpngate.Node{Config: "candidate profile"}, at.Add(-test.age))
			if test.change != nil {
				test.change(&s, &n)
			}
			if actual := validEgressCache(s, n, at); actual != test.valid {
				t.Fatalf("cache reuse=%v want %v for %s", actual, test.valid, test.name)
			}
		})
	}
}

func TestExpiredEgressRefreshFailurePreservesLastMeasurementWithoutExtendingTTL(t *testing.T) {
	s := vpngate.DefaultSettings()
	n := measuredCacheFixture(s, vpngate.Node{Config: "candidate profile"}, time.Now().Add(-8*24*time.Hour))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("X-Rl", "0")
		w.Header().Set("X-Ttl", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := proxyClient(diagnosticUpstream(t, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	r := resultFromNode(n)
	enrichEgress(context.Background(), client, s, n, &r)
	if requests.Load() != 1 || r.EgressIP != n.EgressIP || r.EgressCheckedAt != n.EgressCheckedAt || r.EgressCacheKey != n.EgressCacheKey || r.EgressError == "" {
		t.Fatalf("failed refresh destroyed or extended previous measurement: %+v requests=%d", r, requests.Load())
	}
	applyToNode(&n, r)
	if validEgressCache(s, n, time.Now()) {
		t.Fatal("failed refresh became a new success cache")
	}
}

func TestFailedAndCanceledProbePreserveDatedEgressCache(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(strconv.FormatBool(canceled), func(t *testing.T) {
			m, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			s := m.settings
			n := measuredCacheFixture(s, vpngate.Node{ID: "cached", Config: "invalid profile"}, time.Now().Add(-time.Hour))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantStatus := "failed"
			if canceled {
				cancel()
				wantStatus = "canceled"
			}
			r := m.probe(ctx, s, n)
			if r.Status != wantStatus || r.EgressIP != n.EgressIP || r.EgressCheckedAt != n.EgressCheckedAt || r.EgressLanguage != "zh-CN" || r.EgressCacheKey != n.EgressCacheKey {
				t.Fatalf("transient probe status discarded durable egress cache: %+v", r)
			}
		})
	}
}

func TestSettingsPathChangesInvalidateDisplayedEgress(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*vpngate.Settings)
		clear  bool
	}{
		{name: "upstream", change: func(s *vpngate.Settings) { s.Upstream.Port++ }, clear: true},
		{name: "core", change: func(s *vpngate.Settings) { s.CorePath = "different-core" }},
		{name: "process-paths", change: func(s *vpngate.Settings) { s.Upstream.ProcessPaths = []string{"proxy.exe"} }},
		{name: "test-target", change: func(s *vpngate.Settings) { s.TestURL = "https://different.example/204" }},
		{name: "business-preset", change: func(s *vpngate.Settings) { s.RoutingPreset = "global" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			n := measuredCacheFixture(m.settings, vpngate.Node{ID: "cached", Config: "profile"}, time.Now().Add(-time.Hour))
			m.pool = vpngate.NewPool([]vpngate.Node{n})
			s := m.settings
			test.change(&s)
			m.mu.Lock()
			m.updateSettingsLocked(s)
			m.mu.Unlock()
			actual := m.State().Nodes[0]
			if test.clear {
				if actual.EgressIP != "" || actual.EgressCountry != "" || actual.EgressCheckedAt != "" || actual.EgressCacheKey != "" {
					t.Fatalf("old path's measured exit still displayed: %+v", actual)
				}
			} else if actual.EgressIP != n.EgressIP || actual.EgressCheckedAt != n.EgressCheckedAt {
				t.Fatalf("unchanged egress path lost cache: %+v", actual)
			}
		})
	}
}

func TestDesktopStartupPreservesEgressAcrossCorePathChanges(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			m, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			m.settings.CorePath, m.settings.RulesAutoUpdate = "saved-core", false
			n := measuredCacheFixture(m.settings, vpngate.Node{ID: "cached", Config: "profile"}, time.Now().Add(-time.Hour))
			m.pool = vpngate.NewPool([]vpngate.Node{n})
			m.mu.Lock()
			err = m.persistLocked()
			m.mu.Unlock()
			if err != nil {
				m.Close()
				t.Fatal(err)
			}
			m.Close()
			corePath := "saved-core"
			if changed {
				corePath = "new-portable-core"
			}
			loaded, err := NewDesktop(m.dir, corePath)
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.Close()
			actual := loaded.State().Nodes[0]
			if actual.EgressIP != n.EgressIP || actual.EgressCheckedAt != n.EgressCheckedAt {
				t.Fatalf("restart discarded cache: %+v", actual)
			}
		})
	}
}

func TestFailedProbeDoesNotDisplayAnotherProfileEgress(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := m.settings
	n := measuredCacheFixture(s, vpngate.Node{ID: "cached", Config: "previous profile"}, time.Now().Add(-time.Hour))
	n.Config = "different invalid profile"
	r := m.probe(context.Background(), s, n)
	if r.Status != "failed" || r.EgressIP != "" || r.EgressCountry != "" || r.EgressCheckedAt != "" {
		t.Fatalf("failed replacement profile displayed old exit: %+v", r)
	}
}

func TestExpiredEgressCacheQueriesAgainAndRenewsSuccessfulDate(t *testing.T) {
	m, s, n := egressProbeFixture(t, egressSuccessBody, 0)
	requestPath := filepath.Join(t.TempDir(), "egress-requests")
	t.Setenv("VPNGATE_TEST_EGRESS_REQUEST", requestPath)
	n = measuredCacheFixture(s, n, time.Now().Add(-7*24*time.Hour))
	oldChecked := n.EgressCheckedAt
	t.Setenv("VPNGATE_TEST_EGRESS_BODY", strings.Replace(egressSuccessBody, "8.8.4.4", "9.9.9.9", 1))
	r := m.probe(context.Background(), s, n)
	requests, err := os.ReadFile(requestPath)
	if r.Status != "available" || r.EgressIP != "9.9.9.9" || r.EgressCountry != "美国" || r.EgressRegion != "加利福尼亚州" || r.EgressCheckedAt == oldChecked || err != nil || strings.Count(string(requests), "\n") != 1 {
		t.Fatalf("expired cache was reused or Chinese refresh missing: %+v requests=%q error=%v", r, requests, err)
	}
}

func TestProbeEgressFailureDoesNotInvalidateConnectivity(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{name: "malformed", body: `{"status":`},
		{name: "provider-failure", body: `{"status":"fail","message":"reserved range","query":"8.8.4.4"}`},
		{name: "non-public", body: strings.Replace(egressSuccessBody, "8.8.4.4", "127.0.0.1", 1)},
		{name: "missing-country", body: `{"status":"success","query":"8.8.4.4","countryCode":"US"}`},
		{name: "rate-limit", body: `{"status":"fail","message":"rate limit"}`, status: http.StatusTooManyRequests},
		{name: "oversized", body: strings.Repeat("x", 17<<10)},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, s, n := egressProbeFixture(t, test.body, test.status)
			r := m.probe(context.Background(), s, n)
			fields := egressJSONFields(t, r)
			if r.Status != "available" || r.LatencyMs == nil || r.Error != "" || r.FailureKind != "" {
				t.Fatalf("metadata error marked working tunnel as failed: %+v", r)
			}
			if fields["egressError"] == nil || fields["egressError"] == "" || fields["egressIP"] != "" || fields["egressCountry"] != "" {
				t.Fatalf("metadata failure exposed partial/stale exit: %+v", fields)
			}
		})
	}
}

func TestProbeParentCancellationInterruptsEgressLookup(t *testing.T) {
	m, s, n := egressProbeFixture(t, egressSuccessBody, 0)
	requestPath := filepath.Join(t.TempDir(), "egress-request")
	t.Setenv("VPNGATE_TEST_EGRESS_REQUEST", requestPath)
	t.Setenv("VPNGATE_TEST_EGRESS_BLOCK", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan vpngate.ProbeResult, 1)
	go func() { result <- m.probe(ctx, s, n) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
waitForLookup:
	for {
		select {
		case <-deadline.C:
			t.Fatal("probe never reached candidate egress lookup")
		case r := <-result:
			t.Fatalf("probe ended before lookup cancellation: %+v", r)
		case <-ticker.C:
			if _, err := os.Stat(requestPath); err == nil {
				break waitForLookup
			}
		}
	}
	cancel()
	select {
	case r := <-result:
		if r.Status != "canceled" || r.FailureKind != "canceled" || r.EgressIP != "" {
			t.Fatalf("parent cancellation left a successful or stale exit result: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("egress lookup ignored parent cancellation")
	}
	entries, err := os.ReadDir(filepath.Join(m.dir, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled egress probe retained temporary core files: entries=%d error=%v", len(entries), err)
	}
}

func TestEgressSurvivesProbeRestorationAndPersistence(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var n vpngate.Node
	if err = json.Unmarshal([]byte(`{"id":"one","status":"available","egressIP":"8.8.4.4","egressCountry":"美国","egressCountryCode":"US","egressRegion":"加利福尼亚州","egressCity":"山景城","egressError":""}`), &n); err != nil {
		t.Fatal(err)
	}
	var restored vpngate.Node
	applyToNode(&restored, resultFromNode(n))
	if egressJSONFields(t, restored)["egressRegion"] != "加利福尼亚州" {
		t.Fatal("environment-failure rollback discarded measured exit")
	}
	m.pool = vpngate.NewPool([]vpngate.Node{n})
	m.mu.Lock()
	err = m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := New(m.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if egressJSONFields(t, loaded.State().Nodes[0])["egressCity"] != "山景城" {
		t.Fatal("persisted state lost measured exit")
	}
}

func TestProbeBatchRestoresEgressAfterSharedEnvironmentFailure(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var previous vpngate.Node
	if err = json.Unmarshal([]byte(`{"id":"one","status":"available","egressIP":"8.8.4.4","egressCountry":"美国","egressCountryCode":"US","egressRegion":"加利福尼亚州","egressCity":"山景城","egressError":""}`), &previous); err != nil {
		t.Fatal(err)
	}
	m.pool = vpngate.NewPool([]vpngate.Node{previous})
	s := m.settings
	s.Upstream.Port = 0
	s.TestConcurrency = 1
	m.settings = s
	_, err = m.probeBatch(context.Background(), &vpngate.Job{}, s, []vpngate.Node{previous})
	if err == nil {
		t.Fatal("invalid shared proxy was not reported")
	}
	actual := m.pool.Snapshot()[0]
	if actual.Status != "available" || actual.EgressIP != "8.8.4.4" || actual.EgressCountry != "美国" || actual.EgressCountryCode != "US" || actual.EgressRegion != "加利福尼亚州" || actual.EgressCity != "山景城" || actual.EgressError != "" {
		t.Fatalf("environment failure discarded previous measured egress: %+v", actual)
	}
}

func TestLookupEgressRejectsUnusableProviderData(t *testing.T) {
	for _, test := range []struct {
		name, body string
	}{
		{name: "malformed", body: `{"status":`},
		{name: "empty", body: ""},
		{name: "null", body: `null`},
		{name: "trailing-json", body: egressSuccessBody + `{}`},
		{name: "wrong-type", body: `{"status":"success","query":123,"country":"Japan","countryCode":"JP"}`},
		{name: "missing-status", body: `{"query":"8.8.4.4","country":"Japan","countryCode":"JP"}`},
		{name: "provider-failure", body: `{"status":"fail","message":"reserved range","query":"8.8.4.4"}`},
		{name: "missing-ip", body: `{"status":"success","country":"Japan","countryCode":"JP"}`},
		{name: "missing-country", body: `{"status":"success","query":"8.8.4.4","countryCode":"JP"}`},
		{name: "missing-country-code", body: `{"status":"success","query":"8.8.4.4","country":"Japan"}`},
		{name: "invalid-country-code", body: strings.Replace(egressSuccessBody, `"US"`, `"USA"`, 1)},
		{name: "country-control", body: strings.Replace(egressSuccessBody, "美国", `United\nStates`, 1)},
		{name: "region-control", body: strings.Replace(egressSuccessBody, "加利福尼亚州", `Cali\u0000fornia`, 1)},
		{name: "country-too-long", body: strings.Replace(egressSuccessBody, "美国", strings.Repeat("x", 129), 1)},
		{name: "oversized", body: strings.Repeat("x", 17<<10)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			client, err := proxyClient(diagnosticUpstream(t, server.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			location, err := lookupEgress(context.Background(), client)
			if err == nil || location != (egressLocation{}) {
				t.Fatalf("unusable metadata accepted or partially exposed: %+v error=%v", location, err)
			}
		})
	}
}

func TestLookupEgressRejectsNonPublicAddresses(t *testing.T) {
	for _, address := range []string{
		"invalid", "0.0.0.0", "0.1.2.3", "10.1.2.3", "127.0.0.1", "169.254.1.2", "172.16.0.1", "192.168.1.1", "100.64.0.1", "192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "::", "::1", "fe80::1", "fc00::1", "ff02::1", "::ffff:127.0.0.1", "100::1", "2001:db8::1", "2001:2::1", "3fff::1", "64:ff9b:1::1", "100:0:0:1::1", "5f00::1", "4000::1", "::8.8.4.4", "2001:100::1", "2606:4700:4700::1111%eth0", "::ffff:8.8.4.4%eth0",
	} {
		t.Run(address, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(strings.Replace(egressSuccessBody, "8.8.4.4", address, 1)))
			}))
			defer server.Close()
			client, err := proxyClient(diagnosticUpstream(t, server.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			if location, lookupErr := lookupEgress(context.Background(), client); lookupErr == nil || location.IP != "" {
				t.Fatalf("non-public address accepted: %+v error=%v", location, lookupErr)
			}
		})
	}
}

func TestLookupEgressUsesOnlySuppliedProxyAndAcceptsLastAllowedResponse(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "ip-api.com")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Scheme != "http" || r.URL.Host != "ip-api.com" || r.URL.Path != "/json/" || r.URL.Query().Get("fields") != "status,message,query,country,countryCode,regionName,city" || r.URL.Query().Get("lang") != "zh-CN" {
			t.Errorf("wrong provider query through proxy: %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Rl", "0")
		w.Header().Set("X-Ttl", "42")
		_, _ = w.Write([]byte(strings.Replace(egressSuccessBody, "8.8.4.4", "2606:4700:4700::1111", 1)))
	}))
	defer server.Close()
	client, err := proxyClient(diagnosticUpstream(t, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	location, err := lookupEgress(context.Background(), client)
	if err != nil || location.IP != "2606:4700:4700::1111" || location.Country != "美国" || location.Region != "加利福尼亚州" || location.City != "山景城" || requests.Load() != 1 {
		t.Fatalf("lookup bypassed supplied proxy, retried, or rejected final valid response: %+v requests=%d error=%v", location, requests.Load(), err)
	}
}

func TestLookupEgressDoesNotRetryOrFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusFound} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if status == http.StatusTooManyRequests {
					w.Header().Set("X-Rl", "0")
					w.Header().Set("X-Ttl", "42")
				}
				w.Header().Set("Location", "http://must-not-resolve.invalid/egress")
				w.WriteHeader(status)
			}))
			defer server.Close()
			client, err := proxyClient(diagnosticUpstream(t, server.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			location, err := lookupEgress(context.Background(), client)
			if err == nil || location != (egressLocation{}) || requests.Load() != 1 {
				t.Fatalf("provider error retried or exposed metadata: %+v requests=%d error=%v", location, requests.Load(), err)
			}
			if status == http.StatusTooManyRequests && !strings.Contains(err.Error(), "42") {
				t.Fatalf("rate-limit response lost provider retry interval: %v", err)
			}
		})
	}
}

func TestLookupEgressBoundsTimeoutAndRespectsCancellation(t *testing.T) {
	for _, test := range []struct {
		name          string
		parentTimeout time.Duration
		clientTimeout time.Duration
	}{
		{name: "parent-cancel", parentTimeout: 30 * time.Millisecond},
		{name: "client-timeout", clientTimeout: 30 * time.Millisecond},
		{name: "lookup-five-second-cap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
			defer server.Close()
			client, err := proxyClient(diagnosticUpstream(t, server.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			client.Timeout = test.clientTimeout
			ctx := context.Background()
			if test.parentTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.parentTimeout)
				defer cancel()
			}
			started := time.Now()
			location, err := lookupEgress(ctx, client)
			if !errors.Is(err, context.DeadlineExceeded) || location != (egressLocation{}) {
				t.Fatalf("canceled/timed-out lookup returned metadata: %+v error=%v", location, err)
			}
			if elapsed := time.Since(started); elapsed > 7*time.Second || test.name != "lookup-five-second-cap" && elapsed > time.Second {
				t.Fatalf("lookup ignored its timeout: %s", elapsed)
			}
		})
	}
}
