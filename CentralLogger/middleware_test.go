package logger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestIDAndRequestLogMiddleware(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "http.jsonl")
	if err := InitLogger(logPath); err != nil {
		t.Fatalf("InitLogger failed: %v", err)
	}
	defer CloseLogger()

	handler := RequestIDMiddleware(RequestLogMiddleware("http-test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFromContext(r.Context()) != "req-123" {
			t.Fatalf("request id not attached to context")
		}
		if _, ok := w.(http.Flusher); !ok {
			t.Fatalf("request log middleware did not preserve http.Flusher")
		}
		_, _ = w.Write([]byte("ok"))
	})))
	req := httptest.NewRequest(http.MethodGet, "/path?token=secret", nil)
	req.Header.Set("X-Request-ID", "req-123")
	req.Header.Set("User-Agent", "test-agent")
	req.RemoteAddr = "192.0.2.25:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Header().Get("X-Request-ID") != "req-123" {
		t.Fatalf("missing response request id")
	}
	if err := Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var entry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("decode log: %v\n%s", err, string(data))
	}
	if entry.RequestID != "req-123" || entry.Category != CategoryAPI {
		t.Fatalf("unexpected request metadata: %#v", entry)
	}
	if entry.Fields["client_ip"] != "192.0.2.25" || entry.Fields["path"] != "/path" || entry.Fields["status"] != "200" {
		t.Fatalf("unexpected safe fields: %#v", entry.Fields)
	}
	if strings.Contains(string(data), "token=secret") {
		t.Fatalf("query secret leaked in request log: %s", string(data))
	}
}

func TestRecoveryMiddlewareLogsPanic(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "panic.jsonl")
	if err := InitLogger(logPath); err != nil {
		t.Fatalf("InitLogger failed: %v", err)
	}
	defer CloseLogger()

	handler := RequestIDMiddleware(RecoveryMiddleware("http-test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})))
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	req.Header.Set("X-Request-ID", "panic-req")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	if err := Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), `"error_kind":"panic"`) || !strings.Contains(string(data), `"request_id":"panic-req"`) {
		t.Fatalf("panic log missing metadata: %s", string(data))
	}
}
