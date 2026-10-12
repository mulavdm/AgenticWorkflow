package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/framework"
	"github.com/user/CentralLogger"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/workflow"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "Path to config file")
	flag.Parse()

	// Initialize the ArtifactFlow framework (starts engine, sidecar, SQLite tracking)
	app, err := framework.Init("blackboard.db")
	if err != nil {
		fmt.Printf("Fatal error initializing framework: %v\n", err)
		os.Exit(1)
	}
	defer app.Close()

	// Load Workflow
	wfPath := "workflows/hello_world.yaml"
	if len(flag.Args()) > 0 {
		wfPath = flag.Args()[0]
	}

	wf, err := workflow.LoadFromFile(wfPath)
	if err != nil {
		logger.Error("Workflow", fmt.Sprintf("Failed to load workflow %s: %v", wfPath, err))
		os.Exit(1)
	}

	// Run Workflow
	runID := fmt.Sprintf("run-%d", time.Now().Unix())
	if err := app.Engine.Run(context.Background(), wf, runID); err != nil {
		logger.Error("Workflow", fmt.Sprintf("Execution failed: %v", err))
	} else {
		logger.Info("Workflow", "Execution succeeded!")
	}

	// Wait for interrupt
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	logger.Info("System", "Shutting down gracefully...")
}
