package vpngateruntime

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"guiforcores/internal/vpngate"
)

type socks4Transport struct {
	template *http.Transport
	upstream vpngate.Upstream
}

func (t socks4Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// net/http may detach a dial from request cancellation so the connection can
	// be reused. These isolated requests disable reuse; keep cancellation attached
	// while the SOCKS handshake (including local DNS) is in progress.
	transport := t.template.Clone()
	transport.DialContext = func(transportCtx context.Context, network, address string) (net.Conn, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(transportCtx)
		stop := context.AfterFunc(req.Context(), cancel)
		defer stop()
		defer cancel()
		return dialSOCKS4(ctx, t.upstream, network, address)
	}
	defer transport.CloseIdleConnections()
	return transport.RoundTrip(req)
}

type socks4ReplyError struct{ code byte }

func (e socks4ReplyError) Error() string {
	return fmt.Sprintf("SOCKS4 代理拒绝目标连接（响应码 %d）", e.code)
}

type socks4ProtocolError struct{}

func (socks4ProtocolError) Error() string {
	return "外部 mixed 未返回 SOCKS4/4a 协议响应，请检查代理协议设置"
}

// Management downloads use the same SOCKS version/DNS mode as the native core.
// This only establishes a normal TCP proxy stream; OpenVPN stays in sing-box.
func dialSOCKS4(parent context.Context, u vpngate.Upstream, network, target string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" {
		return nil, fmt.Errorf("SOCKS4/4a 管理请求只支持 TCP")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	request, err := socks4Request(ctx, u, target)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(u.Host, strconv.Itoa(u.Port)))
	if err != nil {
		return nil, err
	}
	stopClosing := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopClosing()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err == nil {
		err = exchangeSOCKS4(conn, request)
	}
	if err != nil {
		conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if !stopClosing() {
		conn.Close()
		return nil, ctx.Err()
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func socks4Request(ctx context.Context, u vpngate.Upstream, target string) ([]byte, error) {
	if u.Password != "" || len(u.Username) > 255 || strings.ContainsAny(u.Username, "\r\n\x00") {
		return nil, fmt.Errorf("SOCKS4/4a UserID 无效或使用了不受支持的密码")
	}
	host, rawPort, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("SOCKS4 目标地址无效")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 || host == "" || len(host) > 255 || strings.ContainsRune(host, 0) {
		return nil, fmt.Errorf("SOCKS4 目标地址或端口无效")
	}
	ip := net.ParseIP(host)
	var domain string
	if ip == nil && u.Protocol == "socks4" {
		addresses, lookupErr := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		if lookupErr != nil {
			return nil, fmt.Errorf("SOCKS4 本地 IPv4 解析失败：%w", lookupErr)
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("SOCKS4 目标没有 IPv4 地址")
		}
		ip = net.IP(addresses[0].AsSlice())
	}
	var ipv4 net.IP
	if ip != nil {
		ipv4 = ip.To4()
	}
	if ipv4 == nil {
		if u.Protocol != "socks4a" {
			return nil, fmt.Errorf("SOCKS4 只支持 IPv4 目标，请使用 SOCKS4a 处理远程域名")
		}
		// Native SOCKS4a also represents non-IPv4 addresses by their string form.
		ipv4, domain = net.IPv4(0, 0, 0, 1).To4(), host
	}
	request := make([]byte, 8, 10+len(u.Username)+len(domain))
	request[0], request[1] = 4, 1
	binary.BigEndian.PutUint16(request[2:4], uint16(port))
	copy(request[4:8], ipv4)
	request = append(request, u.Username...)
	request = append(request, 0)
	if domain != "" {
		request = append(request, domain...)
		request = append(request, 0)
	}
	return request, nil
}

func exchangeSOCKS4(conn net.Conn, request []byte) error {
	count, err := conn.Write(request)
	if err != nil {
		return err
	}
	if count != len(request) {
		return io.ErrShortWrite
	}
	var reply [8]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[0] != 0 {
		return socks4ProtocolError{}
	}
	if reply[1] < 90 || reply[1] > 93 {
		return socks4ProtocolError{}
	}
	if reply[1] != 90 {
		return socks4ReplyError{reply[1]}
	}
	return nil
}

func checkSOCKS4ProxyAccess(ctx context.Context, conn net.Conn, u vpngate.Upstream, target string) error {
	request, err := socks4Request(ctx, u, target)
	if err != nil {
		return nil // Candidate DNS/address failure is not proof of a shared outage.
	}
	err = exchangeSOCKS4(conn, request)
	var badProtocol socks4ProtocolError
	if errors.As(err, &badProtocol) {
		return err
	}
	// Reply 91 conflates target and UserID rejection; 92/93 are identd failures.
	// No destination-specific response can justify marking the whole pool down.
	return nil
}
