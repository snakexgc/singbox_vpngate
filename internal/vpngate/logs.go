package vpngate

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const DefaultLogLevel = "info"

func ValidLogLevel(level string) bool {
	switch level {
	case "trace", "debug", "info", "warn", "error", "fatal", "panic":
		return true
	default:
		return false
	}
}

type LogEntry struct {
	ID      uint64 `json:"id"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

const (
	logHistoryLimit = 500
	MaxLogLineBytes = 4096
)

// LogBuffer retains a bounded tail of one source for the current app session.
type LogBuffer struct {
	mu      sync.Mutex
	entries [logHistoryLimit]LogEntry
	next    uint64
}

func (b *LogBuffer) Add(at time.Time, level, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	if len(message) > MaxLogLineBytes {
		end := MaxLogLineBytes
		for end > 0 && !utf8.RuneStart(message[end]) {
			end--
		}
		message = strings.Clone(message[:end]) + "…"
	} else {
		message = strings.Clone(message)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[b.next%logHistoryLimit] = LogEntry{b.next + 1, at.UTC().Format(time.RFC3339Nano), level, message}
	b.next++
}

func (b *LogBuffer) Since(after uint64) []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	oldest := b.next - min(b.next, logHistoryLimit)
	if after > b.next {
		after = oldest
	}
	after = max(after, oldest)
	result := make([]LogEntry, 0, b.next-after)
	for i := after; i < b.next; i++ {
		result = append(result, b.entries[i%logHistoryLimit])
	}
	return result
}
