package vpngateruntime

import (
	"encoding/base64"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"guiforcores/internal/vpngate"
)

// Copy only bounded error evidence before the generated profile/log directory is
// removed. No certificate/configuration contents or unbounded log file enter State.
func coreFailureDiagnostic(err error, dir string, upstream vpngate.Upstream) string {
	redact := redactUpstream(upstream)
	message := boundedDiagnostic(redact(err.Error()), 2048)
	for _, name := range []string{"core.log", "process.log"} {
		file, openErr := os.Open(filepath.Join(dir, name))
		if openErr != nil {
			continue
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			continue
		}
		const maxLogBytes = 32 * 1024
		var data []byte
		if info.Size() > maxLogBytes {
			// Preserve the initial cause as well as the current failure. Repeated
			// "endpoint is not ready" messages must not erase the original error.
			data, _ = io.ReadAll(io.LimitReader(file, maxLogBytes/2))
			_, _ = file.Seek(-maxLogBytes/2, io.SeekEnd)
			tail, _ := io.ReadAll(io.LimitReader(file, maxLogBytes/2))
			data = append(append(data, '\n'), tail...)
		} else {
			data, _ = io.ReadAll(io.LimitReader(file, maxLogBytes))
		}
		file.Close()
		var evidence []string
		for _, line := range strings.Split(string(data), "\n") {
			failure := false
		severity:
			for _, field := range strings.Fields(strings.ToUpper(line)) {
				switch strings.SplitN(field, "[", 2)[0] {
				case "ERROR", "WARN", "FATAL":
					failure = true
					break severity
				case "INFO", "DEBUG", "TRACE":
					break severity
				}
			}
			if !failure {
				continue
			}
			entry := boundedDiagnostic(redact(line), 600)
			if len(evidence) < 2 {
				evidence = append(evidence, entry)
			} else {
				evidence[1] = entry
			}
		}
		if len(evidence) > 0 {
			message += "；内核：" + strings.Join(evidence, " | ")
		}
	}
	return boundedDiagnostic(message, 4096)
}

func redactUpstream(upstream vpngate.Upstream) func(string) string {
	var replacements []string
	if upstream.Username != "" || upstream.Password != "" {
		credentials := upstream.Username + ":" + upstream.Password
		replacements = append(replacements, base64.StdEncoding.EncodeToString([]byte(credentials)), "[redacted]")
	}
	for _, secret := range []string{upstream.Password, upstream.Username} {
		if secret == "" {
			continue
		}
		replacements = append(replacements, url.QueryEscape(secret), "[redacted]", secret, "[redacted]")
	}
	return strings.NewReplacer(replacements...).Replace
}

func boundedDiagnostic(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}
