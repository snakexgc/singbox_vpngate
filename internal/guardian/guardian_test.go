package guardian

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type memoryBackend struct {
	rules                   []rule
	fail                    error
	validateErr             error
	applyCalls, removeCalls int
	luid                    uint64
}

func (b *memoryBackend) validate(Policy) error                { return b.validateErr }
func (b *memoryBackend) interfaceLUID(string) (uint64, error) { return b.luid, nil }
func (b *memoryBackend) servicePath() string {
	return `C:\Program Files\SingBoxVPNGate\vpngate-service.exe`
}
func (b *memoryBackend) apply(_ context.Context, r []rule) error {
	b.applyCalls++
	if b.fail != nil {
		return b.fail
	}
	b.rules = append([]rule(nil), r...)
	return nil
}
func (b *memoryBackend) remove(context.Context) error {
	b.removeCalls++
	if b.fail != nil {
		return b.fail
	}
	b.rules = nil
	return nil
}
func (b *memoryBackend) status() ProtectionStatus {
	return ProtectionStatus{Protected: len(b.rules) > 0, FilterCount: len(b.rules)}
}
func testPolicy() Policy {
	return Policy{CorePath: `C:\Program Files\SingBoxVPNGate\core\sing-box.exe`, UpstreamHost: "127.0.0.1", UpstreamPort: 7890, TunInterface: TunName}
}

func TestArmNeverClaimsSuccessOnFailedCommit(t *testing.T) {
	b := &memoryBackend{fail: errors.New("commit denied")}
	p := &Protection{backend: b}
	if err := p.Arm(context.Background(), testPolicy()); err == nil {
		t.Fatal("commit failure accepted")
	}
	if p.Status().Protected {
		t.Fatal("failed WFP commit was reported protected")
	}
	if b.removeCalls != 0 {
		t.Fatal("arm failure removed protection")
	}
}
func TestRearmPreservesOldPolicyOnFailureAndOnlyExplicitDisarmRemoves(t *testing.T) {
	b := &memoryBackend{}
	p := &Protection{backend: b}
	if err := p.Arm(context.Background(), testPolicy()); err != nil {
		t.Fatal(err)
	}
	old := len(b.rules)
	b.fail = errors.New("rearm failed")
	if err := p.Arm(context.Background(), testPolicy()); err == nil {
		t.Fatal("expected error")
	}
	if !p.Status().Protected || len(b.rules) != old {
		t.Fatal("rearm lost old protection")
	}
	if b.removeCalls != 0 {
		t.Fatal("rearm removed old protection")
	}
	b.fail = nil
	if err := p.Disarm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.Status().Protected || b.removeCalls != 1 {
		t.Fatal("explicit disarm did not remove protection")
	}
}
func TestArmValidatesBeforeMutation(t *testing.T) {
	for _, change := range []func(*Policy){func(p *Policy) { p.UpstreamHost = "proxy.example.org" }, func(p *Policy) { p.UpstreamPort = 0 }, func(p *Policy) { p.UpstreamPort = 65536 }, func(p *Policy) { p.TunInterface = "any-tun" }} {
		b := &memoryBackend{}
		p := &Protection{backend: b}
		policy := testPolicy()
		change(&policy)
		if err := p.Arm(context.Background(), policy); err == nil {
			t.Fatal("invalid policy accepted")
		}
		if b.applyCalls != 0 || b.removeCalls != 0 {
			t.Fatal("invalid policy mutated WFP")
		}
	}
	b := &memoryBackend{validateErr: errors.New("user-writable core")}
	p := &Protection{backend: b}
	if err := p.Arm(context.Background(), testPolicy()); err == nil || b.applyCalls != 0 {
		t.Fatal("untrusted path was applied")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b = &memoryBackend{}
	p = &Protection{backend: b}
	if err := p.Arm(ctx, testPolicy()); !errors.Is(err, context.Canceled) || b.applyCalls != 0 {
		t.Fatal("canceled activation mutated protection")
	}
}
func TestRulesDenyBothFamiliesAndOnlyExactManagedInterface(t *testing.T) {
	p := testPolicy()
	p.UpstreamHost = "198.51.100.10"
	p.UpstreamProcessPaths = []string{`C:\Program Files\Proxy\proxy.exe`}
	for _, luid := range []uint64{0, 987654321} {
		rules := compileRules(p, netip.MustParseAddr(p.UpstreamHost), luid, "service.exe")
		blocks, tuns := 0, 0
		names := map[string]bool{}
		for _, r := range rules {
			if names[r.Name] {
				t.Fatalf("duplicate rule %s", r.Name)
			}
			names[r.Name] = true
			if r.Block {
				blocks++
				if r.App != "" || r.Interface != 0 || r.Protocol != 0 || r.Remote.IsValid() {
					t.Fatal("deny is conditional")
				}
				continue
			}
			if r.Interface != 0 {
				tuns++
				if r.Interface != luid {
					t.Fatal("generic or wrong interface exemption")
				}
			}
			if !r.Loopback && r.Interface == 0 && r.App == "" && r.Protocol == 0 {
				t.Fatal("unscoped permit")
			}
			if r.RemotePort == 53 || r.LocalPort == 53 {
				t.Fatal("physical DNS exemption")
			}
			if r.App == "service.exe" && (r.Protocol != 6 || r.RemotePort != 7890 || r.Remote.String() != "198.51.100.10/32" || r.Inbound) {
				t.Fatal("service physical exception is broader than upstream")
			}
		}
		if blocks != 4 {
			t.Fatalf("want four v4/v6 in/out denies, got %d", blocks)
		}
		if luid == 0 && tuns != 0 || luid != 0 && tuns != 4 {
			t.Fatalf("unexpected TUN exemptions: %d", tuns)
		}
	}
}
func TestLocalhostDoesNotResolveSystemDNS(t *testing.T) {
	b := &memoryBackend{}
	p := &Protection{backend: b}
	policy := testPolicy()
	policy.UpstreamHost = "localhost"
	if e := p.Arm(context.Background(), policy); e != nil {
		t.Fatal(e)
	}
}
func TestIPCFramesBoundedAndVersioned(t *testing.T) {
	var buf bytes.Buffer
	want := request{Version: 1, Method: "state", Payload: json.RawMessage(`{}`)}
	if e := writeFrame(&buf, want); e != nil {
		t.Fatal(e)
	}
	var got request
	if e := readFrame(&buf, &got); e != nil || got.Method != want.Method {
		t.Fatalf("roundtrip: %v", e)
	}
	for _, size := range []uint32{0, maxFrame + 1, ^uint32(0)} {
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], size)
		if e := readFrame(bytes.NewReader(n[:]), &got); e == nil {
			t.Fatal("oversized/empty frame accepted")
		}
	}
	if e := writeFrame(&buf, strings.Repeat("x", maxFrame)); e == nil {
		t.Fatal("oversized response accepted")
	}
	if e := readFrame(strings.NewReader("\x05\x00\x00\x00{}"), &got); e == nil {
		t.Fatal("truncated frame accepted")
	}
}
func TestIPCRejectsInstallerAndArbitraryExecutionMethods(t *testing.T) {
	called := 0
	handler := func(context.Context, string, json.RawMessage) (any, error) {
		called++
		return map[string]bool{"ok": true}, nil
	}
	for _, method := range []string{"service.install", "service.recover", "service.uninstall", "exec", "file.write", "connect;cmd.exe"} {
		res := dispatch(context.Background(), handler, request{Version: 1, Method: method})
		if res.Error == "" {
			t.Fatalf("privileged method %s accepted", method)
		}
	}
	if res := dispatch(context.Background(), handler, request{Version: 2, Method: "state"}); res.Error == "" {
		t.Fatal("unknown protocol version accepted")
	}
	if called != 0 {
		t.Fatal("rejected IPC invoked handler")
	}
	if res := dispatch(context.Background(), handler, request{Version: 1, Method: "state"}); res.Error != "" || called != 1 {
		t.Fatal("allowed method rejected")
	}
}

type responseBuffer struct {
	bytes.Buffer
	ack io.Reader
}

func (b *responseBuffer) Read(p []byte) (int, error) { return b.ack.Read(p) }

func TestResponseWaitsForClientConsumptionAcknowledgement(t *testing.T) {
	ackReader, ackWriter := io.Pipe()
	defer ackReader.Close()
	defer ackWriter.Close()
	buffer := &responseBuffer{ack: ackReader}
	done := make(chan error, 1)
	go func() { done <- sendResponse(buffer, response{Data: json.RawMessage(`{"ok":true}`)}) }()
	// A buffered Write succeeds before a real pipe client consumes its payload.
	// The server must retain its pipe until the client acknowledges that read.
	select {
	case e := <-done:
		t.Fatalf("server disconnected before ACK: %v", e)
	case <-time.After(25 * time.Millisecond):
	}
	if _, e := ackWriter.Write([]byte{responseACK}); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("ACK did not release server")
	}
}
func TestResponseRejectsMissingOrInvalidAcknowledgement(t *testing.T) {
	for _, ack := range [][]byte{nil, {0}} {
		stream := &responseBuffer{ack: bytes.NewReader(ack)}
		if e := sendResponse(stream, response{}); e == nil {
			t.Fatal("missing/invalid ACK accepted")
		}
	}
}
func TestClientAcknowledgesOnlyCompleteResponse(t *testing.T) {
	var complete bytes.Buffer
	if e := writeFrame(&complete, response{Data: json.RawMessage(`{"ok":true}`)}); e != nil {
		t.Fatal(e)
	}
	for _, truncated := range []bool{false, true} {
		data := complete.Bytes()
		if truncated {
			data = data[:len(data)-1]
		}
		stream := &clientResponseStream{reader: bytes.NewReader(data)}
		_, e := receiveResponse(stream)
		if truncated {
			if e == nil || stream.written.Len() != 0 {
				t.Fatal("client acknowledged a truncated response")
			}
		} else {
			if e != nil || !bytes.Equal(stream.written.Bytes(), []byte{responseACK}) {
				t.Fatalf("complete response ACK failed: %v", e)
			}
		}
	}
}

type clientResponseStream struct {
	reader  io.Reader
	written bytes.Buffer
}

func (s *clientResponseStream) Read(p []byte) (int, error)  { return s.reader.Read(p) }
func (s *clientResponseStream) Write(p []byte) (int, error) { return s.written.Write(p) }

func TestConnectionSlotsApplyBackpressureAndObserveCancellation(t *testing.T) {
	slots := newConnectionSlots(2)
	ctx := context.Background()
	if e := slots.acquire(ctx); e != nil {
		t.Fatal(e)
	}
	if e := slots.acquire(ctx); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- slots.acquire(ctx) }()
	select {
	case e := <-done:
		t.Fatalf("saturated listener was not blocked: %v", e)
	case <-time.After(25 * time.Millisecond):
	}
	slots.release()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("client slot was not reused")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if e := slots.acquire(canceled); !errors.Is(e, context.Canceled) {
		t.Fatalf("saturated shutdown not canceled: %v", e)
	}
	slots.release()
	slots.release()
}
func TestIPCResponseLargerThanOneMiB(t *testing.T) {
	want := response{Data: json.RawMessage(`"` + strings.Repeat("x", 2<<20) + `"`)}
	var frame bytes.Buffer
	if e := writeFrame(&frame, want); e != nil {
		t.Fatalf("supported large pool response rejected: %v", e)
	}
	var got response
	if e := readFrame(&frame, &got); e != nil || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("large response truncated: %v", e)
	}
}
