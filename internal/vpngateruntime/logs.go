package vpngateruntime

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"guiforcores/internal/vpngate"
)

func (m *Manager) setLogLevel(level string) (vpngate.State, error) {
	if !vpngate.ValidLogLevel(level) {
		return vpngate.State{}, fmt.Errorf("无效的 sing-box 日志等级")
	}
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.logLevel != level {
		previous := m.logLevel
		m.logLevel = level
		if err := m.persistLocked(); err != nil {
			m.logLevel = previous
			return vpngate.State{}, err
		}
	}
	return m.stateLocked(), nil
}

func (m *Manager) ReadCoreLogs(after uint64) []vpngate.LogEntry {
	return m.coreLogs.Since(after)
}

// The core writes stdout and stderr to the same stream. Keep at most one
// bounded partial line between calls, including when a line spans pipe reads.
type coreLogWriter struct {
	buffer  *vpngate.LogBuffer
	redact  func(string) string
	partial []byte
}

func (w *coreLogWriter) Write(data []byte) (int, error) {
	total := len(data)
	for len(data) > 0 {
		line, rest, hasNewline := bytes.Cut(data, []byte{'\n'})
		remaining := vpngate.MaxLogLineBytes + 1 - len(w.partial)
		w.partial = append(w.partial, line[:min(remaining, len(line))]...)
		if hasNewline {
			w.flush()
		}
		data = rest
	}
	return total, nil
}

func (w *coreLogWriter) flush() {
	if len(w.partial) == 0 {
		return
	}
	line := strings.TrimSpace(string(w.partial))
	truncated := len(w.partial) > vpngate.MaxLogLineBytes
	w.partial = w.partial[:0]
	if truncated {
		w.buffer.Add(time.Now(), "", "[log line exceeded size limit]")
		return
	}
	at, level, message := time.Now(), "", line
	// sing-box timestamps use a numeric UTC offset followed by wall-clock time.
	const date = "-0700 2006-01-02 15:04:05"
	if len(line) > len(date) && line[len(date)] == ' ' {
		if parsed, err := time.Parse(date, line[:len(date)]); err == nil {
			at, line = parsed, line[len(date)+1:]
		}
	}
	code, payload, found := strings.Cut(line, " ")
	if name := strings.ToLower(code); found && vpngate.ValidLogLevel(name) {
		level, message = name, payload
	}
	w.buffer.Add(at, level, w.redact(message))
}
