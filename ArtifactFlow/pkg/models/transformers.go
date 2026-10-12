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

// TransformersBackend wraps the python-sidecar running on localhost:8000
type TransformersBackend struct {
	BaseURL string
}

func NewTransformersBackend() *TransformersBackend {
	return &TransformersBackend{
		BaseURL: "http://127.0.0.1:8000",
	}
}

func (t *TransformersBackend) Name() string {
	return "transformers"
}

func (t *TransformersBackend) Supports(m Model) bool {
	// The python sidecar is responsible for loading PyTorch / safetensors models
	return m.Backend == "transformers"
}

// pythonSidecarRequest represents the JSON expected by FastAPI
type pythonSidecarRequest struct {
	ModelRepo  string   `json:"model_repo"`
	Prompt     string   `json:"prompt"`
	ImagePaths []string `json:"image_paths,omitempty"`
}

// pythonSidecarResponse represents the JSON returned by FastAPI
type pythonSidecarResponse struct {
	Text string `json:"text"`
}

func (t *TransformersBackend) Generate(ctx context.Context, req GenerateRequest, model Model) (GenerateResponse, error) {
	log.Printf("[Backend: Transformers] Executing %s...\n", model.ID)

	payload := pythonSidecarRequest{
		ModelRepo:  model.Repo,
		Prompt:     req.Prompt,
		ImagePaths: req.Images,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.BaseURL+"/generate", bytes.NewReader(body))
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to execute http request to sidecar: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return GenerateResponse{}, fmt.Errorf("sidecar returned status %d: %s", resp.StatusCode, string(errBody))
	}

	var sidecarResp pythonSidecarResponse
	if err := json.NewDecoder(resp.Body).Decode(&sidecarResp); err != nil {
		return GenerateResponse{}, fmt.Errorf("failed to decode sidecar response: %w", err)
	}

	return GenerateResponse{
		Text: sidecarResp.Text,
	}, nil
}
