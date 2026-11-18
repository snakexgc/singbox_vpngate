package vpngate

import (
	"encoding/json"
	"fmt"
	"net"
)

type object = map[string]any

// BuildConfig is deterministic and performs no filesystem or network mutations.
// Production with nil node is a protected offline policy: VPN branches reject.
// Probes always force the one candidate through VPN, regardless of business preset.
func BuildConfig(s Settings, node *Node, options ConfigOptions) ([]byte, error) {
	if err := ValidateSettings(s); err != nil {
		return nil, err
	}
	port := options.MixedPort
	if port == 0 {
		port = s.MixedPort
	}
	if !validPort(port) {
		return nil, fmt.Errorf("invalid listener port")
	}
	if options.ControllerPort != 0 && (!validPort(options.ControllerPort) || options.ControllerPort == port) {
		return nil, fmt.Errorf("invalid/conflicting controller port")
	}
	preset := s.RoutingPreset
	if !options.Production {
		preset = "global"
		if node == nil {
			return nil, fmt.Errorf("probe node required")
		}
	}
	var endpoint object
	if preset != "direct" && node != nil {
		var err error
		endpoint, err = ParseOpenVPN(node.Config)
		if err != nil {
			return nil, fmt.Errorf("node profile: %w", err)
		}
		endpoint["tag"] = "vpngate"
		endpoint["detour"] = "upstream"
	}
	upstream := object{"type": s.Upstream.Protocol, "tag": "upstream", "server": s.Upstream.Host, "server_port": s.Upstream.Port, "domain_resolver": "direct-dns"}
	if s.Upstream.Protocol == "socks4" || s.Upstream.Protocol == "socks4a" || s.Upstream.Protocol == "socks5" {
		upstream["type"] = "socks"
		upstream["version"] = s.Upstream.Protocol[len("socks"):]
	}
	if s.Upstream.Username != "" || s.Upstream.Password != "" {
		upstream["username"] = s.Upstream.Username
		upstream["password"] = s.Upstream.Password
	}
	level := options.LogLevel
	if level == "" {
		level = DefaultLogLevel
	}
	if !ValidLogLevel(level) {
		return nil, fmt.Errorf("invalid log level")
	}
	config := object{"log": object{"level": level, "timestamp": true}, "outbounds": []any{object{"type": "direct", "tag": "direct"}, upstream}}
	if options.LogPath != "" {
		config["log"].(object)["output"] = options.LogPath
	}
	if endpoint != nil {
		config["endpoints"] = []any{endpoint}
	}
	inbounds := []any{object{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": port}}
	if options.Production && s.TUNEnabled {
		inbounds = append(inbounds, object{"type": "tun", "tag": "tun-in", "interface_name": "SingBoxVPNGate", "address": []string{"172.28.0.1/30", "fdfe:dcba:9876::1/126"}, "auto_route": true, "strict_route": true, "dns_mode": "hijack"})
	}
	config["inbounds"] = inbounds
	if options.Production && options.ControllerPort != 0 {
		config["experimental"] = object{"clash_api": object{"external_controller": fmt.Sprintf("127.0.0.1:%d", options.ControllerPort)}}
	}
	rules := []any{}
	dnsRules := []any{}
	if options.Production {
		// Private names are hosts-only. Resolve business destinations here too:
		// a direct outbound's default resolver would otherwise bypass DNS rules.
		rules = append(rules, object{"domain": []string{"localhost"}, "domain_suffix": []string{"local", "lan"}, "action": "resolve", "server": "local-dns"})
		dnsRules = append(dnsRules, object{"domain": []string{"localhost"}, "domain_suffix": []string{"local", "lan"}, "action": "route", "server": "local-dns"})
	}
	// Exempt the external proxy process and its endpoint to prevent routing loops.
	if options.Production && len(s.Upstream.ProcessPaths) > 0 {
		rules = append(rules, object{"process_path": s.Upstream.ProcessPaths, "action": "route", "outbound": "direct"})
		dnsRules = append(dnsRules, object{"process_path": s.Upstream.ProcessPaths, "action": "route", "server": "direct-dns"})
	}
	if ip := net.ParseIP(s.Upstream.Host); options.Production && ip != nil {
		suffix := "/32"
		if ip.To4() == nil {
			suffix = "/128"
		}
		rules = append(rules, object{"ip_cidr": []string{ip.String() + suffix}, "port": []int{s.Upstream.Port}, "action": "route", "outbound": "direct"})
	} else if options.Production {
		dnsRules = append(dnsRules, object{"domain": []string{s.Upstream.Host}, "action": "route", "server": "direct-dns"})
	}
	rules = append(rules, object{"port": []int{53}, "action": "hijack-dns"})
	if options.Production && (preset == "bypass-cn" || preset == "blacklist") {
		// Recover HTTP/TLS/QUIC names when applications use cached IPs or their
		// own DNS. Sniffing supplies routing metadata; it never replaces the
		// original destination or depends solely on the DNS reverse cache.
		rules = append(rules, object{"action": "sniff", "sniffer": []string{"http", "tls", "quic"}})
	}
	if options.Production {
		rules = append(rules, object{"ip_is_private": true, "action": "route", "outbound": "direct"})
	}
	// An ordinary direct dialer is implicit. Native startup rejects an explicit
	// DNS detour to an empty direct outbound even though `check` accepts it.
	dnsServers := []any{object{"type": "tcp", "tag": "direct-dns", "server": "223.5.5.5", "server_port": 53}}
	if options.Production {
		dnsServers = append(dnsServers, object{"type": "hosts", "tag": "local-dns", "predefined": object{"localhost": []string{"127.0.0.1", "::1"}}})
	}
	if endpoint != nil {
		dnsServers = append(dnsServers, object{"type": "tcp", "tag": "vpn-dns", "server": "1.1.1.1", "server_port": 53, "detour": "vpngate"})
		if net.ParseIP(endpoint["server"].(string)) == nil {
			endpoint["domain_resolver"] = "bootstrap-dns"
			dnsServers = append(dnsServers, object{"type": "tcp", "tag": "bootstrap-dns", "server": "1.1.1.1", "server_port": 53, "detour": "upstream"})
		}
	}
	routeFinal := "direct"
	dnsFinal := "direct-dns"
	ruleSets := []any{}
	vpnRule := func(extra object) object {
		if extra == nil {
			extra = object{}
		}
		if endpoint == nil {
			extra["action"] = "reject"
		} else {
			extra["action"] = "route"
			extra["outbound"] = "vpngate"
		}
		return extra
	}
	vpnDNSRule := func(extra object) object {
		if extra == nil {
			extra = object{}
		}
		if endpoint == nil {
			extra["action"] = "reject"
		} else {
			extra["action"] = "route"
			extra["server"] = "vpn-dns"
		}
		return extra
	}
	switch preset {
	case "direct":
	case "bypass-cn":
		if options.CNRulesPath == "" {
			return nil, fmt.Errorf("CN domain rules are required")
		}
		ruleSets = append(ruleSets, object{"type": "local", "tag": "cn", "format": "binary", "path": options.CNRulesPath})
		rules = append(rules, object{"rule_set": []string{"cn"}, "action": "route", "outbound": "direct"})
		dnsRules = append(dnsRules, object{"rule_set": []string{"cn"}, "action": "route", "server": "direct-dns"})
		if endpoint != nil {
			rules = append(rules, object{"ip_version": 6, "action": "reject"})
			routeFinal = "vpngate"
			dnsFinal = "vpn-dns"
		} else {
			rules = append(rules, vpnRule(nil))
			dnsRules = append(dnsRules, vpnDNSRule(nil))
		}
	case "blacklist":
		if options.GFWRulesPath == "" {
			return nil, fmt.Errorf("GFW domain rules are required")
		}
		ruleSets = append(ruleSets, object{"type": "local", "tag": "gfw", "format": "binary", "path": options.GFWRulesPath})
		rules = append(rules, object{"rule_set": []string{"gfw"}, "ip_version": 6, "action": "reject"}, vpnRule(object{"rule_set": []string{"gfw"}}))
		dnsRules = append(dnsRules, vpnDNSRule(object{"rule_set": []string{"gfw"}}))
	case "global":
		if endpoint != nil {
			rules = append(rules, object{"ip_version": 6, "action": "reject"})
			routeFinal = "vpngate"
			dnsFinal = "vpn-dns"
		} else {
			rules = append(rules, vpnRule(nil))
			dnsRules = append(dnsRules, vpnDNSRule(nil))
		}
	}
	route := object{"rules": rules, "final": routeFinal, "auto_detect_interface": true, "default_domain_resolver": "direct-dns"}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}
	config["route"] = route
	config["dns"] = object{"servers": dnsServers, "rules": dnsRules, "final": dnsFinal, "strategy": "ipv4_only", "reverse_mapping": true}
	return json.MarshalIndent(config, "", "  ")
}
