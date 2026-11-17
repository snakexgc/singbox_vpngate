//go:build windows

package vpngateruntime

import (
	"errors"
	"slices"
	"testing"
)

func TestProxyRecoveryPreservesAnotherApplicationsEndpoint(t *testing.T) {
	snapshot := &systemProxySnapshot{SID: "owned-user", OwnedServer: "127.0.0.1:20122", Values: map[string]registryValue{"ProxyEnable": {Exists: true, Type: 4, Data: []byte{0, 0, 0, 0}}}}
	writes := 0
	changed, err := restoreProxyValues(snapshot, "127.0.0.1:9999", 20122, func(string, registryValue) error { writes++; return nil })
	if err != nil || changed || writes != 0 {
		t.Fatalf("recovery overwrote another endpoint: changed=%v writes=%d err=%v", changed, writes, err)
	}
	values := map[string]registryValue{}
	changed, err = restoreProxyValues(snapshot, "127.0.0.1:20122", 20122, func(name string, value registryValue) error { values[name] = value; return nil })
	if err != nil || !changed || len(values) != 1 || values["ProxyEnable"].Data[0] != 0 {
		t.Fatalf("owned endpoint not restored: %#v %v", values, err)
	}
}

func TestProxyRecoveryKeepsWriteErrorsForRetry(t *testing.T) {
	snapshot := &systemProxySnapshot{OwnedServer: "127.0.0.1:20122", Values: map[string]registryValue{"ProxyServer": {}}}
	denied := errors.New("registry denied")
	changed, err := restoreProxyValues(snapshot, "127.0.0.1:20122", 20122, func(string, registryValue) error { return denied })
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
		})
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
		})
		if err != nil || !changed || !slices.Equal(writes, []string{"AutoConfigURL", "ProxyEnable", "ProxyOverride", "ProxyServer"}) || current != "previous-proxy:8080" {
			t.Fatalf("retry was incomplete or nondeterministic: writes=%v current=%q err=%v", writes, current, err)
		}
	}
}
