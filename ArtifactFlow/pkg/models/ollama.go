package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

type OllamaBackend struct{}

func NewOllamaBackend() *OllamaBackend {
	return &OllamaBackend{}
}

func (b *OllamaBackend) Name() string {
	return "ollama"
}

func (b *OllamaBackend) Supports(model Model) bool {
	return model.Format == "gguf" || model.Provider == "ollama"
}

func (b *OllamaBackend) Generate(ctx context.Context, req GenerateRequest, model Model) (GenerateResponse, error) {
	log.Printf("[Backend: Ollama] Executing %s...", model.ID)
	log.Printf("  -> Repo: %s", model.Repo)

	payload := map[string]any{
		"model":  model.Repo,
		"prompt": req.Prompt,
		"stream": false,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Assuming default Ollama host port
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:11434/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to send request to ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return GenerateResponse{}, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}

	return GenerateResponse{Text: result.Response}, nil
}
