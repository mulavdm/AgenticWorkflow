package logger

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogLevelFiltering(t *testing.T) {
	SetLogLevel(INFO)
	if !shouldLog(INFO) {
		t.Error("expected INFO to be logged when level is INFO")
	}
	if !shouldLog(WARN) {
		t.Error("expected WARN to be logged when level is INFO")
	}
	if shouldLog(DEBUG) {
		t.Error("expected DEBUG NOT to be logged when level is INFO")
	}

	SetLogLevel(DEBUG)
	if !shouldLog(DEBUG) {
		t.Error("expected DEBUG to be logged when level is DEBUG")
	}

	SetLogLevel(ERROR)
	if shouldLog(WARN) {
		t.Error("expected WARN NOT to be logged when level is ERROR")
	}
}

func TestStdLogWriterParsing(t *testing.T) {
	// Redirect console stderr output temporarily to capture direct prints
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
	}()

	// Setup a buffer for JSONL output
	var buf bytes.Buffer
	jsonEnc = json.NewEncoder(&buf)

	// Enable logging everything
	SetLogLevel(DEBUG)

	writer := NewStdLogWriter("TestDefault")

	tests := []struct {
		input             string
		expectedLevel     LogLevel
		expectedComponent string
		expectedMessage   string
	}{
		{
			input:             "[DEBUG] [Main] Hello World",
			expectedLevel:     DEBUG,
			expectedComponent: "Main",
			expectedMessage:   "Hello World",
		},
		{
			input:             "[ERROR] [Coordinator] Failed to connect",
			expectedLevel:     ERROR,
			expectedComponent: "Coordinator",
			expectedMessage:   "Failed to connect",
		},
		{
			input:             "Unstructured log message",
			expectedLevel:     INFO,
			expectedComponent: "TestDefault",
			expectedMessage:   "Unstructured log message",
		},
		{
			input:             "[WARN] Warning message with no component",
			expectedLevel:     WARN,
			expectedComponent: "TestDefault",
			expectedMessage:   "Warning message with no component",
		},
		{
			input:             "[Main] Component prefix but no level",
			expectedLevel:     INFO,
			expectedComponent: "Main",
			expectedMessage:   "Component prefix but no level",
		},
	}

	for _, tc := range tests {
		buf.Reset()
		_, err := writer.Write([]byte(tc.input + "\n"))
		if err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}

		var entry LogEntry
		if err := json.NewDecoder(&buf).Decode(&entry); err != nil {
			t.Fatalf("failed to decode log entry for input %q: %v", tc.input, err)
		}

		if entry.Level != tc.expectedLevel {
			t.Errorf("for input %q: expected level %q, got %q", tc.input, tc.expectedLevel, entry.Level)
		}
		if entry.Component != tc.expectedComponent {
			t.Errorf("for input %q: expected component %q, got %q", tc.input, tc.expectedComponent, entry.Component)
		}
		if entry.Message != tc.expectedMessage {
			t.Errorf("for input %q: expected message %q, got %q", tc.input, tc.expectedMessage, entry.Message)
		}
	}

	// Close pipe
	w.Close()
}

func TestPruneLogs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "artifactflow_log_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	SetLogDir(tempDir)

	// Create some dummy sidecar and session log files with different modification times
	filesToCreate := []struct {
		name    string
		content string
		age     time.Duration // age relative to now (negative is past)
	}{
		{"sidecar_llama.log", "some llama logs", -10 * time.Minute},
		{"sidecar_rust.log", "some rust logs which are quite long", -5 * time.Minute},
		{"session_123.json", "{}", -1 * time.Minute},
		{"session_456.json", "{}", 0},
		{"unrelated.log", "unrelated data that should not be pruned", -20 * time.Minute},
	}

	for _, ft := range filesToCreate {
		path := filepath.Join(tempDir, ft.name)
		if err := os.WriteFile(path, []byte(ft.content), 0644); err != nil {
			t.Fatalf("failed to write dummy log file %s: %v", ft.name, err)
		}
		// Set modification time
		mtime := time.Now().Add(ft.age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("failed to set mtime for %s: %v", ft.name, err)
		}
	}

	// We want to keep total size of pruneable files small.
	// Total size of pruneable files:
	// - sidecar_llama.log: 15 bytes
	// - sidecar_rust.log: 33 bytes
	// - session_123.json: 2 bytes
	// - session_456.json: 2 bytes
	// Total pruneable: 52 bytes.
	// Let's prune with a maxSize of 30 bytes.
	// This should delete the oldest pruneable files (sidecar_llama.log - 15 bytes)
	// and potentially sidecar_rust.log (33 bytes) until the size <= 30.
	// The unrelated.log (41 bytes) should NOT be deleted regardless of age.

	PruneLogs(30)

	// Check which files remain
	checkFileExists := func(name string) bool {
		_, err := os.Stat(filepath.Join(tempDir, name))
		return err == nil
	}

	if checkFileExists("sidecar_llama.log") {
		t.Error("expected sidecar_llama.log (oldest) to be pruned")
	}

	if !checkFileExists("unrelated.log") {
		t.Error("expected unrelated.log to be preserved (not starting with sidecar_ or session_)")
	}

	if !checkFileExists("session_456.json") {
		t.Error("expected newest session_456.json to be preserved")
	}
}

func TestInitLoggerReinitializesAndDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	first := filepath.Join(tempDir, "central_first.jsonl")
	second := filepath.Join(tempDir, "central_second.jsonl")

	if err := InitLogger(first); err != nil {
		t.Fatalf("InitLogger first failed: %v", err)
	}
	InfoFields("Test", "first", Fields{
		RunID:    "run-1",
		NodeID:   "node-1",
		Provider: "local",
		Model:    "model-a",
		Category: CategoryCore,
		Fields:   map[string]string{"k": "v"},
	})
	if err := Flush(); err != nil {
		t.Fatalf("Flush first failed: %v", err)
	}

	if err := InitLogger(second); err != nil {
		t.Fatalf("InitLogger second failed: %v", err)
	}
	defer CloseLogger()
	WarnFields("Test", "second", Fields{Category: CategoryDiagnostic, ErrorKind: "test"})
	if err := Flush(); err != nil {
		t.Fatalf("Flush second failed: %v", err)
	}

	diag := GetDiagnostics()
	if !diag.Initialized {
		t.Fatal("expected logger to be initialized")
	}
	if diag.ActiveLogPath != second {
		t.Fatalf("expected active log path %q, got %q", second, diag.ActiveLogPath)
	}
	if diag.LogDir != tempDir {
		t.Fatalf("expected log dir %q, got %q", tempDir, diag.LogDir)
	}
	for _, category := range []LogCategory{CategorySecurity, CategoryAudit, CategoryRealtime, CategoryStorage} {
		if !containsCategory(diag.Categories, category) {
			t.Fatalf("expected diagnostics to include category %q", category)
		}
	}

	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first log: %v", err)
	}
	if !strings.Contains(string(firstBytes), `"run_id":"run-1"`) || !strings.Contains(string(firstBytes), `"k":"v"`) {
		t.Fatalf("first log missing structured fields: %s", string(firstBytes))
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read second log: %v", err)
	}
	if !strings.Contains(string(secondBytes), `"error_kind":"test"`) {
		t.Fatalf("second log missing error kind: %s", string(secondBytes))
	}
}

func TestStructuredFieldsRedactSensitiveValues(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "security_test.jsonl")
	if err := InitLogger(logPath); err != nil {
		t.Fatalf("InitLogger failed: %v", err)
	}
	defer CloseLogger()

	secretToken := "session-token-super-secret"
	publicKey := `{"kty":"RSA","n":"public-key-material"}`
	fingerprint := "AA:BB:CC:DD:EE:FF"
	payload := "encrypted-payload-body"
	ciphertext := "ciphertext-body"
	email := "Person@Example.COM"
	cookie := "session=very-secret-cookie"
	csrf := "csrf-secret"
	invite := "invite-secret"
	jwt := "jwt-secret"
	authorization := "Bearer secret"
	InfoFields("SecurityTest", "redaction", Fields{
		Category: CategorySecurity,
		Fields: map[string]string{
			"session_token":        secretToken,
			"public_key_jwk":       publicKey,
			"fingerprint":          fingerprint,
			"payload_preview":      payload,
			"ciphertext":           ciphertext,
			"email":                email,
			"cookie":               cookie,
			"csrf_token":           csrf,
			"invite_token":         invite,
			"jwt":                  jwt,
			"authorization_header": authorization,
			"safe_field":           "kept",
		},
	})
	if err := Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := string(data)
	for _, leaked := range []string{secretToken, publicKey, fingerprint, payload, ciphertext, strings.ToLower(email), email, cookie, csrf, invite, jwt, authorization} {
		if strings.Contains(line, leaked) {
			t.Fatalf("log leaked sensitive value %q: %s", leaked, line)
		}
	}
	for _, expected := range []string{"redacted_token", "redacted_public_key", "redacted_fingerprint", "redacted_payload", "ciphertext bytes=", "redacted_email", "redacted_cookie", "redacted_authorization", `"safe_field":"kept"`} {
		if !strings.Contains(line, expected) {
			t.Fatalf("log missing expected safe marker %q: %s", expected, line)
		}
	}
}

func TestConcurrentStructuredJSONLWrites(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "central_concurrent.jsonl")
	if err := InitLogger(logPath); err != nil {
		t.Fatalf("InitLogger failed: %v", err)
	}
	defer CloseLogger()

	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				InfoFields("Concurrent", "entry", Fields{
					RunID:     "run-concurrent",
					RequestID: "req",
					Category:  CategorySession,
					Fields: map[string]string{
						"worker": string(rune('A' + worker)),
					},
				})
			}
		}()
	}
	wg.Wait()
	if err := Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != workers*perWorker {
		t.Fatalf("expected %d log lines, got %d", workers*perWorker, len(lines))
	}
	for i, line := range lines {
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\n%s", i, err, line)
		}
		if entry.RunID != "run-concurrent" || entry.Category != CategorySession {
			t.Fatalf("line %d missing structured metadata: %#v", i, entry)
		}
	}
}

func TestPruneLogsByCategory(t *testing.T) {
	tempDir := t.TempDir()
	SetLogDir(tempDir)

	create := func(name string, size int, age time.Duration) {
		path := filepath.Join(tempDir, name)
		if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		mtime := time.Now().Add(age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	create("api_old.log", 40, -10*time.Minute)
	create("api_new.log", 10, -1*time.Minute)
	create("sidecar_old.log", 40, -10*time.Minute)
	create("sidecar_new.log", 10, -1*time.Minute)
	create("security_old.log", 40, -10*time.Minute)
	create("security_new.log", 10, -1*time.Minute)

	PruneLogsByCategory(Retention{MaxBytesByCategory: map[LogCategory]int64{
		CategoryAPI:      20,
		CategorySidecar:  100,
		CategorySecurity: 20,
	}})

	if _, err := os.Stat(filepath.Join(tempDir, "api_old.log")); !os.IsNotExist(err) {
		t.Fatalf("expected old api log to be pruned, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "api_new.log")); err != nil {
		t.Fatalf("expected new api log to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "sidecar_old.log")); err != nil {
		t.Fatalf("expected sidecar log to remain under sidecar budget: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "security_old.log")); !os.IsNotExist(err) {
		t.Fatalf("expected old security log to be pruned, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "security_new.log")); err != nil {
		t.Fatalf("expected new security log to remain: %v", err)
	}
}

func containsCategory(categories []LogCategory, target LogCategory) bool {
	for _, category := range categories {
		if category == target {
			return true
		}
	}
	return false
}
