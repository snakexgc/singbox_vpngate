package vpngate

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DefaultSettings uses the user's local HTTP mixed listener; saved settings win.
func DefaultSettings() Settings {
	return Settings{Upstream: Upstream{Host: "127.0.0.1", Port: 10808, Protocol: "http", ProcessPaths: []string{}}, PoolCapacity: 200, RefreshMinutes: 30, RoutingPreset: "bypass-cn", MixedPort: 20122, TUNEnabled: true, SystemProxyEnabled: true, TestURL: "https://www.gstatic.com/generate_204", TestConcurrency: 3, TestTimeoutSeconds: 45, RulesAutoUpdate: true, RulesUpdateHours: 24, CNRulesURL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs", GFWRulesURL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs"}
}

func validHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 || strings.TrimSpace(host) != host {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
func validPort(p int) bool { return p >= 1 && p <= 65535 }
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("HTTP(S) URL without credentials or fragment required")
	}
	if !validHost(u.Hostname()) {
		return fmt.Errorf("invalid URL host")
	}
	return nil
}

// ValidateSettings validates executable/network-ready settings; drafts may have port zero.
func ValidateSettings(s Settings) error {
	if !validHost(s.Upstream.Host) || !validPort(s.Upstream.Port) {
		return fmt.Errorf("upstream host and explicit port 1..65535 required")
	}
	if s.Upstream.Protocol != "socks5" && s.Upstream.Protocol != "socks4" && s.Upstream.Protocol != "socks4a" && s.Upstream.Protocol != "http" {
		return fmt.Errorf("upstream protocol must be http, socks4, socks4a or socks5")
	}
	if strings.ContainsAny(s.Upstream.Username+s.Upstream.Password, "\r\n\x00") {
		return fmt.Errorf("invalid upstream credentials")
	}
	if s.Upstream.Protocol == "socks4" || s.Upstream.Protocol == "socks4a" {
		if s.Upstream.Password != "" {
			return fmt.Errorf("SOCKS4/4a 不支持密码认证；用户名仅作为 UserID 发送")
		}
		if len(s.Upstream.Username) > 255 {
			return fmt.Errorf("SOCKS4/4a UserID 不能超过 255 字节")
		}
	}
	if s.PoolCapacity < 1 || s.PoolCapacity > 10000 {
		return fmt.Errorf("poolCapacity must be 1..10000")
	}
	if s.RefreshMinutes < 1 || s.RefreshMinutes > 10080 {
		return fmt.Errorf("refreshMinutes must be 1..10080")
	}
	switch s.RoutingPreset {
	case "direct", "bypass-cn", "blacklist", "global":
	default:
		return fmt.Errorf("invalid routingPreset")
	}
	if !validPort(s.MixedPort) {
		return fmt.Errorf("invalid mixedPort")
	}
	if s.TestConcurrency < 1 || s.TestConcurrency > 32 || s.TestTimeoutSeconds < 1 || s.TestTimeoutSeconds > 600 {
		return fmt.Errorf("invalid test limits")
	}
	if s.RulesUpdateHours < 1 || s.RulesUpdateHours > 8760 {
		return fmt.Errorf("invalid rulesUpdateHours")
	}
	for _, raw := range []string{s.TestURL, s.CNRulesURL, s.GFWRulesURL} {
		if err := validateURL(raw); err != nil {
			return err
		}
	}
	for _, p := range s.Upstream.ProcessPaths {
		if strings.TrimSpace(p) == "" || strings.ContainsAny(p, "\r\n\x00") {
			return fmt.Errorf("invalid upstream process path")
		}
	}
	return nil
}
