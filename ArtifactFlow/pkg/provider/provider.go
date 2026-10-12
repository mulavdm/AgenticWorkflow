package provider

import (
	"fmt"
)

type ProviderType string

const (
	LocalProvider ProviderType = "local"
	CloudProvider ProviderType = "cloud"
)

type ModelProvider interface {
	Generate(prompt string) (string, error)
	GenerateEmbedding(data []byte) ([]float32, error)
}

// LocalLMProvider stub
type LocalLMProvider struct {
	endpoint string
}

func NewLocalLMProvider(endpoint string) *LocalLMProvider {
	return &LocalLMProvider{endpoint: endpoint}
}

func (p *LocalLMProvider) Generate(prompt string) (string, error) {
	// TODO: Call LM Studio / Ollama REST API
	return fmt.Sprintf("Local response to: %s", prompt), nil
}

func (p *LocalLMProvider) GenerateEmbedding(data []byte) ([]float32, error) {
	// TODO: Call local embedding API
	return []float32{0.1, 0.2, 0.3}, nil
}
