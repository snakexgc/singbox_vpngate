package vpngateruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"guiforcores/internal/vpngate"
)

func TestSOCKSPreflightNegotiatesAuthenticationWithoutConnect(t *testing.T) {
	for _, test := range []struct {
		name           string
		method, status byte
		credentials    bool
		emptyPassword  bool
		wantError      string
	}{
		{name: "no-auth", method: 0},
		{name: "accepted-auth", method: 2, credentials: true},
		{name: "accepted-empty-password", method: 2, credentials: true, emptyPassword: true},
		{name: "rejected-auth", method: 2, status: 1, credentials: true, wantError: "认证失败"},
		{name: "required-auth", method: 255, wantError: "认证方式"},
	} {
		t.Run(test.name, func(t *testing.T) {
			type exchange struct{ negotiated, connectSent bool }
			done := make(chan exchange, 1)
			address := loopbackTCPService(t, func(conn net.Conn) {
				result := exchange{}
				defer func() { done <- result }()
				var greeting [2]byte
				if _, err := io.ReadFull(conn, greeting[:]); err != nil || greeting[0] != 5 {
					return
				}
				methods := make([]byte, greeting[1])
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				expectedMethod := test.method
				if expectedMethod == 255 {
					expectedMethod = 0
				}
				result.negotiated = bytes.Equal(methods, []byte{expectedMethod})
				_, _ = conn.Write([]byte{5, test.method})
				if test.method == 2 {
					var prefix [2]byte
					if _, err := io.ReadFull(conn, prefix[:]); err != nil || prefix[0] != 1 {
						return
					}
					username := make([]byte, prefix[1])
					if _, err := io.ReadFull(conn, username); err != nil {
						return
					}
					var length [1]byte
					if _, err := io.ReadFull(conn, length[:]); err != nil {
						return
					}
					password := make([]byte, length[0])
					if _, err := io.ReadFull(conn, password); err != nil {
						return
					}
					wantedPassword := "fixture-password"
					if test.emptyPassword {
						wantedPassword = ""
					}
					if string(username) != "fixture-user" || string(password) != wantedPassword {
						t.Error("SOCKS control exchange did not use configured authentication")
					}
					_, _ = conn.Write([]byte{1, test.status})
				}
				var extra [1]byte
				count, _ := conn.Read(extra[:])
				result.connectSent = count != 0
			})
			upstream := diagnosticUpstream(t, "http://"+address)
			upstream.Protocol = "socks5"
			if test.credentials {
				upstream.Username, upstream.Password = "fixture-user", "fixture-password"
				if test.emptyPassword {
					upstream.Password = ""
				}
			}
			err := preflight(context.Background(), upstream)
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("incorrect SOCKS authentication result: %v", err)
			}
			select {
			case result := <-done:
				if !result.negotiated || result.connectSent {
					t.Fatalf("control check skipped authentication or sent CONNECT: %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("SOCKS control connection was not closed promptly")
			}
		})
	}
}

func TestHTTPProxyAccessChecksOnlyCandidateAndExplicitCommonFailures(t *testing.T) {
	const target = "192.0.2.10:443"
	for _, test := range []struct {
		status    int
		wantError string
	}{
		{status: http.StatusOK},
		{status: http.StatusProxyAuthRequired, wantError: "HTTP 407"},
		{status: http.StatusMethodNotAllowed, wantError: "不支持 CONNECT"},
		{status: http.StatusNotImplemented, wantError: "不支持 CONNECT"},
		{status: http.StatusBadGateway},
		{status: http.StatusForbidden},
	} {
		t.Run(strconv.Itoa(test.status), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodConnect || r.Host != target {
					t.Errorf("proxy check used unrelated request: method=%s target=%s", r.Method, r.Host)
				}
				wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-password"))
				if r.Header.Get("Proxy-Authorization") != wantAuth {
					t.Error("HTTP control exchange omitted configured authentication")
				}
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			upstream := diagnosticUpstream(t, server.URL)
			upstream.Username, upstream.Password = "fixture-user", "fixture-password"
			err := checkProxyAccess(context.Background(), upstream, target)
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("incorrect proxy/target failure classification for HTTP %d: %v", test.status, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("control check sent %d requests", requests.Load())
			}
		})
	}
}

func TestHTTPProxyTargetTimeoutIsNotACommonOutage(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := checkProxyAccess(ctx, diagnosticUpstream(t, server.URL), "192.0.2.10:443"); err != nil {
		t.Fatalf("candidate CONNECT timeout was treated as a common proxy outage: %v", err)
	}
}

func TestHTTPProxyAuthFailureIsEnvironmentBeforeCoreStartup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusProxyAuthRequired) }))
	defer server.Close()
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := vpngate.DefaultSettings()
	s.Upstream = diagnosticUpstream(t, server.URL)
	s.CorePath = filepath.Join(t.TempDir(), "must-not-launch.exe")
	node := vpngate.Node{ID: "candidate", Config: "client\ndev tun\nproto tcp\nremote 192.0.2.10 443\n<ca>\nfixture-ca\n</ca>\n"}
	result := m.probe(context.Background(), s, node)
	if result.Status != "untested" || result.FailureKind != "environment" || !strings.Contains(result.Error, "HTTP 407") {
		t.Fatalf("common proxy authentication failure was attributed to candidate: %+v", result)
	}
	if _, err = os.Stat(filepath.Join(m.dir, "probes")); !os.IsNotExist(err) {
		t.Fatalf("authentication failure reached core configuration/startup: %v", err)
	}
}

func TestSOCKSProxyProtocolAndCredentialBounds(t *testing.T) {
	address := loopbackTCPService(t, func(conn net.Conn) {
		var greeting [3]byte
		_, _ = io.ReadFull(conn, greeting[:])
		_, _ = conn.Write([]byte{4, 0})
	})
	for _, test := range []struct {
		name, username, password, wantError string
	}{
		{name: "wrong-protocol-version", wantError: "SOCKS5 协议响应"},
		{name: "oversized-username", username: strings.Repeat("u", 256), password: "p", wantError: "255 字节"},
		{name: "missing-username", password: "p", wantError: "用户名为空"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := diagnosticUpstream(t, "http://"+address)
			upstream.Protocol, upstream.Username, upstream.Password = "socks5", test.username, test.password
			if err := preflight(context.Background(), upstream); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("unsafe SOCKS negotiation accepted: %v", err)
			}
		})
	}
}

// A listener check must not request unrelated websites. The proxy accepts
// application traffic while TLS to the VPNGate homepage would stall forever.
func TestMixedListenerCheckDoesNotDependOnWebsiteTLS(t *testing.T) {
	release := make(chan struct{})
	var websiteAttempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			if r.Host != "www.vpngate.net:443" {
				t.Errorf("unexpected CONNECT target: %s", r.Host)
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			websiteAttempts.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
			<-release
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer close(release)
	upstream := diagnosticUpstream(t, server.URL)
	client, err := proxyClient(upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://application-target.invalid/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("control application request failed: %s", response.Status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = preflight(ctx, upstream)
	if err != nil || websiteAttempts.Load() != 0 {
		t.Fatalf("listener reachability wrongly depended on website TLS: attempts=%d error=%v", websiteAttempts.Load(), err)
	}
}

// A node test must reach core validation without an unrelated website request.
func TestNodeTestsDoNotDependOnVPNGateWebsite(t *testing.T) {
	var websiteAttempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect && r.Host == "www.vpngate.net:443" {
			websiteAttempts.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.settings.Upstream = diagnosticUpstream(t, server.URL)
	m.settings.CorePath = filepath.Join(t.TempDir(), "deliberately-missing-core.exe")
	m.pool = vpngate.NewPool([]vpngate.Node{{ID: "existing-node", ConfigHash: "config", Status: "untested"}})
	job, err := m.startTests(nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		state := m.State()
		for _, current := range state.Jobs {
			if current.ID == job.ID && current.Status != "running" {
				if websiteAttempts.Load() != 0 {
					t.Fatalf("node tests wrongly depended on VPNGate website: requests=%d status=%s message=%s", websiteAttempts.Load(), current.Status, current.Message)
				}
				if !strings.Contains(current.Message, "无法读取 sing-box 版本") {
					t.Fatalf("did not reach independent core validation: %s", current.Message)
				}
				return
			}
		}
		select {
		case <-deadline:
			t.Fatal("node test job never finished")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestOneFailedNodeMustNotAbortBatchOnUnrelatedWebsiteFailure(t *testing.T) {
	var websiteAttempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect && r.Host == "www.vpngate.net:443" {
			websiteAttempts.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := vpngate.DefaultSettings()
	s.Upstream = diagnosticUpstream(t, server.URL)
	s.TestConcurrency = 1
	m.settings = s
	nodes := []vpngate.Node{{ID: "bad-one", ConfigHash: "one", Config: "invalid profile", Status: "untested"}, {ID: "bad-two", ConfigHash: "two", Config: "invalid profile", Status: "untested"}}
	m.pool = vpngate.NewPool(nodes)
	job := &vpngate.Job{}
	results, batchErr := m.probeBatch(context.Background(), job, s, nodes)
	if batchErr != nil || websiteAttempts.Load() != 0 || job.Completed != 2 || len(results) != 2 {
		t.Fatalf("unrelated website failure aborted node batch: requests=%d completed=%d/2 results=%d error=%v", websiteAttempts.Load(), job.Completed, len(results), batchErr)
	}
	if job.Failed != 2 {
		t.Fatalf("invalid node profiles were not counted as node failures: %+v", job)
	}
	for _, current := range m.pool.Snapshot() {
		if current.Status != "failed" || current.FailureKind != "node" {
			t.Fatalf("invalid profile was misclassified as a shared upstream outage: %+v", current)
		}
	}
}

func TestUnreachableMixedListenerPreservesPreviousNodeHealth(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := vpngate.DefaultSettings()
	s.Upstream = vpngate.Upstream{Host: "127.0.0.1", Port: port, Protocol: "http"}
	s.TestConcurrency = 1
	m.settings = s
	latency := int64(42)
	nodes := []vpngate.Node{{ID: "previous-one", ConfigHash: "one", Config: "invalid profile", Status: "available", LatencyMs: &latency}, {ID: "previous-two", ConfigHash: "two", Config: "invalid profile", Status: "available", LatencyMs: &latency}}
	m.pool = vpngate.NewPool(nodes)
	_, batchErr := m.probeBatch(context.Background(), &vpngate.Job{}, s, nodes)
	if batchErr == nil || !strings.Contains(batchErr.Error(), "无法连接配置的外部 mixed 监听端口") || strings.Contains(batchErr.Error(), "www.vpngate.net") {
		t.Fatalf("listener failure lacks accurate scope: %v", batchErr)
	}
	for _, current := range m.pool.Snapshot() {
		if current.Status != "available" || current.LatencyMs == nil || *current.LatencyMs != latency {
			t.Fatalf("common listener failure poisoned previous node health: %+v", current)
		}
	}
}

func diagnosticUpstream(t *testing.T, raw string) vpngate.Upstream {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, rawPort, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	return vpngate.Upstream{Host: host, Port: port, Protocol: "http"}
}
