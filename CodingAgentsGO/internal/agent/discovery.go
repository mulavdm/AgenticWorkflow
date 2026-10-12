package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type DiscoverableModel struct {
	ID       string `json:"id"`
	Provider string `json:"provider"` // "ollama", "lmstudio", "llamacpp"
	Type     string `json:"type"`     // "llm", "embedding"
	IsLoaded bool   `json:"is_loaded"`
}

func DiscoverModels() []DiscoverableModel {
	models := []DiscoverableModel{}

	// 1. Discover Ollama Models
	ollamaURL := os.Getenv("LOCAL_OLLAMA_URL")
	if ollamaURL != "" {
		oModels, _ := fetchOllamaModels(ollamaURL)
		models = append(models, oModels...)
	}

	// 2. Discover LM Studio Models
	lmStudioURL := os.Getenv("LMSTUDIO_URL")
	if lmStudioURL != "" {
		lModels, _ := fetchLMStudioModels(lmStudioURL)
		models = append(models, lModels...)
	}

	openAIURL := strings.ToLower(os.Getenv("OPENAI_URL"))
	openAIModel := os.Getenv("OPENAI_MODEL")
	if openAIModel != "" && strings.Contains(strings.ToLower(openAIModel), ".gguf") &&
		(strings.Contains(openAIURL, "127.0.0.1") || strings.Contains(openAIURL, "localhost") || strings.Contains(openAIURL, "::1")) {
		models = append(models, DiscoverableModel{
			ID:       openAIModel,
			Provider: "llamacpp",
			Type:     "llm",
			IsLoaded: false,
		})
	}

	return models
}

func fetchOllamaModels(baseURL string) ([]DiscoverableModel, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/api/tags", baseURL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	res := []DiscoverableModel{}
	for _, m := range data.Models {
		res = append(res, DiscoverableModel{ID: m.Name, Provider: "ollama", Type: "llm", IsLoaded: true})
	}
	return res, nil
}

func fetchLMStudioModels(baseURL string) ([]DiscoverableModel, error) {
	// Adjust URL to point to /api/v1/models (stripping /chat if present)
	apiBase := baseURL
	if idx := findV1(apiBase); idx != -1 {
		apiBase = apiBase[:idx+3] + "/models"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(apiBase)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Discovery] LM Studio returned status %d", resp.StatusCode)
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Read body for logging on failure
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Models []struct {
			Type            string   `json:"type"`
			Key             string   `json:"key"`
			SelectedVariant string   `json:"selected_variant"`
			Variants        []string `json:"variants"`
			LoadedInstances []struct {
				ID string `json:"id"`
			} `json:"loaded_instances"`
		} `json:"models"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		log.Printf("[Discovery] Parse failed: %v. Body: %s", err, string(body))
		return nil, err
	}

	res := []DiscoverableModel{}
	for _, m := range data.Models {
		// Prefer the specific selected variant if available
		id := m.SelectedVariant
		if id == "" && len(m.Variants) > 0 {
			id = m.Variants[0]
		}
		if id == "" {
			id = m.Key
		}

		if id != "" {
			res = append(res, DiscoverableModel{
				ID:       id,
				Provider: "lmstudio",
				Type:     m.Type,
				IsLoaded: len(m.LoadedInstances) > 0,
			})
		}
	}
	return res, nil
}

func LoadLMStudioModel(baseURL, modelID string) error {
	// Adjust URL to point to /api/v1/models/load
	apiBase := baseURL
	if idx := findV1(apiBase); idx != -1 {
		apiBase = apiBase[:idx+3] + "/models/load"
	} else {
		apiBase = strings.TrimSuffix(apiBase, "/") + "/api/v1/models/load"
	}

	reqBody := map[string]interface{}{
		"model": modelID,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	log.Printf("[Discovery] Attempting to load LM Studio model: %s...", modelID)
	resp, err := http.Post(apiBase, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("load failed (%d): %s", resp.StatusCode, string(body))
	}

	log.Printf("[Discovery] Model %s loaded successfully.", modelID)
	return nil
}

func UnloadLMStudioModel(baseURL, instanceID string) error {
	apiBase := baseURL
	if idx := findV1(apiBase); idx != -1 {
		apiBase = apiBase[:idx+3] + "/models/unload"
	} else {
		apiBase = strings.TrimSuffix(apiBase, "/") + "/api/v1/models/unload"
	}

	reqBody := map[string]interface{}{
		"instance_id": instanceID,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	log.Printf("[Discovery] Unloading model instance: %s...", instanceID)
	resp, err := http.Post(apiBase, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unload failed (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func DownloadLMStudioModel(baseURL, modelID string) error {
	apiBase := baseURL
	if idx := findV1(apiBase); idx != -1 {
		apiBase = apiBase[:idx+3] + "/models/download"
	} else {
		apiBase = strings.TrimSuffix(apiBase, "/") + "/api/v1/models/download"
	}

	reqBody := map[string]interface{}{
		"model": modelID,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	log.Printf("[Discovery] Requesting download for model: %s...", modelID)
	resp, err := http.Post(apiBase, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download request failed (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func findV1(url string) int {
	for i := 0; i < len(url)-3; i++ {
		if url[i:i+3] == "/v1" {
			return i
		}
	}
	return -1
}
