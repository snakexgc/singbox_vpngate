package vpngate

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

const maxFeedSize = 32 * 1024 * 1024

func hashString(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// ParseCSV validates the complete document before returning any candidates.
// Unsupported per-node profiles are skipped; an empty/malformed feed is an error.
func ParseCSV(r io.Reader) ([]Node, error) {
	content, err := io.ReadAll(io.LimitReader(r, maxFeedSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxFeedSize {
		return nil, fmt.Errorf("VPNGate feed exceeds size limit")
	}
	text := strings.TrimPrefix(string(content), "\ufeff")
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#HostName,") {
			start = i
			lines[i] = strings.TrimPrefix(strings.TrimSpace(line), "#")
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("VPNGate CSV header missing")
	}
	var cleaned []string
	terminated := false
	for _, line := range lines[start:] {
		if strings.TrimSpace(line) == "*" {
			terminated = true
			break
		}
		if strings.TrimSpace(line) != "" {
			cleaned = append(cleaned, line)
		}
	}
	if !terminated {
		return nil, fmt.Errorf("VPNGate feed terminator missing; download may be truncated")
	}
	reader := csv.NewReader(strings.NewReader(strings.Join(cleaned, "\n")))
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	columns := map[string]int{}
	for i, key := range header {
		if _, ok := columns[key]; ok {
			return nil, fmt.Errorf("duplicate CSV column")
		}
		columns[key] = i
	}
	for _, key := range []string{"HostName", "IP", "Score", "Speed", "CountryLong", "CountryShort", "OpenVPN_ConfigData_Base64"} {
		if _, ok := columns[key]; !ok {
			return nil, fmt.Errorf("CSV missing %s", key)
		}
	}
	nodes := []Node{}
	seen := map[string]bool{}
	for {
		record, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("malformed CSV record: %w", e)
		}
		value := func(key string) string { return record[columns[key]] }
		ip := net.ParseIP(value("IP"))
		if ip == nil || ip.To4() == nil {
			continue
		}
		score, e1 := strconv.ParseInt(value("Score"), 10, 64)
		speed, e2 := strconv.ParseInt(value("Speed"), 10, 64)
		if e1 != nil || e2 != nil || score < 0 || speed < 0 {
			continue
		}
		b64 := value("OpenVPN_ConfigData_Base64")
		if len(b64) > 2*maxConfigSize {
			continue
		}
		data, e := base64.StdEncoding.DecodeString(b64)
		if e != nil {
			continue
		}
		config := strings.ReplaceAll(string(data), "\r\n", "\n")
		endpoint, e := ParseOpenVPN(config)
		if e != nil {
			continue
		}
		remote := endpoint["server"].(string)
		remoteIP := net.ParseIP(remote)
		// A mismatched advertised IPv4 identity must not dedupe/rank a different server.
		if remoteIP != nil && !remoteIP.Equal(ip) {
			continue
		}
		port := endpoint["server_port"].(int)
		configHash := hashString(config)
		identity := ip.String() + ":" + strconv.Itoa(port) + ":tcp"
		if seen[identity] {
			continue
		}
		seen[identity] = true
		nodes = append(nodes, Node{ID: hashString(identity)[:24], Hostname: value("HostName"), IP: ip.String(), Country: value("CountryLong"), CountryCode: strings.ToUpper(value("CountryShort")), Port: port, Protocol: "tcp", Score: score, Speed: speed, Config: config, ConfigHash: configHash, Status: "untested"})
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("feed contains no supported TCP OpenVPN nodes")
	}
	return nodes, nil
}
