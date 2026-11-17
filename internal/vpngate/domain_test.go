package vpngate

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const ovpnFixture = `client
dev tun
proto tcp
remote 203.0.113.7 443
resolv-retry infinite
nobind
persist-key
persist-tun
cipher AES-128-CBC
auth SHA1
remote-cert-tls server
verb 3
<ca>
-----BEGIN CERTIFICATE-----
fixture-ca
-----END CERTIFICATE-----
</ca>
<cert>
-----BEGIN CERTIFICATE-----
fixture-client
-----END CERTIFICATE-----
</cert>
<key>
-----BEGIN PRIVATE KEY-----
fixture-key
-----END PRIVATE KEY-----
</key>
`

func configuredSettings() Settings { s := DefaultSettings(); s.Upstream.Port = 7890; return s }

func TestSettingsRequireExplicitUpstreamAndRejectInvalidURLs(t *testing.T) {
	s := DefaultSettings()
	if s.Upstream.Host != "127.0.0.1" || s.Upstream.Port != 10808 || s.Upstream.Protocol != "http" || s.Upstream.Username != "" || s.Upstream.Password != "" {
		t.Fatalf("wrong first-run upstream: %+v", s.Upstream)
	}
	s.Upstream.Port = 0
	if ValidateSettings(s) == nil {
		t.Fatal("guessed/missing upstream port accepted")
	}
	s.Upstream.Port = 7890
	if err := ValidateSettings(s); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"file:///secret", "http://user:password@example.org/test", "https://"} {
		s.TestURL = raw
		if ValidateSettings(s) == nil {
			t.Fatalf("unsafe test URL accepted %q", raw)
		}
	}
}

func TestOpenVPNMapsNativeTLSWithoutLegacyCipher(t *testing.T) {
	endpoint, err := ParseOpenVPN(ovpnFixture)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint["network"] != "tcp" || endpoint["server"] != "203.0.113.7" || endpoint["server_port"] != 443 {
		t.Fatalf("wrong transport: %#v", endpoint)
	}
	if _, ok := endpoint["cipher"]; ok {
		t.Fatal("legacy TLS cipher emitted")
	}
	if endpoint["data_ciphers_fallback"] != "AES-128-CBC" || endpoint["auth"] != "SHA1" {
		t.Fatal(endpoint)
	}
	tls := endpoint["tls"].(map[string]any)
	if tls["remote_certificate_tls"] != "server" || !strings.Contains(tls["certificate"].(string), "fixture-ca") {
		t.Fatal(tls)
	}
	for _, directive := range []string{"script-security 2", "up malicious.exe", "auth-user-pass credentials.txt", "tls-cipher DEFAULT:@SECLEVEL=0", "unknown-critical value", "route-ipv6 2000::/3", "remote 203.0.113.8 443"} {
		if _, err := ParseOpenVPN(ovpnFixture + directive + "\n"); err == nil {
			t.Errorf("unsafe directive accepted: %s", directive)
		}
	}
	if _, err := ParseOpenVPN(strings.Replace(ovpnFixture, "proto tcp", "proto udp", 1)); err == nil {
		t.Fatal("UDP accepted")
	}
}

func TestCSVFindsHeaderAndSkipsUnsupportedNodesButRejectsMalformedFeed(t *testing.T) {
	feed := "*vpn_servers\n#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,OpenVPN_ConfigData_Base64\nnode,203.0.113.7,10,1,1000,Japan,JP," + base64.StdEncoding.EncodeToString([]byte(ovpnFixture)) + "\n*\n"
	nodes, err := ParseCSV(strings.NewReader(feed))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("%v %#v", err, nodes)
	}
	if nodes[0].Status != "untested" || nodes[0].LatencyMs != nil || nodes[0].ID == "" || nodes[0].ConfigHash == "" {
		t.Fatal(nodes[0])
	}
	if _, err := ParseCSV(strings.NewReader("<html>maintenance</html>")); err == nil {
		t.Fatal("HTML accepted")
	}
	if _, err := ParseCSV(strings.NewReader(strings.Replace(feed, "node,203.0.113.7", "node,invalid", 1))); err == nil {
		t.Fatal("invalid feed accepted without supported nodes")
	}
}

func TestPoolDedupesRanksAndProtectsActiveAgainstCapacityAndDelete(t *testing.T) {
	slow, fast := int64(200), int64(20)
	p := NewPool([]Node{{ID: "active", IP: "1.1.1.1", Port: 443, ConfigHash: "a", Status: "failed"}, {ID: "slow", IP: "2.2.2.2", Port: 443, ConfigHash: "b", Status: "available", LatencyMs: &slow}})
	p.Merge([]Node{{ID: "fast", IP: "3.3.3.3", Port: 443, ConfigHash: "c", Status: "available", LatencyMs: &fast}, {ID: "duplicate", IP: "3.3.3.3", Port: 443, ConfigHash: "c", Status: "untested"}}, 10, "active")
	p.Optimize(2, "active")
	ns := p.Snapshot()
	if len(ns) != 2 || ns[0].ID != "fast" || ns[1].ID != "active" {
		t.Fatal(ns)
	}
	if p.Delete("active", "active") == nil {
		t.Fatal("active deleted")
	}
	if p.DeleteFailed("active") != 0 {
		t.Fatal("active failed node deleted")
	}
	ns[0].ID = "mutated"
	*ns[0].LatencyMs = 999
	if p.Snapshot()[0].ID != "fast" || *p.Snapshot()[0].LatencyMs != 20 {
		t.Fatal("snapshot aliases pool")
	}
	if !p.UpdateProbe(ProbeResult{NodeID: "fast", Status: "canceled", LastTested: "2026-11-16T00:00:00Z"}) {
		t.Fatal("probe not applied")
	}
}

func TestConfigPresetsKeepSingleEndpointAndSplitDNSWithNoFallback(t *testing.T) {
	s := configuredSettings()
	node := Node{Config: ovpnFixture}
	for _, preset := range []string{"direct", "bypass-cn", "blacklist", "global"} {
		s.RoutingPreset = preset
		raw, err := BuildConfig(s, &node, ConfigOptions{Production: true, ControllerPort: 20123, CNRulesPath: "cn.srs", GFWRulesPath: "gfw.srs"})
		if err != nil {
			t.Fatal(preset, err)
		}
		var config map[string]any
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		eps, _ := config["endpoints"].([]any)
		if preset == "direct" {
			if len(eps) != 0 {
				t.Fatal("direct created VPN endpoint")
			}
		} else {
			if len(eps) != 1 {
				t.Fatal("must have exactly one endpoint", config)
			}
			ep := eps[0].(map[string]any)
			if ep["system"] != false || ep["detour"] != "upstream" {
				t.Fatal(ep)
			}
		}
		dns := config["dns"].(map[string]any)
		route := config["route"].(map[string]any)
		wantFinal := "vpngate"
		wantDNS := "vpn-dns"
		if preset == "direct" || preset == "blacklist" {
			wantFinal = "direct"
			wantDNS = "direct-dns"
		}
		if route["final"] != wantFinal || dns["final"] != wantDNS {
			t.Fatal(preset, route, dns)
		}
		if strings.Contains(string(raw), "urltest") || strings.Contains(string(raw), "selector") {
			t.Fatal("fallback emitted")
		}
		if preset == "bypass-cn" && (strings.Contains(string(raw), "geoip") || strings.Contains(string(raw), "gfw.srs")) {
			t.Fatal("CN preset must use domain cn rules only")
		}
	}
	s.RoutingPreset = "global"
	raw, err := BuildConfig(s, &node, ConfigOptions{MixedPort: 23456})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"type": "tun"`) || strings.Contains(string(raw), `"external_controller"`) {
		t.Fatal("probe mutated production surface")
	}
	s.RoutingPreset = "bypass-cn"
	if _, err := BuildConfig(s, &node, ConfigOptions{Production: true}); err == nil {
		t.Fatal("missing rules accepted")
	}
}

func TestProtectedOfflineAndProbeHaveNoDirectVPNFallback(t *testing.T) {
	s := configuredSettings()
	s.RoutingPreset = "global"
	raw, err := BuildConfig(s, nil, ConfigOptions{Production: true})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	rules := config["route"].(map[string]any)["rules"].([]any)
	if rules[len(rules)-1].(map[string]any)["action"] != "reject" {
		t.Fatal("offline VPN branch does not reject")
	}
	raw, err = BuildConfig(s, &Node{Config: ovpnFixture}, ConfigOptions{MixedPort: 20001})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	ep := config["endpoints"].([]any)[0].(map[string]any)
	if ep["tag"] != "vpngate" {
		t.Fatal("runtime controller cannot find named endpoint", ep)
	}
	rules = config["route"].(map[string]any)["rules"].([]any)
	for _, r := range rules {
		if r.(map[string]any)["ip_is_private"] == true {
			t.Fatal("probe can bypass candidate for private target")
		}
		if r.(map[string]any)["outbound"] == "direct" {
			t.Fatal("probe can bypass candidate for external upstream address")
		}
	}
}

func TestAutomaticPoolMergeNeverEvictsAndChangedConfigInvalidatesHealth(t *testing.T) {
	p := NewPool([]Node{{ID: "old", IP: "203.0.113.1", Port: 443, ConfigHash: "old", Status: "available"}})
	p.Merge([]Node{{ID: "new", IP: "203.0.113.2", Port: 443, ConfigHash: "new", Status: "available", Score: 999}}, 1, "")
	if got := p.Snapshot(); len(got) != 1 || got[0].ID != "old" {
		t.Fatal(got)
	}
	p.Merge([]Node{{ID: "changed", IP: "203.0.113.1", Port: 443, ConfigHash: "changed", Status: "untested"}}, 2, "")
	got := p.Snapshot()
	if len(got) != 1 || got[0].ConfigHash != "changed" || got[0].Status != "untested" || got[0].LatencyMs != nil {
		t.Fatal("changed config must update same remote and invalidate health", got)
	}
}

func TestAvailableLatencyTieRetainsExistingEvenIfFeedScoreIncreases(t *testing.T) {
	delay := int64(20)
	p := NewPool([]Node{{ID: "existing", IP: "203.0.113.1", Port: 443, Protocol: "tcp", Status: "available", LatencyMs: &delay, Score: 1}, {ID: "candidate", IP: "203.0.113.2", Port: 443, Protocol: "tcp", Status: "available", LatencyMs: &delay, Score: 999999}})
	p.Optimize(1, "")
	if got := p.Snapshot(); len(got) != 1 || got[0].ID != "existing" {
		t.Fatal("equal measured performance churns pool", got)
	}
}

func TestFeedIdentityStableAcrossCertificateAndFormattingChanges(t *testing.T) {
	feed := func(config string) string {
		return "#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,OpenVPN_ConfigData_Base64\nnode,203.0.113.7,10,1,1000,Japan,JP," + base64.StdEncoding.EncodeToString([]byte(config)) + "\n*\n"
	}
	old, err := ParseCSV(strings.NewReader(feed(ovpnFixture)))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ParseCSV(strings.NewReader(feed(strings.ReplaceAll(ovpnFixture, "fixture-ca", "new-ca"))))
	if err != nil {
		t.Fatal(err)
	}
	if old[0].ID != changed[0].ID || old[0].ConfigHash == changed[0].ConfigHash {
		t.Fatal("identity or config freshness conflated", old, changed)
	}
}

func TestOpenVPNExplicitDisabledTimersAndMSSRemainDisabled(t *testing.T) {
	ep, err := ParseOpenVPN(ovpnFixture + "reneg-sec 0\nping-restart 0\nmssfix 0\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"renegotiate_disabled", "ping_restart_disabled", "mss_fix_disabled"} {
		if ep[key] != true {
			t.Errorf("%s lost disable semantics: %#v", key, ep)
		}
	}
	if _, err := ParseOpenVPN(ovpnFixture + "key-direction 1\n"); err == nil {
		t.Fatal("orphan key direction silently ignored")
	}
}

func TestFeedCannotPartiallyReplacePoolAfterTruncation(t *testing.T) {
	feed := "#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,OpenVPN_ConfigData_Base64\nnode,203.0.113.7,10,1,1000,Japan,JP," + base64.StdEncoding.EncodeToString([]byte(ovpnFixture)) + "\n"
	if _, err := ParseCSV(strings.NewReader(feed)); err == nil {
		t.Fatal("truncated document accepted as complete refresh")
	}
}

func TestDNSCaptureAndDomainRemoteBootstrapCannotLeakOrLoop(t *testing.T) {
	s := configuredSettings()
	s.RoutingPreset = "global"
	raw, err := BuildConfig(s, &Node{Config: strings.Replace(ovpnFixture, "203.0.113.7", "vpn.example.org", 1)}, ConfigOptions{Production: true})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	ep := config["endpoints"].([]any)[0].(map[string]any)
	if ep["domain_resolver"] != "bootstrap-dns" {
		t.Fatal("remote hostname resolution may depend on not-yet-connected VPN")
	}
	servers := config["dns"].(map[string]any)["servers"].([]any)
	found := false
	for _, item := range servers {
		server := item.(map[string]any)
		if server["tag"] == "bootstrap-dns" {
			found = true
			if server["detour"] != "upstream" {
				t.Fatal("remote DNS escaped external upstream")
			}
		}
	}
	if !found {
		t.Fatal("missing bootstrap resolver")
	}
	rules := config["route"].(map[string]any)["rules"].([]any)
	captured := false
	for _, item := range rules {
		rule := item.(map[string]any)
		if rule["action"] == "hijack-dns" {
			if ports, ok := rule["port"].([]any); ok && len(ports) == 1 && ports[0] == float64(53) {
				captured = true
			}
		}
	}
	if !captured {
		t.Fatal("DNS hijack relies on sniffing that is not enabled")
	}
}
