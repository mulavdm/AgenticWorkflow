package types

import (
	"context"
	"time"
)

// Role defines the responsibility of an agent in the council.
type Role string

const (
	RolePlanner     Role = "planner"
	RoleCritic      Role = "critic"
	RoleSynthesizer Role = "synthesizer"
	RoleRedTeamer   Role = "redteamer"
	RoleUser        Role = "user"
	RoleContext     Role = "context"
	RoleSystem      Role = "system"
	RoleAnalyst     Role = "analyst"
	RoleSecurity    Role = "security"
	RoleCoder       Role = "coder"
)

// GetRoleColor returns the standardized hex color for a given role.
func GetRoleColor(role Role) string {
	switch role {
	case RoleAnalyst, RoleContext:
		return "#60A5FA" // Blue
	case RolePlanner:
		return "#F59E0B" // Amber
	case RoleCritic:
		return "#EF4444" // Red
	case RoleSecurity:
		return "#8B5CF6" // Purple
	case RoleCoder:
		return "#3B82F6" // Indigo/Blue
	case RoleSynthesizer:
		return "#10B981" // Green
	case RoleSystem:
		return "#6B7280" // Gray
	case RoleUser:
		return "#FFFFFF" // White
	default:
		return "#9CA3AF" // Light Gray
	}
}

// Usage metrics from the model provider.
type Usage struct {
	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

// EventType defines the type of state transition.
type EventType string

const (
	EventMessageAdded      EventType = "message_added"
	EventPhaseStarted      EventType = "phase_started"
	EventToolRequested     EventType = "tool_requested"
	EventToolPolicyDecided EventType = "tool_policy_decided"
	EventToolDenied        EventType = "tool_denied"
	EventToolExecuted      EventType = "tool_executed"
	EventToolResult        EventType = "tool_result"
	EventApprovalRequested EventType = "approval_requested"
	EventApprovalDecided   EventType = "approval_decided"
	EventRunPaused         EventType = "run_paused"
	EventRunResumed        EventType = "run_resumed"
	EventErrorEncountered  EventType = "error_encountered"
	EventContextPruned     EventType = "context_pruned"
)

// StateEvent represents an immutable change to the council state.
type StateEvent struct {
	ID        string      `json:"id"`
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Phase     string      `json:"phase"`
	TraceID   string      `json:"trace_id"`
	Payload   interface{} `json:"payload,omitempty"`
}

// Message represents a single interaction in the council.
// EmbeddingProvider defines the interface for generating numerical embeddings from text.
type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
	GetDimensions() int
}

// VectorStore defines the interface for semantic memory retrieval.
type VectorStore interface {
	IndexMessage(ctx context.Context, msgID string, vector []float32, metadata map[string]string) error
	SearchMessages(ctx context.Context, queryVector []float32, limit int) ([]string, error)
	GetMessage(ctx context.Context, id string) (Message, error)
	DeletePrefix(ctx context.Context, prefix string) error
	Close() error
}

type Message struct {
	ID        string                 `json:"id"`
	TraceID   string                 `json:"trace_id,omitempty"`
	Role      Role                   `json:"role"`
	Model     string                 `json:"model,omitempty"` // Single truth for agent identity
	Content   string                 `json:"content"`
	Summary   string                 `json:"summary,omitempty"` // Natural language summary for context compaction
	Visible   bool                   `json:"visible"`
	Timestamp time.Time              `json:"timestamp"`
	Phase     string                 `json:"phase,omitempty"`
	Retry     int                    `json:"retry,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Usage     *Usage                 `json:"usage,omitempty"`
}

// RoleColors defines the universal color scheme for the framework.
var RoleColors = map[Role]string{
	RoleSystem:      "\033[33m", // Yellow
	RoleUser:        "\033[36m", // Cyan
	RolePlanner:     "\033[32m", // Green
	RoleContext:     "\033[34m", // Blue
	RoleCritic:      "\033[35m", // Magenta
	RoleRedTeamer:   "\033[31m", // Red
	RoleCoder:       "\033[34m", // Blue
	RoleSynthesizer: "\033[37m", // White/Gray
}

// UIEvent represents a single update sent to the dashboard.
type UIEvent struct {
	Type    string      `json:"type"` // "phase", "message", "usage"
	Payload interface{} `json:"payload"`
}

// OrchestrationError represents a structured error in the agent pipeline.
type OrchestrationError struct {
	Phase   string `json:"phase"`
	Role    Role   `json:"role"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
	Retry   int    `json:"retry"`
}

func (e OrchestrationError) Error() string {
	return e.Message
}

// ErrToolExecuted is a special signal that a tool was called and the phase should loop.
var ErrToolExecuted = &OrchestrationError{Message: "TOOL_EXECUTED"}

// ErrApprovalRequired is a special signal that a run has paused for human review.
var ErrApprovalRequired = &OrchestrationError{Message: "APPROVAL_REQUIRED"}

// ApprovalRequiredError carries the approval event id that can unblock a paused run.
type ApprovalRequiredError struct {
	ApprovalID string `json:"approval_id"`
	Tool       string `json:"tool"`
	Reason     string `json:"reason"`
}

func (e *ApprovalRequiredError) Error() string {
	if e == nil {
		return ErrApprovalRequired.Error()
	}
	return ErrApprovalRequired.Error() + ": " + e.Reason
}

func (e *ApprovalRequiredError) Is(target error) bool {
	return target == ErrApprovalRequired
}

func (e *ApprovalRequiredError) Paused() bool {
	return true
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalDenied   ApprovalStatus = "denied"
)

type Approval struct {
	ID             string         `json:"id"`
	RunID          string         `json:"run_id"`
	Role           Role           `json:"role"`
	Tool           string         `json:"tool"`
	Args           string         `json:"args"`
	Reason         string         `json:"reason"`
	Status         ApprovalStatus `json:"status"`
	ResumeMode     string         `json:"resume_mode,omitempty"`
	DecisionReason string         `json:"decision_reason,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	DecidedAt      *time.Time     `json:"decided_at,omitempty"`
}

// AgentResponse represents the unified output from any agent.
type AgentResponse struct {
	Content  string                 `json:"content"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Usage    *Usage                 `json:"usage,omitempty"`
}

// Broadcaster defines the interface for sending events to the UI.
type Broadcaster interface {
	Broadcast(evt UIEvent)
}
