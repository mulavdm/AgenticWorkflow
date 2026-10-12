package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type LlamaCppBackend struct{}

func NewLlamaCppBackend() *LlamaCppBackend {
	return &LlamaCppBackend{}
}

func (b *LlamaCppBackend) Name() string {
	return "llama.cpp"
}

func (b *LlamaCppBackend) Supports(model Model) bool {
	return model.Format == "gguf"
}

func (b *LlamaCppBackend) Generate(ctx context.Context, req GenerateRequest, model Model) (GenerateResponse, error) {
	log.Printf("[Backend: llama.cpp] Executing %s...", model.ID)
	log.Printf("  -> Repo: %s", model.Repo)

	if req.Audio != "" {
		log.Printf("  -> Audio attached: %s", req.Audio)
		
		// Ensure audio file is absolute
		audioAbs, err := filepath.Abs(req.Audio)
		if err != nil {
			return GenerateResponse{}, err
		}

		// whisper.cpp is found through the environment: WHISPER_CPP_CLI names
		// whisper-cli and WHISPER_CPP_MODEL the ggml model it loads.
		exePath := os.Getenv("WHISPER_CPP_CLI")
		modelPath := os.Getenv("WHISPER_CPP_MODEL")
		if exePath == "" || modelPath == "" {
			return GenerateResponse{}, fmt.Errorf("audio transcription needs WHISPER_CPP_CLI and WHISPER_CPP_MODEL to be set")
		}

		cmd := exec.CommandContext(ctx, exePath, "-m", modelPath, "-f", audioAbs, "-nt", "-np")
		cmd.Dir = filepath.Dir(exePath)
		
		var outb, errb bytes.Buffer
		cmd.Stdout = &outb
		cmd.Stderr = &errb
		
		if err := cmd.Run(); err != nil {
			return GenerateResponse{}, fmt.Errorf("whisper-cli failed: %v\nstderr: %s", err, errb.String())
		}
		
		// The output has the text, but often has leading/trailing whitespaces
		transcript := strings.TrimSpace(outb.String())
		return GenerateResponse{Text: transcript}, nil
	}

	log.Printf("  -> Prompt: %s", req.Prompt)

	// Fallback to text generation via OpenAI compatible endpoint (e.g. LM Studio)
	payload := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": req.Prompt},
		},
		"temperature": 0.7,
		"max_tokens": 1024,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:1234/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to send request to llama.cpp/lmstudio server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return GenerateResponse{}, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}
	if len(result.Choices) == 0 {
		return GenerateResponse{}, fmt.Errorf("no completion choices returned")
	}

	return GenerateResponse{Text: result.Choices[0].Message.Content}, nil
}
