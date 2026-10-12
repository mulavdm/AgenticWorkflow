package types

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveAssetPath checks if a path exists relative to the current working directory.
// If not, it attempts to resolve it relative to the executable's directory.
func ResolveAssetPath(relPath string) string {
	if _, err := os.Stat(relPath); err == nil {
		return relPath
	}
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		targetPath := filepath.Join(execDir, relPath)
		if _, err := os.Stat(targetPath); err == nil {
			return targetPath
		}
	}
	return relPath
}

// LoadPrompt reads a system prompt or instruction from the prompts/ directory.
func LoadPrompt(name string) (string, error) {
	path := ResolveAssetPath(filepath.Join("prompts", name+".txt"))
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to load prompt %s: %w", name, err)
	}
	return string(content), nil
}

// LoadConstitution reads the agent-specific constitution.md from its internal package.
func LoadConstitution(agentName string) (string, error) {
	path := ResolveAssetPath(filepath.Join("internal", "agent", agentName, "constitution.md"))
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to load constitution for %s: %w", agentName, err)
	}
	return string(content), nil
}
