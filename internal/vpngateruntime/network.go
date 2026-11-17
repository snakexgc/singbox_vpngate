package vpngateruntime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
	"guiforcores/internal/vpngate"
)

// Every management request has an explicit proxy; environment and direct fallback
// are intentionally absent. Each probe gets a fresh transport and connection.
func proxyClient(u vpngate.Upstream) (*http.Client, error) {
	if u.Host == "" || strings.ContainsAny(u.Host, "/\\@ \t\r\n") || u.Port < 1 || u.Port > 65535 {
		return nil, fmt.Errorf("请配置有效的外部 mixed 地址和端口")
	}
	if u.Username == "" && u.Password != "" {
		return nil, fmt.Errorf("外部 mixed 代理密码已填写但用户名为空，请检查认证设置")
	}
	if (u.Protocol == "socks4" || u.Protocol == "socks4a") && (u.Password != "" || len(u.Username) > 255 || strings.ContainsAny(u.Username, "\r\n\x00")) {
		return nil, fmt.Errorf("SOCKS4/4a 仅支持不超过 255 字节的 UserID，不支持密码认证")
	}
	addr := net.JoinHostPort(u.Host, strconv.Itoa(u.Port))
	tr := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	var transport http.RoundTripper = tr
	switch u.Protocol {
	case "http":
		uurl := &url.URL{Scheme: "http", Host: addr}
		if u.Username != "" {
			uurl.User = url.UserPassword(u.Username, u.Password)
		}
		tr.Proxy = http.ProxyURL(uurl)
	case "socks5":
		var auth *proxy.Auth
		if u.Username != "" {
			auth = &proxy.Auth{User: u.Username, Password: u.Password}
		}
		d, err := proxy.SOCKS5("tcp", addr, auth, &net.Dialer{Timeout: 10 * time.Second})
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 context dialer unavailable")
		}
		tr.DialContext = cd.DialContext
	case "socks4", "socks4a":
		transport = socks4Transport{template: tr, upstream: u}
	default:
		return nil, fmt.Errorf("外部 mixed 协议必须是 HTTP、SOCKS4、SOCKS4a 或 SOCKS5")
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}}, nil
}

func download(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("下载失败：HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("下载超过大小限制")
	}
	return data, nil
}

func fingerprint(s vpngate.Settings) string {
	data, _ := json.Marshal(struct {
		Upstream vpngate.Upstream
		URL      string
		CorePath string
	}{s.Upstream, s.TestURL, s.CorePath})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func preflight(ctx context.Context, u vpngate.Upstream) error {
	return checkProxyAccess(ctx, u, "")
}

// This check exchanges proxy control messages only. It never forwards VPN or
// website traffic. SOCKS authentication needs no target; HTTP authentication is
// inspected only when the caller supplies the actual candidate OpenVPN remote.
func checkProxyAccess(ctx context.Context, u vpngate.Upstream, target string) error {
	// Reuse the explicit configuration validation without making a proxy request.
	client, err := proxyClient(u)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	address := net.JoinHostPort(u.Host, strconv.Itoa(u.Port))
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(checkCtx, "tcp", address)
	if err != nil {
		return fmt.Errorf("无法连接配置的外部 mixed 监听端口 %s：%w", address, err)
	}
	defer conn.Close()
	stopClosing := context.AfterFunc(checkCtx, func() { conn.Close() })
	defer stopClosing()
	deadline, _ := checkCtx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("无法设置外部 mixed 检查期限：%w", err)
	}
	if u.Protocol == "socks5" {
		return checkSOCKSAuthentication(conn, u)
	}
	if u.Protocol == "socks4" || u.Protocol == "socks4a" {
		if target != "" {
			return checkSOCKS4ProxyAccess(checkCtx, conn, u, target)
		}
		return nil // SOCKS4 has no greeting independent of a real destination.
	}
	if target != "" {
		return checkHTTPProxyAuthentication(conn, u, target)
	}
	return nil
}

func checkSOCKSAuthentication(conn net.Conn, u vpngate.Upstream) error {
	if len(u.Username) > 255 || len(u.Password) > 255 {
		return fmt.Errorf("SOCKS5 用户名和密码不能超过 255 字节")
	}
	methods := []byte{0}
	if u.Username != "" {
		// Match native sing-box: configured credentials require method 2 only.
		// Offering no-auth as well could make the check pass on a path the core
		// itself would reject.
		methods = []byte{2}
	}
	if _, err := conn.Write(append([]byte{5, byte(len(methods))}, methods...)); err != nil {
		return fmt.Errorf("外部 mixed SOCKS5 协议协商失败：%w", err)
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return fmt.Errorf("外部 mixed SOCKS5 协议协商失败：%w", err)
	}
	if reply[0] != 5 {
		return fmt.Errorf("外部 mixed 未返回 SOCKS5 协议响应，请检查代理协议设置")
	}
	switch reply[1] {
	case 0:
		return nil
	case 2:
		if u.Username == "" && u.Password == "" {
			return fmt.Errorf("外部 mixed SOCKS5 需要用户名和密码认证")
		}
		auth := append([]byte{1, byte(len(u.Username))}, []byte(u.Username)...)
		auth = append(auth, byte(len(u.Password)))
		auth = append(auth, []byte(u.Password)...)
		if _, err := conn.Write(auth); err != nil {
			return fmt.Errorf("外部 mixed SOCKS5 认证交换失败：%w", err)
		}
		if _, err := io.ReadFull(conn, reply[:]); err != nil {
			return fmt.Errorf("外部 mixed SOCKS5 认证交换失败：%w", err)
		}
		if reply[0] != 1 {
			return fmt.Errorf("外部 mixed 未返回有效的 SOCKS5 认证协议响应")
		}
		if reply[1] != 0 {
			return fmt.Errorf("外部 mixed SOCKS5 认证失败，请检查用户名和密码")
		}
		return nil
	default:
		return fmt.Errorf("外部 mixed SOCKS5 拒绝已配置的认证方式，请检查代理协议和认证设置")
	}
}

func checkHTTPProxyAuthentication(conn net.Conn, u vpngate.Upstream, target string) error {
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Host: target}, Host: target, Header: make(http.Header)}
	if u.Username != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.Username+":"+u.Password)))
	}
	if err := req.Write(conn); err != nil {
		return nil // The target may have failed; native sing-box owns that verdict.
	}
	reader := bufio.NewReader(io.LimitReader(conn, 16<<10))
	prefix, err := reader.Peek(5)
	if len(prefix) > 0 && prefix[0] != 'H' || err == nil && string(prefix) != "HTTP/" {
		return fmt.Errorf("外部 mixed 未返回 HTTP 代理协议响应，请检查代理协议设置")
	}
	if err != nil {
		return nil // Timeout/EOF is not proof of a shared proxy failure.
	}
	res, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil
	}
	// Close the control socket without reading or forwarding response/tunnel data.
	switch res.StatusCode {
	case http.StatusProxyAuthRequired:
		return fmt.Errorf("外部 mixed HTTP 代理认证失败（HTTP 407），请检查用户名和密码")
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return fmt.Errorf("外部 mixed HTTP 代理不支持 CONNECT（HTTP %d），请检查代理协议设置", res.StatusCode)
	default:
		// In particular, 403/502 and target-specific timeouts cannot justify
		// canceling every node. sing-box makes the actual VPN connection.
		return nil
	}
}
