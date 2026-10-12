package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/user/cognitive-orchestrator/internal/types"
)

func normalizePhase(p string) string {
	p = strings.ToLower(p)
	if strings.HasPrefix(p, "context") || strings.HasPrefix(p, "analyst") {
		return "analyst"
	}
	if strings.HasPrefix(p, "planner") || strings.HasPrefix(p, "orchestrator") {
		return "planner"
	}
	if strings.HasPrefix(p, "security") || strings.HasPrefix(p, "redteam") || strings.HasPrefix(p, "redteamer") {
		return "security"
	}
	if strings.HasPrefix(p, "critic") || strings.HasPrefix(p, "critical") || strings.HasPrefix(p, "reviewer") {
		return "critic"
	}
	if strings.HasPrefix(p, "coder") {
		return "coder"
	}
	if strings.HasPrefix(p, "synthesizer") {
		return "synthesizer"
	}
	return p
}

// OllamaProvider implements the Agent interface for a local Ollama instance.
type OllamaProvider struct {
	cfg    types.AgentConfig
	url    string
	apiKey string
}

func NewOllamaProvider(cfg types.AgentConfig, url string, apiKey string) *OllamaProvider {
	if url == "" {
		url = "http://localhost:11434"
	}
	url = strings.TrimSuffix(url, "/")

	if !strings.Contains(url, "/api") {
		url = url + "/api/chat"
	} else if strings.HasSuffix(url, "/api") {
		url = url + "/chat"
	}
	return &OllamaProvider{cfg: cfg, url: url, apiKey: apiKey}
}

func (p *OllamaProvider) buildHistory(ctx context.Context, state *types.CouncilState, instructions string) []ollamaMessage {
	common := buildProviderHistory(ctx, p.cfg, state, instructions, 2000)
	history := make([]ollamaMessage, 0, len(common))
	for _, msg := range common {
		history = append(history, ollamaMessage{Role: msg.Role, Content: msg.Content})
	}
	return history
}

func (p *OllamaProvider) GetConfig() types.AgentConfig {
	return p.cfg
}

func (p *OllamaProvider) UpdateConfig(cfg types.AgentConfig) {
	p.cfg = cfg
}

func (p *OllamaProvider) UpdateURL(url string) {
	p.url = url
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type ollamaOptions struct {
	NumPredict int      `json:"num_predict,omitempty"`
	Stop       []string `json:"stop,omitempty"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   string          `json:"format,omitempty"`
	Options  ollamaOptions   `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		Thinking  string           `json:"thinking,omitempty"`
		ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	Done               bool  `json:"done"`
	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

type ProviderProbe struct {
	Provider     string               `json:"provider"`
	URL          string               `json:"url"`
	OK           bool                 `json:"ok"`
	Models       []string             `json:"models,omitempty"`
	Capabilities providerCapabilities `json:"capabilities"`
	Error        string               `json:"error,omitempty"`
}

func ProbeProvider(ctx context.Context, url string) ProviderProbe {
	probe := ProviderProbe{URL: url}
	normalizedURL := strings.ToLower(strings.TrimSpace(url))
	switch {
	case strings.Contains(normalizedURL, "/v1/chat/completions"):
		probe.Provider = "openai-compatible"
		probe.URL = strings.TrimSuffix(url[:strings.Index(normalizedURL, "/v1/chat/completions")], "/") + "/v1/models"
		probe.Capabilities = capabilitiesForProvider(probe.Provider)
	case strings.Contains(normalizedURL, ":1234") || strings.Contains(normalizedURL, "lmstudio") || strings.Contains(normalizedURL, "/api/v1"):
		probe.Provider = "lmstudio"
		probe.URL = strings.TrimSuffix(url, "/")
		if !strings.Contains(strings.ToLower(probe.URL), "/api/v1") {
			probe.URL += "/api/v1/models"
		}
		probe.Capabilities = capabilitiesForProvider(probe.Provider)
	case strings.Contains(normalizedURL, ":11434") || strings.Contains(normalizedURL, "ollama") || strings.Contains(normalizedURL, "/api/chat"):
		probe.Provider = "ollama"
		base := url
		if idx := strings.Index(strings.ToLower(base), "/api/chat"); idx >= 0 {
			base = base[:idx]
		}
		probe.URL = strings.TrimSuffix(base, "/") + "/api/tags"
		probe.Capabilities = capabilitiesForProvider(probe.Provider)
	default:
		probe.Provider = "openai-compatible"
		probe.URL = strings.TrimSuffix(url, "/") + "/v1/models"
		probe.Capabilities = capabilitiesForProvider(probe.Provider)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.URL, nil)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		probe.Error = fmt.Sprintf("status %d: %s", resp.StatusCode, string(body))
		return probe
	}
	probe.OK = true
	probe.Models = parseProbeModels(body)
	return probe
}

func capabilitiesForProvider(provider string) providerCapabilities {
	switch provider {
	case "openai-compatible":
		return providerCapabilities{
			SystemMessages:       true,
			DeveloperMessages:    false,
			JSONSchema:           true,
			Streaming:            true,
			OpenAICompatibleChat: true,
			ContextSize:          24576,
		}
	case "lmstudio":
		return providerCapabilities{
			SystemMessages: true,
			JSONSchema:     true,
			Streaming:      false,
			ContextSize:    16384,
		}
	case "ollama":
		return providerCapabilities{
			SystemMessages: true,
			JSONSchema:     false,
			Streaming:      false,
		}
	default:
		return providerCapabilities{}
	}
}

func parseProbeModels(body []byte) []string {
	var payload struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var models []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		models = append(models, id)
	}
	for _, model := range payload.Models {
		if model.Name != "" {
			add(model.Name)
		} else {
			add(model.Model)
		}
	}
	for _, model := range payload.Data {
		add(model.ID)
	}
	return models
}

func logProviderRequest(provider, model string, bytes int, payload string) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CODINGAGENTS_LOG_PROMPTS")))
	if value == "1" || value == "true" || value == "yes" || value == "full" {
		log.Printf("[%s] Prompt sent to %s:\n%s", provider, model, payload)
		return
	}
	log.Printf("[%s] Prompt request prepared for %s (%d bytes). Set CODINGAGENTS_LOG_PROMPTS=full to log full payloads.", provider, model, bytes)
}

func (p *OllamaProvider) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	assembled := assembleChat(ctx, p.cfg, state, instructions, chatAssemblyOptions{ExecutionLogLimit: 2000})
	messages := chatMessagesFromAssembly(assembled)

	maxTokens := p.cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	reqBody := ollamaChatRequest{
		Model:    p.cfg.Model,
		Messages: messages,
		Stream:   true,
		Options: ollamaOptions{
			NumPredict: maxTokens,
			Stop:       []string{"[OBSERVATION", "### SYSTEM", "### USER", "[SYSTEM RESPONSE]", "### ASSISTANT"},
		},
	}
	if structuredOutputEnabled() {
		reqBody.Format = "json"
		reqBody.Messages[0].Content += "\n\n" + structuredOutputInstructions()
	}

	jsonData, err := json.MarshalIndent(reqBody, "", "  ")
	if err != nil {
		return types.AgentResponse{}, err
	}
	logProviderRequest("Ollama", p.cfg.Model, len(jsonData), string(jsonData))

	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(jsonData))
	if err != nil {
		return types.AgentResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start)

	if err != nil {
		return types.AgentResponse{}, fmt.Errorf("http request failed after %v: %w", duration, err)
	}

	content, usage, errStream := readStream(ctx, resp.Body, false, state, nil)
	if errStream != nil {
		return types.AgentResponse{}, errStream
	}

	if resp.StatusCode != http.StatusOK {
		return types.AgentResponse{}, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, content)
	}

	if normalized, ok, err := normalizeStructuredAgentOutput(content); ok || err != nil {
		if err != nil {
			return types.AgentResponse{}, err
		}
		content = normalized
	}

	return types.AgentResponse{
		Content: content,
		Usage:   usage,
	}, nil
}

func getRawProvider(cfg types.AgentConfig, url, apiKey string) Agent {
	normalizedURL := strings.ToLower(url)

	if strings.Contains(normalizedURL, "/v1/chat/completions") {
		return NewOpenAIProvider(cfg, url, apiKey)
	}
	if strings.Contains(normalizedURL, "/api/chat") {
		return NewOllamaProvider(cfg, url, apiKey)
	}
	if strings.Contains(normalizedURL, "/api/v1") {
		return NewLMStudioProvider(cfg, url)
	}

	// 1. Detect LM Studio by port or keyword
	if strings.Contains(normalizedURL, "lmstudio") || strings.Contains(normalizedURL, ":1234") {
		return NewLMStudioProvider(cfg, url)
	}

	// 2. Detect Ollama by port or keyword, or fallback for localhost
	if strings.Contains(normalizedURL, "ollama") || strings.Contains(normalizedURL, ":11434") ||
		strings.Contains(normalizedURL, "localhost") || strings.Contains(normalizedURL, "127.0.0.1") {
		return NewOllamaProvider(cfg, url, apiKey)
	}

	// 3. Default to OpenAI-compatible
	return NewOpenAIProvider(cfg, url, apiKey)
}

func GetProvider(cfg types.AgentConfig, url, apiKey string) Agent {
	primary := getRawProvider(cfg, url, apiKey)
	if providerFallbacksDisabled() {
		return primary
	}

	// Construct fallbacks based on environment variables
	var fallbacks []Agent

	ollamaURL := os.Getenv("LOCAL_OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	lmStudioURL := os.Getenv("LMSTUDIO_URL")
	if lmStudioURL == "" {
		lmStudioURL = "http://127.0.0.1:1234"
	}
	openaiURL := os.Getenv("OPENAI_URL")
	if openaiURL == "" {
		openaiURL = "https://api.openai.com/v1/chat/completions"
	}

	normalizedURL := strings.ToLower(url)
	isPrimaryOllama := strings.Contains(normalizedURL, "ollama") || strings.Contains(normalizedURL, ":11434") || (url == "")
	isPrimaryLMStudio := strings.Contains(normalizedURL, "lmstudio") || strings.Contains(normalizedURL, ":1234")

	if isPrimaryOllama {
		// Fallback to LM Studio
		if isEndpointResponsive(lmStudioURL) {
			cfgLM := cfg
			cfgLM.Model = os.Getenv("LMSTUDIO_MODEL")
			if cfgLM.Model == "" {
				cfgLM.Model = "local-model"
			}
			fallbacks = append(fallbacks, getRawProvider(cfgLM, lmStudioURL, ""))
		}

		// Fallback to OpenAI
		cfgOA := cfg
		cfgOA.Model = os.Getenv("OPENAI_MODEL")
		if cfgOA.Model == "" {
			cfgOA.Model = "gpt-4o-mini"
		}
		fallbacks = append(fallbacks, getRawProvider(cfgOA, openaiURL, os.Getenv("OPENAI_API_KEY")))
	} else if isPrimaryLMStudio {
		// Fallback to Ollama
		if isEndpointResponsive(ollamaURL) {
			cfgOl := cfg
			cfgOl.Model = os.Getenv("LOCAL_OLLAMA_MODEL")
			if cfgOl.Model == "" {
				cfgOl.Model = "gemma2"
			}
			fallbacks = append(fallbacks, getRawProvider(cfgOl, ollamaURL, ""))
		}

		// Fallback to OpenAI
		cfgOA := cfg
		cfgOA.Model = os.Getenv("OPENAI_MODEL")
		if cfgOA.Model == "" {
			cfgOA.Model = "gpt-4o-mini"
		}
		fallbacks = append(fallbacks, getRawProvider(cfgOA, openaiURL, os.Getenv("OPENAI_API_KEY")))
	} else {
		// Fallback to Ollama
		if isEndpointResponsive(ollamaURL) {
			cfgOl := cfg
			cfgOl.Model = os.Getenv("LOCAL_OLLAMA_MODEL")
			if cfgOl.Model == "" {
				cfgOl.Model = "gemma2"
			}
			fallbacks = append(fallbacks, getRawProvider(cfgOl, ollamaURL, ""))
		}

		// Fallback to LM Studio
		if isEndpointResponsive(lmStudioURL) {
			cfgLM := cfg
			cfgLM.Model = os.Getenv("LMSTUDIO_MODEL")
			if cfgLM.Model == "" {
				cfgLM.Model = "local-model"
			}
			fallbacks = append(fallbacks, getRawProvider(cfgLM, lmStudioURL, ""))
		}
	}

	return &FallbackProvider{
		primary:   primary,
		fallbacks: fallbacks,
	}
}

func providerFallbacksDisabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CODINGAGENTS_DISABLE_PROVIDER_FALLBACKS")))
	return value == "1" || value == "true" || value == "yes"
}

// OpenAIProvider implements the Agent interface for OpenAI-compatible APIs.
type OpenAIProvider struct {
	cfg    types.AgentConfig
	url    string
	apiKey string
}

func NewOpenAIProvider(cfg types.AgentConfig, url string, apiKey string) *OpenAIProvider {
	if url == "" {
		url = "https://api.openai.com/v1/chat/completions"
	}
	return &OpenAIProvider{cfg: cfg, url: url, apiKey: apiKey}
}

func (p *OpenAIProvider) buildHistory(ctx context.Context, state *types.CouncilState, instructions string) []ollamaMessage {
	common := buildProviderHistory(ctx, p.cfg, state, instructions, 5000)
	history := make([]ollamaMessage, 0, len(common))
	for _, msg := range common {
		history = append(history, ollamaMessage{Role: msg.Role, Content: msg.Content})
	}
	return history
}

func (p *OpenAIProvider) GetConfig() types.AgentConfig {
	return p.cfg
}

func (p *OpenAIProvider) UpdateConfig(cfg types.AgentConfig) {
	p.cfg = cfg
}

func (p *OpenAIProvider) UpdateURL(url string) {
	p.url = url
}

func (p *OpenAIProvider) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	assembled := assembleChat(ctx, p.cfg, state, instructions, chatAssemblyOptions{ExecutionLogLimit: 5000})
	messages := chatMessagesFromAssembly(assembled)

	reqBody := map[string]interface{}{
		"model":    p.cfg.Model,
		"messages": messages,
		"stream":   true,
	}
	if p.cfg.MaxTokens > 0 {
		reqBody["max_tokens"] = p.cfg.MaxTokens
	}
	if p.cfg.Temperature > 0 {
		reqBody["temperature"] = p.cfg.Temperature
	}
	if structuredOutputEnabled() {
		reqBody["response_format"] = structuredResponseFormat()
		messages[0].Content += "\n\n" + structuredOutputInstructions()
		reqBody["messages"] = messages
	}

	jsonData, err := json.MarshalIndent(reqBody, "", "  ")
	if err != nil {
		return types.AgentResponse{}, err
	}
	logProviderRequest("OpenAI", p.cfg.Model, len(jsonData), string(jsonData))

	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(jsonData))
	if err != nil {
		return types.AgentResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return types.AgentResponse{}, err
	}

	content, usage, errStream := readStream(ctx, resp.Body, true, state, nil)
	if errStream != nil {
		return types.AgentResponse{}, errStream
	}

	if resp.StatusCode != http.StatusOK {
		return types.AgentResponse{}, fmt.Errorf("openai-compatible api returned status %d: %s", resp.StatusCode, content)
	}

	if normalized, ok, err := normalizeStructuredAgentOutput(content); ok || err != nil {
		if err != nil {
			return types.AgentResponse{}, err
		}
		content = normalized
	}

	return types.AgentResponse{
		Content: content,
		Usage:   usage,
	}, nil
}

// LMStudioProvider implements the Agent interface for LM Studio's custom API.
type LMStudioProvider struct {
	cfg types.AgentConfig
	url string
}

func NewLMStudioProvider(cfg types.AgentConfig, url string) *LMStudioProvider {
	if url == "" {
		url = "http://127.0.0.1:1234/api/v1/chat"
	}
	// Map localhost to 127.0.0.1 to avoid IPv6 issues on Windows
	url = strings.Replace(url, "localhost", "127.0.0.1", 1)
	// Ensure it points to the custom chat endpoint if only base URL provided
	if !strings.Contains(url, "/api/v1") {
		url = strings.TrimSuffix(url, "/") + "/api/v1/chat"
	}
	return &LMStudioProvider{cfg: cfg, url: url}
}

func (p *LMStudioProvider) buildHistory(ctx context.Context, state *types.CouncilState, instructions string) []map[string]string {
	common := buildProviderHistory(ctx, p.cfg, state, instructions, 2000)
	history := make([]map[string]string, 0, len(common))
	for _, msg := range common {
		history = append(history, map[string]string{
			"type":    "text",
			"content": fmt.Sprintf("### %s:\n%s", strings.ToUpper(msg.Role), msg.Content),
		})
	}
	return history
}

func (p *LMStudioProvider) GetConfig() types.AgentConfig {
	return p.cfg
}

func (p *LMStudioProvider) UpdateConfig(cfg types.AgentConfig) {
	p.cfg = cfg
}

func (p *LMStudioProvider) UpdateURL(url string) {
	p.url = url
}

func (p *LMStudioProvider) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	assembled := assembleChat(ctx, p.cfg, state, instructions, chatAssemblyOptions{ExecutionLogLimit: 2000})

	// For the native stateful API (LM Studio 0.4.0+), we use 'input' as an array of objects
	// and take advantage of the 'system_prompt' field.
	var inputHistory []map[string]string

	if assembled.User != "" {
		inputHistory = append(inputHistory, map[string]string{
			"type":    "text",
			"content": assembled.User,
		})
	}
	if assembled.Developer != "" {
		inputHistory = append(inputHistory, map[string]string{
			"type":    "text",
			"content": assembled.Developer,
		})
	}

	// 3. Append Phase History (Hybrid Retrieval)
	inputHistory = append(inputHistory, p.buildHistory(ctx, state, instructions)...)

	maxTokens := p.cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	ctxLen := p.cfg.ContextLength
	if ctxLen == 0 {
		ctxLen = 16384
	}

	reqBody := map[string]interface{}{
		"model":             p.cfg.Model,
		"input":             inputHistory,
		"system_prompt":     assembled.System,
		"max_output_tokens": maxTokens,
		"temperature":       p.cfg.Temperature,
		"context_length":    ctxLen,
		"store":             false, // Ensure the runtime maintains the single source of truth
	}
	if structuredOutputEnabled() {
		reqBody["structured"] = true
		reqBody["system_prompt"] = assembled.System + "\n\n" + structuredOutputInstructions()
	}

	jsonData, err := json.MarshalIndent(reqBody, "", "  ")
	if err != nil {
		return types.AgentResponse{}, err
	}
	logProviderRequest("LM Studio", p.cfg.Model, len(jsonData), string(jsonData))

	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(jsonData))
	if err != nil {
		return types.AgentResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return types.AgentResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.AgentResponse{}, err
	}

	if resp.StatusCode != http.StatusOK {
		return types.AgentResponse{}, fmt.Errorf("lm studio returned status %d: %s", resp.StatusCode, string(body))
	}

	var lmStudioResp struct {
		ModelInstanceID string `json:"model_instance_id"`
		Output          []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &lmStudioResp); err != nil {
		return types.AgentResponse{}, err
	}

	if len(lmStudioResp.Output) == 0 {
		return types.AgentResponse{}, fmt.Errorf("no output returned from lm studio")
	}

	// Extract content from the first 'message' type output
	var content string
	for _, out := range lmStudioResp.Output {
		if out.Type == "message" {
			content = out.Content
			break
		}
	}

	if content == "" {
		log.Printf("[LM Studio] WARNING: Received empty content from API. Raw output: %+v", lmStudioResp.Output)
	}
	if normalized, ok, err := normalizeStructuredAgentOutput(content); ok || err != nil {
		if err != nil {
			return types.AgentResponse{}, err
		}
		content = normalized
	}

	return types.AgentResponse{
		Content: content,
		Metadata: map[string]interface{}{
			"model": lmStudioResp.ModelInstanceID,
		},
	}, nil
}

// FallbackProvider wraps a primary provider and acts as a dynamic failover broker.
type FallbackProvider struct {
	primary   Agent
	fallbacks []Agent
}

func (f *FallbackProvider) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	resp, err := f.primary.Respond(ctx, state, instructions)
	if err == nil {
		return resp, nil
	}

	log.Printf("[FallbackProvider] Primary provider failed: %v. Attempting fallbacks...", err)

	var lastErr error = err
	for _, fb := range f.fallbacks {
		if isCloudProvider(fb) {
			allowCloud := strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_CLOUD_FALLBACK")))
			if allowCloud != "true" && allowCloud != "1" && allowCloud != "yes" {
				log.Printf("[FallbackProvider] Skip cloud fallback to model %s: ALLOW_CLOUD_FALLBACK is not enabled.", fb.GetConfig().Model)
				continue
			}
		}

		log.Printf("[FallbackProvider] Attempting failover to model: %s", fb.GetConfig().Model)
		respFallback, errFallback := fb.Respond(ctx, state, instructions)
		if errFallback == nil {
			log.Printf("[FallbackProvider] Fallback to model %s succeeded.", fb.GetConfig().Model)
			return respFallback, nil
		}
		log.Printf("[FallbackProvider] Fallback to model %s failed: %v", fb.GetConfig().Model, errFallback)
		lastErr = errFallback
	}

	return types.AgentResponse{}, fmt.Errorf("primary failed (%w), and all fallbacks failed. Last fallback error: %v", err, lastErr)
}

func (f *FallbackProvider) GetConfig() types.AgentConfig {
	return f.primary.GetConfig()
}

func (f *FallbackProvider) UpdateConfig(cfg types.AgentConfig) {
	f.primary.UpdateConfig(cfg)
	for _, fb := range f.fallbacks {
		modelOverride := fb.GetConfig().Model
		fbCfg := cfg
		fbCfg.Model = modelOverride
		fb.UpdateConfig(fbCfg)
	}
}

func (f *FallbackProvider) UpdateURL(url string) {
	if p, ok := f.primary.(interface{ UpdateURL(string) }); ok {
		p.UpdateURL(url)
	}
}

func (f *FallbackProvider) Unwrap() Agent {
	return f.primary
}

func isCloudProvider(agent Agent) bool {
	switch p := agent.(type) {
	case *OpenAIProvider:
		url := strings.ToLower(p.url)
		if strings.Contains(url, "localhost") || strings.Contains(url, "127.0.0.1") || strings.Contains(url, "::1") {
			return false
		}
		return true
	default:
		return false
	}
}

func isEndpointResponsive(rawURL string) bool {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	target := rawURL
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}
	resp, err := client.Get(target)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// readStream parses chunked SSE or JSON stream lines in real time and enforces grounding rules.
func readStream(ctx context.Context, body io.ReadCloser, isOpenAI bool, state *types.CouncilState, onContent func(string)) (string, *types.Usage, error) {
	defer body.Close()

	reader := bufio.NewReader(body)
	var accumulated strings.Builder
	var lastUsage *types.Usage

	for {
		if err := ctx.Err(); err != nil {
			return accumulated.String(), lastUsage, err
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return accumulated.String(), lastUsage, err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var chunkContent string

		if isOpenAI {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var streamResp struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}

			if err := json.Unmarshal([]byte(data), &streamResp); err == nil {
				if len(streamResp.Choices) > 0 {
					chunkContent = streamResp.Choices[0].Delta.Content
				}
				if streamResp.Usage != nil {
					lastUsage = &types.Usage{
						PromptEvalCount: streamResp.Usage.PromptTokens,
						EvalCount:       streamResp.Usage.CompletionTokens,
					}
				}
			}
		} else {
			var streamResp ollamaChatResponse
			if err := json.Unmarshal([]byte(line), &streamResp); err == nil {
				chunkContent = streamResp.Message.Content
				if streamResp.Done {
					lastUsage = &types.Usage{
						TotalDuration:      streamResp.TotalDuration,
						LoadDuration:       streamResp.LoadDuration,
						PromptEvalCount:    streamResp.PromptEvalCount,
						PromptEvalDuration: streamResp.PromptEvalDuration,
						EvalCount:          streamResp.EvalCount,
						EvalDuration:       streamResp.EvalDuration,
					}
				}
			}
		}

		if chunkContent != "" {
			accumulated.WriteString(chunkContent)
			if onContent != nil {
				onContent(chunkContent)
			}

			if err := streamValidate(state, accumulated.String()); err != nil {
				log.Printf("[StreamingValidation] Rule violation matched mid-stream: %v. Aborting connection.", err)
				return accumulated.String(), lastUsage, err
			}
		}
	}

	return accumulated.String(), lastUsage, nil
}

func streamValidate(state *types.CouncilState, accumulated string) error {
	// 1. Hallucination check
	markers := []string{"[SYSTEM RESPONSE]", "[OBSERVATION from", "[LISTED DIRECTORIES", "[FILE CONTENT", "### CONTEXT"}
	for _, m := range markers {
		if strings.Contains(accumulated, m) {
			return fmt.Errorf("hallucination detected mid-stream: do not simulate system responses (detected: %s)", m)
		}
	}

	// 2. Simple Grounding check on tool arguments
	if strings.Contains(accumulated, "[TOOL:") {
		re := regexp.MustCompile(`(?si)\[?\s*TOOL:\s*(\w+)\s*(?:,?\s*ARGS:\s*(\{.*?\}))?.*?\]?`)
		match := re.FindStringSubmatch(accumulated)
		if len(match) > 0 {
			toolArgs := "{}"
			if len(match) > 2 && match[2] != "" {
				toolArgs = match[2]
			}

			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(toolArgs), &args); err == nil && args.Path != "" {
				cleanPath := filepath.ToSlash(args.Path)
				if strings.HasPrefix(cleanPath, "/") || strings.HasPrefix(cleanPath, "\\") {
					cleanPath = strings.TrimLeft(cleanPath, "/\\")
				}
				if filepath.IsAbs(args.Path) && !filepath.IsAbs(cleanPath) {
					// Allow if it was absolute-style path due to leading slash (e.g. /path)
				} else if filepath.IsAbs(args.Path) {
					return fmt.Errorf("PATH ERROR mid-stream: '%s' is a forbidden absolute path. Use a relative path from the project root.", args.Path)
				}
			}
		}
	}
	return nil
}
