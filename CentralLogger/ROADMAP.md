# Central Logger Roadmap

This document outlines future features for log rotation, retention rules, and category filters inside the structured logger module.

## 📋 Logger Milestones

### 1. Log Rotation
*   Implement size-based log file splitting (e.g., rotate logs when a file exceeds 10MB) to prevent disk fill errors.
*   Enforce max-age deletion rules (e.g. drop log archives older than 30 days automatically).

### 2. Category Pipelines
*   Expose customized category writers to allow different modules (like photos EXIF scaling) to pipe diagnostic entries directly to separate files.
*   Implement log compression (gzip) for archived log files.
