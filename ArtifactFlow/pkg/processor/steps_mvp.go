package processor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/provider"
)

// ensureOutputDir creates a local ./output directory to store physical artifacts
func ensureOutputDir() string {
	dir := "./output"
	os.MkdirAll(dir, 0755)
	return dir
}

// ---------------------------------------------------------
// LLM Generate Processor
// ---------------------------------------------------------

type LLMConfig struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type LLMGenerateProcessor struct {
	lm provider.ModelProvider
}

func NewLLMGenerateProcessor(lm provider.ModelProvider) *LLMGenerateProcessor {
	return &LLMGenerateProcessor{lm: lm}
}

func (p *LLMGenerateProcessor) Name() string { return "llm.generate" }

func (p *LLMGenerateProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	var cfg LLMConfig
	if err := UnmarshalConfig(config, &cfg); err != nil {
		return nil, err
	}

	// For the MVP, if we have a prompt in config, we use it.
	// If we have input artifacts (e.g. text from a previous step), we append their content.
	prompt := cfg.Prompt
	for _, in := range inputs {
		if in.Kind == artifact.KindText || in.Kind == artifact.KindMarkdown {
			b, err := os.ReadFile(in.URI)
			if err == nil {
				prompt += "\n" + string(b)
			}
		}
	}

	res, err := p.lm.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("llm generation failed: %w", err)
	}

	// Save to physical file
	outPath := filepath.Join(ensureOutputDir(), "llm_output.txt") // Simple MVP naming
	if err := os.WriteFile(outPath, []byte(res), 0644); err != nil {
		return nil, err
	}

	return []artifact.Artifact{
		{
			ID:   "generated-text",
			Kind: artifact.KindText,
			URI:  outPath,
		},
	}, nil
}

// ---------------------------------------------------------
// Shell Tool Processor
// ---------------------------------------------------------

type ShellConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type ShellProcessor struct{}

func NewShellProcessor() *ShellProcessor { return &ShellProcessor{} }
func (p *ShellProcessor) Name() string   { return "tool.shell" }

func (p *ShellProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	var cfg ShellConfig
	if err := UnmarshalConfig(config, &cfg); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("shell command failed: %s - %w", string(out), err)
	}

	outPath := filepath.Join(ensureOutputDir(), "shell_output.txt")
	if err := os.WriteFile(outPath, out, 0644); err != nil {
		return nil, err
	}

	return []artifact.Artifact{
		{
			ID:   "shell-result",
			Kind: artifact.KindText,
			URI:  outPath,
		},
	}, nil
}

// ---------------------------------------------------------
// Export Markdown Processor
// ---------------------------------------------------------

type ExportConfig struct {
	Filename string `json:"filename"`
}

type ExportMarkdownProcessor struct{}

func NewExportMarkdownProcessor() *ExportMarkdownProcessor { return &ExportMarkdownProcessor{} }
func (p *ExportMarkdownProcessor) Name() string            { return "export.markdown" }

func (p *ExportMarkdownProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	var cfg ExportConfig
	if err := UnmarshalConfig(config, &cfg); err != nil {
		return nil, err
	}

	if cfg.Filename == "" {
		cfg.Filename = "exported.md"
	}

	outPath := filepath.Join(ensureOutputDir(), cfg.Filename)

	// Merge all input text into one markdown file
	file, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	for i, in := range inputs {
		b, err := os.ReadFile(in.URI)
		if err == nil {
			if i > 0 {
				file.WriteString("\n\n---\n\n")
			}
			file.Write(b)
		}
	}

	return []artifact.Artifact{
		{
			ID:   "exported-markdown",
			Kind: artifact.KindMarkdown,
			URI:  outPath,
		},
	}, nil
}
