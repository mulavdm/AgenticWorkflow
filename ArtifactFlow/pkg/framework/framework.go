package framework

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/ipc"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/models"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/processor"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/processors/core"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/sys"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/workflow"
	"github.com/user/CentralLogger"
)

type ArtifactFlowApp struct {
	Engine     *workflow.Engine
	Registry   *processor.Registry
	Models     *models.Registry
	Supervisor *sys.Supervisor

	ipcClient  *ipc.Client
	blackboard *state.Blackboard
}

// Init configures the standard framework, spawns the internal Rust compute sidecar,
// initializes the SQLite tracking DB, and returns the app.
func Init(dbPath string) (*ArtifactFlowApp, error) {
	app := &ArtifactFlowApp{
		Supervisor: sys.NewSupervisor(),
	}

	// Resolve centralized log directory relative to the SQLite dbPath
	dbDir := filepath.Dir(dbPath)
	logDir := filepath.Join(dbDir, "logs")
	logPath := filepath.Join(logDir, "artifactflow.log")

	// Initialize Logger
	if err := logger.InitLogger(logPath); err != nil {
		return nil, fmt.Errorf("failed to initialize logger at %s: %v", logPath, err)
	}

	// Set the centralized log directory for supervised sidecar logs
	logger.SetLogDir(logDir)

	// Prune old log files at startup to keep the directory under 100MB
	logger.PruneLogs(100 * 1024 * 1024)

	logger.Info("Framework", "Initializing ArtifactFlow Engine...")

	// 1. Spawn Internal Rust Sidecar
	if err := app.startSidecar(); err != nil {
		return nil, fmt.Errorf("failed to start internal Rust engine: %w", err)
	}

	// Note: Python Sidecar is no longer hardcoded here.
	// Downstream applications must call app.RegisterSidecar() to start their own custom ML environments.

	// 2. Connect IPC
	pipeName := `\\.\pipe\artifactflow_ipc`
	app.ipcClient = ipc.NewClient(pipeName)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("Framework", "Connecting to internal Rust engine via IPC...")
	if err := app.ipcClient.Connect(ctx, 5); err != nil {
		app.Close()
		return nil, fmt.Errorf("failed to connect to IPC bridge: %w", err)
	}

	// 3. Setup SQLite Blackboard
	var err error
	app.blackboard, err = state.NewBlackboard(dbPath)
	if err != nil {
		app.Close()
		return nil, fmt.Errorf("failed to initialize blackboard: %w", err)
	}

	// 4. Setup Registry and register built-in processors
	app.Registry = processor.NewRegistry()

	// Initialize local capability manager
	capManager := processor.NewLocalCapabilityManager()
	app.Registry.SetCapabilityManager(capManager)

	// 5. Initialize Model Registry
	app.Models = models.NewRegistry()
	app.Models.RegisterBackend(models.NewOllamaBackend())
	app.Models.RegisterBackend(models.NewLlamaCppBackend())
	app.Models.RegisterBackend(models.NewTransformersBackend())

	// Automatically hook into models.Registry to register llama.cpp model capabilities
	app.Models.OnRegister = func(m models.Model) {
		if m.Backend == "llama.cpp" && m.Format == "gguf" {
			repo := m.Repo
			logger.Info("Framework", fmt.Sprintf("Registering llama.cpp capability for model repo: %s", repo))
			capManager.Register("llama.cpp:"+repo, func(ctx context.Context) error {
				// If this is a non-embedding model, stop other started non-embedding llama.cpp sidecars to free VRAM
				if !strings.Contains(strings.ToLower(repo), "embedding") {
					capManager.StopOtherLlamaModels("llama.cpp:"+repo, func(name string) error {
						logger.Info("Framework", fmt.Sprintf("Stopping inactive LLM sidecar to free VRAM: %s", name))
						return app.Supervisor.StopSidecar(name)
					})
				}
				start := time.Now()
				if err := app.startLlamaCppSidecar(ctx, m.ID, repo); err != nil {
					return err
				}
				capName := "llama.cpp:" + repo
				details := processor.CapabilityState{
					StartupDuration: time.Since(start).String(),
					Metadata:        map[string]any{"model_id": m.ID, "repo": repo, "backend": m.Backend},
				}
				if app.blackboard != nil {
					var endpoint string
					if err := app.blackboard.LoadConfig("runtimes", capName, &endpoint); err == nil {
						details.EndpointURL = endpoint
					}
				}
				if status, ok := app.Supervisor.Status(capName); ok {
					details.PID = status.PID
					details.HealthURL = status.HealthURL
					details.LogPath = status.LogPath
					details.RuntimeArgs = status.Args
				}
				capManager.UpdateCapabilityDetails(capName, details)
				return nil
			})
		}
	}

	// Register built-in processors
	app.Registry.Register(processor.NewShellProcessor())
	app.Registry.Register(processor.NewExportMarkdownProcessor())
	app.Registry.Register(&core.ConstantProcessor{})
	app.Registry.Register(&core.SwitchProcessor{})

	// Register Standard Rust Proxy Processors
	app.Registry.Register(processor.NewRustProxyProcessor("video.extract_frames", app.ipcClient))
	// Add more here natively as they are added to Rust...

	// 6. Initialize Engine
	app.Engine = workflow.NewEngine(app.Registry, app.blackboard)

	logger.Info("Framework", "ArtifactFlow Engine is ready.")
	return app, nil
}

func (app *ArtifactFlowApp) startSidecar() error {
	exePath, _ := os.Executable()
	rustExe := filepath.Join(filepath.Dir(exePath), "rust-sidecar.exe")

	if _, err := os.Stat(rustExe); os.IsNotExist(err) {
		// Fallback for go run where bin is one directory up
		rustExe = filepath.Join("..", "bin", "rust-sidecar.exe")
		if _, err := os.Stat(rustExe); os.IsNotExist(err) {
			rustExe = filepath.Join("bin", "rust-sidecar.exe")
		}
	}

	if _, err := os.Stat(rustExe); os.IsNotExist(err) {
		logger.Warn("Framework", fmt.Sprintf("Internal rust-sidecar.exe not found at %s. Assuming it is running separately for dev.", rustExe))
		return nil
	}

	err := app.Supervisor.StartSidecar(context.Background(), sys.SidecarConfig{
		Name:    "rust-sidecar",
		BinPath: rustExe,
	})
	if err != nil {
		return err
	}

	logger.Info("Framework", "Spawned internal Rust engine (supervised via Job Object)")
	return nil
}

// RegisterSidecar launches a generic sidecar and waits for it to become healthy
func (app *ArtifactFlowApp) RegisterSidecar(name string, dir string, command []string, healthUrl string) error {
	if len(command) == 0 {
		return fmt.Errorf("invalid command for sidecar %s", name)
	}

	err := app.Supervisor.StartSidecar(context.Background(), sys.SidecarConfig{
		Name:      name,
		BinPath:   command[0],
		Args:      command[1:],
		Dir:       dir,
		HealthURL: healthUrl,
	})
	if err != nil {
		return err
	}

	logger.Info("Framework", fmt.Sprintf("Spawned %s sidecar (supervised via Job Object)", name))
	return nil
}

func (app *ArtifactFlowApp) startLlamaCppSidecar(ctx context.Context, modelName string, repo string) error {
	logger.Info("Framework", fmt.Sprintf("Spawning llama.cpp sidecar for model: %s (Repo: %s)...", modelName, repo))

	// 1. Find free port
	port, err := findFreePort()
	if err != nil {
		return fmt.Errorf("failed to find a free port: %w", err)
	}

	// 2. Resolve model path
	modelPath := resolveGGUFModelPath(repo)

	if modelPath == "" || !fileExists(modelPath) {
		return fmt.Errorf("GGUF model file %q not found (repo: %q). Please place it in the models/ directory", repo, repo)
	}

	// 3. Resolve llama-server.exe binary path
	binPath := os.Getenv("LLAMACPP_BIN_PATH")
	if binPath == "" {
		var exeDir string
		if execPath, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(execPath)
		}

		candidates := []string{
			filepath.Join("bin", "llama.cpp", "build-gfx1101-vs2022", "bin", "llama-server.exe"),
			filepath.Join("bin", "llama.cpp", "bin", "llama-server.exe"),
			filepath.Join("..", "bin", "llama.cpp", "build-gfx1101-vs2022", "bin", "llama-server.exe"),
			filepath.Join("..", "ArtifactFlow", "bin", "llama.cpp", "build-gfx1101-vs2022", "bin", "llama-server.exe"),
			filepath.Join("..", "ArtifactFlow", "bin", "llama.cpp", "bin", "llama-server.exe"),
			"llama-server.exe",
		}
		if exeDir != "" {
			candidates = append(candidates, filepath.Join(exeDir, "llama-server.exe"))
		}

		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				binPath = c
				break
			}
		}
	}

	if binPath == "" || !fileExists(binPath) {
		return fmt.Errorf("llama-server.exe binary not found. Please ensure it is built or set LLAMACPP_BIN_PATH")
	}

	// Convert both modelPath and binPath to absolute paths to prevent resolution issues when setting cmd.Dir
	if abs, err := filepath.Abs(modelPath); err == nil {
		modelPath = abs
	}
	if abs, err := filepath.Abs(binPath); err == nil {
		binPath = abs
	}

	// 4. Resolve other configuration defaults
	ctxSize := os.Getenv("LLAMACPP_CTX_SIZE")
	if ctxSize == "" {
		ctxSize = "24576"
	}
	ngl := os.Getenv("LLAMACPP_NGL")
	if ngl == "" {
		ngl = "999"
	}
	slots := os.Getenv("LLAMACPP_PARALLEL")
	if slots == "" {
		slots = os.Getenv("LLAMACPP_SLOTS")
	}
	if slots == "" {
		slots = "1" // Single-slot by default
	}

	// 5. Build command line arguments
	args := []string{
		"-m", modelPath,
		"--port", fmt.Sprintf("%d", port),
		"-c", ctxSize,
		"--embeddings",
		"--pooling", "mean",
		"-ngl", ngl,
		"--parallel", slots,
	}

	// For reasoning models like Gemma, we often want to set block size and other parameters
	args = append(args, "-b", "8192", "-ub", "8192")

	// 6. Start llama-server sidecar using Supervisor
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	err = app.Supervisor.StartSidecar(ctx, sys.SidecarConfig{
		Name:      "llama.cpp:" + repo,
		BinPath:   binPath,
		Args:      args,
		Dir:       filepath.Dir(binPath),
		HealthURL: healthURL,
	})
	if err != nil {
		return fmt.Errorf("failed to start llama-server for %s: %w", repo, err)
	}

	logger.Info("Framework", fmt.Sprintf("llama.cpp server for %s is healthy and ready on port %d (supervised via Job Object).", repo, port))

	// 7. Save active endpoint to blackboard configs
	endpointURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	if app.blackboard != nil {
		errSave := app.blackboard.SaveConfig("runtimes", "llama.cpp:"+repo, endpointURL)
		if errSave != nil {
			logger.Error("Framework", fmt.Sprintf("Failed to save runtime config to blackboard: %v", errSave))
		} else {
			logger.Info("Framework", fmt.Sprintf("Registered endpoint %s in blackboard configs", endpointURL))
		}
	}
	return nil
}

func findFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func resolveGGUFModelPath(repo string) string {
	modelPath := os.Getenv("LLAMACPP_MODEL_PATH")
	var searchDirs []string

	if modelPath == "" {
		candidates := []string{
			repo,
			filepath.Join("models", "Gemma-4-E4B", repo),
			filepath.Join("models", repo),
			filepath.Join("..", "models", repo),
			filepath.Join("..", "ArtifactFlow", "models", "Gemma-4-E4B", repo),
			filepath.Join("..", "ArtifactFlow", "models", repo),
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate
			}
		}
	} else if info, err := os.Stat(modelPath); err == nil {
		if !info.IsDir() {
			return modelPath
		}
		if direct := filepath.Join(modelPath, repo); fileExists(direct) {
			return direct
		}
		searchDirs = appendUniqueDir(searchDirs, modelPath)
		if parent := filepath.Dir(modelPath); parent != modelPath {
			searchDirs = appendUniqueDir(searchDirs, parent)
		}
	}

	appData := os.Getenv("APPDATA")
	if appData == "" {
		if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			appData = filepath.Join(userProfile, "AppData", "Roaming")
		}
	}
	if appData != "" {
		searchDirs = appendUniqueDir(searchDirs, filepath.Join(appData, "CodingAgentsGO", "models"))
	}

	searchDirs = appendUniqueDir(searchDirs, "models")
	searchDirs = appendUniqueDir(searchDirs, "../models")
	searchDirs = appendUniqueDir(searchDirs, "../ArtifactFlow/models")

	for _, searchDir := range searchDirs {
		found := findFileInTree(searchDir, repo)
		if found != "" {
			return found
		}
	}
	return ""
}

func appendUniqueDir(dirs []string, dir string) []string {
	if strings.TrimSpace(dir) == "" {
		return dirs
	}
	clean := filepath.Clean(dir)
	for _, existing := range dirs {
		if strings.EqualFold(filepath.Clean(existing), clean) {
			return dirs
		}
	}
	return append(dirs, clean)
}

func findFileInTree(root, name string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil || !fileExists(absRoot) {
		return ""
	}
	var found string
	_ = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !info.IsDir() && info.Name() == name {
			found = path
		}
		return nil
	})
	return found
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (app *ArtifactFlowApp) Blackboard() *state.Blackboard {
	return app.blackboard
}

func (app *ArtifactFlowApp) Close() {
	if app.ipcClient != nil {
		app.ipcClient.Close()
	}
	if app.Supervisor != nil {
		app.Supervisor.Close() // Automatically terminates all associated processes and closes Job Objects
	}
	if app.blackboard != nil {
		app.blackboard.Close()
	}
	logger.CloseLogger()
}
