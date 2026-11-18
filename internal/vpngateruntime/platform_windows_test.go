//go:build windows

package vpngateruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"slices"
	"testing"
	"time"
	"unsafe"
)

func TestResolveUpstreamOwnerIdentifiesLocalListener(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::"} {
		t.Run(host, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(host)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			paths, err := resolveUpstreamOwner(ctx, "127.0.0.1", listener.Addr().(*net.TCPAddr).Port)
			if err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(paths, exe) {
				t.Fatalf("auto-identified %v, expected %q", paths, exe)
			}
		})
	}
}

func TestResolveUpstreamOwnerSkipsRemoteProxyEvenWithMatchingLocalPort(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, host := range []string{"192.0.2.10", "proxy.example.org"} {
		paths, err := resolveUpstreamOwner(context.Background(), host, listener.Addr().(*net.TCPAddr).Port)
		if err != nil || len(paths) != 0 {
			t.Fatalf("remote proxy %s got local owners %v, err=%v", host, paths, err)
		}
	}
}

func TestUpstreamOwnerMatchesListenerAddressAndPort(t *testing.T) {
	for _, test := range []struct {
		name, upstream, listener string
		port                     uint32
		want                     bool
	}{
		{"exact IPv4", "127.0.0.1", "127.0.0.1", 7890, true},
		{"IPv4 wildcard", "127.0.0.1", "0.0.0.0", 7890, true},
		{"other loopback", "127.0.0.1", "127.0.0.2", 7890, false},
		{"LAN only", "127.0.0.1", "192.0.2.1", 7890, false},
		{"other port", "127.0.0.1", "127.0.0.1", 7891, false},
		{"exact IPv6", "::1", "::1", 7890, true},
		{"IPv6 wildcard", "::1", "::", 7890, true},
		{"different family", "127.0.0.1", "::1", 7890, false},
		{"ambiguous dual stack", "127.0.0.1", "::", 7890, false},
		{"mapped IPv4", "127.0.0.1", "::ffff:127.0.0.1", 7890, true},
		{"invalid address", "127.0.0.1", "invalid", 7890, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := matchesUpstreamListener(net.ParseIP(test.upstream), 7890, test.listener, test.port); got != test.want {
				t.Fatalf("listener %s:%d matches upstream %s:7890 = %v; want %v", test.listener, test.port, test.upstream, got, test.want)
			}
		})
	}
}

func TestProxyPortChangeFailureRetainsRecoveryOwnership(t *testing.T) {
	for _, failAt := range []string{"snapshot", "ProxyServer", "ProxyOverride", "AutoConfigURL", "AutoDetect", "ProxyEnable", "commit"} {
		t.Run(failAt, func(t *testing.T) {
			snapshot := &systemProxySnapshot{OwnedServer: "127.0.0.1:20122", Values: map[string]registryValue{"ProxyServer": {Exists: true, Data: []byte("original:8080")}}}
			current := "127.0.0.1:20122"
			denied := errors.New("injected write failure")
			saves := 0
			var durable []byte
			err := applyProxySettings(snapshot, current, "127.0.0.1:20124", func() error {
				saves++
				if failAt == "snapshot" || failAt == "commit" && saves == 2 {
					return denied
				}
				durable, _ = json.Marshal(snapshot)
				return nil
			}, func(name, value string) error {
				if name == failAt {
					return denied
				}
				if name == "ProxyServer" {
					current = value
				}
				return nil
			}, func(name string, value uint32) error {
				if name == failAt {
					return denied
				}
				return nil
			}, nil)
			if !errors.Is(err, denied) {
				t.Fatalf("write failure was not returned: %v", err)
			}
			if len(durable) > 0 {
				if err = json.Unmarshal(durable, &snapshot); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := restoreProxyValues(snapshot, current, 20124, func(name string, value registryValue) error {
				if name == "ProxyServer" {
					current = string(value.Data)
				}
				return nil
			}, nil)
			if err != nil || !changed || current != "original:8080" {
				t.Fatalf("port change lost the still-owned endpoint: current=%s changed=%v err=%v", current, changed, err)
			}
		})
	}
}

func TestProxyRecoveryPreservesAnotherApplicationsEndpoint(t *testing.T) {
	snapshot := &systemProxySnapshot{SID: "owned-user", OwnedServer: "127.0.0.1:20122", Values: map[string]registryValue{"ProxyEnable": {Exists: true, Type: 4, Data: []byte{0, 0, 0, 0}}}}
	writes := 0
	changed, err := restoreProxyValues(snapshot, "127.0.0.1:9999", 20122, func(string, registryValue) error { writes++; return nil }, nil)
	if err != nil || changed || writes != 0 {
		t.Fatalf("recovery overwrote another endpoint: changed=%v writes=%d err=%v", changed, writes, err)
	}
	values := map[string]registryValue{}
	changed, err = restoreProxyValues(snapshot, "127.0.0.1:20122", 20122, func(name string, value registryValue) error { values[name] = value; return nil }, nil)
	if err != nil || !changed || len(values) != 1 || values["ProxyEnable"].Data[0] != 0 {
		t.Fatalf("owned endpoint not restored: %#v %v", values, err)
	}
}

func TestProxyRecoveryKeepsWriteErrorsForRetry(t *testing.T) {
	snapshot := &systemProxySnapshot{OwnedServer: "127.0.0.1:20122", Values: map[string]registryValue{"ProxyServer": {}}}
	denied := errors.New("registry denied")
	changed, err := restoreProxyValues(snapshot, "127.0.0.1:20122", 20122, func(string, registryValue) error { return denied }, nil)
	if !errors.Is(err, denied) || changed {
		t.Fatalf("recovery reported success despite failure: %v %v", changed, err)
	}
}

func TestProxyRecoveryRestoresServerLastAndCanRetryOtherValueFailure(t *testing.T) {
	snapshot := &systemProxySnapshot{OwnedServer: "127.0.0.1:20122", Values: map[string]registryValue{
		"ProxyServer":   {Exists: true, Data: []byte("previous-proxy:8080")},
		"ProxyEnable":   {Exists: true, Data: []byte{0}},
		"ProxyOverride": {Exists: true, Data: []byte("localhost")},
		"AutoConfigURL": {},
	}}
	// Retry a failed write against the evolving current endpoint. If the server
	// is restored early, a later failure makes the retry abandon the snapshot.
	for attempt := 0; attempt < 30; attempt++ {
		current := snapshot.OwnedServer
		writes := []string{}
		denied := errors.New("temporary registry denial")
		changed, err := restoreProxyValues(snapshot, current, 20122, func(name string, value registryValue) error {
			writes = append(writes, name)
			if name == "ProxyOverride" {
				return denied
			}
			if name == "ProxyServer" {
				current = string(value.Data)
			}
			return nil
		}, nil)
		if changed || !errors.Is(err, denied) || current != snapshot.OwnedServer {
			t.Fatalf("failed restore lost endpoint ownership: writes=%v current=%q err=%v", writes, current, err)
		}
		writes = nil
		changed, err = restoreProxyValues(snapshot, current, 20122, func(name string, value registryValue) error {
			writes = append(writes, name)
			if name == "ProxyServer" {
				current = string(value.Data)
			}
			return nil
		}, nil)
		if err != nil || !changed || !slices.Equal(writes, []string{"AutoConfigURL", "ProxyEnable", "ProxyOverride", "ProxyServer"}) || current != "previous-proxy:8080" {
			t.Fatalf("retry was incomplete or nondeterministic: writes=%v current=%q err=%v", writes, current, err)
		}
	}
}

func TestProxyApplyDisablesPACAndWPADWithDurableFlags(t *testing.T) {
	originalFlags := uint32(15) // DIRECT | PROXY | AUTO_PROXY_URL | AUTO_DETECT
	flags := originalFlags
	snapshot := &systemProxySnapshot{OwnedServer: "127.0.0.1:20122", ConnectionFlags: &originalFlags}
	var durable []byte
	err := applyProxySettings(snapshot, "127.0.0.1:20122", "127.0.0.1:20124", func() error {
		durable, _ = json.Marshal(snapshot)
		return nil
	}, func(string, string) error { return nil }, func(string, uint32) error { return nil }, func(value uint32) error {
		var saved systemProxySnapshot
		if err := json.Unmarshal(durable, &saved); err != nil || saved.ConnectionFlags == nil || *saved.ConnectionFlags != 15 {
			t.Fatal("original connection flags were not durable before Windows changed")
		}
		flags = value
		return nil
	})
	if err != nil || flags != 3 {
		t.Fatalf("manual proxy left PAC or WPAD enabled: flags=%d err=%v", flags, err)
	}
}

func TestProxyFlagsFailureKeepsOwnedEndpointForRecoveryRetry(t *testing.T) {
	originalFlags := uint32(9) // DIRECT | AUTO_DETECT
	snapshot := &systemProxySnapshot{OwnedServer: "127.0.0.1:20122", ConnectionFlags: &originalFlags, Values: map[string]registryValue{"ProxyServer": {Exists: true, Data: []byte("original:8080")}}}
	denied := errors.New("WinINet options rejected")
	err := applyProxySettings(snapshot, "127.0.0.1:20122", "127.0.0.1:20124", func() error { return nil }, func(string, string) error { return nil }, func(string, uint32) error { return nil }, func(uint32) error { return denied })
	if !errors.Is(err, denied) {
		t.Fatalf("WinINet flags failure was ignored: %v", err)
	}
	current := "127.0.0.1:20124"
	write := func(name string, value registryValue) error {
		if name == "ProxyServer" {
			current = string(value.Data)
		}
		return nil
	}
	changed, err := restoreProxyValues(snapshot, current, 20124, write, func(uint32) error { return denied })
	if changed || !errors.Is(err, denied) || current != "127.0.0.1:20124" {
		t.Fatalf("failed flag restoration lost endpoint ownership: current=%s changed=%v err=%v", current, changed, err)
	}
	flags := uint32(3)
	changed, err = restoreProxyValues(snapshot, current, 20124, write, func(value uint32) error { flags = value; return nil })
	if err != nil || !changed || flags != 9 || current != "original:8080" {
		t.Fatalf("flag restoration retry failed: current=%s flags=%d changed=%v err=%v", current, flags, changed, err)
	}
}

func TestProxyFlagQueryPrefersUIAndFallsBackForOlderWinINet(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		var options []uint32
		flags, err := queryConnectionFlags(func(option uint32) (uint32, error) {
			options = append(options, option)
			if fallback && option == 10 {
				return 0, errors.New("FLAGS_UI unsupported")
			}
			return 9, nil
		})
		want := []uint32{10}
		if fallback {
			want = []uint32{10, 1}
		}
		if err != nil || flags != 9 || !slices.Equal(options, want) {
			t.Fatalf("incorrect connection flag query: options=%v flags=%d err=%v", options, flags, err)
		}
	}
}

func TestProxyFlagsRecoveryPreservesAnotherApplicationsEndpoint(t *testing.T) {
	flags := uint32(9)
	snapshot := &systemProxySnapshot{OwnedServer: "127.0.0.1:20122", ConnectionFlags: &flags}
	writes := 0
	changed, err := restoreProxyValues(snapshot, "other-proxy:8080", 20122, func(string, registryValue) error { writes++; return nil }, func(uint32) error { writes++; return nil })
	if err != nil || changed || writes != 0 {
		t.Fatalf("another application's proxy flags were overwritten: changed=%v writes=%d err=%v", changed, writes, err)
	}
}

func TestProxyFlagsRecoveryRequiresOriginalCurrentUser(t *testing.T) {
	if err := validateProxyFlagsOwner("S-1-5-21-1234", "S-1-5-21-1234"); err != nil {
		t.Fatal(err)
	}
	for _, current := range []string{"S-1-5-18", "S-1-5-21-5678", ""} {
		if err := validateProxyFlagsOwner("S-1-5-21-1234", current); err == nil {
			t.Fatalf("wrong account %q could restore current-HKCU options", current)
		}
	}
}

func TestProxyFlagCaptureRejectsForeignLegacySnapshotBeforeQuery(t *testing.T) {
	snapshot := &systemProxySnapshot{SID: "S-1-5-21-1234"}
	queries := 0
	err := captureProxyConnectionFlags(snapshot, true, "S-1-5-21-5678", func() (uint32, error) { queries++; return 9, nil })
	if err == nil || queries != 0 || snapshot.ConnectionFlags != nil {
		t.Fatalf("foreign snapshot captured current-user flags: captured=%v queries=%d err=%v", snapshot.ConnectionFlags != nil, queries, err)
	}
}

func TestProxyFlagCaptureNeverQueriesWinINetForService(t *testing.T) {
	snapshot := &systemProxySnapshot{SID: "S-1-5-21-1234"}
	queries := 0
	query := func() (uint32, error) { queries++; return 9, nil }
	if err := captureProxyConnectionFlags(snapshot, false, snapshot.SID, query); err != nil || queries != 0 {
		t.Fatalf("legacy service queried current-HKCU flags: queries=%d err=%v", queries, err)
	}
	flags := uint32(9)
	snapshot.ConnectionFlags = &flags
	if err := captureProxyConnectionFlags(snapshot, false, snapshot.SID, query); err == nil || queries != 0 {
		t.Fatalf("service accepted a desktop connection snapshot: queries=%d err=%v", queries, err)
	}
}

func TestWinINetConnectionFlagsReadOnly(t *testing.T) {
	if os.Getenv("VPNGATE_TEST_WININET_QUERY") != "1" {
		t.Skip("opt-in read-only query of the current user's LAN proxy flags")
	}
	if _, err := queryConnectionFlags(queryConnectionFlagsOption); err != nil {
		t.Fatal(err)
	}
}

func TestWinINetConnectionOptionWindows64ABI(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("Windows 64-bit SDK ABI")
	}
	var option internetPerConnOption
	var list internetPerConnOptionList
	if unsafe.Sizeof(option) != 16 || unsafe.Offsetof(option.Value) != 8 {
		t.Fatalf("INTERNET_PER_CONN_OPTIONW size=%d union offset=%d", unsafe.Sizeof(option), unsafe.Offsetof(option.Value))
	}
	if unsafe.Sizeof(list) != 32 || unsafe.Offsetof(list.Connection) != 8 || unsafe.Offsetof(list.Count) != 16 || unsafe.Offsetof(list.OptionError) != 20 || unsafe.Offsetof(list.Options) != 24 {
		t.Fatalf("INTERNET_PER_CONN_OPTION_LISTW layout=%+v size=%d", list, unsafe.Sizeof(list))
	}
}
