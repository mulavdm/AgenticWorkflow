package types

import (
	"fmt"
	"sync"
	"time"
)

// Artifacts represents the shared documents in the workspace.
type Artifacts struct {
	ProjectMap     string `json:"project_map"`     // Structural overview of the codebase
	TechnicalPlan  string `json:"technical_plan"`  // Current step-by-step plan
	ExecutionLogs  string `json:"execution_logs"`  // Summary of tool outputs and results
	Critique       string `json:"critique"`        // Latest critique from the Critic
	SecurityReport string `json:"security_report"` // Latest security audit
}

// CouncilState maintains the shared transcript and context for the orchestration.
// It acts as a materialized view derived from the Events log, which serves as the SSoT.
type CouncilState struct {
	mu sync.RWMutex

	SessionID string       `json:"session_id"`
	TraceID   string       `json:"trace_id"`
	Messages  []Message    `json:"messages"`
	Events    []StateEvent `json:"events"`

	// WorkspaceMode enables isolated agent context based on artifacts.
	WorkspaceMode bool `json:"workspace_mode"`

	// WorkspaceDir is the physical path on disk where shared artifacts are persisted.
	WorkspaceDir string `json:"workspace_dir"`

	// Artifacts contains the materialized state for isolated agents.
	Artifacts Artifacts `json:"artifacts"`

	// Phase tracks the current state of the orchestration loop.
	Phase string `json:"phase"`

	// RetryCount tracks how many times the current phase has been retried.
	RetryCount int `json:"retry_count"`

	// TokenUsage helps monitor efficiency and costs.
	TokenUsage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"token_usage"`

	// LastError stores the most recent error encountered.
	LastError *OrchestrationError `json:"last_error,omitempty"`

	// ToolHistory tracks tool calls for loop detection, synchronized with messages.
	ToolHistory []string `json:"tool_history"`

	// OnMessageAdded is a hook called whenever a message is added to the state.
	OnMessageAdded func(role Role, content string) `json:"-"`
}

// NewCouncilState initializes a new orchestration state.
func NewCouncilState(sessionID, traceID string) *CouncilState {
	return &CouncilState{
		SessionID:     sessionID,
		TraceID:       traceID,
		Messages:      []Message{},
		Events:        []StateEvent{},
		Phase:         "initialization",
		RetryCount:    0,
		WorkspaceMode: true, // Default to true for the new architecture
	}
}

// FlushLastMessages surgically removes the last N messages and corresponding tool history (Thread-Safe).
func (s *CouncilState) FlushLastMessages(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Messages) >= n {
		s.Messages = s.Messages[:len(s.Messages)-n]
	}
	// Prune tool history: 1 tool per 2 messages (agent call + observation)
	toolsToPrune := n / 2
	if toolsToPrune < 1 && n > 0 {
		toolsToPrune = 1
	}
	if len(s.ToolHistory) >= toolsToPrune {
		s.ToolHistory = s.ToolHistory[:len(s.ToolHistory)-toolsToPrune]
	}

	s.addEventLocked(EventContextPruned, map[string]int{"messages_removed": n})
}

// AddMessage appends a new message and broadcasts it.
func (s *CouncilState) AddMessage(role Role, content string, metadata map[string]interface{}, bc Broadcaster) {
	s.AddMessageWithMetadata(role, content, metadata, bc, nil)
}

// AddMessageWithMetadata appends a new message and broadcasts it.
func (s *CouncilState) AddMessageWithMetadata(role Role, content string, metadata map[string]interface{}, bc Broadcaster, usage *Usage) {
	var msg Message
	var hook func(Role, string)

	s.mu.Lock()

	isVisible := role != RoleSystem

	msg = Message{
		ID:        s.generateID(),
		TraceID:   s.TraceID,
		Role:      role,
		Model:     s.getModelFromMetadata(metadata),
		Content:   content,
		Visible:   isVisible,
		Timestamp: time.Now(),
		Phase:     s.Phase,
		Retry:     s.RetryCount,
		Metadata:  metadata,
		Usage:     usage,
	}
	s.Messages = append(s.Messages, msg)

	// Record Event
	s.addEventLocked(EventMessageAdded, msg)

	if usage != nil {
		s.TokenUsage.PromptTokens += usage.PromptEvalCount
		s.TokenUsage.CompletionTokens += usage.EvalCount
	}

	hook = s.OnMessageAdded
	s.mu.Unlock()

	if bc != nil {
		bc.Broadcast(UIEvent{Type: "message", Payload: &msg})
	}

	if hook != nil {
		hook(role, content)
	}
}

// Lock manually locks the state for complex external operations.
func (s *CouncilState) Lock() {
	s.mu.Lock()
}

// Unlock manually unlocks the state.
func (s *CouncilState) Unlock() {
	s.mu.Unlock()
}

// RLock manually locks the state for reading.
func (s *CouncilState) RLock() {
	s.mu.RLock()
}

// RUnlock manually unlocks the state.
func (s *CouncilState) RUnlock() {
	s.mu.RUnlock()
}

// SetMessageSummary updates a message's summary in a thread-safe way.
func (s *CouncilState) SetMessageSummary(id string, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Messages {
		if s.Messages[i].ID == id {
			s.Messages[i].Summary = summary
			break
		}
	}
}

// SnapshotMessages returns a copy of the current message list.
func (s *CouncilState) SnapshotMessages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := make([]Message, len(s.Messages))
	copy(msgs, s.Messages)
	return msgs
}

func (s *CouncilState) getModelFromMetadata(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	// Check lowercase
	if model, ok := metadata["model"].(string); ok && model != "" {
		return model
	}
	// Check uppercase
	if model, ok := metadata["Model"].(string); ok && model != "" {
		return model
	}
	return ""
}

// AddEvent records a state transition in the immutable event log.
func (s *CouncilState) AddEvent(eventType EventType, payload interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addEventLocked(eventType, payload)
}

func (s *CouncilState) addEventLocked(eventType EventType, payload interface{}) {
	event := StateEvent{
		ID:        s.generateID(),
		Type:      eventType,
		Timestamp: time.Now(),
		Phase:     s.Phase,
		TraceID:   s.TraceID,
		Payload:   payload,
	}
	s.Events = append(s.Events, event)
}

// GetCompactedMessages returns a list of messages where older messages use their summaries if available.
func (s *CouncilState) GetCompactedMessages(recentThreshold int) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	compacted := make([]Message, len(s.Messages))
	for i, msg := range s.Messages {
		compacted[i] = msg
		// If message is older than the threshold and has a summary, use it
		if i < len(s.Messages)-recentThreshold && msg.Summary != "" {
			compacted[i].Content = msg.Summary
			// Mark it as a summary for the agent
			compacted[i].Metadata = map[string]interface{}{
				"is_summary":   true,
				"original_len": len(msg.Content),
			}
		}
	}
	return compacted
}

func (s *CouncilState) generateID() string {
	return fmt.Sprintf("%s.%06d", time.Now().Format("20060102150405.000000000"), len(s.Messages)+len(s.Events))
}

// Clone creates a deep copy of the state for thread-safe parallel processing.
func (s *CouncilState) Clone() *CouncilState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	newMessages := make([]Message, len(s.Messages))
	copy(newMessages, s.Messages)

	newHistory := make([]string, len(s.ToolHistory))
	copy(newHistory, s.ToolHistory)

	newEvents := make([]StateEvent, len(s.Events))
	copy(newEvents, s.Events)

	clone := &CouncilState{
		SessionID:      s.SessionID,
		TraceID:        s.TraceID,
		Messages:       newMessages,
		Events:         newEvents,
		Phase:          s.Phase,
		RetryCount:     s.RetryCount,
		ToolHistory:    newHistory,
		WorkspaceMode:  s.WorkspaceMode,
		WorkspaceDir:   s.WorkspaceDir,
		Artifacts:      s.Artifacts,
		OnMessageAdded: s.OnMessageAdded,
	}
	clone.TokenUsage.PromptTokens = s.TokenUsage.PromptTokens
	clone.TokenUsage.CompletionTokens = s.TokenUsage.CompletionTokens

	if s.LastError != nil {
		errCopy := *s.LastError
		clone.LastError = &errCopy
	}

	return clone
}
