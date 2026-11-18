package bridge

import (
	"strings"
	"testing"
)

func TestAppLogCaptureOmitsSensitiveBridgeArguments(t *testing.T) {
	const secret = "test-bearer-not-for-view"
	writer := appLogWriter{}
	_, _ = writer.Write([]byte("2026/11/18 12:34:56 Requests: headers=Authorization " + secret))
	_, _ = writer.Write([]byte("2026/11/18 12:34:57 Failed to read local settings: file missing"))
	logs := appSessionLogs.Since(0)
	if len(logs) < 2 || logs[len(logs)-2].Message != "Requests" || strings.Contains(logs[len(logs)-1].Message, secret) {
		t.Fatalf("legacy bridge logging captured sensitive request data: %+v", logs)
	}
	if logs[len(logs)-1].Message != "Failed to read local settings: file missing" {
		t.Fatalf("diagnostic was not preserved: %+v", logs[len(logs)-1])
	}
}
