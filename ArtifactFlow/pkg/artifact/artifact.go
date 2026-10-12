package artifact

// Kind represents the high-level semantic type of the artifact.
type Kind string

const (
	KindText         Kind = "text"
	KindMarkdown     Kind = "markdown"
	KindVideo        Kind = "video"
	KindImage        Kind = "image"
	KindCode         Kind = "code"
	KindAudio        Kind = "audio"
	KindValue        Kind = "value"
	KindAgentMessage Kind = "agent_message"
	KindAgentEvent   Kind = "agent_event"
)

// Artifact is the core data structure flowing through the DAG.
// It is intentionally agnostic to what it represents (video, text, code, etc.).
type Artifact struct {
	ID       string         `json:"id"`
	Kind     Kind           `json:"kind"`
	URI      string         `json:"uri"`  // Path to the actual file/content on disk
	MIME     string         `json:"mime"` // Optional MIME type for parsing hints
	Metadata map[string]any `json:"metadata"`
}
