// Package guardian owns the privileged VPN protection boundary. Closing the GUI,
// service, or WFP engine handle never removes protection; only Disarm does.
package guardian

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"sync"
)

const ServiceName = "SingBoxVPNGate"
const TunName = "SingBoxVPNGate"
const PipeName = `\\.\pipe\SingBoxVPNGate.v1`

// State can contain thousands of public node records. Frame allocation remains
// bounded independently of the much smaller kernel pipe buffers.
const maxFrame = 16 << 20
const responseACK byte = 0xa5

type connectionSlots chan struct{}

func newConnectionSlots(limit int) connectionSlots { return make(connectionSlots, limit) }
func (s connectionSlots) acquire(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	select {
	case s <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s connectionSlots) release() { <-s }

var ErrUnavailable = errors.New("Windows protection service unavailable")

type Policy struct {
	CorePath             string
	UpstreamHost         string
	UpstreamPort         int
	UpstreamProcessPaths []string
	TunInterface         string
}

type ProtectionStatus struct {
	Protected   bool   `json:"protected"`
	FilterCount int    `json:"filterCount"`
	TunLUID     uint64 `json:"tunLuid"`
	Message     string `json:"message"`
}

type ServiceInfo struct {
	Installed     bool   `json:"installed"`
	Running       bool   `json:"running"`
	Protected     bool   `json:"protected"`
	RequiresAdmin bool   `json:"requiresAdmin"`
	Message       string `json:"message"`
}

type Handler func(context.Context, string, json.RawMessage) (any, error)

type rule struct {
	Name                  string
	V6, Inbound, Block    bool
	App                   string
	Interface             uint64
	Remote                netip.Prefix
	Protocol              uint8
	LocalPort, RemotePort uint16
	Loopback              bool
}

type backend interface {
	validate(Policy) error
	interfaceLUID(string) (uint64, error)
	servicePath() string
	apply(context.Context, []rule) error
	remove(context.Context) error
	status() ProtectionStatus
}

type Protection struct {
	mu      sync.Mutex
	backend backend
}

func NewProtection() *Protection { return &Protection{backend: newBackend()} }

func (p *Protection) Arm(ctx context.Context, policy Policy) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if policy.TunInterface != TunName {
		return fmt.Errorf("managed TUN interface must be %q", TunName)
	}
	if policy.UpstreamPort < 1 || policy.UpstreamPort > 65535 {
		return errors.New("explicit upstream port required")
	}
	// Resolving a host here could leak DNS before protection is armed. Require an
	// explicit literal endpoint; management DNS belongs inside the upstream proxy.
	upstreamHost := strings.TrimSpace(policy.UpstreamHost)
	if strings.EqualFold(upstreamHost, "localhost") {
		upstreamHost = "127.0.0.1"
	}
	ip, err := netip.ParseAddr(upstreamHost)
	if err != nil {
		return errors.New("upstream host must be an explicit IP address (no system DNS lookup)")
	}
	if err = p.backend.validate(policy); err != nil {
		return err
	}
	luid, err := p.backend.interfaceLUID(policy.TunInterface)
	if err != nil {
		return err
	}
	rules := compileRules(policy, ip.Unmap(), luid, p.backend.servicePath())
	return p.backend.apply(ctx, rules)
}

func (p *Protection) Disarm(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backend.remove(ctx)
}
func (p *Protection) Status() ProtectionStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backend.status()
}

func compileRules(p Policy, upstream netip.Addr, luid uint64, service string) []rule {
	var rules []rule
	for _, v6 := range []bool{false, true} {
		for _, in := range []bool{false, true} {
			key := fmt.Sprintf("v6=%t/in=%t/", v6, in)
			rules = append(rules, rule{Name: key + "deny", V6: v6, Inbound: in, Block: true}, rule{Name: key + "loopback", V6: v6, Inbound: in, Loopback: true})
			// The canonical core also owns explicitly configured direct/bypass
			// routes. Its protected executable and service-owned generated config
			// form the trust boundary for those intentional physical connections.
			rules = append(rules, rule{Name: key + "managed-core", V6: v6, Inbound: in, App: p.CorePath})
			if luid != 0 {
				rules = append(rules, rule{Name: key + "managed-tun", V6: v6, Inbound: in, Interface: luid})
			}
			for i, path := range p.UpstreamProcessPaths {
				rules = append(rules, rule{Name: fmt.Sprintf("%supstream-process-%d", key, i), V6: v6, Inbound: in, App: path})
			}
			// Guardian management traffic may only reach the configured proxy
			// endpoint. A local upstream is covered by the loopback exception.
			if !in && upstream.Is6() == v6 {
				for i, path := range []string{service} {
					if path != "" {
						rules = append(rules, rule{Name: fmt.Sprintf("%sproxy-%d", key, i), V6: v6, App: path, Remote: netip.PrefixFrom(upstream, upstream.BitLen()), Protocol: 6, RemotePort: uint16(p.UpstreamPort)})
					}
				}
			}
		}
	}
	// Deliberately no blanket LAN or DNS exception. Address acquisition is the
	// only physical LAN traffic other than the explicitly trusted upstream app.
	for _, in := range []bool{false, true} {
		local, remote := uint16(68), uint16(67)
		rules = append(rules, rule{Name: fmt.Sprintf("dhcp4/%t", in), Inbound: in, Protocol: 17, LocalPort: local, RemotePort: remote})
		rules = append(rules, rule{Name: fmt.Sprintf("dhcp6/%t", in), V6: true, Inbound: in, Protocol: 17, LocalPort: 546, RemotePort: 547, Remote: netip.MustParsePrefix("fe80::/10")})
		if !in {
			rules = append(rules, rule{Name: "dhcp6/multicast", V6: true, Protocol: 17, LocalPort: 546, RemotePort: 547, Remote: netip.MustParsePrefix("ff02::1:2/128")})
		}
		for _, prefix := range []string{"fe80::/10", "ff02::/16"} {
			for _, typ := range []uint16{133, 134, 135, 136} {
				rules = append(rules, rule{Name: fmt.Sprintf("ndp/%t/%s/%d", in, prefix, typ), V6: true, Inbound: in, Protocol: 58, LocalPort: typ, Remote: netip.MustParsePrefix(prefix)})
			}
		}
	}
	return rules
}

type request struct {
	Version int             `json:"version"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}
type response struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

func readFrame(r io.Reader, v any) error {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return err
	}
	size := binary.LittleEndian.Uint32(n[:])
	if size == 0 || size > maxFrame {
		return errors.New("invalid IPC frame size")
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) == 0 || len(b) > maxFrame {
		return errors.New("IPC response exceeds limit")
	}
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(b)))
	if err = writeAll(w, n[:]); err != nil {
		return err
	}
	return writeAll(w, b)
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, e := w.Write(b)
		if e != nil {
			return e
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

// A successful pipe Write only means the kernel buffered the response. Windows
// discards unread bytes at DisconnectNamedPipe. The client acknowledges only
// after reading the entire frame; the server retains its pipe until that ACK or
// the response I/O deadline. No unbounded FlushFileBuffers call is needed.
func sendResponse(stream io.ReadWriter, res response) error {
	if e := writeFrame(stream, res); e != nil {
		return e
	}
	var ack [1]byte
	if _, e := io.ReadFull(stream, ack[:]); e != nil {
		return e
	}
	if ack[0] != responseACK {
		return errors.New("invalid IPC response acknowledgement")
	}
	return nil
}
func receiveResponse(stream io.ReadWriter) (response, error) {
	var res response
	if e := readFrame(stream, &res); e != nil {
		return res, e
	}
	if e := writeAll(stream, []byte{responseACK}); e != nil {
		return res, e
	}
	return res, nil
}

func dispatch(ctx context.Context, h Handler, req request) response {
	if req.Version != 1 {
		return response{Error: "unsupported IPC version"}
	}
	switch req.Method {
	case "state", "settings.update", "nodes.refresh", "nodes.use", "nodes.deleteFailed", "nodes.delete", "connect", "stop", "tests.start", "jobs.cancel", "rules.update", "service.status":
	default:
		return response{Error: "unsupported service method"}
	}
	v, err := h(ctx, req.Method, req.Payload)
	if err != nil {
		return response{Error: err.Error()}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return response{Error: err.Error()}
	}
	return response{Data: b}
}
