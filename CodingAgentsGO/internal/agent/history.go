package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type historyMessage struct {
	Role    string
	Content string
}

func buildKnownFacts(ctx context.Context, cfg types.AgentConfig, state *types.CouncilState) string {
	editedPaths := make(map[string]bool)
	var testFailures []string

	state.RLock()
	events := make([]types.StateEvent, len(state.Events))
	copy(events, state.Events)
	state.RUnlock()

	for i, evt := range events {
		switch evt.Type {
		case types.EventToolRequested:
			if payloadMap, ok := evt.Payload.(map[string]string); ok {
				toolName := payloadMap["tool"]
				toolArgs := payloadMap["args"]
				if toolName == "writer" {
					var args struct {
						Action string `json:"action"`
						Path   string `json:"path"`
					}
					if err := json.Unmarshal([]byte(toolArgs), &args); err == nil && args.Path != "" {
						if args.Action == "write" || args.Action == "patch" || args.Action == "append" || args.Action == "patch_block" {
							editedPaths[args.Path] = true
						}
					}
				}
			} else if pInterface, ok := evt.Payload.(map[string]interface{}); ok {
				toolName, _ := pInterface["tool"].(string)
				toolArgs, _ := pInterface["args"].(string)
				if toolName == "writer" {
					var args struct {
						Action string `json:"action"`
						Path   string `json:"path"`
					}
					if err := json.Unmarshal([]byte(toolArgs), &args); err == nil && args.Path != "" {
						if args.Action == "write" || args.Action == "patch" || args.Action == "append" || args.Action == "patch_block" {
							editedPaths[args.Path] = true
						}
					}
				}
			}
		case types.EventToolResult:
			if payloadMap, ok := evt.Payload.(map[string]string); ok {
				status := payloadMap["status"]
				toolName := payloadMap["tool"]
				if status == "error" {
					errStr := payloadMap["error"]
					argsStr := ""
					for j := i - 1; j >= 0; j-- {
						if events[j].Type == types.EventToolRequested {
							var tName string
							if pm, ok := events[j].Payload.(map[string]string); ok {
								tName = pm["tool"]
								argsStr = pm["args"]
							} else if pmInterface, ok := events[j].Payload.(map[string]interface{}); ok {
								tName, _ = pmInterface["tool"].(string)
								argsStr, _ = pmInterface["args"].(string)
							}
							if tName == toolName {
								break
							}
						}
					}
					testFailures = append(testFailures, fmt.Sprintf("Tool %s failed: %s (args: %s)", toolName, errStr, argsStr))
				}
			} else if pInterface, ok := evt.Payload.(map[string]interface{}); ok {
				status, _ := pInterface["status"].(string)
				toolName, _ := pInterface["tool"].(string)
				if status == "error" {
					errStr, _ := pInterface["error"].(string)
					argsStr := ""
					for j := i - 1; j >= 0; j-- {
						if events[j].Type == types.EventToolRequested {
							var tName string
							if pm, ok := events[j].Payload.(map[string]string); ok {
								tName = pm["tool"]
								argsStr = pm["args"]
							} else if pmInterface, ok := events[j].Payload.(map[string]interface{}); ok {
								tName, _ = pmInterface["tool"].(string)
								argsStr, _ = pmInterface["args"].(string)
							}
							if tName == toolName {
								break
							}
						}
					}
					testFailures = append(testFailures, fmt.Sprintf("Tool %s failed: %s (args: %s)", toolName, errStr, argsStr))
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("[KNOWN FACTS]\n")
	if len(editedPaths) > 0 {
		sb.WriteString("Modified Files:\n")
		for path := range editedPaths {
			sb.WriteString(fmt.Sprintf("- %s\n", path))
		}
	} else {
		sb.WriteString("Modified Files: None\n")
	}

	if len(testFailures) > 0 {
		sb.WriteString("\nExecution/Test Failures:\n")
		for _, fail := range testFailures {
			sb.WriteString(fmt.Sprintf("- %s\n", fail))
		}
	}

	return sb.String()
}

func buildProviderHistory(ctx context.Context, cfg types.AgentConfig, state *types.CouncilState, instructions string, executionLogLimit int) []historyMessage {
	const recentLimit = 5
	const semanticLimit = 10

	var history []historyMessage
	seen := make(map[string]bool)

	if state.WorkspaceMode {
		factsBlock := buildKnownFacts(ctx, cfg, state)
		history = append(history, historyMessage{Role: "system", Content: factsBlock})

		if state.Artifacts.ProjectMap != "" {
			history = append(history, historyMessage{Role: "system", Content: "[GROUND TRUTH: PROJECT MAP]\n" + state.Artifacts.ProjectMap})
		}
		if state.Artifacts.TechnicalPlan != "" {
			history = append(history, historyMessage{Role: "system", Content: "[GROUND TRUTH: TECHNICAL PLAN]\n" + state.Artifacts.TechnicalPlan})
		}
		if state.Artifacts.ExecutionLogs != "" {
			logs := state.Artifacts.ExecutionLogs
			if executionLogLimit > 0 && len(logs) > executionLogLimit {
				logs = "... [TRUNCATED]\n" + logs[len(logs)-executionLogLimit:]
			}
			history = append(history, historyMessage{Role: "system", Content: "[GROUND TRUTH: EXECUTION LOGS]\n" + logs})
		}
	}

	compacted := state.GetCompactedMessages(recentLimit)
	var phaseMessages []types.Message
	for _, msg := range compacted {
		if normalizePhase(msg.Phase) == normalizePhase(state.Phase) {
			phaseMessages = append(phaseMessages, msg)
		}
	}

	startIdx := len(phaseMessages) - recentLimit
	if startIdx < 0 {
		startIdx = 0
	}

	for i := startIdx; i < len(phaseMessages); i++ {
		msg := phaseMessages[i]
		seen[msg.ID] = true
		history = append(history, historyMessage{
			Role:    roleForProviderMessage(cfg.Role, msg.Role),
			Content: msg.Content,
		})
	}

	if cfg.Embedding != nil && cfg.Memory != nil {
		codebaseDir, _ := os.Getwd()
		latestModTime := getLatestWorkspaceModTime(codebaseDir)

		emb, err := cfg.Embedding.GenerateEmbedding(ctx, instructions)
		if err == nil {
			relevantIDs, _ := cfg.Memory.SearchMessages(ctx, emb, semanticLimit)
			for _, id := range relevantIDs {
				if seen[id] {
					continue
				}

				// 1. Try to find message in current session's compacted memory first
				found := false
				for _, msg := range compacted {
					if msg.ID == id {
						history = append(history, historyMessage{
							Role:    "user",
							Content: "[HISTORICAL CONTEXT]: " + msg.Content,
						})
						seen[id] = true
						found = true
						break
					}
				}

				// 2. Reconciliation fallback: Retrieve from durable storage and validate/compact
				if !found {
					msg, err := cfg.Memory.GetMessage(ctx, id)
					if err == nil && msg.Visible && msg.Content != "" {
						content := msg.Content
						if msg.Summary != "" {
							content = msg.Summary
						} else if len(content) > 1000 {
							content = content[:1000] + "... [TRUNCATED]"
						}

						label := "[HISTORICAL CONTEXT]"
						if msg.TraceID != "" && msg.TraceID != state.TraceID {
							// Only allow historical context from other runs if they are user queries or final synthesizer results.
							// This prevents polluting a new run's context with confusing intermediate thoughts or system errors from previous runs.
							if msg.Role != types.RoleUser && msg.Role != types.RoleSynthesizer {
								continue
							}
							
							// Check if the codebase has been modified since this historical report was generated
							isStale := false
							if !latestModTime.IsZero() && msg.Timestamp.Before(latestModTime) {
								isStale = true
							}

							if isStale && msg.Role == types.RoleSynthesizer {
								content = "[WARNING: The codebase has been modified since this historical report was generated. Treat this report as outdated/reference-only. Always use explorer tools to examine the current state of the files. Do not copy this report verbatim if the codebase has changed.]\n" + content
								label = fmt.Sprintf("[CROSS-RUN CONTEXT from run %s (STALE/OUTDATED CODES)]", msg.TraceID)
							} else {
								label = fmt.Sprintf("[CROSS-RUN CONTEXT from run %s]", msg.TraceID)
							}
						}

						history = append(history, historyMessage{
							Role:    "user",
							Content: label + ": " + content,
						})
						seen[id] = true
					}
				}
			}
		}
	}

	return history
}

func roleForProviderMessage(agentRole, msgRole types.Role) string {
	if normalizePhase(string(msgRole)) == normalizePhase(string(agentRole)) {
		return "assistant"
	}
	return "user"
}

func getLatestWorkspaceModTime(workspaceDir string) time.Time {
	if workspaceDir == "" {
		return time.Time{}
	}

	var latest time.Time
	filepath.Walk(workspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip directories that we don't care about or that change constantly
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".shadow_workspaces" || name == "logs" || name == "scratch" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip temporary files, hidden files, or log/db files
		base := info.Name()
		if strings.HasPrefix(base, ".") || strings.HasSuffix(base, ".log") || strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".exe") || strings.HasSuffix(base, ".dll") {
			return nil
		}

		modTime := info.ModTime()
		if modTime.After(latest) {
			latest = modTime
		}
		return nil
	})

	return latest
}
