package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"
)

type requestIDContextKey struct{}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(data)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// RequestIDMiddleware ensures every request has an ID available in context and response headers.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request ID attached by RequestIDMiddleware.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// RequestLogMiddleware logs safe request metadata after the wrapped handler completes.
func RequestLogMiddleware(component string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		fields := SafeClientFields(r)
		fields["method"] = r.Method
		fields["path"] = r.URL.Path
		fields["status"] = strconv.Itoa(status)
		fields["bytes"] = strconv.Itoa(rec.bytes)
		fields["duration_ms"] = strconv.FormatInt(time.Since(start).Milliseconds(), 10)
		InfoFields(component, "http request completed", Fields{
			RequestID: RequestIDFromContext(r.Context()),
			Category:  CategoryAPI,
			Fields:    fields,
		})
	})
}

// RecoveryMiddleware logs panics with safe request metadata before returning 500.
func RecoveryMiddleware(component string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				fields := SafeClientFields(r)
				fields["method"] = r.Method
				fields["path"] = r.URL.Path
				fields["panic"] = fmt.Sprint(recovered)
				fields["stack"] = string(debug.Stack())
				ErrorFields(component, "http request panic recovered", Fields{
					RequestID: RequestIDFromContext(r.Context()),
					Category:  CategoryDiagnostic,
					ErrorKind: "panic",
					Fields:    fields,
				})
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// SafeClientFields returns non-secret request metadata suitable for structured logs.
func SafeClientFields(r *http.Request) map[string]string {
	fields := map[string]string{}
	if r == nil {
		return fields
	}
	fields["client_ip"] = clientIP(r)
	if ua := r.UserAgent(); ua != "" {
		fields["user_agent"] = ua
	}
	if ref := r.Referer(); ref != "" {
		fields["referer"] = ref
	}
	if requestID := RequestIDFromContext(r.Context()); requestID != "" {
		fields["request_id"] = requestID
	}
	return fields
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
