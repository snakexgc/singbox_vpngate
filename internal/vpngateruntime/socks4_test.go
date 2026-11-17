package vpngateruntime

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type socks4ObservedRequest struct {
	ip         net.IP
	port       uint16
	user, host string
}

func socks4Fixture(t *testing.T, status byte, serveHTTP bool) (string, <-chan socks4ObservedRequest) {
	t.Helper()
	requests := make(chan socks4ObservedRequest, 8)
	address := loopbackTCPService(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		var head [8]byte
		if _, err := io.ReadFull(reader, head[:]); err != nil {
			return
		}
		if head[0] != 4 || head[1] != 1 {
			t.Error("wrong SOCKS4 CONNECT framing")
			return
		}
		user, err := reader.ReadString(0)
		if err != nil {
			return
		}
		request := socks4ObservedRequest{ip: net.IP(append([]byte(nil), head[4:8]...)), port: binary.BigEndian.Uint16(head[2:4]), user: strings.TrimSuffix(user, "\x00")}
		if head[4] == 0 && head[5] == 0 && head[6] == 0 && head[7] != 0 {
			host, err := reader.ReadString(0)
			if err != nil {
				return
			}
			request.host = strings.TrimSuffix(host, "\x00")
		}
		requests <- request
		if _, err = conn.Write([]byte{0, status, 0, 0, 0, 0, 0, 0}); err != nil || status != 90 || !serveHTTP {
			return
		}
		requestHTTP, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		requestHTTP.Body.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\nvia-socks4!")
	})
	return address, requests
}

func TestSOCKS4ManagementTrafficUsesSelectedDNSMode(t *testing.T) {
	for _, test := range []struct{ protocol, target, wantHost string }{
		{"socks4", "http://localhost:18080/feed", ""},
		{"socks4a", "http://must-not-resolve.invalid:18080/feed", "must-not-resolve.invalid"},
	} {
		t.Run(test.protocol, func(t *testing.T) {
			address, requests := socks4Fixture(t, 90, true)
			upstream := diagnosticUpstream(t, "http://"+address)
			upstream.Protocol, upstream.Username = test.protocol, "local-user"
			client, err := proxyClient(upstream)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			response, err := client.Get(test.target)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != "via-socks4!" {
				t.Fatalf("management download failed: %q %v", body, err)
			}
			select {
			case got := <-requests:
				if got.host != test.wantHost || got.user != "local-user" || got.port != 18080 {
					t.Fatalf("wrong SOCKS4 framing or DNS mode: %+v", got)
				}
				if test.protocol == "socks4" && !got.ip.Equal(net.IPv4(127, 0, 0, 1)) {
					t.Fatalf("SOCKS4 did not locally resolve IPv4: %+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("SOCKS4 upstream was bypassed")
			}
		})
	}
}

func TestSOCKS4CandidateRejectionDoesNotBecomeSharedOutage(t *testing.T) {
	for _, protocol := range []string{"socks4", "socks4a"} {
		address, requests := socks4Fixture(t, 91, false)
		upstream := diagnosticUpstream(t, "http://"+address)
		upstream.Protocol = protocol
		if err := checkProxyAccess(context.Background(), upstream, "192.0.2.10:443"); err != nil {
			t.Fatalf("candidate rejection became shared outage: %v", err)
		}
		select {
		case got := <-requests:
			if !got.ip.Equal(net.IPv4(192, 0, 2, 10)) || got.port != 443 {
				t.Fatalf("control check used unrelated target: %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("candidate was not checked")
		}
	}
}

func TestSOCKS4ManagementHandshakeCancellationClosesSocket(t *testing.T) {
	closed := make(chan struct{}, 1)
	address := loopbackTCPService(t, func(conn net.Conn) {
		data := make([]byte, 1024)
		_, _ = conn.Read(data)
		_, _ = conn.Read(data)
		closed <- struct{}{}
	})
	upstream := diagnosticUpstream(t, "http://"+address)
	upstream.Protocol = "socks4a"
	client, err := proxyClient(upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://must-not-resolve.invalid/feed", nil)
	if _, err = client.Do(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled SOCKS4 handshake ignored cancellation: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled SOCKS4 handshake retained socket")
	}
}

func TestSOCKS4MalformedProxyResponseIsEnvironmentFailure(t *testing.T) {
	address := loopbackTCPService(t, func(conn net.Conn) {
		var request [9]byte // Header plus empty UserID terminator.
		if _, err := io.ReadFull(conn, request[:]); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 0, 0, 0, 0, 0, 0, 0})
	})
	upstream := diagnosticUpstream(t, "http://"+address)
	upstream.Protocol = "socks4"
	if err := checkProxyAccess(context.Background(), upstream, "192.0.2.10:443"); err == nil || !strings.Contains(err.Error(), "协议响应") {
		t.Fatalf("wrong protocol did not identify environment failure: %v", err)
	}
}

func TestSOCKS4ManagementRejectionNeverFallsBack(t *testing.T) {
	address, requests := socks4Fixture(t, 91, false)
	upstream := diagnosticUpstream(t, "http://"+address)
	upstream.Protocol = "socks4a"
	client, err := proxyClient(upstream)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err = client.Get("http://must-not-resolve.invalid/feed"); err == nil || !strings.Contains(err.Error(), "91") {
		t.Fatalf("SOCKS4 proxy rejection was hidden or bypassed: %v", err)
	}
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("SOCKS4 management request bypassed explicit proxy")
	}
}

func TestSOCKS4ClientRejectsUnsupportedPasswordBeforeNetwork(t *testing.T) {
	for _, protocol := range []string{"socks4", "socks4a"} {
		u := diagnosticUpstream(t, "http://127.0.0.1:1")
		u.Protocol, u.Username, u.Password = protocol, "user-id", "unsupported"
		if _, err := proxyClient(u); err == nil || !strings.Contains(err.Error(), "SOCKS4") {
			t.Fatalf("unsupported SOCKS4 password was silently dropped: %v", err)
		}
	}
}
