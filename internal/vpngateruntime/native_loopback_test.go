package vpngateruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"guiforcores/internal/vpngate"
)

// TestNativeOpenVPNLoopback is deliberately opt-in. Both native OpenVPN endpoints
// use internal stacks; nothing invokes the manager, guardian, TUN or OS settings.
func TestNativeOpenVPNLoopback(t *testing.T) {
	core := os.Getenv("VPNGATE_LOOPBACK_CORE")
	if core == "" {
		t.Skip("set VPNGATE_LOOPBACK_CORE to the official sing-box executable to run the local OpenVPN data-path test")
	}
	core, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, protocol string
		authenticate   bool
	}{
		{name: "http", protocol: "http"},
		{name: "socks4", protocol: "socks4"},
		{name: "socks4a", protocol: "socks4a"},
		{name: "socks5", protocol: "socks5"},
		{name: "socks5-auth", protocol: "socks5", authenticate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runNativeOpenVPNLoopback(t, core, test.protocol, test.authenticate)
		})
	}
}

func TestNativeOpenVPNBadUpstreamAuthIsEnvironmentFailure(t *testing.T) {
	core := os.Getenv("VPNGATE_LOOPBACK_CORE")
	if core == "" {
		t.Skip("set VPNGATE_LOOPBACK_CORE to test native upstream authentication failure classification")
	}
	core, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	// Authentication is rejected before this address could be dialed. No VPN
	// server, external target or machine networking configuration is involved.
	vpnPort := loopbackFreePort(t)
	var rejectedAuthentications atomic.Int64
	upstream, _, _, _ := loopbackSOCKS5Proxy(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(vpnPort)), "loopback-user", "accepted-test-password", &rejectedAuthentications)
	_, upstreamPort, err := net.SplitHostPort(upstream)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(upstreamPort)
	if err != nil {
		t.Fatal(err)
	}
	ca, _, _, clientCert, clientKey := loopbackCertificates(t)
	profile := fmt.Sprintf("client\ndev tun\nproto tcp\nremote 127.0.0.1 %d\nremote-cert-tls server\nverify-x509-name loopback-server name\ndata-ciphers AES-256-GCM\nauth SHA256\n<ca>\n%s</ca>\n<cert>\n%s</cert>\n<key>\n%s</key>\n", vpnPort, ca, clientCert, clientKey)
	manager, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	settings := vpngate.DefaultSettings()
	settings.CorePath = core
	settings.Upstream = vpngate.Upstream{Protocol: "socks5", Host: "127.0.0.1", Port: port, Username: "loopback-user", Password: "rejected-test-password"}
	settings.TUNEnabled, settings.SystemProxyEnabled = false, false
	settings.TestTimeoutSeconds = 4
	settings.TestURL = "https://192.0.2.123/should-not-be-reached"
	result := manager.probe(context.Background(), settings, vpngate.Node{ID: "valid-node-with-unusable-upstream-auth", Config: profile})
	t.Logf("native bad-upstream-auth result: status=%s failureKind=%s error=%s", result.Status, result.FailureKind, result.Error)
	if rejectedAuthentications.Load() == 0 {
		t.Fatal("test did not exercise actual upstream authentication rejection")
	}
	if result.FailureKind != "environment" || result.Status != "untested" {
		t.Fatalf("bad upstream authentication was attributed to the VPNGate node: status=%s failureKind=%s", result.Status, result.FailureKind)
	}
}

func runNativeOpenVPNLoopback(t *testing.T, core, protocol string, authenticate bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	ca, serverCert, serverKey, clientCert, clientKey := loopbackCertificates(t)
	var targetRequests atomic.Int64
	const expectedBody = "native OpenVPN over explicit upstream: local DNS and HTTPS success\n"
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		_, _ = io.WriteString(w, expectedBody)
	}))
	t.Cleanup(target.Close)
	targetPort := target.Listener.Addr().(*net.TCPAddr).Port
	// Only the server knows this mapping. TEST-NET-1 is a reserved documentation
	// range, never a real Internet service. gVisor rejects loopback destinations
	// inside the tunnel, so the packet destination cannot itself be 127/8.
	const tunnelTarget = "192.0.2.123"
	const tunnelHostname = "native-loopback.invalid"
	alias := net.JoinHostPort(tunnelHostname, strconv.Itoa(targetPort))
	var dnsAliases []string
	var egressPort int
	var egressRequests atomic.Int64
	if protocol == "http" {
		dnsAliases = append(dnsAliases, "ip-api.com")
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != "ip-api.com" || r.URL.Path != "/json/" || r.URL.Query().Get("lang") != "zh-CN" {
				t.Error("native egress request lost its expected provider host, path or language")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			egressRequests.Add(1)
			_, _ = io.WriteString(w, egressSuccessBody)
		}))
		t.Cleanup(provider.Close)
		egressPort = provider.Listener.Addr().(*net.TCPAddr).Port
	}
	dnsPort, dnsRequests := loopbackDNS(t, tunnelHostname, tunnelTarget, dnsAliases...)
	vpnPort := loopbackFreePort(t)
	vpnAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(vpnPort))
	routeRules := []any{
		map[string]any{"ip_cidr": []string{tunnelTarget + "/32"}, "port": []int{targetPort}, "action": "route-options", "override_address": "127.0.0.1"},
		// BuildConfig's vpn-dns server is 1.1.1.1:53. Only the VPN server
		// redirects that tunnel destination to the ephemeral local DNS stub.
		map[string]any{"ip_cidr": []string{"1.1.1.1/32"}, "port": []int{53}, "action": "route-options", "override_address": "127.0.0.1", "override_port": dnsPort},
		map[string]any{"ip_cidr": []string{tunnelTarget + "/32"}, "port": []int{targetPort}, "action": "route", "outbound": "direct"},
		map[string]any{"ip_cidr": []string{"1.1.1.1/32"}, "port": []int{53}, "action": "route", "outbound": "direct"},
		map[string]any{"ip_cidr": []string{"127.0.0.1/32"}, "port": []int{targetPort, dnsPort}, "action": "route", "outbound": "direct"},
	}
	if egressPort != 0 {
		// The candidate asks the local provider only from inside OpenVPN.
		// DNS resolves the real provider hostname to a test-only address.
		routeRules = append(routeRules, map[string]any{
			"ip_cidr": []string{tunnelTarget + "/32"}, "port": []int{80}, "action": "route", "outbound": "direct",
			"override_address": "127.0.0.1", "override_port": egressPort,
		})
	}
	routeRules = append(routeRules, map[string]any{"action": "reject"})
	serverConfig := map[string]any{
		"log": map[string]any{"level": "debug", "timestamp": true},
		"endpoints": []any{map[string]any{
			"type": "openvpn-server", "tag": "loopback-vpn-server", "listen": "127.0.0.1", "listen_port": vpnPort,
			"system": false, "network": "tcp", "mode": "tls", "address": []string{"10.253.241.1/24"},
			"data_ciphers": []string{"AES-256-GCM"}, "auth": "SHA256", "handshake_window": "10s",
			"tls": map[string]any{"certificate": serverCert, "key": serverKey, "client_certificate": ca, "verify_client_certificate": "require", "remote_certificate_tls": "client"},
		}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"final": "direct", "rules": routeRules},
	}
	serverRaw, err := json.MarshalIndent(serverConfig, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	server := loopbackStartCore(t, ctx, core, dir, "server", serverRaw)
	server.waitListener(t, ctx, vpnAddress)
	var proxyAddress string
	var proxySessions, sentBytes, receivedBytes *atomic.Int64
	username, password := "", ""
	if authenticate {
		username, password = "loopback-user", "loopback-password"
	}
	remoteHost := "127.0.0.1"
	var bootstrapRequests *atomic.Int64
	if protocol == "socks4" || protocol == "socks4a" {
		username = "loopback-userid"
		allowedTargets := map[string]string{vpnAddress: vpnAddress}
		if protocol == "socks4a" {
			// Native OpenVPN resolves its remote through BuildConfig's bootstrap
			// DNS over upstream before opening the VPN connection. Only this proxy
			// can reach that local DNS mapping; system DNS cannot provide the name.
			remoteHost = "loopback-vpn.invalid"
			bootstrapPort, requests := loopbackDNS(t, remoteHost, "127.0.0.1")
			bootstrapRequests = requests
			allowedTargets["1.1.1.1:53"] = net.JoinHostPort("127.0.0.1", strconv.Itoa(bootstrapPort))
			allowedTargets[net.JoinHostPort(remoteHost, strconv.Itoa(vpnPort))] = vpnAddress
		}
		proxyAddress, proxySessions, sentBytes, receivedBytes = loopbackSOCKS4Proxy(t, allowedTargets, username)
	} else if protocol == "socks5" {
		proxyAddress, proxySessions, sentBytes, receivedBytes = loopbackSOCKS5Proxy(t, vpnAddress, username, password)
	} else {
		proxyAddress, proxySessions, sentBytes, receivedBytes = loopbackCONNECTProxy(t, vpnAddress)
	}
	_, proxyPort, err := net.SplitHostPort(proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	proxyPortNumber, err := strconv.Atoi(proxyPort)
	if err != nil {
		t.Fatal(err)
	}
	mixedPort := loopbackFreePort(t)
	settings := vpngate.DefaultSettings()
	settings.Upstream = vpngate.Upstream{Protocol: protocol, Host: "127.0.0.1", Port: proxyPortNumber, Username: username, Password: password}
	settings.MixedPort = mixedPort
	settings.TUNEnabled, settings.SystemProxyEnabled = false, false
	profile := fmt.Sprintf("client\ndev tun\nproto tcp\nremote %s %d\nremote-cert-tls server\nverify-x509-name loopback-server name\ndata-ciphers AES-256-GCM\nauth SHA256\n<ca>\n%s</ca>\n<cert>\n%s</cert>\n<key>\n%s</key>\n", remoteHost, vpnPort, ca, clientCert, clientKey)
	clientRaw, err := vpngate.BuildConfig(settings, &vpngate.Node{Config: profile}, vpngate.ConfigOptions{Production: false, MixedPort: mixedPort})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	client := loopbackStartCore(t, ctx, core, dir, "client", clientRaw)
	mixedAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(mixedPort))
	client.waitListener(t, ctx, mixedAddress)
	proxyURL := &url.URL{Scheme: "http", Host: mixedAddress}
	roots := x509.NewCertPool()
	roots.AddCert(target.Certificate())
	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		TLSClientConfig:     &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12},
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	// The mixed listener starts before OpenVPN's asynchronous handshake finishes.
	// Retry real HTTPS requests within one bounded window rather than treating
	// listener readiness as proof of an established VPN session.
	requestContext, stopRequests := context.WithTimeout(ctx, 25*time.Second)
	defer stopRequests()
	var response *http.Response
	for {
		req, requestErr := http.NewRequestWithContext(requestContext, http.MethodGet, "https://"+alias+"/native-loopback", nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response, err = httpClient.Do(req)
		if err == nil {
			break
		}
		select {
		case <-requestContext.Done():
			t.Fatalf("HTTPS through native OpenVPN never became ready: %v", err)
		case <-client.done:
			t.Fatalf("native client exited during handshake: %v", client.err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || string(body) != expectedBody {
		t.Fatalf("unexpected HTTPS result: status=%d body=%q error=%v", response.StatusCode, body, readErr)
	}
	if proxySessions.Load() == 0 || sentBytes.Load() == 0 || receivedBytes.Load() == 0 || dnsRequests.Load() == 0 || targetRequests.Load() != 1 {
		t.Fatalf("incomplete data-path evidence: upstream=%s sessions=%d sent=%d received=%d VPN_DNS=%d HTTPS=%d", protocol, proxySessions.Load(), sentBytes.Load(), receivedBytes.Load(), dnsRequests.Load(), targetRequests.Load())
	}
	if bootstrapRequests != nil && bootstrapRequests.Load() == 0 {
		t.Fatal("native remote hostname was not resolved through the explicit upstream DNS path")
	}
	if egressPort != 0 {
		lookupClient, err := proxyClient(vpngate.Upstream{Host: "127.0.0.1", Port: mixedPort, Protocol: "http"})
		if err != nil {
			t.Fatal(err)
		}
		defer lookupClient.CloseIdleConnections()
		location, err := lookupEgress(ctx, lookupClient)
		if err != nil || egressRequests.Load() != 1 || location.IP != "8.8.4.4" || location.City != "山景城" {
			t.Fatalf("the native candidate did not carry the provider lookup: requests=%d result=%+v err=%v", egressRequests.Load(), location, err)
		}
	}
	t.Logf("native OpenVPN mutual TLS + DNS + HTTPS passed: upstream=%s authenticated=%t latency=%s sessions=%d upstream_bytes=%d downstream_bytes=%d VPN_DNS=%d target_requests=%d", protocol, authenticate, time.Since(started).Round(time.Millisecond), proxySessions.Load(), sentBytes.Load(), receivedBytes.Load(), dnsRequests.Load(), targetRequests.Load())
}

func loopbackCertificates(t *testing.T) (caPEM, serverCert, serverKey, clientCert, clientKey string) {
	t.Helper()
	now := time.Now()
	makeKey := func() *rsa.PrivateKey {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	caKey := makeKey()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ephemeral loopback test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	leaf := func(serial int64, name string, usage x509.ExtKeyUsage) (string, string) {
		key := makeKey()
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	}
	serverCert, serverKey = leaf(2, "loopback-server", x509.ExtKeyUsageServerAuth)
	clientCert, clientKey = leaf(3, "loopback-client", x509.ExtKeyUsageClientAuth)
	return
}

func loopbackFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

type loopbackLog struct {
	sync.Mutex
	data bytes.Buffer
}

func (b *loopbackLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.data.Write(p)
}

func (b *loopbackLog) String() string {
	b.Lock()
	defer b.Unlock()
	return b.data.String()
}

type loopbackCore struct {
	done chan struct{}
	log  *loopbackLog
	err  error
}

func loopbackStartCore(t *testing.T, ctx context.Context, core, dir, name string, config []byte) *loopbackCore {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, core, "run", "-D", dir, "-c", path)
	hideCommand(cmd)
	result := &loopbackCore{done: make(chan struct{}), log: &loopbackLog{}}
	cmd.Stdout, cmd.Stderr = result.log, result.log
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	go func() {
		result.err = cmd.Wait()
		close(result.done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-result.done:
		case <-time.After(5 * time.Second):
			t.Errorf("%s core did not exit after kill", name)
		}
		if t.Failed() {
			t.Logf("%s native core output:\n%s", name, result.log.String())
		}
	})
	return result
}

func (core *loopbackCore) waitListener(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		connection, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err == nil {
			connection.Close()
			return
		}
		select {
		case <-core.done:
			t.Fatalf("native core exited before %s was ready: %v\n%s", address, core.err, core.log.String())
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("native core listener %s not ready\n%s", address, core.log.String())
		case <-tick.C:
		}
	}
}

func loopbackCONNECTProxy(t *testing.T, allowed string) (string, *atomic.Int64, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var sessions, sent, received atomic.Int64
	var mu sync.Mutex
	active := map[net.Conn]struct{}{}
	var workers sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != allowed {
			http.Error(w, "only CONNECT to the local VPN server is allowed", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp4", allowed, 3*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		mu.Lock()
		active[upstream], active[downstream] = struct{}{}, struct{}{}
		mu.Unlock()
		defer func() {
			upstream.Close()
			downstream.Close()
			mu.Lock()
			delete(active, upstream)
			delete(active, downstream)
			mu.Unlock()
		}()
		_ = upstream.SetDeadline(time.Now().Add(60 * time.Second))
		_ = downstream.SetDeadline(time.Now().Add(60 * time.Second))
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		sessions.Add(1)
		workers.Add(1)
		copied := make(chan struct{})
		go func() {
			defer workers.Done()
			// A small counting writer exposes traffic before the stream closes.
			_, _ = io.Copy(loopbackCountingWriter{Writer: downstream, count: &received}, upstream)
			downstream.Close()
			close(copied)
		}()
		_, _ = io.Copy(loopbackCountingWriter{Writer: upstream, count: &sent}, buffered)
		upstream.Close()
		<-copied
	}))
	t.Cleanup(func() {
		proxy.CloseClientConnections()
		mu.Lock()
		for connection := range active {
			connection.Close()
		}
		mu.Unlock()
		proxy.Close()
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("CONNECT proxy cleanup exceeded five seconds")
		}
	})
	return proxy.Listener.Addr().String(), &sessions, &sent, &received
}

// loopbackTCPService owns only ephemeral loopback sockets and waits for every
// handler before cleanup returns. A handler cannot resolve or dial remote hosts.
func loopbackTCPService(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	active := map[net.Conn]struct{}{}
	var workers sync.WaitGroup
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			active[connection] = struct{}{}
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer connection.Close()
				defer func() { mu.Lock(); delete(active, connection); mu.Unlock() }()
				_ = connection.SetDeadline(time.Now().Add(60 * time.Second))
				handler(connection)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-accepted
		mu.Lock()
		for connection := range active {
			connection.Close()
		}
		mu.Unlock()
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("loopback TCP service cleanup exceeded five seconds")
		}
	})
	return listener.Addr().String()
}

func loopbackSOCKS5Proxy(t *testing.T, allowed, username, password string, rejectedAuthentications ...*atomic.Int64) (string, *atomic.Int64, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var sessions, sent, received atomic.Int64
	address := loopbackTCPService(t, func(downstream net.Conn) {
		var greeting [2]byte
		if _, err := io.ReadFull(downstream, greeting[:]); err != nil || greeting[0] != 5 {
			return
		}
		methods := make([]byte, int(greeting[1]))
		if _, err := io.ReadFull(downstream, methods); err != nil {
			return
		}
		method := byte(0)
		if username != "" {
			method = 2
		}
		if !bytes.Contains(methods, []byte{method}) {
			_, _ = downstream.Write([]byte{5, 255})
			return
		}
		if _, err := downstream.Write([]byte{5, method}); err != nil {
			return
		}
		if method == 2 {
			var version [1]byte
			if _, err := io.ReadFull(downstream, version[:]); err != nil || version[0] != 1 {
				return
			}
			readField := func() (string, error) {
				var length [1]byte
				if _, err := io.ReadFull(downstream, length[:]); err != nil {
					return "", err
				}
				field := make([]byte, int(length[0]))
				_, err := io.ReadFull(downstream, field)
				return string(field), err
			}
			user, userErr := readField()
			pass, passErr := readField()
			if userErr != nil || passErr != nil || user != username || pass != password {
				if userErr == nil && passErr == nil {
					for _, rejected := range rejectedAuthentications {
						rejected.Add(1)
					}
				}
				_, _ = downstream.Write([]byte{1, 1})
				return
			}
			if _, err := downstream.Write([]byte{1, 0}); err != nil {
				return
			}
		}
		var request [4]byte
		if _, err := io.ReadFull(downstream, request[:]); err != nil || request[0] != 5 || request[1] != 1 || request[2] != 0 {
			return
		}
		// The generated OpenVPN remote is an IPv4 loopback literal. Reject domain
		// and IPv6 forms as well as any destination other than that local server.
		var destination [6]byte
		if request[3] != 1 {
			return
		}
		if _, err := io.ReadFull(downstream, destination[:]); err != nil {
			return
		}
		requested := net.JoinHostPort(net.IP(destination[:4]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(destination[4:]))))
		if requested != allowed {
			_, _ = downstream.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		upstream, err := net.DialTimeout("tcp4", allowed, 3*time.Second)
		if err != nil {
			return
		}
		defer upstream.Close()
		_ = upstream.SetDeadline(time.Now().Add(60 * time.Second))
		if _, err := downstream.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
			return
		}
		sessions.Add(1)
		copied := make(chan struct{})
		go func() {
			_, _ = io.Copy(loopbackCountingWriter{Writer: downstream, count: &received}, upstream)
			downstream.Close()
			close(copied)
		}()
		_, _ = io.Copy(loopbackCountingWriter{Writer: upstream, count: &sent}, downstream)
		upstream.Close()
		<-copied
	})
	return address, &sessions, &sent, &received
}

func loopbackDNS(t *testing.T, hostname, target string, aliases ...string) (int, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	ip := net.ParseIP(target).To4()
	address := loopbackTCPService(t, func(connection net.Conn) {
		for {
			var size [2]byte
			if _, err := io.ReadFull(connection, size[:]); err != nil {
				return
			}
			packet := make([]byte, int(binary.BigEndian.Uint16(size[:])))
			if _, err := io.ReadFull(connection, packet); err != nil {
				return
			}
			var query dnsmessage.Message
			if err := query.Unpack(packet); err != nil || len(query.Questions) != 1 {
				return
			}
			question := query.Questions[0]
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, Authoritative: true, RecursionDesired: query.RecursionDesired, RecursionAvailable: true}, Questions: query.Questions}
			name := strings.TrimSuffix(question.Name.String(), ".")
			if (name == hostname || slices.Contains(aliases, name)) && question.Type == dnsmessage.TypeA && question.Class == dnsmessage.ClassINET {
				requests.Add(1)
				response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{ip[0], ip[1], ip[2], ip[3]}}}}
			} else {
				response.RCode = dnsmessage.RCodeNameError
			}
			wire, err := response.Pack()
			if err != nil {
				return
			}
			binary.BigEndian.PutUint16(size[:], uint16(len(wire)))
			if _, err = connection.Write(append(size[:], wire...)); err != nil {
				return
			}
		}
	})
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return portNumber, &requests
}

func loopbackSOCKS4Proxy(t *testing.T, allowedTargets map[string]string, expectedUserID string) (string, *atomic.Int64, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var sessions, sent, received atomic.Int64
	address := loopbackTCPService(t, func(downstream net.Conn) {
		var header [8]byte
		if _, err := io.ReadFull(downstream, header[:]); err != nil || header[0] != 4 || header[1] != 1 {
			return
		}
		readString := func() (string, error) {
			var value []byte
			for len(value) <= 255 {
				var octet [1]byte
				if _, err := io.ReadFull(downstream, octet[:]); err != nil {
					return "", err
				}
				if octet[0] == 0 {
					return string(value), nil
				}
				value = append(value, octet[0])
			}
			return "", fmt.Errorf("oversized SOCKS4 string")
		}
		userID, err := readString()
		if err != nil || userID != expectedUserID {
			return
		}
		host := net.IP(header[4:8]).String()
		if header[4] == 0 && header[5] == 0 && header[6] == 0 && header[7] != 0 {
			host, err = readString()
			if err != nil {
				return
			}
		}
		requested := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(header[2:4]))))
		allowed, ok := allowedTargets[requested]
		if !ok {
			_, _ = downstream.Write([]byte{0, 91, 0, 0, 0, 0, 0, 0})
			return
		}
		// The name/address is explicitly allowlisted and mapped to this local
		// server; the proxy never resolves or forwards an external destination.
		upstream, err := net.DialTimeout("tcp4", allowed, 3*time.Second)
		if err != nil {
			return
		}
		defer upstream.Close()
		_ = upstream.SetDeadline(time.Now().Add(60 * time.Second))
		if _, err = downstream.Write([]byte{0, 90, 0, 0, 127, 0, 0, 1}); err != nil {
			return
		}
		sessions.Add(1)
		copied := make(chan struct{})
		go func() {
			_, _ = io.Copy(loopbackCountingWriter{Writer: downstream, count: &received}, upstream)
			downstream.Close()
			close(copied)
		}()
		_, _ = io.Copy(loopbackCountingWriter{Writer: upstream, count: &sent}, downstream)
		upstream.Close()
		<-copied
	})
	return address, &sessions, &sent, &received
}

type loopbackCountingWriter struct {
	io.Writer
	count *atomic.Int64
}

func (w loopbackCountingWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.count.Add(int64(n))
	return n, err
}
