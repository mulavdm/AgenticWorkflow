package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"

	_ "modernc.org/sqlite"
)

type Blackboard struct {
	db *sql.DB
	mu sync.Mutex
}

const currentSchemaVersion = 1

func NewBlackboard(dbPath string) (*Blackboard, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	b := &Blackboard{db: db}
	if err := b.initSchema(); err != nil {
		return nil, err
	}
	if err := b.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return b, nil
}

func (b *Blackboard) initSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS workflow_runs (
		run_id TEXT PRIMARY KEY,
		workflow_name TEXT NOT NULL,
		status TEXT NOT NULL,
		started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		completed_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS node_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL,
		node_id TEXT NOT NULL,
		status TEXT NOT NULL,
		error TEXT,
		started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		completed_at DATETIME,
		UNIQUE(run_id, node_id)
	);

	CREATE TABLE IF NOT EXISTS artifacts (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		node_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		uri TEXT NOT NULL,
		mime TEXT,
		metadata_json TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS run_events (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		node_id TEXT,
		type TEXT NOT NULL,
		payload_json TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		node_id TEXT,
		role TEXT NOT NULL,
		model TEXT,
		content TEXT NOT NULL,
		visible INTEGER NOT NULL DEFAULT 1,
		metadata_json TEXT,
		usage_json TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS lessons (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task TEXT NOT NULL,
		critique TEXT NOT NULL,
		resolution TEXT NOT NULL,
		tags TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_lessons_tags ON lessons(tags);

	CREATE TABLE IF NOT EXISTS vectors (
		id TEXT PRIMARY KEY,
		run_id TEXT,
		target_id TEXT NOT NULL,
		dims INTEGER NOT NULL,
		vector_json TEXT NOT NULL,
		metadata_json TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS configs (
		namespace TEXT NOT NULL,
		key TEXT NOT NULL,
		value_json TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(namespace, key)
	);

	CREATE TABLE IF NOT EXISTS schema_versions (
		name TEXT PRIMARY KEY,
		version INTEGER NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_workflow_runs_started ON workflow_runs(started_at DESC, run_id DESC);
	CREATE INDEX IF NOT EXISTS idx_node_runs_run ON node_runs(run_id, started_at, node_id);
	CREATE INDEX IF NOT EXISTS idx_artifacts_run_node ON artifacts(run_id, node_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_run_events_run_time ON run_events(run_id, created_at, id);
	CREATE INDEX IF NOT EXISTS idx_messages_run_time ON messages(run_id, created_at, id);
	CREATE INDEX IF NOT EXISTS idx_vectors_run_dims ON vectors(run_id, dims, created_at, id);
	CREATE INDEX IF NOT EXISTS idx_vectors_target ON vectors(target_id);
	`

	_, err := b.db.Exec(query)
	return err
}

// SchemaVersion returns the blackboard schema version recorded in SQLite.
func (b *Blackboard) SchemaVersion() (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.schemaVersionLocked()
}

func (b *Blackboard) schemaVersionLocked() (int, error) {
	var version int
	err := b.db.QueryRow(`SELECT version FROM schema_versions WHERE name = 'blackboard'`).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return version, err
}

// Migrate applies idempotent blackboard migrations and records the active schema version.
func (b *Blackboard) Migrate() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	version, err := b.schemaVersionLocked()
	if err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("blackboard schema version %d is newer than supported version %d", version, currentSchemaVersion)
	}
	if version == currentSchemaVersion {
		return nil
	}

	migrations := []string{
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_started ON workflow_runs(started_at DESC, run_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_node_runs_run ON node_runs(run_id, started_at, node_id)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_run_node ON artifacts(run_id, node_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_run_events_run_time ON run_events(run_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_run_time ON messages(run_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_vectors_run_dims ON vectors(run_id, dims, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_vectors_target ON vectors(target_id)`,
	}
	for _, query := range migrations {
		if _, err := b.db.Exec(query); err != nil {
			return err
		}
	}
	_, err = b.db.Exec(`
		INSERT INTO schema_versions (name, version) VALUES ('blackboard', ?)
		ON CONFLICT(name) DO UPDATE SET version = excluded.version, updated_at = CURRENT_TIMESTAMP
	`, currentSchemaVersion)
	return err
}

func (b *Blackboard) Close() error {
	return b.db.Close()
}

func (b *Blackboard) StartWorkflowRun(runID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`
		INSERT INTO workflow_runs (run_id, workflow_name, status) VALUES (?, ?, 'running')
		ON CONFLICT(run_id) DO UPDATE SET status = 'running', completed_at = NULL
	`, runID, name)
	return err
}

func (b *Blackboard) UpdateWorkflowRun(runID, status string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if status == "running" || status == "paused" {
		_, err := b.db.Exec(`UPDATE workflow_runs SET status = ?, completed_at = NULL WHERE run_id = ?`, status, runID)
		return err
	}
	_, err := b.db.Exec(`UPDATE workflow_runs SET status = ?, completed_at = CURRENT_TIMESTAMP WHERE run_id = ?`, status, runID)
	return err
}

type WorkflowRun struct {
	RunID        string     `json:"run_id"`
	WorkflowName string     `json:"workflow_name"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type NodeRun struct {
	RunID       string     `json:"run_id"`
	NodeID      string     `json:"node_id"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type TimelineEntry struct {
	Kind      string            `json:"kind"`
	ID        string            `json:"id"`
	RunID     string            `json:"run_id"`
	NodeID    string            `json:"node_id,omitempty"`
	Role      string            `json:"role,omitempty"`
	Type      string            `json:"type,omitempty"`
	Content   string            `json:"content,omitempty"`
	Artifact  artifact.Artifact `json:"artifact,omitempty"`
	Payload   map[string]any    `json:"payload,omitempty"`
	Metadata  map[string]any    `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

type RunSnapshot struct {
	Run       WorkflowRun         `json:"run"`
	Nodes     []NodeRun           `json:"nodes"`
	Messages  []Message           `json:"messages"`
	Events    []Event             `json:"events"`
	Artifacts []artifact.Artifact `json:"artifacts"`
	Timeline  []TimelineEntry     `json:"timeline"`
}

type WorkflowCursor struct {
	RunID         string          `json:"run_id"`
	WorkflowName  string          `json:"workflow_name"`
	StartNode     string          `json:"start_node"`
	CurrentNode   string          `json:"current_node"`
	TerminalState string          `json:"terminal_state,omitempty"`
	WorkflowJSON  json.RawMessage `json:"workflow_json"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Message struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	NodeID    string         `json:"node_id,omitempty"`
	Role      string         `json:"role"`
	Model     string         `json:"model,omitempty"`
	Content   string         `json:"content"`
	Visible   bool           `json:"visible"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Usage     map[string]any `json:"usage,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type Event struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	NodeID    string         `json:"node_id,omitempty"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type Lesson struct {
	ID         int       `json:"id"`
	Task       string    `json:"task"`
	Critique   string    `json:"critique"`
	Resolution string    `json:"resolution"`
	Tags       string    `json:"tags"`
	CreatedAt  time.Time `json:"created_at"`
}

type VectorEntry struct {
	ID        string            `json:"id"`
	RunID     string            `json:"run_id,omitempty"`
	TargetID  string            `json:"target_id"`
	Vector    []float32         `json:"vector"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// VectorQuery filters vector entries before caller-side similarity scoring.
// Metadata filters are matched against known metadata keys to support current
// CodingAgents code chunks while leaving room for richer vector records.
type VectorQuery struct {
	RunID            string
	Kind             string
	SourcePathPrefix string
	EmbeddingModelID string
	Dimensions       int
	Limit            int
}

func (b *Blackboard) GetWorkflowRun(runID string) (*WorkflowRun, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var run WorkflowRun
	var completed sql.NullTime
	err := b.db.QueryRow(`SELECT run_id, workflow_name, status, started_at, completed_at FROM workflow_runs WHERE run_id = ?`, runID).
		Scan(&run.RunID, &run.WorkflowName, &run.Status, &run.StartedAt, &completed)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow run not found: %s", runID)
		}
		return nil, err
	}
	if completed.Valid {
		run.CompletedAt = &completed.Time
	}
	return &run, nil
}

func (b *Blackboard) ListWorkflowRuns(limit int) ([]WorkflowRun, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}

	rows, err := b.db.Query(`SELECT run_id, workflow_name, status, started_at, completed_at FROM workflow_runs ORDER BY started_at DESC, run_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []WorkflowRun
	for rows.Next() {
		var run WorkflowRun
		var completed sql.NullTime
		if err := rows.Scan(&run.RunID, &run.WorkflowName, &run.Status, &run.StartedAt, &completed); err != nil {
			return nil, err
		}
		if completed.Valid {
			run.CompletedAt = &completed.Time
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (b *Blackboard) CancelWorkflowRun(runID string) error {
	return b.UpdateWorkflowRun(runID, "cancelled")
}

func (b *Blackboard) SaveWorkflowCursor(cursor WorkflowCursor) error {
	cursor.UpdatedAt = time.Now()
	return b.SaveConfig("workflow_cursor", cursor.RunID, cursor)
}

func (b *Blackboard) LoadWorkflowCursor(runID string) (*WorkflowCursor, error) {
	var cursor WorkflowCursor
	if err := b.LoadConfig("workflow_cursor", runID, &cursor); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow cursor not found: %s", runID)
		}
		return nil, err
	}
	return &cursor, nil
}

func (b *Blackboard) SaveMessage(msg Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	metaBytes, _ := json.Marshal(msg.Metadata)
	usageBytes, _ := json.Marshal(msg.Usage)
	visible := 0
	if msg.Visible {
		visible = 1
	}
	_, err := b.db.Exec(`
		INSERT INTO messages (id, run_id, node_id, role, model, content, visible, metadata_json, usage_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		msg.ID, msg.RunID, msg.NodeID, msg.Role, msg.Model, msg.Content, visible, string(metaBytes), string(usageBytes),
	)
	return err
}

func (b *Blackboard) GetMessages(runID string) ([]Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rows, err := b.db.Query(`SELECT id, run_id, node_id, role, model, content, visible, metadata_json, usage_json, created_at FROM messages WHERE run_id = ? ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var metaJSON, usageJSON string
		var visible int
		if err := rows.Scan(&msg.ID, &msg.RunID, &msg.NodeID, &msg.Role, &msg.Model, &msg.Content, &visible, &metaJSON, &usageJSON, &msg.CreatedAt); err != nil {
			return nil, err
		}
		msg.Visible = visible == 1
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &msg.Metadata)
		}
		if usageJSON != "" {
			_ = json.Unmarshal([]byte(usageJSON), &msg.Usage)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (b *Blackboard) GetMessage(id string) (Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var msg Message
	var metaJSON, usageJSON string
	var visible int
	err := b.db.QueryRow(`SELECT id, run_id, node_id, role, model, content, visible, metadata_json, usage_json, created_at FROM messages WHERE id = ?`, id).
		Scan(&msg.ID, &msg.RunID, &msg.NodeID, &msg.Role, &msg.Model, &msg.Content, &visible, &metaJSON, &usageJSON, &msg.CreatedAt)
	if err != nil {
		return Message{}, err
	}
	msg.Visible = visible == 1
	if metaJSON != "" {
		_ = json.Unmarshal([]byte(metaJSON), &msg.Metadata)
	}
	if usageJSON != "" {
		_ = json.Unmarshal([]byte(usageJSON), &msg.Usage)
	}
	return msg, nil
}

func (b *Blackboard) UpdateMessageVisibility(id string, visible bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	v := 0
	if visible {
		v = 1
	}
	_, err := b.db.Exec(`UPDATE messages SET visible = ? WHERE id = ?`, v, id)
	return err
}

func (b *Blackboard) SaveEvent(evt Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	payloadBytes, _ := json.Marshal(evt.Payload)
	_, err := b.db.Exec(`
		INSERT INTO run_events (id, run_id, node_id, type, payload_json)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		evt.ID, evt.RunID, evt.NodeID, evt.Type, string(payloadBytes),
	)
	return err
}

func (b *Blackboard) GetEvents(runID string) ([]Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rows, err := b.db.Query(`SELECT id, run_id, node_id, type, payload_json, created_at FROM run_events WHERE run_id = ? ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var evt Event
		var payloadJSON string
		if err := rows.Scan(&evt.ID, &evt.RunID, &evt.NodeID, &evt.Type, &payloadJSON, &evt.CreatedAt); err != nil {
			return nil, err
		}
		if payloadJSON != "" {
			_ = json.Unmarshal([]byte(payloadJSON), &evt.Payload)
		}
		events = append(events, evt)
	}
	return events, rows.Err()
}

func (b *Blackboard) SaveLesson(lesson Lesson) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`INSERT INTO lessons (task, critique, resolution, tags) VALUES (?, ?, ?, ?)`,
		lesson.Task, lesson.Critique, lesson.Resolution, lesson.Tags)
	return err
}

func (b *Blackboard) SearchLessons(query string, limit int) ([]Lesson, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 {
		limit = 5
	}
	term := "%" + query + "%"
	rows, err := b.db.Query(`SELECT id, task, critique, resolution, tags, created_at FROM lessons WHERE task LIKE ? OR tags LIKE ? ORDER BY created_at DESC LIMIT ?`, term, term, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lessons []Lesson
	for rows.Next() {
		var lesson Lesson
		if err := rows.Scan(&lesson.ID, &lesson.Task, &lesson.Critique, &lesson.Resolution, &lesson.Tags, &lesson.CreatedAt); err != nil {
			return nil, err
		}
		lessons = append(lessons, lesson)
	}
	return lessons, rows.Err()
}

func (b *Blackboard) SaveVector(entry VectorEntry) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vectorBytes, _ := json.Marshal(entry.Vector)
	metaBytes, _ := json.Marshal(entry.Metadata)
	_, err := b.db.Exec(`
		INSERT INTO vectors (id, run_id, target_id, dims, vector_json, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET vector_json = excluded.vector_json, metadata_json = excluded.metadata_json`,
		entry.ID, entry.RunID, entry.TargetID, len(entry.Vector), string(vectorBytes), string(metaBytes),
	)
	return err
}

func (b *Blackboard) GetVector(id string) (VectorEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var entry VectorEntry
	var vectorJSON, metaJSON string
	err := b.db.QueryRow(`SELECT id, run_id, target_id, vector_json, metadata_json, created_at FROM vectors WHERE id = ?`, id).
		Scan(&entry.ID, &entry.RunID, &entry.TargetID, &vectorJSON, &metaJSON, &entry.CreatedAt)
	if err != nil {
		return VectorEntry{}, err
	}

	_ = json.Unmarshal([]byte(vectorJSON), &entry.Vector)
	if metaJSON != "" {
		_ = json.Unmarshal([]byte(metaJSON), &entry.Metadata)
	}
	return entry, nil
}

func (b *Blackboard) GetVectors() ([]VectorEntry, error) {
	return b.QueryVectors(VectorQuery{})
}

// QueryVectors returns vector records filtered by run, dimensions, and metadata.
func (b *Blackboard) QueryVectors(query VectorQuery) ([]VectorEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sqlQuery := `SELECT id, run_id, target_id, vector_json, metadata_json, created_at FROM vectors`
	var clauses []string
	var args []any
	if query.RunID != "" {
		clauses = append(clauses, `run_id = ?`)
		args = append(args, query.RunID)
	}
	if query.Dimensions > 0 {
		clauses = append(clauses, `dims = ?`)
		args = append(args, query.Dimensions)
	}
	if len(clauses) > 0 {
		sqlQuery += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	sqlQuery += ` ORDER BY created_at, id`

	rows, err := b.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []VectorEntry
	for rows.Next() {
		var entry VectorEntry
		var vectorJSON, metaJSON string
		if err := rows.Scan(&entry.ID, &entry.RunID, &entry.TargetID, &vectorJSON, &metaJSON, &entry.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(vectorJSON), &entry.Vector)
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &entry.Metadata)
		}
		if !vectorEntryMatchesQuery(entry, query) {
			continue
		}
		entries = append(entries, entry)
		if query.Limit > 0 && len(entries) >= query.Limit {
			break
		}
	}
	return entries, rows.Err()
}

func vectorEntryMatchesQuery(entry VectorEntry, query VectorQuery) bool {
	if query.Kind != "" {
		kind := entry.Metadata["kind"]
		if kind == "" {
			kind = entry.Metadata["content_kind"]
		}
		if kind == "" {
			kind = entry.Metadata["role"]
		}
		if query.Kind == "memory" && kind != "code" && kind != "" {
			// Backward compatibility: historical message vectors used role names
			// (planner, coder, user, etc.) before explicit kind metadata existed.
		} else if kind != query.Kind {
			return false
		}
	}
	if query.SourcePathPrefix != "" {
		source := entry.Metadata["source_path"]
		if source == "" {
			source = entry.Metadata["phase"]
		}
		source = filepathSlash(source)
		prefix := filepathSlash(query.SourcePathPrefix)
		if !strings.HasPrefix(source, prefix) {
			return false
		}
	}
	if query.EmbeddingModelID != "" {
		modelID := entry.Metadata["embedding_model_id"]
		if modelID == "" {
			modelID = entry.Metadata["model_id"]
		}
		if modelID != query.EmbeddingModelID {
			return false
		}
	}
	return true
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

func (b *Blackboard) DeleteVectorsByPrefix(prefix string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`DELETE FROM vectors WHERE id LIKE ?`, prefix+"%")
	return err
}

func (b *Blackboard) SaveConfig(namespace, key string, value any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	valueBytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = b.db.Exec(`
		INSERT INTO configs (namespace, key, value_json) VALUES (?, ?, ?)
		ON CONFLICT(namespace, key) DO UPDATE SET value_json = excluded.value_json, updated_at = CURRENT_TIMESTAMP`,
		namespace, key, string(valueBytes),
	)
	return err
}

func (b *Blackboard) LoadConfig(namespace, key string, dest any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var valueJSON string
	err := b.db.QueryRow(`SELECT value_json FROM configs WHERE namespace = ? AND key = ?`, namespace, key).Scan(&valueJSON)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(valueJSON), dest)
}

func (b *Blackboard) RecordNodeStart(runID, nodeID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`
		INSERT INTO node_runs (run_id, node_id, status) VALUES (?, ?, 'running')
		ON CONFLICT(run_id, node_id) DO UPDATE SET status = 'running', error = NULL, completed_at = NULL
	`, runID, nodeID)
	return err
}

func (b *Blackboard) RecordNodeCompletion(runID, nodeID, status, errorMsg string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.db.Exec(`UPDATE node_runs SET status = ?, error = ?, completed_at = CURRENT_TIMESTAMP WHERE run_id = ? AND node_id = ?`, status, errorMsg, runID, nodeID)
	return err
}

func (b *Blackboard) SaveArtifact(runID, nodeID string, art artifact.Artifact) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	metaBytes, _ := json.Marshal(art.Metadata)

	_, err := b.db.Exec(`
		INSERT INTO artifacts (id, run_id, node_id, kind, uri, mime, metadata_json) 
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		art.ID, runID, nodeID, string(art.Kind), art.URI, art.MIME, string(metaBytes),
	)
	return err
}

func (b *Blackboard) GetArtifact(id string) (*artifact.Artifact, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var art artifact.Artifact
	var kind string
	var metaJSON string

	err := b.db.QueryRow(`SELECT id, kind, uri, mime, metadata_json FROM artifacts WHERE id = ?`, id).
		Scan(&art.ID, &kind, &art.URI, &art.MIME, &metaJSON)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("artifact not found: %s", id)
		}
		return nil, err
	}

	art.Kind = artifact.Kind(kind)
	if metaJSON != "" {
		_ = json.Unmarshal([]byte(metaJSON), &art.Metadata)
	}

	return &art, nil
}

func (b *Blackboard) GetNodeStatus(runID, nodeID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var status string
	err := b.db.QueryRow(`SELECT status FROM node_runs WHERE run_id = ? AND node_id = ?`, runID, nodeID).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // Not started
		}
		return "", err
	}
	return status, nil
}

func (b *Blackboard) GetNodeRuns(runID string) ([]NodeRun, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rows, err := b.db.Query(`SELECT run_id, node_id, status, COALESCE(error, ''), started_at, completed_at FROM node_runs WHERE run_id = ? ORDER BY started_at, node_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []NodeRun
	for rows.Next() {
		var node NodeRun
		var completed sql.NullTime
		if err := rows.Scan(&node.RunID, &node.NodeID, &node.Status, &node.Error, &node.StartedAt, &completed); err != nil {
			return nil, err
		}
		if completed.Valid {
			node.CompletedAt = &completed.Time
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (b *Blackboard) GetNodeArtifacts(runID, nodeID string) ([]artifact.Artifact, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rows, err := b.db.Query(`SELECT id, kind, uri, mime, metadata_json FROM artifacts WHERE run_id = ? AND node_id = ?`, runID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []artifact.Artifact
	for rows.Next() {
		var art artifact.Artifact
		var kind string
		var metaJSON string

		if err := rows.Scan(&art.ID, &kind, &art.URI, &art.MIME, &metaJSON); err != nil {
			return nil, err
		}

		art.Kind = artifact.Kind(kind)
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &art.Metadata)
		}
		artifacts = append(artifacts, art)
	}
	return artifacts, nil
}

func (b *Blackboard) GetRunArtifacts(runID string) ([]artifact.Artifact, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rows, err := b.db.Query(`SELECT id, kind, uri, mime, metadata_json FROM artifacts WHERE run_id = ? ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []artifact.Artifact
	for rows.Next() {
		var art artifact.Artifact
		var kind string
		var metaJSON string
		if err := rows.Scan(&art.ID, &kind, &art.URI, &art.MIME, &metaJSON); err != nil {
			return nil, err
		}
		art.Kind = artifact.Kind(kind)
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &art.Metadata)
		}
		artifacts = append(artifacts, art)
	}
	return artifacts, rows.Err()
}

func (b *Blackboard) GetTimeline(runID string) ([]TimelineEntry, error) {
	messages, err := b.GetMessages(runID)
	if err != nil {
		return nil, err
	}
	events, err := b.GetEvents(runID)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	rows, err := b.db.Query(`SELECT id, node_id, kind, uri, mime, metadata_json, created_at FROM artifacts WHERE run_id = ? ORDER BY created_at, id`, runID)
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	var artifactEntries []TimelineEntry
	for rows.Next() {
		var art artifact.Artifact
		var nodeID, kind, metaJSON string
		var createdAt time.Time
		if err := rows.Scan(&art.ID, &nodeID, &kind, &art.URI, &art.MIME, &metaJSON, &createdAt); err != nil {
			rows.Close()
			b.mu.Unlock()
			return nil, err
		}
		art.Kind = artifact.Kind(kind)
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &art.Metadata)
		}
		artifactEntries = append(artifactEntries, TimelineEntry{
			Kind:      "artifact",
			ID:        art.ID,
			RunID:     runID,
			NodeID:    nodeID,
			Type:      string(art.Kind),
			Artifact:  art,
			Metadata:  art.Metadata,
			CreatedAt: createdAt,
		})
	}
	rowsErr := rows.Err()
	rows.Close()
	b.mu.Unlock()
	if rowsErr != nil {
		return nil, rowsErr
	}

	timeline := make([]TimelineEntry, 0, len(messages)+len(events)+len(artifactEntries))
	for _, msg := range messages {
		timeline = append(timeline, TimelineEntry{
			Kind:      "message",
			ID:        msg.ID,
			RunID:     msg.RunID,
			NodeID:    msg.NodeID,
			Role:      msg.Role,
			Content:   msg.Content,
			Metadata:  msg.Metadata,
			CreatedAt: msg.CreatedAt,
		})
	}
	for _, evt := range events {
		timeline = append(timeline, TimelineEntry{
			Kind:      "event",
			ID:        evt.ID,
			RunID:     evt.RunID,
			NodeID:    evt.NodeID,
			Type:      evt.Type,
			Payload:   evt.Payload,
			CreatedAt: evt.CreatedAt,
		})
	}
	timeline = append(timeline, artifactEntries...)
	sort.Slice(timeline, func(i, j int) bool {
		if !timeline[i].CreatedAt.Equal(timeline[j].CreatedAt) {
			return timeline[i].CreatedAt.Before(timeline[j].CreatedAt)
		}
		if timeline[i].Kind != timeline[j].Kind {
			return timeline[i].Kind < timeline[j].Kind
		}
		return timeline[i].ID < timeline[j].ID
	})
	return timeline, nil
}

func (b *Blackboard) ReconstructRun(runID string) (*RunSnapshot, error) {
	run, err := b.GetWorkflowRun(runID)
	if err != nil {
		return nil, err
	}
	nodes, err := b.GetNodeRuns(runID)
	if err != nil {
		return nil, err
	}
	messages, err := b.GetMessages(runID)
	if err != nil {
		return nil, err
	}
	events, err := b.GetEvents(runID)
	if err != nil {
		return nil, err
	}
	artifacts, err := b.GetRunArtifacts(runID)
	if err != nil {
		return nil, err
	}
	timeline, err := b.GetTimeline(runID)
	if err != nil {
		return nil, err
	}
	return &RunSnapshot{
		Run:       *run,
		Nodes:     nodes,
		Messages:  messages,
		Events:    events,
		Artifacts: artifacts,
		Timeline:  timeline,
	}, nil
}
