package types

// AgentConfig holds agent-specific settings like model name and system prompt.
type AgentConfig struct {
	Name          string   `json:"name"`
	Role          Role     `json:"role"`
	SystemPrompt  string   `json:"system_prompt"` // Combined result (Internal)
	Directive     string   `json:"directive"`     // Editable "Mission" part
	Constitution  string   `json:"constitution"`  // Read-only "DNA" part
	Model         string   `json:"model"`
	Temperature   float32  `json:"temperature"`
	MaxTokens     int      `json:"max_tokens"`
	ContextLength int      `json:"context_length"`
	Tools         []string `json:"tools"`
	// We keep these as interfaces to avoid circular dependencies
	Embedding EmbeddingProvider `json:"-"`
	Memory    VectorStore       `json:"-"`
	IsCustom  bool              `json:"is_custom"`
}
