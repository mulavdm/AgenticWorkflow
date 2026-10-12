package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/cognitive-orchestrator/internal/types"
)

func TestParseTargetRole(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   types.Role
	}{
		{name: "planner tag", prompt: "@cloud @planner draft a plan", want: types.RolePlanner},
		{name: "coder alias", prompt: "@writer update docs", want: types.RoleCoder},
		{name: "no role tag", prompt: "@cloud run default workflow", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseTargetRole(tt.prompt); got != tt.want {
				t.Fatalf("parseTargetRole() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDoctorReportWarnsOnWorkspaceAbsoluteModelPath(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "AgenticWorkflow", "ArtifactFlow", "models", "Gemma-4-E4B")
	if err := ensureWritableDir(modelDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMACPP_MODEL_PATH", modelDir)
	t.Setenv("OPENAI_MODEL", "Gemma-4-E4B-Q8_K_P.gguf")
	t.Setenv("OPENAI_URL", "http://127.0.0.1:8081/v1/chat/completions")
	t.Setenv("CODINGAGENTS_DEV_WORKSPACE_FALLBACK", "")

	report := buildDoctorReport("", filepath.Join(t.TempDir(), "db.sqlite"), filepath.Join(t.TempDir(), "agents_config.json"))

	if !report.OK {
		t.Fatalf("workspace path warning should not fail doctor: %#v", report)
	}
	if !hasDoctorWarning(report, "llamacpp_model_path_workspace") {
		t.Fatalf("expected workspace model path warning, got %#v", report.Checks)
	}
	if len(report.RepairHints) == 0 {
		t.Fatalf("expected repair hint for workspace path")
	}
}

func TestDoctorReportSuppressesWorkspaceWarningWhenDevFallbackEnabled(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "AgenticWorkflow", "ArtifactFlow", "models", "Gemma-4-E4B")
	if err := ensureWritableDir(modelDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMACPP_MODEL_PATH", modelDir)
	t.Setenv("OPENAI_MODEL", "Gemma-4-E4B-Q8_K_P.gguf")
	t.Setenv("OPENAI_URL", "http://127.0.0.1:8081/v1/chat/completions")
	t.Setenv("CODINGAGENTS_DEV_WORKSPACE_FALLBACK", "1")

	report := buildDoctorReport("", filepath.Join(t.TempDir(), "db.sqlite"), filepath.Join(t.TempDir(), "agents_config.json"))

	if hasDoctorWarning(report, "llamacpp_model_path_workspace") {
		t.Fatalf("did not expect workspace warning when dev fallback is acknowledged: %#v", report.Checks)
	}
}

func TestDoctorReportWarnsWhenGGUFEndpointIsNotLocalLlamaCpp(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "Gemma-4-E4B-Q8_K_P.gguf")
	t.Setenv("OPENAI_URL", "https://api.openai.com/v1/chat/completions")

	report := buildDoctorReport("", filepath.Join(t.TempDir(), "db.sqlite"), filepath.Join(t.TempDir(), "agents_config.json"))

	if !hasDoctorWarning(report, "llamacpp_endpoint") {
		t.Fatalf("expected endpoint warning for GGUF model over remote URL, got %#v", report.Checks)
	}
}

func hasDoctorWarning(report doctorReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.Warning && !check.OK {
			return true
		}
	}
	return false
}

func TestServerOnly(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "disabled when unset", value: "", want: false},
		{name: "enabled by one", value: "1", want: true},
		{name: "enabled by true", value: "true", want: true},
		{name: "enabled by yes", value: " yes ", want: true},
		{name: "disabled by false", value: "false", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CODINGAGENTS_SERVER_ONLY", tt.value)
			if got := serverOnly(); got != tt.want {
				t.Fatalf("serverOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLocalOpenAICompatibleURL(t *testing.T) {
	for _, url := range []string{
		"http://127.0.0.1:8081/v1/chat/completions",
		"http://localhost:8081/v1/chat/completions",
	} {
		if !isLocalOpenAICompatibleURL(url) {
			t.Fatalf("expected local OpenAI-compatible URL: %s", url)
		}
	}
	for _, url := range []string{
		"https://api.openai.com/v1/chat/completions",
		"http://127.0.0.1:11434/api/chat",
		"",
	} {
		if isLocalOpenAICompatibleURL(url) {
			t.Fatalf("did not expect local llama.cpp URL: %s", url)
		}
	}
}

func TestLooksLikeWorkspaceAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AgenticWorkflow", "ArtifactFlow", "models")
	if !looksLikeWorkspaceAbsolutePath(path, filepath.Join("C:\\", "Users", "me", "AppData", "Local", "Programs", "CodingAgentsGO")) {
		t.Fatalf("expected workspace path to be detected: %s", path)
	}
	installPath := filepath.Join("C:\\", "Users", "me", "AppData", "Local", "Programs", "CodingAgentsGO", "llama-server.exe")
	if looksLikeWorkspaceAbsolutePath(installPath, filepath.Dir(installPath)) {
		t.Fatalf("installed path should not be treated as workspace path")
	}
	if strings.Contains(filepath.ToSlash(installPath), "/AgenticWorkflow/") {
		t.Fatalf("test fixture accidentally contains workspace marker")
	}
}
