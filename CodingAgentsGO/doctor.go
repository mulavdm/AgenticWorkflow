package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type doctorReport struct {
	OK          bool          `json:"ok"`
	Version     string        `json:"version"`
	EnvPath     string        `json:"env_path"`
	DBPath      string        `json:"db_path"`
	ConfigPath  string        `json:"config_path"`
	Checks      []doctorCheck `json:"checks"`
	RepairHints []string      `json:"repair_hints,omitempty"`
}

type doctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
	Warning bool   `json:"warning,omitempty"`
}

func doctorRequested(args []string) bool {
	if len(args) < 2 {
		return false
	}
	cmd := strings.ToLower(strings.TrimSpace(args[1]))
	return cmd == "doctor" || cmd == "healthcheck"
}

func doctorJSONRequested(args []string) bool {
	for _, arg := range args[2:] {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "--json", "-json", "json":
			return true
		}
	}
	return false
}

func runDoctor(envPath, dbPath, configPath string, jsonOutput bool) int {
	report := buildDoctorReport(envPath, dbPath, configPath)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		printDoctorReport(report)
	}
	if !report.OK {
		return 1
	}
	return 0
}

func buildDoctorReport(envPath, dbPath, configPath string) doctorReport {
	report := doctorReport{
		OK:         true,
		Version:    buildVersion,
		EnvPath:    envPath,
		DBPath:     dbPath,
		ConfigPath: configPath,
	}
	add := func(name string, ok bool, detail string, warning bool) {
		report.Checks = append(report.Checks, doctorCheck{Name: name, OK: ok, Detail: detail, Warning: warning})
		if !ok && !warning {
			report.OK = false
		}
	}
	assetCheck := func(name, rel string) {
		path := types.ResolveAssetPath(rel)
		if _, err := os.Stat(path); err == nil {
			add(name, true, path, false)
			return
		}
		add(name, false, fmt.Sprintf("missing %s (resolved as %s)", rel, path), false)
		report.RepairHints = append(report.RepairHints, "Re-run install.ps1 from the workspace root or verify the deployed prompts/web/internal folders.")
	}

	if envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			add("env_file", true, envPath, false)
		} else {
			add("env_file", false, fmt.Sprintf("%s not found; process environment will be used", envPath), true)
		}
	}
	if dir := filepath.Dir(dbPath); ensureWritableDir(dir) == nil {
		add("database_directory", true, dir, false)
	} else {
		add("database_directory", false, fmt.Sprintf("%s is not writable", dir), false)
		report.RepairHints = append(report.RepairHints, "Check AppData permissions or set CODINGAGENTS_LOCAL_DB=1 for project-local state.")
	}
	if dir := filepath.Dir(configPath); ensureWritableDir(dir) == nil {
		add("config_directory", true, dir, false)
	} else {
		add("config_directory", false, fmt.Sprintf("%s is not writable", dir), false)
	}

	assetCheck("web_assets", "web")
	assetCheck("prompts", "prompts")
	assetCheck("agent_constitutions", filepath.Join("internal", "agent"))

	checkBinary := func(name, envName, fallback string, required bool) {
		path := os.Getenv(envName)
		if path == "" {
			path = fallback
		}
		if path == "" {
			add(name, !required, fmt.Sprintf("%s is not configured", envName), !required)
			return
		}
		if _, err := os.Stat(path); err == nil {
			add(name, true, path, false)
			return
		}
		add(name, false, fmt.Sprintf("%s not found at %s", name, path), !required)
		if required {
			report.RepairHints = append(report.RepairHints, fmt.Sprintf("Set %s or reinstall the missing runtime binary.", envName))
		}
	}
	exeDir := ""
	if exePath, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exePath)
	}
	checkBinary("rust_sidecar", "RUST_SIDECAR_PATH", filepath.Join(exeDir, "rust-sidecar.exe"), false)
	checkBinary("llama_server", "LLAMACPP_BIN_PATH", filepath.Join(exeDir, "llama-server.exe"), false)
	addRuntimePathChecks(&report, add, exeDir)

	if portAvailable("127.0.0.1:8080") {
		add("dashboard_port", true, "127.0.0.1:8080 is available", false)
	} else {
		add("dashboard_port", false, "127.0.0.1:8080 is already in use", true)
	}

	return report
}

func addRuntimePathChecks(report *doctorReport, add func(string, bool, string, bool), exeDir string) {
	modelPath := os.Getenv("LLAMACPP_MODEL_PATH")
	if modelPath == "" {
		add("llamacpp_model_path", true, "LLAMACPP_MODEL_PATH is not set; ArtifactFlow will search standard model directories", true)
	} else if _, err := os.Stat(modelPath); err == nil {
		add("llamacpp_model_path", true, modelPath, false)
		warnWorkspaceRuntimePath(report, add, "llamacpp_model_path_workspace", "LLAMACPP_MODEL_PATH", modelPath, exeDir)
	} else {
		add("llamacpp_model_path", false, fmt.Sprintf("LLAMACPP_MODEL_PATH does not exist: %s", modelPath), true)
		report.RepairHints = append(report.RepairHints, "Set LLAMACPP_MODEL_PATH to a directory containing your GGUF files, or place models under %APPDATA%\\CodingAgentsGO\\models.")
	}

	binPath := os.Getenv("LLAMACPP_BIN_PATH")
	if binPath != "" {
		warnWorkspaceRuntimePath(report, add, "llamacpp_bin_path_workspace", "LLAMACPP_BIN_PATH", binPath, exeDir)
	}

	model := os.Getenv("OPENAI_MODEL")
	openAIURL := os.Getenv("OPENAI_URL")
	if strings.Contains(strings.ToLower(model), ".gguf") {
		if isLocalOpenAICompatibleURL(openAIURL) {
			add("llamacpp_endpoint", true, openAIURL, false)
		} else {
			add("llamacpp_endpoint", false, fmt.Sprintf("GGUF model %s should use a local OpenAI-compatible llama.cpp endpoint, got %s", model, openAIURL), true)
			report.RepairHints = append(report.RepairHints, "For local llama.cpp, set OPENAI_URL to http://127.0.0.1:<port>/v1/chat/completions or let ArtifactFlow register a dynamic endpoint for @llamacpp runs.")
		}
	}
}

func warnWorkspaceRuntimePath(report *doctorReport, add func(string, bool, string, bool), name, envName, path, exeDir string) {
	if devWorkspaceFallbackEnabled() || !looksLikeWorkspaceAbsolutePath(path, exeDir) {
		return
	}
	add(name, false, fmt.Sprintf("%s points at a workspace path: %s", envName, path), true)
	report.RepairHints = append(report.RepairHints, fmt.Sprintf("Move the referenced %s assets to an install/AppData model directory or set CODINGAGENTS_DEV_WORKSPACE_FALLBACK=1 to acknowledge development-mode workspace dependencies.", envName))
}

func looksLikeWorkspaceAbsolutePath(path, exeDir string) bool {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	if exeDir != "" {
		if rel, err := filepath.Rel(exeDir, absPath); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return false
		}
	}
	lower := strings.ToLower(filepath.ToSlash(absPath))
	return strings.Contains(lower, "/agenticworkflow/") ||
		strings.Contains(lower, "/artifactflow/") ||
		strings.Contains(lower, "/codingagentsgo/")
}

func isLocalOpenAICompatibleURL(rawURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	if !strings.Contains(lower, "/v1/chat/completions") {
		return false
	}
	return strings.Contains(lower, "127.0.0.1") || strings.Contains(lower, "localhost") || strings.Contains(lower, "[::1]")
}

func ensureWritableDir(dir string) error {
	if dir == "" || dir == "." {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

func portAvailable(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func printDoctorReport(report doctorReport) {
	status := "OK"
	if !report.OK {
		status = "FAILED"
	}
	fmt.Printf("CodingAgentsGO doctor: %s (version %s)\n", status, report.Version)
	fmt.Printf("  env:    %s\n", report.EnvPath)
	fmt.Printf("  db:     %s\n", report.DBPath)
	fmt.Printf("  config: %s\n\n", report.ConfigPath)
	for _, check := range report.Checks {
		marker := "OK"
		if !check.OK && check.Warning {
			marker = "WARN"
		} else if !check.OK {
			marker = "FAIL"
		}
		fmt.Printf("[%s] %s", marker, check.Name)
		if check.Detail != "" {
			fmt.Printf(" - %s", check.Detail)
		}
		fmt.Println()
	}
	if len(report.RepairHints) > 0 {
		fmt.Println("\nRepair hints:")
		for _, hint := range report.RepairHints {
			fmt.Printf("- %s\n", hint)
		}
	}
}
