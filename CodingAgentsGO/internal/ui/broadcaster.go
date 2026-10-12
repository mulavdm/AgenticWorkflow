package ui

import (
	"encoding/json"
	"fmt"
	"github.com/user/cognitive-orchestrator/internal/types"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sink defines the interface for any output handler (Console, File, Web, etc.)
type Sink interface {
	OnEvent(evt types.UIEvent)
}

type EventHub struct {
	sinks      []Sink
	roleModels map[types.Role]string
	mu         sync.RWMutex
}

func NewEventHub() *EventHub {
	return &EventHub{
		sinks:      []Sink{},
		roleModels: make(map[types.Role]string),
	}
}

func (h *EventHub) AddSink(s Sink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sinks = append(h.sinks, s)
}

func (h *EventHub) Broadcast(evt types.UIEvent) {
	h.mu.Lock()
	if evt.Type == "message" {
		// Handle pointer payload (preferred)
		if msg, ok := evt.Payload.(*types.Message); ok {
			if msg.Model != "" {
				h.roleModels[msg.Role] = msg.Model
			} else if model, exists := h.roleModels[msg.Role]; exists {
				msg.Model = model
			}
		} else if msg, ok := evt.Payload.(types.Message); ok {
			// Handle value payload (legacy/fallback)
			if msg.Model != "" {
				h.roleModels[msg.Role] = msg.Model
			} else if model, exists := h.roleModels[msg.Role]; exists {
				msg.Model = model
				evt.Payload = msg // Update the value inside the interface
			}
		}
	}
	h.mu.Unlock()

	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sinks {
		s.OnEvent(evt)
	}
}

// ConsoleSink handles synchronized, colored terminal output.
type ConsoleSink struct {
	mu sync.Mutex
}

func (s *ConsoleSink) getColor(role types.Role) string {
	hex := types.GetRoleColor(role)
	// Simple hex to ANSI conversion (rough approximation for 16-color)
	switch hex {
	case "#60A5FA":
		return "\033[1;34m" // Blue
	case "#F59E0B":
		return "\033[1;33m" // Yellow
	case "#EF4444":
		return "\033[1;31m" // Red
	case "#8B5CF6":
		return "\033[1;35m" // Magenta/Purple
	case "#3B82F6":
		return "\033[1;36m" // Cyan (Coder hex color)
	case "#10B981":
		return "\033[1;32m" // Green
	case "#6B7280":
		return "\033[1;30m" // Gray
	default:
		return "\033[0m"
	}
}

func (s *ConsoleSink) OnEvent(evt types.UIEvent) {
	if evt.Type != "message" {
		return
	}

	var msg types.Message
	if m, ok := evt.Payload.(types.Message); ok {
		msg = m
	} else if m, ok := evt.Payload.(*types.Message); ok {
		msg = *m
	} else {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if msg.Role == types.RoleSystem && !strings.Contains(msg.Content, "[OBSERVATION") && !strings.Contains(msg.Content, "[SYSTEM ERROR]") && !strings.Contains(msg.Content, "[SYSTEM HINT]") {
		return
	}

	if msg.Content != "" {
		color := s.getColor(msg.Role)
		reset := "\033[0m"

		if msg.Role == types.RoleSystem {
			content := msg.Content
			if strings.HasPrefix(content, "[OBSERVATION from ") {
				idx := strings.Index(content, "]:")
				if idx != -1 {
					toolName := content[len("[OBSERVATION from ") : idx]
					rest := strings.TrimSpace(content[idx+2:])
					fmt.Printf("\n\033[1;30m[OBSERVATION from %s]\033[0m\n%s\n", toolName, rest)
					return
				}
			} else if strings.HasPrefix(content, "[SYSTEM ERROR]:") {
				rest := strings.TrimSpace(content[len("[SYSTEM ERROR]:"):])
				fmt.Printf("\n\033[1;31m[SYSTEM ERROR]\033[0m %s\n", rest)
				return
			} else if strings.HasPrefix(content, "[SYSTEM HINT]:") {
				rest := strings.TrimSpace(content[len("[SYSTEM HINT]:"):])
				fmt.Printf("\n\033[1;33m[SYSTEM HINT]\033[0m %s\n", rest)
				return
			}
			// Fallback for other system messages
			fmt.Printf("%s[SYSTEM]%s %s\n", color, reset, msg.Content)
		} else {
			label := strings.ToUpper(string(msg.Role))
			if msg.Model != "" {
				label = fmt.Sprintf("%s (%s)", label, msg.Model)
			}
			fmt.Printf("\n\033[1;30m────────────────────────────────────────────────────────────\033[0m\n")
			fmt.Printf("%s[%s]%s\n", color, label, reset)
			fmt.Printf("%s\n", msg.Content)
		}
	}
}

// SSESink handles web dashboard streaming.
type SSESink struct {
	clients map[chan types.UIEvent]bool
	mu      sync.Mutex
}

func NewSSESink() *SSESink {
	return &SSESink{
		clients: make(map[chan types.UIEvent]bool),
	}
}

func (s *SSESink) OnEvent(evt types.UIEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- evt:
		default:
		}
	}
}

func (s *SSESink) Register() chan types.UIEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan types.UIEvent, 10)
	s.clients[ch] = true
	return ch
}

func (s *SSESink) Unregister(ch chan types.UIEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, ch)
	close(ch)
}

func (s *SSESink) SSEHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := s.Register()
	defer s.Unregister(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	for {
		select {
		case evt := <-ch:
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// MarkdownSink generates a live-updating markdown report.
type MarkdownSink struct {
	FilePath string
	mu       sync.Mutex
}

func NewMarkdownSink(path string) *MarkdownSink {
	// Initialize with a header
	header := fmt.Sprintf("# Orchestration Report: %s\n\nGenerated: %s\n\n---\n", filepath.Base(path), time.Now().Format(time.RFC1123))
	_ = os.WriteFile(path, []byte(header), 0644)
	return &MarkdownSink{FilePath: path}
}

func (s *MarkdownSink) OnEvent(evt types.UIEvent) {
	if evt.Type != "message" {
		return
	}
	var msg types.Message
	if m, ok := evt.Payload.(types.Message); ok {
		msg = m
	} else if m, ok := evt.Payload.(*types.Message); ok {
		msg = *m
	} else {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.FilePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	roleLabel := strings.ToUpper(string(msg.Role))
	if msg.Role == types.RoleSystem && strings.Contains(msg.Content, "[OBSERVATION") {
		roleLabel = "OBSERVATION"
	}
	if msg.Model != "" {
		roleLabel = fmt.Sprintf("%s (%s)", roleLabel, msg.Model)
	}

	color := types.GetRoleColor(msg.Role)
	fmt.Fprintf(f, "\n### <span style=\"color:%s\">%s</span> (%s)\n\n", color, roleLabel, time.Now().Format("15:04:05"))
	fmt.Fprintf(f, "```text\n%s\n```\n", msg.Content)
	if msg.Usage != nil {
		fmt.Fprintf(f, "\n> **Usage**: Tokens: Prompt=%d, Eval=%d | Duration: %v\n",
			msg.Usage.PromptEvalCount, msg.Usage.EvalCount, time.Duration(msg.Usage.TotalDuration))
	}
	fmt.Fprintf(f, "\n---\n")
}

// JSONSink handles persistence of the full session state.
type JSONSink struct {
	BaseDir string
}

func NewJSONSink(baseDir string) *JSONSink {
	_ = os.MkdirAll(baseDir, 0755)
	return &JSONSink{BaseDir: baseDir}
}

func (s *JSONSink) OnEvent(evt types.UIEvent) {
	// JSON Sink only reacts to the final state completion event
	if evt.Type != "session_complete" {
		return
	}
	state, ok := evt.Payload.(*types.CouncilState)
	if !ok {
		return
	}

	filename := fmt.Sprintf("session_%s_%s.json", state.SessionID, time.Now().Format("20060102_150405"))
	path := filepath.Join(s.BaseDir, filename)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}
