package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ShadowWorkspace manages a temporary copy of the project.
type ShadowWorkspace struct {
	BasePath   string // Original project path
	ShadowPath string // Path to the shadow copy
}

// NewShadowWorkspace creates a new shadow copy of the project.
func NewShadowWorkspace(originalPath string) (*ShadowWorkspace, error) {
	absOriginal, err := filepath.Abs(originalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Create a shadow directory in the OS temp folder or a specific .shadow folder
	// Let's use a .shadow_workspaces folder in the project root to keep it local but ignored
	shadowRoot := filepath.Join(absOriginal, ".shadow_workspaces")
	if err := os.MkdirAll(shadowRoot, 0755); err != nil {
		return nil, fmt.Errorf("failed to create shadow root: %w", err)
	}

	sessionID := fmt.Sprintf("shadow-%d", os.Getpid())
	shadowPath := filepath.Join(shadowRoot, sessionID)

	s := &ShadowWorkspace{
		BasePath:   absOriginal,
		ShadowPath: shadowPath,
	}

	if err := s.Initialize(); err != nil {
		return nil, err
	}

	return s, nil
}

// Initialize copies the project to the shadow path, excluding ignored files.
func (s *ShadowWorkspace) Initialize() error {
	// 1. Create the shadow directory
	if err := os.MkdirAll(s.ShadowPath, 0755); err != nil {
		return fmt.Errorf("failed to create shadow directory: %w", err)
	}

	// 2. Copy files
	return filepath.Walk(s.BasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path
		rel, err := filepath.Rel(s.BasePath, path)
		if err != nil {
			return err
		}

		// Skip the shadow root itself to avoid infinite recursion
		if rel == ".shadow_workspaces" || strings.HasPrefix(rel, ".shadow_workspaces"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip common ignored directories
		if info.IsDir() {
			if rel == ".git" || rel == "node_modules" || rel == "vendor" || rel == ".shadow_workspaces" {
				return filepath.SkipDir
			}
		}

		targetPath := filepath.Join(s.ShadowPath, rel)

		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		// Copy file
		return copyFile(path, targetPath)
	})
}

// Cleanup removes the shadow workspace.
func (s *ShadowWorkspace) Cleanup() error {
	return os.RemoveAll(s.ShadowPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}

	// Preserve permissions
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, si.Mode())
}
