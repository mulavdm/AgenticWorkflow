package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FileWriter writes content to a file.
type FileWriter struct {
	BasePath string
}

func (f *FileWriter) SetBasePath(path string) {
	f.BasePath = path
}

func (f *FileWriter) Name() string {
	return "writer"
}

func (f *FileWriter) Description() string {
	return "Modify files and directories. Actions: write (overwrite), append (add), patch (search/replace), patch_block (multi-line), fmt (gofmt), mkdir (create directory). ARGS: {\"action\": \"...\", \"path\": \"...\"}. CRITICAL: This tool is for MODIFICATION ONLY. It DOES NOT support 'read' or 'list' actions. Use the 'explorer' tool for reading files."
}

func (f *FileWriter) Execute(ctx context.Context, input string) (string, error) {
	// Pre-process input to handle unescaped control characters in JSON string literals
	input = f.sanitizeJSON(input)

	var args struct {
		Action      string   `json:"action"`
		Path        string   `json:"path"`
		Content     string   `json:"content"`     // Used for 'write' and 'append'
		Target      string   `json:"target"`      // Used for 'patch'
		Replacement string   `json:"replacement"` // Used for 'patch'
		Lines       []string `json:"lines"`       // Used for 'patch_block'
	}

	if err := json.Unmarshal([]byte(input), &args); err != nil {
		preview := input
		if len(preview) > 200 {
			preview = preview[:200] + "... [TRUNCATED]"
		}
		return "", fmt.Errorf("invalid arguments (JSON error: %v). RAW INPUT PREVIEW: %s. HINT: If the input is truncated, your model might be hitting its output token limit or max_output_tokens setting.", err, preview)
	}

	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.Action == "" {
		args.Action = "write"
	}

	cleanArgsPath := args.Path
	if strings.HasPrefix(cleanArgsPath, "/") || strings.HasPrefix(cleanArgsPath, "\\") {
		cleanArgsPath = strings.TrimLeft(cleanArgsPath, "/\\")
	}

	// Clean and validate path to prevent traversal
	path := filepath.Clean(cleanArgsPath)

	// Check if it's an absolute path that's actually within our workspace
	if filepath.IsAbs(path) && f.BasePath != "" {
		rel, err := filepath.Rel(f.BasePath, path)
		if err == nil && !strings.HasPrefix(rel, "..") {
			path = rel // Convert to relative for internal use
		}
	}

	if filepath.IsAbs(path) || (len(path) >= 2 && path[:2] == "..") {
		return "", fmt.Errorf("security violation: path %s is outside the allowed workspace", args.Path)
	}

	// Apply base path if set
	if f.BasePath != "" {
		path = filepath.Join(f.BasePath, path)
	}

	// Pre-check for conflicts
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() && args.Action != "mkdir" {
			return "", fmt.Errorf("conflict: path %s is a directory, but action %s expects a file", path, args.Action)
		}
		if !info.IsDir() && args.Action == "mkdir" {
			return "", fmt.Errorf("conflict: path %s is a file, but action mkdir expects to create a directory", path)
		}
	}

	// Ensure the parent directory exists for file operations
	if args.Action != "mkdir" {
		dir := filepath.Dir(path)
		if dir != "." {
			if info, err := os.Stat(dir); err == nil && !info.IsDir() {
				return "", fmt.Errorf("failed to create directory: %s is a file, cannot create a subdirectory inside it", dir)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("failed to create directory: %w", err)
			}
		}
	}

	switch args.Action {
	case "mkdir":
		if err := os.MkdirAll(path, 0755); err != nil {
			return "", fmt.Errorf("failed to create directory: %w", err)
		}
		return fmt.Sprintf("Successfully created directory %s", path), nil

	case "write":
		if args.Content == "" {
			if args.Target != "" || args.Replacement != "" {
				return "", fmt.Errorf("safety violation: 'write' action received empty content but found 'target' and 'replacement' arguments. Did you mean to use action: 'patch'? 'write' will OVERWRITE the entire file with the 'content' string.")
			}
			return "", fmt.Errorf("safety violation: 'write' action requires non-empty 'content' (to prevent accidental file deletion). If you really want to empty a file, use a single space or newline.")
		}
		if err := os.WriteFile(path, []byte(args.Content), 0644); err != nil {
			return "", fmt.Errorf("failed to write file: %w", err)
		}
		return fmt.Sprintf("Successfully wrote %d bytes to %s", len(args.Content), path), nil

	case "append":
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open file for append: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString(args.Content); err != nil {
			return "", fmt.Errorf("failed to append to file: %w", err)
		}
		return fmt.Sprintf("Successfully appended %d bytes to %s", len(args.Content), path), nil

	case "merge":
		if f.BasePath == "" {
			return "", fmt.Errorf("merge action is only available in shadow mode")
		}
		shadowFile := filepath.Join(f.BasePath, args.Path) // Raw relative path
		mainFile := filepath.Clean(args.Path)              // Path in main project

		data, err := os.ReadFile(shadowFile)
		if err != nil {
			return "", fmt.Errorf("failed to read shadow file %s: %w", args.Path, err)
		}

		if err := os.WriteFile(mainFile, data, 0644); err != nil {
			return "", fmt.Errorf("failed to write to main project file %s: %w", args.Path, err)
		}
		return fmt.Sprintf("Successfully merged %s from shadow workspace to main project", args.Path), nil

	case "patch":
		if args.Target == "" {
			return "", fmt.Errorf("target string is required for patch action")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read file for patching: %w", err)
		}
		content := string(data)
		if !strings.Contains(content, args.Target) {
			return "", fmt.Errorf("target string not found in file. Ensure you use the EXACT characters including whitespace. HINT: Use 'explorer:read' to verify the content.")
		}
		newContent := strings.Replace(content, args.Target, args.Replacement, 1) // Replace first occurrence
		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			return "", fmt.Errorf("failed to write patched file: %w", err)
		}
		return fmt.Sprintf("Successfully patched %s", path), nil

	case "patch_block":
		if args.Target == "" || args.Replacement == "" {
			return "", fmt.Errorf("patch_block requires both 'target' and 'replacement' arguments")
		}
		if len(args.Target) < 5 {
			return "", fmt.Errorf("patch_block 'target' is too short (%d bytes). This is risky and might replace unintended content. Use a larger block or 'patch' action.", len(args.Target))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read file for patching: %w", err)
		}
		content := string(data)

		if !strings.Contains(content, args.Target) {
			return "", fmt.Errorf("target block not found in file. Ensure whitespace and line endings match exactly. HINT: Use 'explorer:read' to see the exact file content.")
		}
		newContent := strings.Replace(content, args.Target, args.Replacement, 1)
		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			return "", fmt.Errorf("failed to write patched file: %w", err)
		}
		return fmt.Sprintf("Successfully patched block in %s", path), nil

	case "fmt":
		// Run gofmt on the file
		cmd := exec.CommandContext(ctx, "gofmt", "-w", path)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return string(output), fmt.Errorf("gofmt failed: %w", err)
		}
		return fmt.Sprintf("Successfully formatted %s", path), nil

	default:
		return "", fmt.Errorf("unknown action: %s", args.Action)
	}
}

// sanitizeJSON cleans up raw LLM output that might contain unescaped control characters in string literals.
func (f *FileWriter) sanitizeJSON(input string) string {
	var sb strings.Builder
	inString := false
	escaped := false

	for _, r := range input {
		if r == '"' && !escaped {
			inString = !inString
		}

		if inString && !escaped {
			if r == '\t' {
				sb.WriteString("\\t")
				continue
			}
			if r == '\n' {
				sb.WriteString("\\n")
				continue
			}
			if r == '\r' {
				sb.WriteString("\\r")
				continue
			}
		}

		if r == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
