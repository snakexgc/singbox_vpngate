package vpngateruntime

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"guiforcores/internal/vpngate"
)

func TestManagementRequestUsesExplicitProxy(t *testing.T) {
	var target string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { target = r.URL.String(); w.Write([]byte("via-upstream")) }))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	p, _ := strconv.Atoi(port)
	client, err := proxyClient(vpngate.Upstream{Host: host, Port: p, Protocol: "http"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://must-not-resolve.invalid/feed", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if target != "http://must-not-resolve.invalid/feed" || string(data) != "via-upstream" {
		t.Fatalf("request bypassed proxy: %s %s", target, data)
	}
}
func TestInvalidProxyNeverFallsBackToEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	for _, u := range []vpngate.Upstream{{}, {Host: "localhost", Port: 80, Protocol: "invalid"}, {Host: "http://localhost", Port: 80, Protocol: "http"}} {
		if _, err := proxyClient(u); err == nil {
			t.Fatalf("accepted unsafe upstream: %+v", u)
		}
	}
}
func TestDownloadRejectsHTTPErrorAndSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "bad") {
			w.WriteHeader(500)
		}
		w.Write([]byte("123456"))
	}))
	defer server.Close()
	client := server.Client()
	if _, err := download(context.Background(), client, server.URL+"/bad", 100); err == nil {
		t.Fatal("accepted HTTP500")
	}
	if _, err := download(context.Background(), client, server.URL, 5); err == nil {
		t.Fatal("accepted oversized body")
	}
}
