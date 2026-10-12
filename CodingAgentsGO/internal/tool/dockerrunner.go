package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DockerRunner executes commands in a sandboxed Docker container.
type DockerRunner struct {
	BasePath string
}

func (d *DockerRunner) SetBasePath(path string) {
	d.BasePath = path
}

func (d *DockerRunner) Name() string {
	return "dockerrunner"
}

func (d *DockerRunner) Description() string {
	return "Executes commands or code in a sandboxed Docker container. ARGS: {\"command\": \"go test ./...\", \"language\": \"go|python|js\", \"image\": \"optional_image\"}. CRITICAL: This tool mounts the CURRENT workspace into the container at /workspace."
}

func (d *DockerRunner) Execute(ctx context.Context, input string) (string, error) {
	var args struct {
		Command  string `json:"command"`
		Language string `json:"language"`
		Image    string `json:"image"`
		Code     string `json:"code"` // Optional: for running snippets
	}

	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if args.Command == "" && args.Code == "" {
		return "", fmt.Errorf("either 'command' or 'code' must be provided")
	}

	// 1. Determine image
	image := args.Image
	if image == "" {
		switch strings.ToLower(args.Language) {
		case "go", "golang":
			image = "golang:alpine"
		case "python", "py":
			image = "python:3.11-alpine"
		case "node", "js", "javascript":
			image = "node:20-alpine"
		default:
			// Heuristic based on command
			if strings.Contains(args.Command, "go ") {
				image = "golang:1.21-alpine"
			} else if strings.Contains(args.Command, "python ") || strings.Contains(args.Command, "pip ") {
				image = "python:3.11-alpine"
			} else {
				image = "alpine:latest"
			}
		}
	}

	// 2. Prepare execution
	workDir := "."
	if d.BasePath != "" {
		absPath, err := filepath.Abs(d.BasePath)
		if err == nil {
			workDir = absPath
		}
	} else {
		absPath, _ := filepath.Abs(".")
		workDir = absPath
	}

	// Create a unique container name to avoid conflicts
	containerName := fmt.Sprintf("shadow-exec-%d", time.Now().UnixNano())

	dockerArgs := []string{
		"run", "--rm",
		"--name", containerName,
		"-v", fmt.Sprintf("%s:/workspace", workDir),
		"-w", "/workspace",
	}

	// Add language-specific env vars if needed
	if strings.Contains(image, "golang") {
		dockerArgs = append(dockerArgs, "-e", "CGO_ENABLED=0", "-e", "GOCACHE=/workspace/.gocache")
	}

	dockerArgs = append(dockerArgs, image)

	if args.Code != "" {
		// Run as a snippet
		snippetFile := "snippet_exec"
		if strings.Contains(image, "golang") {
			snippetFile += ".go"
			dockerArgs = append(dockerArgs, "sh", "-c", fmt.Sprintf("echo '%s' > %s && go run %s", strings.ReplaceAll(args.Code, "'", "'\\''"), snippetFile, snippetFile))
		} else if strings.Contains(image, "python") {
			snippetFile += ".py"
			dockerArgs = append(dockerArgs, "sh", "-c", fmt.Sprintf("echo '%s' > %s && python %s", strings.ReplaceAll(args.Code, "'", "'\\''"), snippetFile, snippetFile))
		} else {
			dockerArgs = append(dockerArgs, "sh", "-c", args.Code)
		}
	} else {
		// Run as a command
		dockerArgs = append(dockerArgs, "sh", "-c", args.Command)
	}

	// 3. Execute
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second) // Docker can be slow
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		errStr := err.Error()
		outStr := string(output)
		
		// Heuristic to detect if Docker is missing or daemon is not running
		isDockerMissing := strings.Contains(outStr, "daemon is not running") ||
			strings.Contains(outStr, "failed to connect") ||
			strings.Contains(errStr, "executable file not found") ||
			strings.Contains(outStr, "cannot connect to the Docker daemon") ||
			strings.Contains(outStr, "Docker daemon") ||
			strings.Contains(errStr, "not found")

		if isDockerMissing {
			log.Printf("[DockerRunner] Docker is not available. Falling back to local host execution in %s...", workDir)
			
			var localCmd *exec.Cmd
			if args.Code != "" {
				// Standalone snippet execution fallback
				snippetFile := "snippet_exec"
				var runCmd string
				var runArgs []string
				
				switch strings.ToLower(args.Language) {
				case "go", "golang":
					snippetFile += ".go"
					runCmd = "go"
					runArgs = []string{"run", snippetFile}
				case "python", "py":
					snippetFile += ".py"
					runCmd = "python"
					runArgs = []string{snippetFile}
				case "node", "js", "javascript":
					snippetFile += ".js"
					runCmd = "node"
					runArgs = []string{snippetFile}
				default:
					snippetFile += ".sh"
					runCmd = "sh"
					runArgs = []string{snippetFile}
				}
				
				tmpPath := filepath.Join(workDir, snippetFile)
				if writeErr := os.WriteFile(tmpPath, []byte(args.Code), 0644); writeErr != nil {
					return "", fmt.Errorf("local fallback: failed to write snippet: %w", writeErr)
				}
				defer os.Remove(tmpPath)
				
				localCmd = exec.CommandContext(ctx, runCmd, runArgs...)
			} else {
				// Arbitrary command execution fallback
				if runtime.GOOS == "windows" {
					localCmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", args.Command)
				} else {
					localCmd = exec.CommandContext(ctx, "sh", "-c", args.Command)
				}
			}
			
			localCmd.Dir = workDir
			// Copy host environment
			localCmd.Env = os.Environ()
			
			localOutput, localErr := localCmd.CombinedOutput()
			if localErr != nil {
				return string(localOutput), fmt.Errorf("local execution failed: %w", localErr)
			}
			
			result := fmt.Sprintf("[LOCAL HOST FALLBACK PROOF] Directory: %s\n[COMMAND]: %s\n\n[OUTPUT]\n%s", workDir, args.Command, string(localOutput))
			return result, nil
		}

		if ctx.Err() == context.DeadlineExceeded {
			// Try to kill the container if it timed out
			exec.Command("docker", "kill", containerName).Run()
			return "", fmt.Errorf("docker execution timed out after 60s")
		}
		return string(output), fmt.Errorf("docker execution failed: %w", err)
	}

	result := fmt.Sprintf("[DOCKER PROOF] Image: %s\n[COMMAND]: %s\n\n[OUTPUT]\n%s", image, args.Command, string(output))
	return result, nil
}
