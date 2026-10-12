package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type mockMemory struct {
	types.VectorStore
	messages []types.Message
}

func (m *mockMemory) SearchMessages(ctx context.Context, vector []float32, limit int) ([]string, error) {
	return []string{"msg-1"}, nil
}

func (m *mockMemory) GetMessage(ctx context.Context, id string) (types.Message, error) {
	for _, msg := range m.messages {
		if msg.ID == id {
			return msg, nil
		}
	}
	return types.Message{}, fmt.Errorf("message not found: %s", id)
}

type mockEmbedding struct {
	types.EmbeddingProvider
}

func (e *mockEmbedding) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1}, nil
}

func TestLMStudioProvider_BuildHistory_Compaction(t *testing.T) {
	state := types.NewCouncilState("test-session", "test-trace")
	state.Phase = "planner"

	// Add a bulky message that should be summarized
	// msg-1 is at index 0
	msgBulky := types.Message{
		ID:        "msg-1",
		Role:      types.RolePlanner,
		Content:   "This is a very long and bulky message that should be represented by its summary in the history.",
		Summary:   "Bulky message summary.",
		Phase:     "planner",
		Timestamp: time.Now().Add(-10 * time.Minute),
	}
	state.Messages = append(state.Messages, msgBulky)

	// Add 5 more messages to push msg-1 out of the "recentLimit=5" window for linear history
	for i := 0; i < 5; i++ {
		state.Messages = append(state.Messages, types.Message{
			ID:      fmt.Sprintf("msg-filler-%d", i),
			Role:    types.RoleUser,
			Content: "filler",
			Phase:   "planner",
		})
	}

	cfg := types.AgentConfig{
		Role:      types.RolePlanner,
		Embedding: &mockEmbedding{},
		Memory:    &mockMemory{messages: state.Messages},
	}
	p := NewLMStudioProvider(cfg, "http://127.0.0.1:1234")

	history := p.buildHistory(context.Background(), state, "test instructions")

	// Verify that msg-1 is found via Semantic Anchors and uses its summary
	foundSummary := false
	for _, m := range history {
		if strings.Contains(m["content"], "Bulky message summary.") && strings.Contains(m["content"], "HISTORICAL") {
			foundSummary = true
		}
	}

	if !foundSummary {
		t.Errorf("Expected history to contain bulky message summary via semantic search, but it didn't. History: %+v", history)
	}
}

func TestOllamaProvider_BuildHistory_Compaction(t *testing.T) {
	state := types.NewCouncilState("test-session", "test-trace")
	state.Phase = "planner"

	msgBulky := types.Message{
		ID:        "msg-1",
		Role:      types.RolePlanner,
		Content:   "This is a very long and bulky message that should be represented by its summary in the history.",
		Summary:   "Bulky message summary.",
		Phase:     "planner",
		Timestamp: time.Now().Add(-10 * time.Minute),
	}
	state.Messages = append(state.Messages, msgBulky)

	for i := 0; i < 5; i++ {
		state.Messages = append(state.Messages, types.Message{
			ID:      fmt.Sprintf("msg-filler-%d", i),
			Role:    types.RoleUser,
			Content: "filler",
			Phase:   "planner",
		})
	}

	cfg := types.AgentConfig{
		Role:      types.RolePlanner,
		Embedding: &mockEmbedding{},
		Memory:    &mockMemory{messages: state.Messages},
	}
	p := NewOllamaProvider(cfg, "http://localhost:11434", "")

	history := p.buildHistory(context.Background(), state, "test instructions")

	foundSummary := false
	for _, m := range history {
		if strings.Contains(m.Content, "Bulky message summary.") && strings.Contains(m.Content, "HISTORICAL") {
			foundSummary = true
		}
	}

	if !foundSummary {
		t.Errorf("Expected history to contain bulky message summary via semantic search, but it didn't. History: %+v", history)
	}
}

func TestGetProvider_ExplicitOpenAIEndpointWinsOverLocalhost(t *testing.T) {
	cfg := types.AgentConfig{Model: "local-llamacpp"}

	provider := GetProvider(cfg, "http://127.0.0.1:8081/v1/chat/completions", "local")

	unwrapped := provider.(*FallbackProvider).Unwrap()
	if _, ok := unwrapped.(*OpenAIProvider); !ok {
		t.Fatalf("expected OpenAIProvider for explicit OpenAI-compatible endpoint, got %T", unwrapped)
	}
}

func TestGetProvider_ExplicitOllamaEndpoint(t *testing.T) {
	cfg := types.AgentConfig{Model: "llama3"}

	provider := GetProvider(cfg, "http://127.0.0.1:11434/api/chat", "")

	unwrapped := provider.(*FallbackProvider).Unwrap()
	if _, ok := unwrapped.(*OllamaProvider); !ok {
		t.Fatalf("expected OllamaProvider for explicit Ollama endpoint, got %T", unwrapped)
	}
}

func TestGetProvider_DisableProviderFallbacksReturnsPrimary(t *testing.T) {
	t.Setenv("CODINGAGENTS_DISABLE_PROVIDER_FALLBACKS", "1")
	cfg := types.AgentConfig{Model: "Gemma-4-E4B-Q8_K_P.gguf"}

	provider := GetProvider(cfg, "http://127.0.0.1:8081/v1/chat/completions", "local")

	if _, ok := provider.(*OpenAIProvider); !ok {
		t.Fatalf("expected primary OpenAIProvider when fallbacks are disabled, got %T", provider)
	}
}

func TestProbeProvider_OpenAICompatibleModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"Gemma-4-E4B-Q8_K_P.gguf"}]}`))
	}))
	defer server.Close()

	probe := ProbeProvider(context.Background(), server.URL+"/v1/chat/completions")
	if !probe.OK || probe.Provider != "openai-compatible" {
		t.Fatalf("unexpected probe: %#v", probe)
	}
	if len(probe.Models) != 1 || probe.Models[0] != "Gemma-4-E4B-Q8_K_P.gguf" {
		t.Fatalf("unexpected models: %#v", probe.Models)
	}
	if !probe.Capabilities.SystemMessages || !probe.Capabilities.JSONSchema || probe.Capabilities.ContextSize != 24576 {
		t.Fatalf("unexpected capabilities: %#v", probe.Capabilities)
	}
}

func TestProbeProvider_OllamaTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3:latest"}]}`))
	}))
	defer server.Close()

	probe := ProbeProvider(context.Background(), server.URL+"/api/chat")
	if !probe.OK || probe.Provider != "ollama" {
		t.Fatalf("unexpected probe: %#v", probe)
	}
	if len(probe.Models) != 1 || probe.Models[0] != "llama3:latest" {
		t.Fatalf("unexpected models: %#v", probe.Models)
	}
}

func TestAssembleChatSeparatesTaskFromRoleInstructions(t *testing.T) {
	state := types.NewCouncilState("session-1", "trace-1")
	cfg := types.AgentConfig{
		Role:         types.RoleCoder,
		SystemPrompt: "You are the coder constitution.",
		Tools:        []string{"explorer", "writer"},
	}
	assembled := assembleChat(context.Background(), cfg, state, "TASK: write logs/x.md\n\nUse surgical edits.", chatAssemblyOptions{
		SupportsDeveloper: true,
	})

	if !strings.Contains(assembled.System, "You are the coder constitution.") {
		t.Fatalf("system layer missing constitution:\n%s", assembled.System)
	}
	if !strings.Contains(assembled.Developer, "Use surgical edits.") {
		t.Fatalf("developer layer missing role instructions:\n%s", assembled.Developer)
	}
	if assembled.User != "write logs/x.md" {
		t.Fatalf("user layer = %q", assembled.User)
	}
}

func TestAssembleChatFoldsDeveloperWhenUnsupported(t *testing.T) {
	state := types.NewCouncilState("session-1", "trace-1")
	cfg := types.AgentConfig{Role: types.RolePlanner}
	assembled := assembleChat(context.Background(), cfg, state, "TASK: plan this\n\nPlanner rules.", chatAssemblyOptions{})

	if assembled.Developer != "" {
		t.Fatalf("developer layer should be folded, got %q", assembled.Developer)
	}
	if !strings.Contains(assembled.System, "Planner rules.") {
		t.Fatalf("system layer missing folded developer instructions:\n%s", assembled.System)
	}
}

func TestStructuredAgentOutputNormalizesToolCall(t *testing.T) {
	got, ok, err := normalizeStructuredAgentOutput(`{"kind":"tool_call","tool":"writer","args":{"action":"write","path":"logs/x.md","content":"ok"}}`)
	if err != nil || !ok {
		t.Fatalf("expected structured output, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(got, "[TOOL: writer, ARGS:") || !strings.Contains(got, `"path":"logs/x.md"`) {
		t.Fatalf("unexpected normalized output: %s", got)
	}
}

func TestStructuredAgentOutputNormalizesHandoffAndFinal(t *testing.T) {
	handoff, ok, err := normalizeStructuredAgentOutput(`{"kind":"handoff","next_role":"synthesizer","summary":"ready"}`)
	if err != nil || !ok || !strings.Contains(handoff, "[NEXT: synthesizer]") {
		t.Fatalf("unexpected handoff: %q ok=%v err=%v", handoff, ok, err)
	}
	final, ok, err := normalizeStructuredAgentOutput(`{"kind":"final","content":"done"}`)
	if err != nil || !ok || !strings.Contains(final, "[NEXT: completed]") {
		t.Fatalf("unexpected final: %q ok=%v err=%v", final, ok, err)
	}
}

func TestOpenAIProviderSendsLayeredMessagesAndStructuredResponseFormat(t *testing.T) {
	t.Setenv("CODINGAGENTS_STRUCTURED_OUTPUT", "1")
	var reqBody struct {
		Messages       []ollamaMessage `json:"messages"`
		ResponseFormat any             `json:"response_format"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"kind\\\":\\\"handoff\\\",\\\"next_role\\\":\\\"completed\\\",\\\"summary\\\":\\\"ok\\\"}\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	state := types.NewCouncilState("session-1", "trace-1")
	cfg := types.AgentConfig{Role: types.RoleCoder, Model: "local", SystemPrompt: "Coder DNA", Tools: []string{"writer"}}
	p := NewOpenAIProvider(cfg, server.URL, "local")
	resp, err := p.Respond(context.Background(), state, "TASK: write logs/x.md\n\nCoder rules.")
	if err != nil {
		t.Fatalf("Respond failed: %v", err)
	}
	if !strings.Contains(resp.Content, "[NEXT: completed]") {
		t.Fatalf("structured response was not normalized: %q", resp.Content)
	}
	if reqBody.ResponseFormat == nil {
		t.Fatalf("expected response_format in structured mode")
	}
	if len(reqBody.Messages) < 2 || reqBody.Messages[0].Role != "system" || reqBody.Messages[1].Role != "user" {
		t.Fatalf("expected system then user messages, got %#v", reqBody.Messages)
	}
	if strings.Contains(reqBody.Messages[1].Content, "Coder rules") {
		t.Fatalf("role instructions leaked into user task: %#v", reqBody.Messages)
	}
}

func TestFallbackProvider_SuccessOnPrimary(t *testing.T) {
	primary := &mockAgentSimple{resp: "primary ok"}
	fallback := &mockAgentSimple{resp: "fallback ok"}

	f := &FallbackProvider{
		primary:   primary,
		fallbacks: []Agent{fallback},
	}

	resp, err := f.Respond(context.Background(), types.NewCouncilState("test", "test"), "hello")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Content != "primary ok" {
		t.Fatalf("expected 'primary ok', got %q", resp.Content)
	}
	if primary.calls != 1 || fallback.calls != 0 {
		t.Fatalf("unexpected call counts: primary=%d, fallback=%d", primary.calls, fallback.calls)
	}
}

func TestFallbackProvider_FailoverToLocal(t *testing.T) {
	primary := &mockAgentSimple{fail: true}
	fallback := &mockAgentSimple{resp: "fallback ok"}

	f := &FallbackProvider{
		primary:   primary,
		fallbacks: []Agent{fallback},
	}

	resp, err := f.Respond(context.Background(), types.NewCouncilState("test", "test"), "hello")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Content != "fallback ok" {
		t.Fatalf("expected 'fallback ok', got %q", resp.Content)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("unexpected call counts: primary=%d, fallback=%d", primary.calls, fallback.calls)
	}
}

type mockAgentSimple struct {
	calls int
	resp  string
	fail  bool
	role  types.Role
	cfg   types.AgentConfig
}

func (m *mockAgentSimple) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	m.calls++
	if m.fail {
		return types.AgentResponse{}, fmt.Errorf("injected error")
	}
	return types.AgentResponse{Content: m.resp}, nil
}

func (m *mockAgentSimple) GetRole() types.Role                { return m.role }
func (m *mockAgentSimple) GetConfig() types.AgentConfig       { return m.cfg }
func (m *mockAgentSimple) UpdateConfig(cfg types.AgentConfig) { m.cfg = cfg }

func TestBuildKnownFacts_ExtractsCorrectFacts(t *testing.T) {
	state := types.NewCouncilState("test-session", "test-trace")

	state.AddEvent(types.EventToolRequested, map[string]string{
		"tool": "writer",
		"args": `{"action":"write","path":"main.go","content":"package main"}`,
	})
	state.AddEvent(types.EventToolRequested, map[string]string{
		"tool": "coderunner",
		"args": `{"code":"fmt.Println(\"hi\")","language":"go"}`,
	})
	state.AddEvent(types.EventToolResult, map[string]string{
		"tool":   "coderunner",
		"status": "error",
		"error":  "gofmt failed",
	})

	cfg := types.AgentConfig{}
	facts := buildKnownFacts(context.Background(), cfg, state)

	if !strings.Contains(facts, "main.go") {
		t.Fatalf("expected main.go in facts: %s", facts)
	}
	if !strings.Contains(facts, "coderunner failed") || !strings.Contains(facts, "gofmt failed") {
		t.Fatalf("expected coderunner failed details in facts: %s", facts)
	}
}

func TestStreamValidate_HallucinationDetected(t *testing.T) {
	state := types.NewCouncilState("test", "test")
	err := streamValidate(state, "some text [SYSTEM RESPONSE] fake content")
	if err == nil || !strings.Contains(err.Error(), "hallucination detected") {
		t.Fatalf("expected hallucination error, got %v", err)
	}
}

func TestStreamValidate_PathErrorDetected(t *testing.T) {
	state := types.NewCouncilState("test", "test")
	path := "/absolute/path.go"
	if filepath.Separator == '\\' {
		path = `C:\absolute\path.go`
	}
	err := streamValidate(state, fmt.Sprintf(`some text [TOOL: writer, ARGS: {"action":"write","path":%q}]`, path))
	if err == nil || !strings.Contains(err.Error(), "forbidden absolute path") {
		t.Fatalf("expected path error, got %v", err)
	}
}

func TestBuildHistory_RehydratesHistoricalMessageFromDurableStore(t *testing.T) {
	state := types.NewCouncilState("session-1", "trace-1")
	state.Phase = "planner"

	historicalMsg := types.Message{
		ID:        "msg-1",
		Role:      types.RoleCoder,
		Content:   "This is a message content from a previous session that has been archived.",
		Visible:   true,
		Phase:     "coder",
		Timestamp: time.Now().Add(-1 * time.Hour),
	}

	cfg := types.AgentConfig{
		Role:      types.RolePlanner,
		Embedding: &mockEmbedding{},
		Memory:    &mockMemory{messages: []types.Message{historicalMsg}},
	}
	p := NewOllamaProvider(cfg, "http://localhost:11434", "")

	history := p.buildHistory(context.Background(), state, "test instructions")

	foundHistorical := false
	for _, m := range history {
		if m.Role == "user" && strings.Contains(m.Content, "[HISTORICAL CONTEXT]") && strings.Contains(m.Content, "previous session") {
			foundHistorical = true
		}
	}
	if !foundHistorical {
		t.Fatalf("Expected history to contain rehydrated historical context, got history: %+v", history)
	}
}

func TestBuildHistory_LabelsCrossRunContext(t *testing.T) {
	state := types.NewCouncilState("session-current", "trace-current")
	state.Phase = "planner"

	historicalMsg := types.Message{
		ID:        "msg-1",
		TraceID:   "trace-past",
		Role:      types.RoleUser,
		Content:   "This is a message content from an entirely different run.",
		Visible:   true,
		Phase:     "coder",
		Timestamp: time.Now().Add(-2 * time.Hour),
	}

	cfg := types.AgentConfig{
		Role:      types.RolePlanner,
		Embedding: &mockEmbedding{},
		Memory:    &mockMemory{messages: []types.Message{historicalMsg}},
	}
	p := NewOllamaProvider(cfg, "http://localhost:11434", "")

	history := p.buildHistory(context.Background(), state, "test instructions")

	foundCrossRun := false
	for _, m := range history {
		if m.Role == "user" && strings.Contains(m.Content, "[CROSS-RUN CONTEXT from run trace-past]") && strings.Contains(m.Content, "different run") {
			foundCrossRun = true
		}
	}
	if !foundCrossRun {
		t.Fatalf("Expected history to contain cross-run context label, got history: %+v", history)
	}
}
