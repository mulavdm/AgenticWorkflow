package types

// GlobalConfig stores system-wide defaults for providers and models.
type GlobalConfig struct {
	DefaultMode    string `json:"default_mode"`     // local, lmstudio, cloud
	OllamaModel    string `json:"ollama_model"`
	LMStudioModel  string `json:"lmstudio_model"`
	OpenAIModel    string `json:"openai_model"`
	OllamaURL      string `json:"ollama_url"`
	LMStudioURL    string `json:"lmstudio_url"`
	OpenAIURL      string `json:"openai_url"`
	OpenAIKey      string `json:"openai_key"`
}
