package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CodeRunner executes code snippets in a temporary directory or workspace.
type CodeRunner struct {
	BasePath string
}

func (c *CodeRunner) SetBasePath(path string) {
	c.BasePath = path
}

func (c *CodeRunner) Name() string {
	return "coderunner"
}

func (c *CodeRunner) Description() string {
	return "Executes a standalone code snippet (Go, Python, etc.). ARGS: {\"code\": \"...\", \"language\": \"go|python|js\"}. CRITICAL: 'code' must contain the FULL valid source code to run (including package main/func main for Go), NOT a package name or file path."
}

func (c *CodeRunner) Execute(ctx context.Context, input string) (string, error) {
	var data struct {
		Code        string `json:"code"`
		CodeToWrite string `json:"code_to_write"` // Fallback for hallucinating models
		Language    string `json:"language"`
	}

	// Try to unmarshal as JSON
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		// If not JSON, treat raw input as code and try to detect language
		data.Code = input
		data.Language = c.detectLanguage(input)
	} else if data.Language == "" {
		data.Language = c.detectLanguage(data.Code)
	}

	if data.Code == "" && data.CodeToWrite != "" {
		data.Code = data.CodeToWrite
	}

	code := data.Code
	// Clean up any potential markdown or escaping issues
	if strings.HasPrefix(code, "```") {
		// Strip markdown if the model included it
		lines := strings.Split(code, "\n")
		if len(lines) > 2 {
			code = strings.Join(lines[1:len(lines)-1], "\n")
			// If we stripped markdown, check if the language was specified in the block
			if strings.HasPrefix(lines[0], "```") {
				lang := strings.TrimPrefix(lines[0], "```")
				if lang != "" && data.Language == "" {
					data.Language = lang
				}
			}
		}
	}

	// 1. Determine execution directory
	execDir := "."
	if c.BasePath != "" {
		execDir = c.BasePath
	}

	// 2. Create a temporary directory OUTSIDE the project for the runner file
	// This prevents conflicts with go.mod or other files in the project root
	tempRunnerDir, err := os.MkdirTemp("", "shadow-runner-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp runner dir: %w", err)
	}
	defer os.RemoveAll(tempRunnerDir)

	var cmd *exec.Cmd
	var filename string

	switch strings.ToLower(data.Language) {
	case "go":
		filename = "main.go"
		tmpFile := filepath.Join(tempRunnerDir, filename)
		if err := os.WriteFile(tmpFile, []byte(code), 0644); err != nil {
			return "", fmt.Errorf("failed to write Go code: %w", err)
		}

		// Use absolute path to the temp file
		absTmpFile, _ := filepath.Abs(tmpFile)
		cmd = exec.CommandContext(ctx, "go", "run", absTmpFile)
		// Disable modules to ensure it runs as a single file if it's just a snippet
		cmd.Env = append(os.Environ(), "GO111MODULE=off")

	case "python", "py":
		filename = "script.py"
		tmpFile := filepath.Join(tempRunnerDir, filename)
		if err := os.WriteFile(tmpFile, []byte(code), 0644); err != nil {
			return "", fmt.Errorf("failed to write Python code: %w", err)
		}
		absTmpFile, _ := filepath.Abs(tmpFile)
		cmd = exec.CommandContext(ctx, "python", absTmpFile)

	case "javascript", "js", "node":
		filename = "script.js"
		tmpFile := filepath.Join(tempRunnerDir, filename)
		if err := os.WriteFile(tmpFile, []byte(code), 0644); err != nil {
			return "", fmt.Errorf("failed to write JS code: %w", err)
		}
		absTmpFile, _ := filepath.Abs(tmpFile)
		cmd = exec.CommandContext(ctx, "node", absTmpFile)

	default:
		return "", fmt.Errorf("unsupported language: %s", data.Language)
	}

	cmd.Dir = execDir

	// 3. Execute
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second) // Increased timeout for project-context runs
	defer cancel()

	output, err := cmd.CombinedOutput()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("code execution timed out after 10s")
		}
		return string(output), fmt.Errorf("execution failed: %w", err)
	}

	result := fmt.Sprintf("[PROOF] Executed: %s (Language: %s)\n[HOST]: %s\n\n[OUTPUT]\n%s", strings.Join(cmd.Args, " "), data.Language, os.Getenv("COMPUTERNAME"), string(output))
	return result, nil
}

func (c *CodeRunner) detectLanguage(code string) string {
	code = strings.TrimSpace(code)
	// Strong Go indicators (syntax based)
	if strings.Contains(code, "package ") || strings.Contains(code, "func ") {
		return "go"
	}
	if strings.Contains(code, ":=") || strings.Contains(code, "chan ") || strings.Contains(code, "go func(") {
		return "go"
	}

	// Python indicators (syntax based)
	if strings.Contains(code, "def ") || strings.Contains(code, "elif:") || strings.Contains(code, "import ") {
		// If it has 'def' and ':' it's likely python
		if strings.Contains(code, ":\n") || strings.Contains(code, ": ") {
			return "python"
		}
	}

	// JavaScript indicators
	if strings.Contains(code, "const ") || strings.Contains(code, "let ") || strings.Contains(code, "=>") {
		return "javascript"
	}

	// Default to Go for this repository
	return "go"
}
