package bridge

import (
	"encoding/json"
	"io"
	"log"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/logger"

	"guiforcores/internal/vpngate"
)

var appSessionLogs vpngate.LogBuffer

type sessionLogger struct{ logger.DefaultLogger }

var appSessionLogger = &sessionLogger{}

func AppLogger() logger.Logger { return appSessionLogger }

func CaptureAppLogs() {
	log.SetOutput(io.MultiWriter(appLogWriter{}, log.Writer()))
}

type appLogWriter struct{}

func (appLogWriter) Write(data []byte) (int, error) {
	message := strings.TrimSpace(string(data))
	const date = "2006/01/02 15:04:05"
	if len(message) > len(date) && message[len(date)] == ' ' {
		if _, err := time.Parse(date, message[:len(date)]); err == nil {
			message = message[len(date)+1:]
		}
	}
	// Legacy bridge messages can include complete request bodies, authorization
	// headers, proxy URLs or process environment. Expose their event names only.
	operation, _, _ := strings.Cut(message, ":")
	switch operation {
	case "Requests", "Requests Canceled", "Download", "Download Canceled", "Upload",
		"Upload Canceled", "Exec", "ExecBackground", "TcpPing", "TcpRequest", "UdpRequest",
		"StartServer", "OpenURI":
		message = operation
	}
	appSessionLogs.Add(time.Now(), "", message)
	return len(data), nil
}

func (l *sessionLogger) Print(message string) {
	appSessionLogs.Add(time.Now(), "", message)
	l.DefaultLogger.Print(message)
}

func (l *sessionLogger) Trace(message string) {
	appSessionLogs.Add(time.Now(), "trace", message)
	l.DefaultLogger.Trace(message)
}

func (l *sessionLogger) Debug(message string) {
	appSessionLogs.Add(time.Now(), "debug", message)
	l.DefaultLogger.Debug(message)
}

func (l *sessionLogger) Info(message string) {
	appSessionLogs.Add(time.Now(), "info", message)
	l.DefaultLogger.Info(message)
}

func (l *sessionLogger) Warning(message string) {
	appSessionLogs.Add(time.Now(), "warn", message)
	l.DefaultLogger.Warning(message)
}

func (l *sessionLogger) Error(message string) {
	appSessionLogs.Add(time.Now(), "error", message)
	l.DefaultLogger.Error(message)
}

func (l *sessionLogger) Fatal(message string) {
	appSessionLogs.Add(time.Now(), "fatal", message)
	l.DefaultLogger.Fatal(message)
}

func readVPNGateLogs(payload json.RawMessage) (any, error) {
	var cursor struct {
		App  uint64 `json:"app"`
		Core uint64 `json:"core"`
	}
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, err
	}
	vpnDesktop.Lock()
	manager := vpnDesktop.manager
	vpnDesktop.Unlock()
	core := []vpngate.LogEntry{}
	if reader, ok := manager.(interface {
		ReadCoreLogs(uint64) []vpngate.LogEntry
	}); ok {
		core = reader.ReadCoreLogs(cursor.Core)
	}
	return struct {
		App  []vpngate.LogEntry `json:"app"`
		Core []vpngate.LogEntry `json:"core"`
	}{App: appSessionLogs.Since(cursor.App), Core: core}, nil
}
