# Agent Guidelines for central-logger

This module is a lightweight, zero-dependency structured JSONL logging library used across all modules.

## Architectural Guidelines

1. **Strict Dependency Control**: Do not add external logging packages (like Logrus, Zap, or ZeroLog) to this module. Keep it built on the standard library (`encoding/json`, `os`, `sync`).
2. **Thread Safety**: All write operations to file descriptors must go through `writeMu.Mutex` to prevent concurrent write interleaving or message corruption.
3. **Log Category Strictness**: Ensure log categories are classified accurately according to `LogCategory` options (`core`, `session`, `sidecar`, `api`, `diagnostic`) to maintain proper retention filters.
4. **No Console Blocking**: Hot paths should avoid locking main execution threads. Always use buffered encoding writes where possible.
