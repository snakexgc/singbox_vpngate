package vpngateruntime

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"guiforcores/internal/vpngate"
)

// This regression uses only loopback listeners, internal routing, and an offline
// VPN policy. It never starts TUN, changes system proxy, or connects to a VPN.
func TestNativeBlacklistRecognizesCachedIPTLSName(t *testing.T) {
	t.Run("no-node", func(t *testing.T) { testNativeBlacklist(t, false) })
	t.Run("unavailable-node", func(t *testing.T) { testNativeBlacklist(t, true) })
}

func testNativeBlacklist(t *testing.T, configureNode bool) {
	core := os.Getenv("VPNGATE_LOOPBACK_CORE")
	if core == "" {
		t.Skip("set VPNGATE_LOOPBACK_CORE to test generated routing with the official core")
	}
	core, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	ruleSource := filepath.Join(dir, "gfw.json")
	ruleBinary := filepath.Join(dir, "gfw.srs")
	if err := os.WriteFile(ruleSource, []byte(`{"version":3,"rules":[{"domain":["blocked-loopback.invalid"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(ctx, core, "rule-set", "compile", "--output", ruleBinary, ruleSource).CombinedOutput(); err != nil {
		t.Fatalf("compile local domain fixture: %v\n%s", err, output)
	}

	var requests atomic.Int64
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "original destination preserved")
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{routingTestCertificate(t)}}
	target.StartTLS()
	t.Cleanup(target.Close)
	targetPort := target.Listener.Addr().(*net.TCPAddr).Port
	settings := vpngate.DefaultSettings()
	settings.RoutingPreset = "blacklist"
	settings.TUNEnabled, settings.SystemProxyEnabled = false, false
	settings.MixedPort = loopbackFreePort(t)
	var node *vpngate.Node
	if configureNode {
		// An owned HTTP upstream always refuses CONNECT. Neither OpenVPN nor
		// any configured DNS transport can reach a network outside the fixture.
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable VPN fixture", http.StatusBadGateway)
		}))
		t.Cleanup(upstream.Close)
		settings.Upstream.Port = upstream.Listener.Addr().(*net.TCPAddr).Port
		ca, _, _, cert, key := loopbackCertificates(t)
		node = &vpngate.Node{Config: fmt.Sprintf("client\ndev tun\nproto tcp\nremote 127.0.0.1 %d\nremote-cert-tls server\n<ca>\n%s</ca>\n<cert>\n%s</cert>\n<key>\n%s</key>\n", loopbackFreePort(t), ca, cert, key)}
	}
	raw, err := vpngate.BuildConfig(settings, node, vpngate.ConfigOptions{Production: true, GFWRulesPath: ruleBinary})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	// Classify the original public-looking IP, then allow only this exact test
	// target to reach a local server. Unknown destinations and all DNS reject.
	// The real domain rules above remain untouched.
	route := config["route"].(map[string]any)
	route["rules"] = append(route["rules"].([]any),
		map[string]any{"ip_cidr": []string{"192.0.2.123/32", "2001:db8::123/128"}, "port": []int{targetPort}, "action": "route", "outbound": "direct", "override_address": "127.0.0.1"},
		map[string]any{"action": "reject"},
	)
	config["dns"].(map[string]any)["rules"] = []any{map[string]any{"action": "reject"}}
	raw, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	client := loopbackStartCore(t, ctx, core, dir, "blacklist-client", raw)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(settings.MixedPort))
	client.waitListener(t, ctx, address)
	proxyURL := &url.URL{Scheme: "http", Host: address}
	roots := x509.NewCertPool()
	roots.AddCert(target.Certificate())
	request := func(t *testing.T, destination, name string) error {
		t.Helper()
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: name, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		response, err := httpClient.Get("https://" + net.JoinHostPort(destination, strconv.Itoa(targetPort)) + "/cached-ip")
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK || string(body) != "original destination preserved" {
			t.Fatalf("unexpected local response: status=%d body=%q", response.StatusCode, body)
		}
		return nil
	}
	for _, destination := range []string{"192.0.2.123", "2001:db8::123"} {
		t.Run(destination, func(t *testing.T) {
			t.Run("listed-SNI-rejects-without-DNS-history", func(t *testing.T) {
				before := requests.Load()
				if err := request(t, destination, "blocked-loopback.invalid"); err == nil || requests.Load() != before {
					t.Fatalf("listed TLS name escaped the offline VPN branch via a cached IP: err=%v direct_requests=%d", err, requests.Load()-before)
				}
			})
			t.Run("unlisted-SNI-preserves-IP-destination", func(t *testing.T) {
				before := requests.Load()
				if err := request(t, destination, "allowed-loopback.invalid"); err != nil {
					t.Fatalf("unlisted request lost its original IP or attempted DNS: %v", err)
				}
				if requests.Load() != before+1 {
					t.Fatal("unlisted request did not reach the original destination")
				}
			})
		})
	}
}

func routingTestCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"blocked-loopback.invalid", "allowed-loopback.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestNativeLocalNamesNeverUsePublicDNS(t *testing.T) {
	core := os.Getenv("VPNGATE_LOOPBACK_CORE")
	if core == "" {
		t.Skip("set VPNGATE_LOOPBACK_CORE to test private-name DNS with the official core")
	}
	core, err := filepath.Abs(core)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 static-test.lan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var publicQueries, targetRequests atomic.Int64
	publicDNS := loopbackTCPService(t, func(connection net.Conn) {
		for {
			var size [2]byte
			if _, err := io.ReadFull(connection, size[:]); err != nil {
				return
			}
			wire := make([]byte, int(binary.BigEndian.Uint16(size[:])))
			if _, err := io.ReadFull(connection, wire); err != nil {
				return
			}
			var query dnsmessage.Message
			if err := query.Unpack(wire); err != nil || len(query.Questions) != 1 {
				return
			}
			publicQueries.Add(1)
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionAvailable: true}, Questions: query.Questions}
			if query.Questions[0].Type == dnsmessage.TypeA {
				response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
			}
			wire, err := response.Pack()
			if err != nil {
				return
			}
			binary.BigEndian.PutUint16(size[:], uint16(len(wire)))
			if _, err := connection.Write(append(size[:], wire...)); err != nil {
				return
			}
		}
	})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		_, _ = io.WriteString(w, "local host reached")
	}))
	t.Cleanup(target.Close)
	settings := vpngate.DefaultSettings()
	settings.RoutingPreset = "direct"
	settings.TUNEnabled, settings.SystemProxyEnabled = false, false
	settings.MixedPort = loopbackFreePort(t)
	raw, err := vpngate.BuildConfig(settings, nil, vpngate.ConfigOptions{Production: true})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	_, publicPort, _ := net.SplitHostPort(publicDNS)
	publicPortNumber, _ := strconv.Atoi(publicPort)
	for _, item := range config["dns"].(map[string]any)["servers"].([]any) {
		server := item.(map[string]any)
		if server["tag"] == "direct-dns" {
			// Replace the public resolver with a loopback observer; no DNS can
			// leave the host even when this regression fails.
			server["server"], server["server_port"] = "127.0.0.1", publicPortNumber
		}
		if server["tag"] == "local-dns" {
			server["path"] = []string{hostsPath}
		}
	}
	raw, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	client := loopbackStartCore(t, ctx, core, dir, "private-names-client", raw)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(settings.MixedPort))
	client.waitListener(t, ctx, address)
	for _, hostname := range []string{"secret.lan", "secret.local", "localhost", "static-test.lan"} {
		t.Run(hostname, func(t *testing.T) {
			before := publicQueries.Load()
			answer := queryMixedDNS(t, address, hostname)
			known := hostname == "localhost" || hostname == "static-test.lan"
			if known {
				if len(answer.Answers) != 1 || answer.Answers[0].Body.(*dnsmessage.AResource).A != [4]byte{127, 0, 0, 1} {
					t.Fatalf("hosts lookup did not return local IPv4: %+v", answer)
				}
			} else if answer.RCode != dnsmessage.RCodeNameError || len(answer.Answers) != 0 {
				t.Errorf("unknown private name did not fail closed: %+v", answer)
			}
			if publicQueries.Load() != before {
				t.Errorf("private DNS name reached public resolver: queries=%d", publicQueries.Load()-before)
			}
		})
	}
	proxyURL := &url.URL{Scheme: "http", Host: address}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	targetPort := target.Listener.Addr().(*net.TCPAddr).Port
	for _, hostname := range []string{"business-secret.lan", "localhost", "static-test.lan"} {
		t.Run("business/"+hostname, func(t *testing.T) {
			beforeDNS, beforeHTTP := publicQueries.Load(), targetRequests.Load()
			response, err := httpClient.Get("http://" + net.JoinHostPort(hostname, strconv.Itoa(targetPort)) + "/hosts")
			if response != nil {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
			if hostname == "business-secret.lan" {
				if err == nil && response.StatusCode < http.StatusBadRequest || targetRequests.Load() != beforeHTTP {
					t.Errorf("unknown private business destination escaped hosts-only resolution: err=%v", err)
				}
			} else if err != nil || response.StatusCode != http.StatusOK || targetRequests.Load() != beforeHTTP+1 {
				t.Errorf("local business destination did not use hosts: err=%v", err)
			}
			if publicQueries.Load() != beforeDNS {
				t.Errorf("business private name reached public resolver: queries=%d", publicQueries.Load()-beforeDNS)
			}
		})
	}
}

func queryMixedDNS(t *testing.T, address, hostname string) dnsmessage.Message {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprint(connection, "CONNECT 192.0.2.53:53 HTTP/1.1\r\nHost: 192.0.2.53:53\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("DNS CONNECT failed: response=%v error=%v", response, err)
	}
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 42, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(hostname + "."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	wire, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(wire)))
	if _, err := connection.Write(append(size[:], wire...)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(reader, size[:]); err != nil {
		t.Fatal(err)
	}
	wire = make([]byte, int(binary.BigEndian.Uint16(size[:])))
	if _, err := io.ReadFull(reader, wire); err != nil {
		t.Fatal(err)
	}
	var answer dnsmessage.Message
	if err := answer.Unpack(wire); err != nil {
		t.Fatal(err)
	}
	return answer
}
