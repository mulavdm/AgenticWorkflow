package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/framework"
	"github.com/user/CentralLogger"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/models"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/workflow"
	"github.com/VideoScriptor/internal/processors"
)

func main() {
	var wfPath string
	flag.StringVar(&wfPath, "workflow", "workflows/dense-script.yaml", "Path to workflow DAG")
	flag.Parse()

	fmt.Println("Starting VideoScriptor (Powered by ArtifactFlow Framework)...")

	// 1. Initialize the ArtifactFlow framework
	// This automatically spawns the internal Rust media sidecar and SQLite blackboard!
	app, err := framework.Init("videoscriptor.db")
	if err != nil {
		fmt.Printf("Fatal error initializing framework: %v\n", err)
		os.Exit(1)
	}
	defer app.Close()

	// 1.5. Explicitly register the VideoScriptor Python Sidecar
	// This was previously hardcoded in the ArtifactFlow framework
	sidecarDir := "../ArtifactFlow/python-sidecar"
	if err := app.RegisterSidecar("videoscriptor-python", sidecarDir, []string{"uv", "run", "uvicorn", "main:app", "--port", "8000"}, "http://127.0.0.1:8000/health"); err != nil {
		fmt.Printf("Warning: Python sidecar failed to start: %v\n", err)
	}

	// 2. Register local models to the ModelRegistry
	app.Models.RegisterModel(models.Model{
		ID:           "whisper_model",
		Provider:     "local",
		Repo:         "whisper-large-v3",
		Format:       "gguf",
		Backend:      "llama.cpp",
		Capabilities: []string{"audio.transcribe"},
	})
	app.Models.RegisterModel(models.Model{
		ID:           "vision_model",
		Provider:     "huggingface",
		Repo:         "Salesforce/blip-image-captioning-base",
		Format:       "safetensors",
		Backend:      "transformers",
		Capabilities: []string{"image.describe"},
	})
	app.Models.RegisterModel(models.Model{
		ID:           "text_model",
		Provider:     "ollama",
		Repo:         "llama3",
		Format:       "gguf",
		Backend:      "ollama",
		Capabilities: []string{"text.generate"},
	})

	// 3. Register custom Go plugins for LLM tasks
	app.Registry.Register(&processors.MockVideoProcessor{})
	app.Registry.Register(processors.NewTranscribeProcessor(app.Models))
	app.Registry.Register(processors.NewVisionCaptionProcessor(app.Models))
	app.Registry.Register(processors.NewScriptMergeProcessor(app.Models))

	// 4. Load Workflow
	wf, err := workflow.LoadFromFile(wfPath)
	if err != nil {
		logger.Error("VideoScriptor", fmt.Sprintf("Failed to load workflow %s: %v", wfPath, err))
		os.Exit(1)
	}

	// 5. Run Workflow
	runID := "video-run-python-sidecar"
	logger.Info("VideoScriptor", "Executing DAG...")
	if err := app.Engine.Run(context.Background(), wf, runID); err != nil {
		logger.Error("VideoScriptor", fmt.Sprintf("Execution failed: %v", err))
		os.Exit(1)
	}

	logger.Info("VideoScriptor", "Successfully generated screenplay!")
}
