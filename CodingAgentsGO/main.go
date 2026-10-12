package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	afartifact "github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/framework"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/models"
	"github.com/joho/godotenv"
	"github.com/user/CentralLogger"
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/agent/analyst"
	"github.com/user/cognitive-orchestrator/internal/agent/coder"
	"github.com/user/cognitive-orchestrator/internal/agent/critic"
	"github.com/user/cognitive-orchestrator/internal/agent/planner"
	"github.com/user/cognitive-orchestrator/internal/agent/security"
	"github.com/user/cognitive-orchestrator/internal/agent/summarizer"
	"github.com/user/cognitive-orchestrator/internal/agent/synthesizer"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/orchestrator"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
	"github.com/user/cognitive-orchestrator/internal/ui"
)

var buildVersion = "dev"

func parseTargetRole(prompt string) types.Role {
	roleTags := map[string]types.Role{
		"@analyst":     types.RoleContext,
		"@context":     types.RoleContext,
		"@planner":     types.RolePlanner,
		"@critic":      types.RoleCritic,
		"@redteam":     types.RoleRedTeamer,
		"@redteamer":   types.RoleRedTeamer,
		"@security":    types.RoleRedTeamer,
		"@coder":       types.RoleCoder,
		"@writer":      types.RoleCoder,
		"@synthesizer": types.RoleSynthesizer,
		"@synth":       types.RoleSynthesizer,
	}
	for tag, role := range roleTags {
		if strings.Contains(prompt, tag) {
			return role
		}
	}
	return ""
}

func main() {
	isDoctor := doctorRequested(os.Args)
	// Redirect Go's standard library log output to ArtifactFlow's centralized logger
	log.SetOutput(logger.NewStdLogWriter("CodingAgents"))
	log.SetFlags(0) // Disable standard log timestamps, as our structured logger manages them

	// Load environment variables from .env file
	envPath := ".env"
	if _, err := os.Stat(envPath); err != nil {
		if execPath, err := os.Executable(); err == nil {
			execDir := filepath.Dir(execPath)
			targetEnvPath := filepath.Join(execDir, ".env")
			if _, err := os.Stat(targetEnvPath); err == nil {
				envPath = targetEnvPath
			}
		}
	}
	if err := godotenv.Load(envPath); err != nil {
		if !isDoctor {
			log.Printf("No .env file found at %s, relying on system environment variables", envPath)
		}
	} else {
		if !isDoctor {
			log.Printf("[Main] Loaded environment from: %s", envPath)
		}
	}

	// 1. Resolve SQLite database and config paths
	dbPath := "artifactflow_agents.db" // default local
	configPath := "agents_config.json" // default local
	if os.Getenv("CODINGAGENTS_LOCAL_DB") != "1" {
		appDataDir := os.Getenv("APPDATA")
		if appDataDir == "" {
			appDataDir = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		globalDBDir := filepath.Join(appDataDir, "CodingAgentsGO")
		_ = os.MkdirAll(globalDBDir, 0755)
		dbPath = filepath.Join(globalDBDir, "artifactflow_agents.db")
		configPath = filepath.Join(globalDBDir, "agents_config.json")
		if !isDoctor {
			log.Printf("[Main] Using unified global database: %s", dbPath)
			log.Printf("[Main] Using unified global config: %s", configPath)
		}
	} else {
		if !isDoctor {
			log.Println("[Main] Using local database in working directory")
		}
	}

	if isDoctor {
		os.Exit(runDoctor(envPath, dbPath, configPath, doctorJSONRequested(os.Args)))
	}

	// Initialize the ArtifactFlow framework (starts engine, sidecar, SQLite blackboard)
	app, err := framework.Init(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize ArtifactFlow framework: %v", err)
	}
	defer app.Close()

	board := app.Blackboard()
	mem := memory.NewArtifactFlowStore(board)
	defer mem.Close()

	// 3. Initialize Tool Registry
	tools := tool.NewRegistry()
	tools.Register(&tool.CodeRunner{})
	tools.Register(&tool.FileExplorer{})
	tools.Register(&tool.FileWriter{})
	tools.Register(&tool.DockerRunner{})

	// 4. Initialize Event Hub and Sinks
	hub := ui.NewEventHub()
	hub.AddSink(&ui.ConsoleSink{})
	hub.AddSink(ui.NewJSONSink("logs"))
	hub.AddSink(ui.NewMarkdownSink(fmt.Sprintf("logs/session_%s.md", time.Now().Format("20060102_150405"))))

	sse := ui.NewSSESink()
	hub.AddSink(sse)

	// 5. Create Coordinator with a Hot-Swap Reloader
	var coord *orchestrator.Coordinator

	// Default configuration loader
	getAgentConfig := func(prompt string, global types.GlobalConfig) (string, string, string) {
		// 1. Determine Mode (Tag > Global Default)
		mode := global.DefaultMode
		if mode == "" {
			mode = "local"
		}

		if strings.Contains(prompt, "@cloud") || strings.Contains(prompt, "@llamacpp") {
			mode = "cloud"
		} else if strings.Contains(prompt, "@lmstudio") {
			mode = "lmstudio"
		} else if strings.Contains(prompt, "@local") {
			mode = "local"
		}

		// Handle @provider:model format
		re := regexp.MustCompile(`@(lmstudio|local|cloud|llamacpp):([^\s]+)`)
		matches := re.FindStringSubmatch(prompt)
		if len(matches) > 2 {
			provider := matches[1]
			model := matches[2]
			switch provider {
			case "lmstudio":
				return model, global.LMStudioURL, ""
			case "local":
				return model, global.OllamaURL, ""
			case "cloud":
				return model, global.OpenAIURL, global.OpenAIKey
			case "llamacpp":
				return model, global.OpenAIURL, global.OpenAIKey
			}
		}

		// 2. Return config based on mode
		switch mode {
		case "cloud":
			return global.OpenAIModel, global.OpenAIURL, global.OpenAIKey
		case "llamacpp":
			return global.OpenAIModel, global.OpenAIURL, global.OpenAIKey
		case "lmstudio":
			return global.LMStudioModel, global.LMStudioURL, ""
		default: // local
			return global.OllamaModel, global.OllamaURL, ""
		}
	}

	reloader := func(prompt string) {
		m, u, k := getAgentConfig(prompt, coord.GlobalConfig)

		// If using a local GGUF model, register it in the ArtifactFlow models registry to trigger sidecar capability mapping
		if strings.Contains(strings.ToLower(m), ".gguf") {
			app.Models.RegisterModel(models.Model{
				ID:           m,
				Provider:     "local",
				Repo:         m,
				Format:       "gguf",
				Backend:      "llama.cpp",
				Capabilities: []string{"text.generate"},
			})
		}

		// Register the dedicated embedding model in the registry to trigger sidecar capability mapping
		embModelName := "Qwen3-Embedding-0.6B-f16.gguf"
		app.Models.RegisterModel(models.Model{
			ID:           embModelName,
			Provider:     "local",
			Repo:         embModelName,
			Format:       "gguf",
			Backend:      "llama.cpp",
			Capabilities: []string{"text.embedding"},
		})

		// If using a local GGUF model, check if a dynamic server endpoint is registered in the blackboard
		if strings.Contains(strings.ToLower(m), ".gguf") {
			var dbEndpoint string
			if boardErr := board.LoadConfig("runtimes", "llama.cpp:"+m, &dbEndpoint); boardErr == nil && dbEndpoint != "" {
				log.Printf("[Orchestrator] Discovered dynamic endpoint for GGUF model %s: %s", m, dbEndpoint)
				u = dbEndpoint + "/chat/completions"
			}
		}

		provider := "local"
		if strings.Contains(u, ":1234") {
			provider = "lmstudio"
		} else if strings.Contains(u, "openai") || strings.Contains(u, "api.openai") {
			provider = "cloud"
		} else if strings.Contains(prompt, "@llamacpp") || (strings.Contains(strings.ToLower(m), ".gguf") && strings.Contains(u, "/v1/")) {
			provider = "llamacpp"
		} else if strings.Contains(prompt, "@cloud") {
			provider = "cloud"
		}
		fmt.Printf("[RUNTIME] Model: %s | Provider: %s | Endpoint: %s\n", m, provider, u)

		// 0. Auto-Load LLM Model if using LM Studio
		allModels := agent.DiscoverModels()
		if strings.Contains(u, ":1234") {
			found := false
			for _, mod := range allModels {
				if mod.ID == m {
					found = true
					if !mod.IsLoaded {
						log.Printf("[Orchestrator] Target LLM model %s is not loaded. Triggering auto-load...", m)
						go agent.LoadLMStudioModel(u, m)
					}
					break
				}
			}
			if !found && m != "" && m != "local-model" {
				log.Printf("[Orchestrator] Target model %s not found on disk. Triggering auto-download...", m)
				go agent.DownloadLMStudioModel(u, m)
			}
		}

		// 1. Initialize Embedding Provider (Dynamic Discovery)
		var emb types.EmbeddingProvider

		if strings.Contains(u, ":1234") {
			// Find first LOADED LM Studio embedding model
			embModel := ""
			for _, mod := range allModels {
				if mod.Provider == "lmstudio" && mod.Type == "embedding" && mod.IsLoaded {
					embModel = mod.ID
					break
				}
			}
			// Fallback: Pick first available and TRIGGER LOAD
			if embModel == "" {
				for _, mod := range allModels {
					if mod.Provider == "lmstudio" && mod.Type == "embedding" {
						embModel = mod.ID
						log.Printf("[Orchestrator] Embedding model %s is not loaded. Triggering auto-load...", mod.ID)
						go agent.LoadLMStudioModel(u, mod.ID)
						break
					}
				}
			}
			if embModel == "" {
				embModel = "text-embedding-nomic-embed-text-v1.5-embedding" // Ultimate default
			}
			emb = &agent.LMStudioEmbeddingProvider{URL: u, Model: embModel}
		} else if strings.Contains(u, "ollama") || strings.Contains(u, ":11434") {
			// Find first Ollama embedding model
			embModel := "nomic-embed-text" // Default
			for _, m := range allModels {
				if m.Provider == "ollama" && (strings.Contains(m.ID, "embed") || m.Type == "embedding") {
					embModel = m.ID
					break
				}
			}
			emb = &agent.OllamaEmbeddingProvider{URL: u, Model: embModel}
		} else {
			// Fallback for OpenAI-compatible dynamic local sidecars (like llama-server) or custom endpoints
			embURL := u
			embModel := m

			// If the dedicated embedding model is registered, try to spawn it and use it
			if _, ok := app.Models.GetModel(embModelName); ok {
				capKey := "llama.cpp:" + embModelName
				log.Printf("[Orchestrator] Ensuring dedicated embedding sidecar %s is active...", capKey)

				dp := &dynamicCapabilityProvider{
					caps: []string{capKey},
				}

				// Ensure capability is active (spawns sidecar if not running) with a timeout to prevent infinite hang
				startupCtx, startupCancel := context.WithTimeout(context.Background(), 60*time.Second)
				err := app.Registry.EnsureCapabilities(startupCtx, dp)
				startupCancel()
				if err == nil {
					var dbEndpoint string
					if boardErr := board.LoadConfig("runtimes", capKey, &dbEndpoint); boardErr == nil && dbEndpoint != "" {
						log.Printf("[Orchestrator] Dedicated embedding sidecar active at: %s", dbEndpoint)
						embURL = dbEndpoint + "/chat/completions"
						embModel = embModelName
					}
				} else {
					log.Printf("[Orchestrator] Failed to spawn dedicated embedding sidecar: %v. Falling back to LLM for embeddings.", err)
				}
			}

			emb = &agent.LMStudioEmbeddingProvider{URL: embURL, Model: embModel}
		}
		coord.Embedding = emb

		// 2. Get Vector Store
		vector := mem.GetVectorStore()

		// 3. Re-initialize Agents with Vector Memory (Skip Pinned)
		coord.Summarizer = summarizer.New(m, u, k)

		roles := []types.Role{
			types.RoleContext,
			types.RolePlanner,
			types.RoleCritic,
			types.RoleRedTeamer,
			types.RoleCoder,
			types.RoleSynthesizer,
		}

		for _, role := range roles {
			existing, ok := coord.Agents[role]
			if ok && existing.GetConfig().IsCustom {
				log.Printf("[Orchestrator] Skipping reload for %s (Pinned)", role)
				continue
			}

			// Initialize new agent based on role
			var newAgent engine.Agent
			switch role {
			case types.RoleContext:
				newAgent = analyst.New(m, u, k, emb, vector)
			case types.RolePlanner:
				newAgent = planner.New(m, u, k, emb, vector)
			case types.RoleCritic:
				newAgent = critic.New(m, u, k, emb, vector)
			case types.RoleRedTeamer:
				newAgent = security.New(m, u, k, emb, vector)
			case types.RoleCoder:
				newAgent = coder.New(m, u, k, emb, vector)
			case types.RoleSynthesizer:
				newAgent = synthesizer.New(m, u, k, emb, vector)
			}

			if newAgent != nil {
				coord.AddAgent(newAgent)
			}
		}

		if emb != nil && vector != nil {
			tools.Register(tool.NewMemoryRetriever(vector, emb))
			tools.Register(tool.NewCodebaseSearch(vector, emb))
			// Trigger codebase indexing now that the embedding provider is fully ready
			go coord.IndexCodebase(context.Background())
		}
	}

	// Initialize Global Config
	globalCfg := types.GlobalConfig{
		DefaultMode:   os.Getenv("ORCHESTRATOR_MODE"),
		OllamaModel:   os.Getenv("LOCAL_OLLAMA_MODEL"),
		LMStudioModel: os.Getenv("LMSTUDIO_MODEL"),
		OpenAIModel:   os.Getenv("OPENAI_MODEL"),
		OllamaURL:     os.Getenv("LOCAL_OLLAMA_URL"),
		LMStudioURL:   os.Getenv("LMSTUDIO_URL"),
		OpenAIURL:     os.Getenv("OPENAI_URL"),
		OpenAIKey:     os.Getenv("OPENAI_API_KEY"),
	}

	coord = orchestrator.NewCoordinator(mem, tools, hub, reloader)
	coord.SetArtifactFlow(app.Blackboard())
	coord.SetRegistry(app.Registry)
	coord.GlobalConfig = globalCfg
	coord.BuildVersion = buildVersion
	coord.ReloadAgents = reloader
	coord.ConfigPath = configPath

	// Initial Load from Persistence (Overwrites ENV if exists)
	coord.LoadState()

	// Initial load of agents
	reloader("")

	// 6. Initialize UI Server (now that coord is ready)
	uiServer := ui.NewServer(sse, mem, coord)
	go func() {
		if err := uiServer.Start("localhost:8080"); err != nil {
			log.Printf("UI Server error: %v", err)
		}
	}()

	if serverOnly() {
		fmt.Println("Dashboard: http://localhost:8080")
		fmt.Println("Server-only mode enabled. Press Ctrl+C to stop.")
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		return
	}

	// 7. Interactive Loop
	fmt.Printf("\n\033[1;36m============================================================\033[0m\n")
	fmt.Printf("\033[1;36m          COGNITIVE ORCHESTRATION FRAMEWORK (CLI)           \033[0m\n")
	fmt.Printf("\033[1;36m============================================================\033[0m\n")
	fmt.Printf("\033[1;33m[SINGLE SOURCE OF TRUTH]\033[0m\n")
	fmt.Printf("  - Web Dashboard: \033[1;34mhttp://localhost:8080\033[0m (running concurrently)\n")
	fmt.Printf("  - Blackboard DB: \033[1;32m%s\033[0m\n", dbPath)
	fmt.Printf("  * Both CLI and Web access the same SQLite database and engine state.\n\n")

	activeModel := ""
	activeURL := ""
	switch strings.ToLower(globalCfg.DefaultMode) {
	case "cloud":
		activeModel = globalCfg.OpenAIModel
		activeURL = globalCfg.OpenAIURL
	case "llamacpp":
		activeModel = globalCfg.OpenAIModel
		activeURL = globalCfg.OpenAIURL
	case "lmstudio":
		activeModel = globalCfg.LMStudioModel
		activeURL = globalCfg.LMStudioURL
	default:
		activeModel = globalCfg.OllamaModel
		activeURL = globalCfg.OllamaURL
	}

	fmt.Printf("\033[1;33m[ACTIVE RUNTIME CONFIG]\033[0m\n")
	fmt.Printf("  - Active Mode:   \033[1;32m%s\033[0m (via ORCHESTRATOR_MODE)\n", globalCfg.DefaultMode)
	fmt.Printf("  - Default Model: \033[1;32m%s\033[0m\n", activeModel)
	fmt.Printf("  - Endpoint URL:  \033[1;32m%s\033[0m\n\n", activeURL)

	fmt.Printf("\033[1;33m[TARGETED ROLES]\033[0m (Route directly to a specific agent phase)\n")
	fmt.Printf("  \033[1;32m@context\033[0m / \033[1;32m@analyst\033[0m  - Run the context gatherer/file mapper phase\n")
	fmt.Printf("  \033[1;32m@planner\033[0m             - Run the technical project planner phase\n")
	fmt.Printf("  \033[1;32m@critic\033[0m              - Run the design critique & code review phase\n")
	fmt.Printf("  \033[1;32m@security\033[0m / \033[1;32m@redteam\033[0m - Run the safety policies & red-team phase\n")
	fmt.Printf("  \033[1;32m@coder\033[0m               - Run the codebase writer & file patcher phase\n")
	fmt.Printf("  \033[1;32m@synthesizer\033[0m         - Run the final reporting & verification phase\n\n")

	fmt.Printf("\033[1;33m[PROVIDER OVERRIDES]\033[0m (Override active backend defaults)\n")
	fmt.Printf("  \033[1;32m@local\033[0m               - Force Ollama backend\n")
	fmt.Printf("  \033[1;32m@lmstudio\033[0m            - Force LM Studio backend\n")
	fmt.Printf("  \033[1;32m@cloud\033[0m               - Force Cloud (OpenAI-compatible) backend\n")
	fmt.Printf("  \033[1;32m@llamacpp\033[0m            - Force local llama.cpp OpenAI-compatible backend\n")
	fmt.Printf("  \033[1;32m@provider:model-id\033[0m   - Direct override, e.g., @llamacpp:Gemma-4-E4B-Q8_K_P.gguf\n\n")

	fmt.Printf("\033[1;33m[BEHAVIOR MODIFIERS]\033[0m (Combinable)\n")
	fmt.Printf("  \033[1;32m@shadow\033[0m              - Run in an isolated workspace and preview changes\n")
	fmt.Printf("  \033[1;32m@council\033[0m             - Enable multi-agent consensus swarm voting\n")
	fmt.Printf("  \033[1;32m@legacy\033[0m / \033[1;32m@linear\033[0m    - Disable shared workspace mode (use linear history)\n\n")

	// Discover models for the CLI header
	allModels := agent.DiscoverModels()

	printGroup := func(title, provider string) {
		fmt.Printf("\033[1;33m[%s]\033[0m\n", title)
		found := false
		for _, m := range allModels {
			if m.Provider == provider {
				fmt.Printf("  - %s\n", m.ID)
				found = true
			}
		}
		if !found {
			fmt.Printf("  (No local models found)\n")
		}
		fmt.Println()
	}

	printGroup("DISCOVERED OLLAMA MODELS (@local)", "ollama")
	printGroup("DISCOVERED LM STUDIO MODELS (@lmstudio)", "lmstudio")

	fmt.Printf("\033[1;36m============================================================\033[0m\n")
	fmt.Printf("Enter request (or 'exit' to quit):\n")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		userInput := scanner.Text()
		if userInput == "exit" {
			break
		}
		if userInput == "" {
			continue
		}

		if strings.Contains(userInput, "@shadow") {
			fmt.Printf("[SHADOW MODE] Execution isolated to .shadow_workspaces/\n")
		}

		targetRole := parseTargetRole(userInput)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)

		runID := "session-" + time.Now().Format("150405.000000")
		_, err := coord.ExecuteRun(ctx, runID, userInput, targetRole, nil)
		cancel()

		if err != nil {
			log.Printf("Execution failed: %v", err)
		}

		// Interactive Approval Loop
		for {
			run, err := coord.GetRun(runID)
			if err != nil {
				break
			}
			if run.Status != "paused" {
				break
			}

			approvals, err := coord.GetRunApprovals(runID)
			if err != nil {
				log.Printf("Failed to get approvals: %v", err)
				break
			}

			var pending []types.Approval
			for _, app := range approvals {
				if app.Status == types.ApprovalPending {
					pending = append(pending, app)
				}
			}

			if len(pending) == 0 {
				break
			}

			appr := pending[0]
			fmt.Printf("\n[APPROVAL REQUIRED]\n")
			fmt.Printf("  - Tool:   %s (requested by @%s)\n", appr.Tool, appr.Role)
			fmt.Printf("  - Args:   %s\n", appr.Args)
			if appr.Reason != "" {
				fmt.Printf("  - Reason: %s\n", appr.Reason)
			}

			fmt.Print("\nApprove this tool call? (y/n) [y]: ")
			responseScanner := bufio.NewScanner(os.Stdin)
			if !responseScanner.Scan() {
				break
			}
			response := strings.TrimSpace(strings.ToLower(responseScanner.Text()))

			if response == "n" || response == "no" {
				fmt.Print("Reason for denial (optional): ")
				reasonScanner := bufio.NewScanner(os.Stdin)
				denialReason := "Denied via CLI"
				if reasonScanner.Scan() {
					inputReason := strings.TrimSpace(reasonScanner.Text())
					if inputReason != "" {
						denialReason = inputReason
					}
				}
				fmt.Printf("Submitting DENIED decision...\n")
				err = coord.DecideApproval(runID, appr.ID, "denied", denialReason)
				if err != nil {
					log.Printf("Failed to submit denial: %v", err)
					break
				}
			} else {
				fmt.Printf("Submitting APPROVED decision...\n")
				err = coord.DecideApproval(runID, appr.ID, "approved", "Approved via CLI")
				if err != nil {
					log.Printf("Failed to submit approval: %v", err)
					break
				}
			}
		}

		fmt.Printf("\n--- Task Complete ---\n\n")
	}
}

func serverOnly() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CODINGAGENTS_SERVER_ONLY")))
	return value == "1" || value == "true" || value == "yes"
}

func devWorkspaceFallbackEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CODINGAGENTS_DEV_WORKSPACE_FALLBACK")))
	return value == "1" || value == "true" || value == "yes"
}

type dynamicCapabilityProvider struct {
	caps []string
}

func (d *dynamicCapabilityProvider) Name() string                   { return "dynamic.llama.embedding" }
func (d *dynamicCapabilityProvider) RequiredCapabilities() []string { return d.caps }
func (d *dynamicCapabilityProvider) Process(ctx context.Context, config map[string]any, inputs []afartifact.Artifact) ([]afartifact.Artifact, error) {
	return nil, nil
}
