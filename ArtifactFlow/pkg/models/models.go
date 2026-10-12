package models

import (
	"context"
	"fmt"
)

// Model represents a registered ML model and its execution requirements.
type Model struct {
	ID           string   `yaml:"id" json:"id"`
	Provider     string   `yaml:"provider" json:"provider"` // e.g. huggingface, local, ollama
	Repo         string   `yaml:"repo" json:"repo"`         // e.g. Qwen/Qwen2.5-VL-7B-Instruct
	Format       string   `yaml:"format" json:"format"`     // e.g. safetensors, gguf, onnx
	Backend      string   `yaml:"backend" json:"backend"`   // e.g. transformers, llama.cpp, ollama
	Capabilities []string `yaml:"capabilities" json:"capabilities"`
}

type GenerateRequest struct {
	ModelID string
	Prompt  string
	Images  []string // Base64 or URIs
	Audio   string   // URI
}

type GenerateResponse struct {
	Text string
}

// ModelBackend is an interface that abstracts away how a model is actually executed.
type ModelBackend interface {
	Name() string
	Supports(model Model) bool
	Generate(ctx context.Context, req GenerateRequest, model Model) (GenerateResponse, error)
}

// Registry stores configured models and registered backend executors.
type Registry struct {
	models     map[string]Model
	backends   map[string]ModelBackend
	OnRegister func(m Model)
}

// NewRegistry creates a new model registry.
func NewRegistry() *Registry {
	return &Registry{
		models:   make(map[string]Model),
		backends: make(map[string]ModelBackend),
	}
}

func (r *Registry) RegisterModel(m Model) {
	r.models[m.ID] = m
	if r.OnRegister != nil {
		r.OnRegister(m)
	}
}

func (r *Registry) GetModel(id string) (Model, bool) {
	m, ok := r.models[id]
	return m, ok
}

func (r *Registry) RegisterBackend(b ModelBackend) {
	r.backends[b.Name()] = b
}

func (r *Registry) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	model, ok := r.models[req.ModelID]
	if !ok {
		return GenerateResponse{}, fmt.Errorf("model %s not found in registry", req.ModelID)
	}

	backend, ok := r.backends[model.Backend]
	if !ok {
		return GenerateResponse{}, fmt.Errorf("backend %s required by model %s is not registered", model.Backend, req.ModelID)
	}

	if !backend.Supports(model) {
		return GenerateResponse{}, fmt.Errorf("backend %s does not support model %s (format: %s)", backend.Name(), model.ID, model.Format)
	}

	return backend.Generate(ctx, req, model)
}
