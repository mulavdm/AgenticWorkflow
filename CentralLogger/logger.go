package logger

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LogLevel definition
type LogLevel string

const (
	INFO  LogLevel = "INFO"
	WARN  LogLevel = "WARN"
	ERROR LogLevel = "ERROR"
	DEBUG LogLevel = "DEBUG"
)

type LogEntry struct {
	Timestamp string            `json:"timestamp"`
	Level     LogLevel          `json:"level"`
	Message   string            `json:"message"`
	Component string            `json:"component,omitempty"`
	RunID     string            `json:"run_id,omitempty"`
	NodeID    string            `json:"node_id,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Model     string            `json:"model,omitempty"`
	Sidecar   string            `json:"sidecar,omitempty"`
	PID       int               `json:"pid,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Category  LogCategory       `json:"category,omitempty"`
	ErrorKind string            `json:"error_kind,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// LogCategory classifies logs for retention and diagnostics.
type LogCategory string

const (
	CategoryCore       LogCategory = "core"
	CategorySession    LogCategory = "session"
	CategorySidecar    LogCategory = "sidecar"
	CategoryAPI        LogCategory = "api"
	CategoryDiagnostic LogCategory = "diagnostic"
	CategorySecurity   LogCategory = "security"
	CategoryAudit      LogCategory = "audit"
	CategoryRealtime   LogCategory = "realtime"
	CategoryStorage    LogCategory = "storage"
)

// Fields contains optional structured metadata for a log entry.
type Fields struct {
	RunID     string
	NodeID    string
	SessionID string
	Provider  string
	Model     string
	Sidecar   string
	PID       int
	RequestID string
	Category  LogCategory
	ErrorKind string
	Fields    map[string]string
}

// Diagnostics reports logger runtime state without exposing file handles.
type Diagnostics struct {
	LogDir          string        `json:"log_dir,omitempty"`
	ActiveLogPath   string        `json:"active_log_path,omitempty"`
	CurrentLevel    LogLevel      `json:"current_level"`
	Retention       Retention     `json:"retention"`
	LastWriteError  string        `json:"last_write_error,omitempty"`
	Initialized     bool          `json:"initialized"`
	SupportedLevels []LogLevel    `json:"supported_levels"`
	Categories      []LogCategory `json:"categories"`
}

// Retention describes the size budgets used by PruneLogs.
type Retention struct {
	MaxBytesByCategory map[LogCategory]int64 `json:"max_bytes_by_category"`
}

var (
	jsonFile *os.File
	jsonEnc  *json.Encoder
)

var (
	levelMu       sync.RWMutex
	currentLevel  LogLevel = INFO
	logDir        string
	activeLogPath string
	logDirMu      sync.RWMutex
	writeMu       sync.Mutex
	lastWriteErr  error
	retention     = Retention{MaxBytesByCategory: map[LogCategory]int64{
		CategoryCore:       100 * 1024 * 1024,
		CategorySession:    100 * 1024 * 1024,
		CategorySidecar:    100 * 1024 * 1024,
		CategoryAPI:        100 * 1024 * 1024,
		CategoryDiagnostic: 25 * 1024 * 1024,
		CategorySecurity:   100 * 1024 * 1024,
		CategoryAudit:      100 * 1024 * 1024,
		CategoryRealtime:   100 * 1024 * 1024,
		CategoryStorage:    100 * 1024 * 1024,
	}}
)

var levelSeverity = map[LogLevel]int{
	DEBUG: 0,
	INFO:  1,
	WARN:  2,
	ERROR: 3,
}

// SetLogLevel sets the global logging severity threshold.
func SetLogLevel(level LogLevel) {
	levelMu.Lock()
	defer levelMu.Unlock()
	upper := LogLevel(strings.ToUpper(string(level)))
	if _, valid := levelSeverity[upper]; valid {
		currentLevel = upper
	}
}

// GetLogLevel retrieves the current global logging severity threshold.
func GetLogLevel() LogLevel {
	levelMu.RLock()
	defer levelMu.RUnlock()
	return currentLevel
}

// SetLogDir sets the directory where centralized and sidecar logs reside.
func SetLogDir(dir string) {
	logDirMu.Lock()
	defer logDirMu.Unlock()
	logDir = dir
}

// GetLogDir retrieves the current centralized log directory.
func GetLogDir() string {
	logDirMu.RLock()
	defer logDirMu.RUnlock()
	return logDir
}

func setActiveLogPath(path string) {
	logDirMu.Lock()
	defer logDirMu.Unlock()
	activeLogPath = path
}

func getActiveLogPath() string {
	logDirMu.RLock()
	defer logDirMu.RUnlock()
	return activeLogPath
}

func shouldLog(level LogLevel) bool {
	levelMu.RLock()
	defer levelMu.RUnlock()

	currentSev, existsCurrent := levelSeverity[currentLevel]
	targetSev, existsTarget := levelSeverity[level]

	if !existsCurrent || !existsTarget {
		return true // Default fallback to log if levels are unrecognized
	}
	return targetSev >= currentSev
}

// InitLogger configures the file logger, ensuring parent directories exist.
func InitLogger(logPath string) error {
	// Initialize levels from environment variables if present
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		SetLogLevel(LogLevel(envLevel))
	} else if envLevel := os.Getenv("ARTIFACTFLOW_LOG_LEVEL"); envLevel != "" {
		SetLogLevel(LogLevel(envLevel))
	}

	// Resolve absolute path and set log directory
	absPath, err := filepath.Abs(logPath)
	if err == nil {
		SetLogDir(filepath.Dir(absPath))
	} else {
		SetLogDir(filepath.Dir(logPath))
	}

	// Ensure directory exists
	if err := os.MkdirAll(GetLogDir(), 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	absLogPath := logPath
	if absPath, err := filepath.Abs(logPath); err == nil {
		absLogPath = absPath
	}

	writeMu.Lock()
	defer writeMu.Unlock()
	if jsonFile != nil {
		_ = jsonFile.Sync()
		_ = jsonFile.Close()
		jsonFile = nil
		jsonEnc = nil
	}

	f, err := os.OpenFile(absLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		lastWriteErr = err
		return err
	}
	jsonFile = f
	jsonEnc = json.NewEncoder(jsonFile)
	lastWriteErr = nil
	setActiveLogPath(absLogPath)
	return nil
}

// CloseLogger closes the active JSONL file handle.
func CloseLogger() {
	writeMu.Lock()
	defer writeMu.Unlock()
	if jsonFile != nil {
		_ = jsonFile.Sync()
		_ = jsonFile.Close()
		jsonFile = nil
		jsonEnc = nil
	}
	setActiveLogPath("")
}

// Flush syncs the active JSONL file to disk.
func Flush() error {
	writeMu.Lock()
	defer writeMu.Unlock()
	if jsonFile == nil {
		return nil
	}
	if err := jsonFile.Sync(); err != nil {
		lastWriteErr = err
		return err
	}
	lastWriteErr = nil
	return nil
}

func writeLog(level LogLevel, component, message string) {
	writeLogWithFields(level, component, message, Fields{})
}

func writeLogWithFields(level LogLevel, component, message string, fields Fields) {
	if !shouldLog(level) {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Message:   message,
		Component: component,
		RunID:     fields.RunID,
		NodeID:    fields.NodeID,
		SessionID: fields.SessionID,
		Provider:  fields.Provider,
		Model:     fields.Model,
		Sidecar:   fields.Sidecar,
		PID:       fields.PID,
		RequestID: fields.RequestID,
		Category:  fields.Category,
		ErrorKind: fields.ErrorKind,
		Fields:    sanitizeFields(fields.Fields),
	}

	writeMu.Lock()
	defer writeMu.Unlock()

	// 1. Console Output - using direct Stderr write to prevent infinite loop
	// when standard library log calls are redirected to this logger.
	prefix := fmt.Sprintf("[%s] %s:", level, component)
	fmt.Fprintf(os.Stderr, "%s %-20s %s\n", time.Now().Format("2006/01/02 15:04:05"), prefix, message)

	// 2. JSONL Output
	if jsonEnc != nil {
		if err := jsonEnc.Encode(entry); err != nil {
			lastWriteErr = err
		} else {
			lastWriteErr = nil
		}
	}
}

func Info(component, msg string)  { writeLog(INFO, component, msg) }
func Warn(component, msg string)  { writeLog(WARN, component, msg) }
func Error(component, msg string) { writeLog(ERROR, component, msg) }
func Debug(component, msg string) { writeLog(DEBUG, component, msg) }

func InfoFields(component, msg string, fields Fields) {
	writeLogWithFields(INFO, component, msg, fields)
}
func WarnFields(component, msg string, fields Fields) {
	writeLogWithFields(WARN, component, msg, fields)
}
func ErrorFields(component, msg string, fields Fields) {
	writeLogWithFields(ERROR, component, msg, fields)
}
func DebugFields(component, msg string, fields Fields) {
	writeLogWithFields(DEBUG, component, msg, fields)
}

// StdLogWriter redirects standard library log outputs to this structured logger.
type StdLogWriter struct {
	defaultComponent string
}

// NewStdLogWriter creates an io.Writer to intercept standard library logs.
func NewStdLogWriter(defaultComponent string) *StdLogWriter {
	return &StdLogWriter{defaultComponent: defaultComponent}
}

// Write intercepts, parses, and structures standard log messages.
func (w *StdLogWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	msg = strings.TrimSuffix(msg, "\n")
	msg = strings.TrimSuffix(msg, "\r")

	level := INFO
	component := w.defaultComponent

	// Smart Level Parsing
	if strings.HasPrefix(msg, "[DEBUG] ") {
		level = DEBUG
		msg = strings.TrimPrefix(msg, "[DEBUG] ")
	} else if strings.HasPrefix(msg, "[ERROR] ") {
		level = ERROR
		msg = strings.TrimPrefix(msg, "[ERROR] ")
	} else if strings.HasPrefix(msg, "[WARN] ") {
		level = WARN
		msg = strings.TrimPrefix(msg, "[WARN] ")
	} else if strings.HasPrefix(msg, "[INFO] ") {
		level = INFO
		msg = strings.TrimPrefix(msg, "[INFO] ")
	}

	// Smart Component Parsing: if message begins with "[Component]"
	if len(msg) > 0 && msg[0] == '[' {
		if idx := strings.Index(msg, "]"); idx > 0 {
			parsedComp := msg[1:idx]
			// Ensure it's a clean component name, not a timestamp or host/IP
			if !strings.Contains(parsedComp, ":") && !strings.Contains(parsedComp, "/") && len(parsedComp) < 30 {
				component = parsedComp
				msg = msg[idx+1:]
				msg = strings.TrimSpace(msg)
			}
		}
	}

	writeLog(level, component, msg)
	return len(p), nil
}

// SetRetention sets per-category log retention budgets. Missing or non-positive
// categories keep their existing defaults.
func SetRetention(next Retention) {
	writeMu.Lock()
	defer writeMu.Unlock()
	if retention.MaxBytesByCategory == nil {
		retention.MaxBytesByCategory = map[LogCategory]int64{}
	}
	for category, maxBytes := range next.MaxBytesByCategory {
		if maxBytes > 0 {
			retention.MaxBytesByCategory[category] = maxBytes
		}
	}
}

// GetDiagnostics reports current logger health and retention state.
func GetDiagnostics() Diagnostics {
	writeMu.Lock()
	defer writeMu.Unlock()

	diag := Diagnostics{
		LogDir:        GetLogDir(),
		ActiveLogPath: getActiveLogPath(),
		CurrentLevel:  GetLogLevel(),
		Retention: Retention{
			MaxBytesByCategory: make(map[LogCategory]int64, len(retention.MaxBytesByCategory)),
		},
		Initialized:     jsonFile != nil && jsonEnc != nil,
		SupportedLevels: []LogLevel{DEBUG, INFO, WARN, ERROR},
		Categories:      []LogCategory{CategoryCore, CategorySession, CategorySidecar, CategoryAPI, CategoryDiagnostic, CategorySecurity, CategoryAudit, CategoryRealtime, CategoryStorage},
	}
	for category, maxBytes := range retention.MaxBytesByCategory {
		diag.Retention.MaxBytesByCategory[category] = maxBytes
	}
	if lastWriteErr != nil {
		diag.LastWriteError = lastWriteErr.Error()
	}
	return diag
}

// PruneLogs limits session and sidecar logs using a shared budget for backward compatibility.
func PruneLogs(maxSize int64) {
	PruneLogsByCategory(Retention{MaxBytesByCategory: map[LogCategory]int64{
		CategorySession: maxSize,
		CategorySidecar: maxSize,
	}})
}

// PruneLogsByCategory limits log files by category, deleting oldest files first
// within each category.
func PruneLogsByCategory(policy Retention) {
	dir := GetLogDir()
	if dir == "" {
		return
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type fileInfo struct {
		path  string
		size  int64
		mtime time.Time
	}

	grouped := make(map[LogCategory][]fileInfo)

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}

		name := f.Name()
		category, ok := categoryForLogFile(name)
		if ok {
			grouped[category] = append(grouped[category], fileInfo{
				path:  filepath.Join(dir, name),
				size:  info.Size(),
				mtime: info.ModTime(),
			})
		}
	}

	for category, logFiles := range grouped {
		maxSize := policy.MaxBytesByCategory[category]
		if maxSize <= 0 {
			continue
		}
		var totalSize int64
		for _, f := range logFiles {
			totalSize += f.size
		}
		if totalSize <= maxSize {
			continue
		}
		sort.Slice(logFiles, func(i, j int) bool {
			return logFiles[i].mtime.Before(logFiles[j].mtime)
		})
		for _, f := range logFiles {
			if totalSize <= maxSize {
				break
			}
			if err := os.Remove(f.path); err == nil {
				totalSize -= f.size
				writeLogWithFields(INFO, "Logger", fmt.Sprintf("Pruned old log file: %s (size: %d bytes)", filepath.Base(f.path), f.size), Fields{Category: CategoryDiagnostic})
			}
		}
	}
}

func categoryForLogFile(name string) (LogCategory, bool) {
	switch {
	case strings.HasPrefix(name, "sidecar_"):
		return CategorySidecar, true
	case strings.HasPrefix(name, "session_"):
		return CategorySession, true
	case strings.HasPrefix(name, "api_") || strings.HasPrefix(name, "ui_"):
		return CategoryAPI, true
	case strings.HasPrefix(name, "diagnostic_") || strings.HasPrefix(name, "doctor_"):
		return CategoryDiagnostic, true
	case strings.HasPrefix(name, "security_"):
		return CategorySecurity, true
	case strings.HasPrefix(name, "audit_"):
		return CategoryAudit, true
	case strings.HasPrefix(name, "realtime_"):
		return CategoryRealtime, true
	case strings.HasPrefix(name, "storage_"):
		return CategoryStorage, true
	case strings.HasPrefix(name, "artifactflow") || strings.HasPrefix(name, "codingagents") || strings.HasPrefix(name, "central"):
		return CategoryCore, true
	default:
		return "", false
	}
}

// RedactToken returns a stable, non-secret summary for token-like values.
func RedactToken(value string) string {
	return redactSecret("token", value)
}

// RedactPublicKey returns a stable, non-secret summary for public key material.
func RedactPublicKey(value string) string {
	return redactSecret("public_key", value)
}

// RedactFingerprint returns a stable, non-secret summary for device or key fingerprints.
func RedactFingerprint(value string) string {
	return redactSecret("fingerprint", value)
}

// RedactEmail returns a stable, non-secret summary for email addresses.
func RedactEmail(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return redactSecret("email", normalized)
}

// RedactCookie returns a stable, non-secret summary for cookie values.
func RedactCookie(value string) string {
	return redactSecret("cookie", value)
}

// RedactAuthorization returns a stable, non-secret summary for authorization headers.
func RedactAuthorization(value string) string {
	return redactSecret("authorization", value)
}

// RedactPayloadPreview returns a length-only payload summary.
func RedactPayloadPreview(value string) string {
	return fmt.Sprintf("[redacted_payload bytes=%d]", len(value))
}

// CiphertextSize returns a length-only ciphertext summary.
func CiphertextSize(value string) string {
	return fmt.Sprintf("[ciphertext bytes=%d]", len(value))
}

func sanitizeFields(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = sanitizeFieldValue(k, v)
	}
	return out
}

func sanitizeFieldValue(key, value string) string {
	name := strings.ToLower(key)
	switch {
	case strings.Contains(name, "email"):
		return RedactEmail(value)
	case strings.Contains(name, "authorization") || strings.Contains(name, "bearer") || strings.Contains(name, "jwt"):
		return RedactAuthorization(value)
	case strings.Contains(name, "cookie"):
		return RedactCookie(value)
	case strings.Contains(name, "csrf") || strings.Contains(name, "invite") || strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "password"):
		return RedactToken(value)
	case strings.Contains(name, "public_key") || strings.Contains(name, "publickey") || strings.HasSuffix(name, "_key") || name == "key":
		return RedactPublicKey(value)
	case strings.Contains(name, "fingerprint"):
		return RedactFingerprint(value)
	case strings.Contains(name, "payload") || strings.Contains(name, "plaintext"):
		return RedactPayloadPreview(value)
	case strings.Contains(name, "ciphertext"):
		return CiphertextSize(value)
	default:
		return value
	}
}

func redactSecret(kind, value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("[redacted_%s bytes=%d sha256=%x]", kind, len(value), sum[:4])
}
