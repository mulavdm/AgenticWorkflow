package summarizer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/types"
)

// Summarizer is a specialist agent that condenses bulky tool outputs into natural language summaries.
type Summarizer struct {
	provider engine.Agent
}

func New(model, url, apiKey string) *Summarizer {
	cfg := types.AgentConfig{
		Name:  "Context Summarizer",
		Role:  types.RoleSystem,
		Model: model,
		SystemPrompt: `You are a Context Compactor. Your job is to summarize bulky technical messages (like file contents, directory trees, or test logs) into concise, semantic natural language.
        
RULES:
1. FOCUS on the "What" and "Why".
2. PRESERVE key technical facts (e.g., file names, error messages, successful actions).
3. DISCARD formatting, repetitive noise, and large raw data blocks.
4. FORMAT: "Turn summary: <Brief summary of the action and result>".`,
	}
	// Wrap the provider in an AgentWrapper to implement engine.Agent (missing GetRole)
	rawProvider := agent.GetProvider(cfg, url, apiKey)
	return &Summarizer{
		provider: &agent.AgentWrapper{Agent: rawProvider},
	}
}

func (s *Summarizer) UpdateURL(url string) {
	if updater, ok := s.provider.(interface{ UpdateURL(string) }); ok {
		updater.UpdateURL(url)
	}
}

func (s *Summarizer) Summarize(ctx context.Context, content string) (string, error) {
	// If content is short, don't waste tokens summarizing
	if len(content) < 500 {
		return content, nil
	}

	// Create a dummy state for the provider call
	state := types.NewCouncilState("summarization", "compaction")
	prompt := fmt.Sprintf("Summarize the following technical message into a concise semantic summary:\n\n%s", content)
	
	resp, err := s.provider.Respond(ctx, state, prompt)
	if err != nil {
		return "", err
	}

	summary := strings.TrimSpace(resp.Content)
	if !strings.HasPrefix(strings.ToLower(summary), "turn summary:") {
		summary = "Turn summary: " + summary
	}
	return summary, nil
}

func (s *Summarizer) SummarizeWithRetry(ctx context.Context, content string) (string, error) {
	var lastErr error
	backoff := 1 * time.Second

	for attempt := 1; attempt <= 3; attempt++ {
		// Enforce a 30-second timeout per attempt to allow retrying if Gemma-4 is temporarily slow/hung
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 30*time.Second)
		summary, err := s.Summarize(attemptCtx, content)
		attemptCancel()

		if err == nil {
			return summary, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			break
		}

		select {
		case <-ctx.Done():
			break
		case <-time.After(backoff):
			backoff *= 2
		}
	}

	log.Printf("[Summarizer] Compaction failed after 3 attempts: %v. Falling back to deterministic truncation.", lastErr)
	return s.DeterministicFallback(content), nil
}

func (s *Summarizer) DeterministicFallback(content string) string {
	if strings.Contains(content, "[OBSERVATION from explorer]: Directory listing") || strings.Contains(content, "[OBSERVATION from explorer]: Recursive listing") {
		lines := strings.Split(content, "\n")
		var header string
		var entries []string
		count := 0
		for _, line := range lines {
			if strings.Contains(line, "listing for") {
				header = line
			} else if strings.Contains(line, "[FILE]") || strings.Contains(line, "[DIR]") {
				if count < 15 {
					entries = append(entries, line)
				}
				count++
			}
		}
		if len(entries) > 0 {
			truncatedMsg := ""
			if count > 15 {
				truncatedMsg = fmt.Sprintf("\n... [TRUNCATED: %d more entries]", count-15)
			}
			return fmt.Sprintf("Turn summary: Deterministic listing of %s showing %d files:\n%s%s", 
				header, count, strings.Join(entries, "\n"), truncatedMsg)
		}
	}

	if strings.Contains(content, "failed:") || strings.Contains(content, "Error:") || strings.Contains(content, "panic:") {
		lines := strings.Split(content, "\n")
		var errorLines []string
		for _, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "failed") || strings.Contains(lower, "error") || strings.Contains(lower, "panic") || strings.Contains(lower, "exit status") {
				if len(errorLines) < 10 {
					errorLines = append(errorLines, line)
				}
			}
		}
		if len(errorLines) > 0 {
			return fmt.Sprintf("Turn summary: Execution failed with errors. Relevant log snippets:\n%s", strings.Join(errorLines, "\n"))
		}
	}

	lines := strings.Split(content, "\n")
	if len(lines) > 20 {
		head := lines[:10]
		tail := lines[len(lines)-10:]
		return fmt.Sprintf("Turn summary: Truncated large output. Content head:\n%s\n... [TRUNCATED %d lines] ...\nContent tail:\n%s",
			strings.Join(head, "\n"), len(lines)-20, strings.Join(tail, "\n"))
	}

	return "Turn summary: " + content
}
