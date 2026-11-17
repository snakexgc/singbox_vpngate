package vpngate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUpstreamProtocolsMapToNativeSOCKSVersions(t *testing.T) {
	for _, test := range []struct{ protocol, kind, version string }{
		{"http", "http", ""}, {"socks4", "socks", "4"}, {"socks4a", "socks", "4a"}, {"socks5", "socks", "5"},
	} {
		t.Run(test.protocol, func(t *testing.T) {
			s := configuredSettings()
			s.Upstream.Protocol = test.protocol
			if err := ValidateSettings(s); err != nil {
				t.Fatal(err)
			}
			raw, err := BuildConfig(s, &Node{Config: ovpnFixture}, ConfigOptions{MixedPort: 24133})
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Outbounds []map[string]any `json:"outbounds"`
			}
			if err = json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			for _, outbound := range config.Outbounds {
				if outbound["tag"] != "upstream" {
					continue
				}
				if outbound["type"] != test.kind {
					t.Fatalf("wrong native outbound type: %v", outbound)
				}
				if test.version != "" && outbound["version"] != test.version {
					t.Fatalf("wrong native SOCKS version: %v", outbound)
				}
				return
			}
			t.Fatal("upstream outbound missing")
		})
	}
}

func TestSOCKS4SettingsRejectUnsupportedPasswordAndOversizedUserID(t *testing.T) {
	for _, protocol := range []string{"socks4", "socks4a"} {
		s := configuredSettings()
		s.Upstream.Protocol, s.Upstream.Username = protocol, "user-id"
		if err := ValidateSettings(s); err != nil {
			t.Fatal(err)
		}
		s.Upstream.Password = "unsupported"
		if err := ValidateSettings(s); err == nil || !strings.Contains(err.Error(), "SOCKS4") {
			t.Fatalf("SOCKS4 password silently accepted: %v", err)
		}
		s.Upstream.Password, s.Upstream.Username = "", strings.Repeat("u", 256)
		if err := ValidateSettings(s); err == nil {
			t.Fatal("SOCKS4 userID exceeded native byte bound")
		}
	}
}
