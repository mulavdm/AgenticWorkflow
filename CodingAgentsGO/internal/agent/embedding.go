package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// OllamaEmbeddingProvider implements types.EmbeddingProvider for Ollama.
type OllamaEmbeddingProvider struct {
	URL   string
	Model string
}

func (p *OllamaEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	// Safely truncate extremely long inputs to prevent batch size/context limit errors (e.g. llama.cpp)
	if len(text) > 12000 {
		text = text[:12000]
	}
	url := strings.TrimSuffix(p.URL, "/") + "/api/embeddings"
	reqBody := map[string]string{
		"model":  p.Model,
		"prompt": text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embedding failed (%d): %s", resp.StatusCode, string(body))
	}

	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return res.Embedding, nil
}

func (p *OllamaEmbeddingProvider) GetDimensions() int {
	return 1024 // Common for nomic-embed-text
}

func (p *OllamaEmbeddingProvider) UpdateURL(url string) {
	p.URL = url
}

func (p *OllamaEmbeddingProvider) GetModel() string {
	return p.Model
}

// LMStudioEmbeddingProvider implements EmbeddingProvider for LM Studio.
type LMStudioEmbeddingProvider struct {
	URL   string
	Model string
}

func (p *LMStudioEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	// Safely truncate extremely long inputs to prevent batch size/context limit errors (e.g. llama.cpp)
	if len(text) > 12000 {
		text = text[:12000]
	}
	url := strings.TrimSuffix(p.URL, "/")

	// Use the same logic as discovery to find the base /v1 endpoint
	v1Idx := -1
	for i := 0; i < len(url)-3; i++ {
		if url[i:i+3] == "/v1" {
			v1Idx = i
			break
		}
	}

	if v1Idx != -1 {
		// IMPORTANT: LM Studio often hosts embeddings at /v1/embeddings (OpenAI style)
		// even if the chat is at /api/v1/chat. We must strip /api if it's there.
		base := url[:v1Idx+3]
		if strings.HasSuffix(base, "/api/v1") {
			base = strings.TrimSuffix(base, "/api/v1") + "/v1"
		}
		url = base + "/embeddings"
	} else {
		// Fallback for non-standard URLs
		if !strings.Contains(url, "/v1") {
			url = strings.TrimSuffix(url, "/") + "/v1/embeddings"
		} else if !strings.HasSuffix(url, "/embeddings") {
			url += "/embeddings"
		}
	}

	reqBody := map[string]interface{}{
		"model":           p.Model,
		"input":           []string{text}, // Use array for better compatibility
		"encoding_format": "float",
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	log.Printf("[VectorStore] Sending embedding request to: %s (Model: %s)", url, p.Model)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Propagate API Key if available (from p.URL or similar, though usually empty for local)
	// We'll use http.DefaultClient but consider passing a custom one if needed
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lm studio embedding failed (%d): %s", resp.StatusCode, string(body))
	}

	var res struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if len(res.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned from lm studio")
	}

	return res.Data[0].Embedding, nil
}

func (p *LMStudioEmbeddingProvider) GetDimensions() int {
	return 768 // Common for BERT/Small embedding models
}

func (p *LMStudioEmbeddingProvider) UpdateURL(url string) {
	p.URL = url
}

func (p *LMStudioEmbeddingProvider) GetModel() string {
	return p.Model
}
