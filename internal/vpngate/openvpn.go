package vpngate

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const maxConfigSize = 1024 * 1024

// ParseOpenVPN accepts only the constrained, inline-material TCP VPNGate profile.
// Unknown options are rejected rather than allowing execution or weakening TLS.
func ParseOpenVPN(config string) (map[string]any, error) {
	if len(config) == 0 || len(config) > maxConfigSize || strings.ContainsRune(config, 0) {
		return nil, fmt.Errorf("invalid OpenVPN config size/content")
	}
	endpoint := map[string]any{"type": "openvpn-client", "system": false, "mode": "tls"}
	tls := map[string]any{}
	endpoint["tls"] = tls
	scanner := bufio.NewScanner(strings.NewReader(config))
	scanner.Buffer(make([]byte, 4096), maxConfigSize)
	seen := map[string]bool{}
	materials := map[string]string{}
	block := ""
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if block != "" {
			if line == "</"+block+">" {
				materials[block] = strings.Join(lines, "\n") + "\n"
				block = ""
				lines = nil
				continue
			}
			if strings.HasPrefix(line, "<") {
				return nil, fmt.Errorf("nested or mismatched inline material")
			}
			lines = append(lines, scanner.Text())
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "<") {
			key := strings.TrimSuffix(strings.TrimPrefix(line, "<"), ">")
			switch key {
			case "ca", "cert", "key", "tls-auth", "tls-crypt", "tls-crypt-v2":
			default:
				return nil, fmt.Errorf("unsupported inline block %s", key)
			}
			if materials[key] != "" {
				return nil, fmt.Errorf("duplicate inline block %s", key)
			}
			block = key
			continue
		}
		// VPNGate profiles have simple tokens; quotation/escape syntax is not guessed.
		if strings.ContainsAny(line, "\"'\\") {
			return nil, fmt.Errorf("unsupported quoting or escaping")
		}
		for i, c := range line {
			if (c == '#' || c == ';') && i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
				line = line[:i]
				break
			}
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		key := strings.ToLower(f[0])
		args := f[1:]
		if seen[key] {
			return nil, fmt.Errorf("duplicate directive %s", key)
		}
		seen[key] = true
		one := func() (string, error) {
			if len(args) != 1 {
				return "", fmt.Errorf("%s requires one argument", key)
			}
			return args[0], nil
		}
		switch key {
		case "client", "nobind", "persist-key", "persist-tun", "pull", "route-nopull":
			if len(args) != 0 {
				return nil, fmt.Errorf("unexpected %s arguments", key)
			}
			if key == "route-nopull" {
				endpoint["route_no_pull"] = true
			}
		case "dev":
			v, e := one()
			if e != nil || v != "tun" {
				return nil, fmt.Errorf("only dev tun is supported")
			}
		case "proto":
			v, e := one()
			if e != nil {
				return nil, e
			}
			switch v {
			case "tcp", "tcp-client":
				endpoint["network"] = "tcp"
			case "tcp4", "tcp4-client":
				endpoint["network"] = "tcp4"
			default:
				return nil, fmt.Errorf("only IPv4-capable TCP OpenVPN is supported")
			}
		case "remote":
			if len(args) != 2 {
				return nil, fmt.Errorf("exactly one remote host and port required")
			}
			if !validHost(args[0]) {
				return nil, fmt.Errorf("invalid remote host")
			}
			if ip := net.ParseIP(args[0]); ip != nil && ip.To4() == nil {
				return nil, fmt.Errorf("IPv6 OpenVPN remote unsupported")
			}
			port, e := strconv.Atoi(args[1])
			if e != nil || !validPort(port) {
				return nil, fmt.Errorf("invalid remote port")
			}
			endpoint["server"] = args[0]
			endpoint["server_port"] = port
		case "cipher", "data-ciphers", "data-ciphers-fallback":
			v, e := one()
			if e != nil {
				return nil, e
			}
			for _, cipher := range strings.Split(v, ":") {
				switch cipher {
				case "AES-128-CBC", "AES-256-CBC", "AES-128-GCM", "AES-256-GCM", "CHACHA20-POLY1305":
				default:
					return nil, fmt.Errorf("unsupported data cipher %s", cipher)
				}
			}
			if key == "cipher" {
				endpoint["data_ciphers_fallback"] = v
				if !seen["data-ciphers"] {
					endpoint["data_ciphers"] = []string{v}
				}
			} else if key == "data-ciphers" {
				endpoint["data_ciphers"] = strings.Split(v, ":")
			} else {
				endpoint["data_ciphers_fallback"] = v
			}
		case "auth":
			v, e := one()
			if e != nil {
				return nil, e
			}
			switch v {
			case "SHA1", "SHA256", "SHA384", "SHA512":
				endpoint["auth"] = v
			default:
				return nil, fmt.Errorf("unsupported auth digest")
			}
		case "remote-cert-tls":
			v, e := one()
			if e != nil || v != "server" {
				return nil, fmt.Errorf("server certificate role required")
			}
			tls["remote_certificate_tls"] = v
		case "ns-cert-type":
			v, e := one()
			if e != nil || v != "server" {
				return nil, fmt.Errorf("unsupported certificate type")
			}
			tls["ns_certificate_type"] = v
		case "verify-x509-name":
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("invalid certificate name constraint")
			}
			tls["server_name"] = args[0]
			kind := "subject"
			if len(args) == 2 {
				kind = args[1]
			}
			switch kind {
			case "subject", "name", "name-prefix":
			default:
				return nil, fmt.Errorf("unsupported certificate name type")
			}
			tls["server_name_type"] = kind
		case "tls-version-min", "tls-version-max":
			v, e := one()
			if e != nil {
				return nil, e
			}
			switch v {
			case "1.0", "1.1", "1.2", "1.3":
			default:
				return nil, fmt.Errorf("unsupported TLS version")
			}
			tls[strings.ReplaceAll(strings.TrimPrefix(key, "tls-"), "-", "_")] = v
		case "key-direction":
			v, e := one()
			if e != nil || (v != "0" && v != "1") {
				return nil, fmt.Errorf("invalid key direction")
			}
			direction := "server"
			if v == "1" {
				direction = "client"
			}
			endpoint["key_direction"] = direction
		case "resolv-retry":
			v, e := one()
			if e != nil || v != "infinite" {
				return nil, fmt.Errorf("unsupported resolv-retry")
			}
		case "verb":
			v, e := one()
			n, pe := strconv.Atoi(v)
			if e != nil || pe != nil || n < 0 || n > 11 {
				return nil, fmt.Errorf("invalid verb")
			}
		case "tun-mtu", "mssfix":
			v, e := one()
			n, pe := strconv.Atoi(v)
			if e != nil || pe != nil || n < 0 || n > 65535 {
				return nil, fmt.Errorf("invalid MTU/MSS")
			}
			field := "mtu"
			if key == "mssfix" {
				field = "mss_fix"
			}
			if n == 0 && key == "mssfix" {
				endpoint["mss_fix_disabled"] = true
			} else if n == 0 {
				return nil, fmt.Errorf("tun-mtu must be positive")
			} else {
				endpoint[field] = n
			}
		case "ping", "ping-restart", "reneg-sec":
			v, e := one()
			n, pe := strconv.Atoi(v)
			if e != nil || pe != nil || n < 0 || n > 86400 {
				return nil, fmt.Errorf("invalid timeout")
			}
			field := map[string]string{"ping": "ping_interval", "ping-restart": "ping_restart", "reneg-sec": "renegotiate_interval"}[key]
			if n == 0 && key != "ping" {
				disabled := map[string]string{"ping-restart": "ping_restart_disabled", "reneg-sec": "renegotiate_disabled"}[key]
				endpoint[disabled] = true
			} else {
				endpoint[field] = strconv.Itoa(n) + "s"
			}
		case "comp-lzo":
			v := "adaptive"
			if len(args) == 1 {
				v = args[0]
			} else if len(args) > 1 {
				return nil, fmt.Errorf("invalid comp-lzo")
			}
			switch v {
			case "no", "yes", "adaptive":
				endpoint["compression_lzo"] = v
			default:
				return nil, fmt.Errorf("unsupported comp-lzo")
			}
		case "compress":
			v, e := one()
			if e != nil {
				return nil, e
			}
			switch v {
			case "lz4", "lz4-v2", "stub", "stub-v2":
				endpoint["compression"] = v
			default:
				return nil, fmt.Errorf("unsupported compression")
			}
		case "auth-user-pass", "ca", "cert", "key", "tls-auth", "tls-crypt", "tls-crypt-v2":
			return nil, fmt.Errorf("external credentials/material not permitted; inline material required")
		default:
			return nil, fmt.Errorf("unsupported OpenVPN directive %s", key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if block != "" {
		return nil, fmt.Errorf("unterminated inline %s", block)
	}
	if endpoint["server"] == nil || endpoint["network"] == nil || !seen["client"] || !seen["dev"] {
		return nil, fmt.Errorf("client/dev tun/TCP remote required")
	}
	if materials["ca"] == "" {
		return nil, fmt.Errorf("inline CA certificate required")
	}
	tls["certificate"] = materials["ca"]
	if (materials["cert"] == "") != (materials["key"] == "") {
		return nil, fmt.Errorf("client certificate and key must be paired")
	}
	if materials["cert"] != "" {
		tls["client_certificate"] = materials["cert"]
		tls["client_key"] = materials["key"]
	}
	wraps := 0
	for _, key := range []string{"tls-auth", "tls-crypt", "tls-crypt-v2"} {
		if materials[key] != "" {
			wraps++
			wrap := map[string]any{"type": strings.ReplaceAll(key, "-", "_"), "key": materials[key]}
			if d, ok := endpoint["key_direction"]; ok {
				wrap["direction"] = d
			}
			tls["control_wrap"] = wrap
		}
	}
	if wraps > 1 {
		return nil, fmt.Errorf("multiple TLS control wrap materials")
	}
	if wraps == 0 && seen["key-direction"] {
		return nil, fmt.Errorf("key-direction requires TLS control wrap material")
	}
	delete(endpoint, "key_direction")
	return endpoint, nil
}
