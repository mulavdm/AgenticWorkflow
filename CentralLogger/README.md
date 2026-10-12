# CentralLogger

CentralLogger is the workspace logging module used by ArtifactFlow and CodingAgentsGO. It owns structured JSONL logging, standard-library log redirection, log directory state, flushing, retention, and diagnostics.

## Public API

- `Info`, `Warn`, `Error`, and `Debug` keep the simple component/message API.
- `InfoFields`, `WarnFields`, `ErrorFields`, and `DebugFields` attach structured metadata such as run id, node id, provider, model, sidecar, PID, request id, category, and error kind.
- `CategorySecurity`, `CategoryAudit`, `CategoryRealtime`, and `CategoryStorage` support sensitive application domains without making the logger domain-specific.
- `RedactToken`, `RedactEmail`, `RedactCookie`, `RedactAuthorization`, `RedactPublicKey`, `RedactFingerprint`, `RedactPayloadPreview`, and `CiphertextSize` expose safe summaries for sensitive metadata.
- `RequestIDMiddleware`, `RequestLogMiddleware`, `RecoveryMiddleware`, and `SafeClientFields` provide reusable `net/http` observability middleware.
- `InitLogger`, `CloseLogger`, and `Flush` manage the active JSONL file safely.
- `SetLogDir`, `GetLogDir`, `SetLogLevel`, and `GetLogLevel` expose runtime configuration.
- `PruneLogs` keeps the legacy session/sidecar retention behavior.
- `PruneLogsByCategory` applies retention budgets by log category.
- `GetDiagnostics` reports logger health, active log path, level, retention policy, supported categories, and the last write error.

Structured field values are sanitized before writing. Keys containing email, cookie, authorization, JWT, CSRF, invite, token, secret, password, public key, fingerprint, payload, plaintext, or ciphertext markers are redacted or summarized by byte length so consumers can safely attach operational metadata without leaking secrets.

Consumers should attach domain metadata but should not implement their own retention, logger health, redaction, or log file ownership policy.
