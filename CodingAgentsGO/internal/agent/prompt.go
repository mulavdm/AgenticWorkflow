package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
)

type providerCapabilities struct {
	SystemMessages       bool `json:"system_messages"`
	DeveloperMessages    bool `json:"developer_messages"`
	JSONSchema           bool `json:"json_schema"`
	Streaming            bool `json:"streaming"`
	ContextSize          int  `json:"context_size,omitempty"`
	OpenAICompatibleChat bool `json:"openai_compatible_chat"`
}

type chatAssemblyOptions struct {
	SupportsDeveloper bool
	ExecutionLogLimit int
}

type chatAssembly struct {
	System    string
	Developer string
	User      string
	History   []historyMessage
}

func assembleChat(ctx context.Context, cfg types.AgentConfig, state *types.CouncilState, instructions string, opts chatAssemblyOptions) chatAssembly {
	task, roleInstructions := splitTaskInstructions(instructions)
	protocol := buildProviderProtocol(cfg)
	system := protocol
	if cfg.SystemPrompt != "" {
		system += "\nROLE-SPECIFIC INSTRUCTIONS:\n" + cfg.SystemPrompt + "\n"
	}

	var developer string
	if roleInstructions != "" {
		developer = "CURRENT ROLE TASK CONTRACT:\n" + roleInstructions
		if !opts.SupportsDeveloper {
			system += "\n" + developer + "\n"
			developer = ""
		}
	}

	if task == "" {
		task = strings.TrimSpace(instructions)
	}

	return chatAssembly{
		System:    strings.TrimSpace(system),
		Developer: strings.TrimSpace(developer),
		User:      strings.TrimSpace(task),
		History:   buildProviderHistory(ctx, cfg, state, instructions, opts.ExecutionLogLimit),
	}
}

func splitTaskInstructions(instructions string) (task string, roleInstructions string) {
	trimmed := strings.TrimSpace(instructions)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "TASK:") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "TASK:"))
		parts := strings.SplitN(rest, "\n\n", 2)
		task = strings.TrimSpace(parts[0])
		if len(parts) == 2 {
			roleInstructions = strings.TrimSpace(parts[1])
		}
		return task, roleInstructions
	}
	return trimmed, ""
}

func buildProviderProtocol(cfg types.AgentConfig) string {
	var b strings.Builder
	b.WriteString("RULES:\n")
	b.WriteString("- REASON: State logic before tools.\n")
	b.WriteString("- TOOLS: End each turn with exactly one tool call, one handoff, or one final answer.\n")
	b.WriteString("- PATHS: Use relative paths with forward slashes (/).\n")
	b.WriteString("- NO SIMULATION: Do not fake user/system messages or tool observations.\n")

	if len(cfg.Tools) > 0 {
		tools := strings.Join(cfg.Tools, ", ")
		b.WriteString(fmt.Sprintf("\nAVAILABLE TOOLS: %s\n", tools))
		b.WriteString("LEGACY TOOL CALL FORMAT: [TOOL: tool_name, ARGS: {\"key\": \"value\"}]\n")
		b.WriteString("TOOL ARGUMENTS REFERENCE:\n")
		b.WriteString(tool.PromptReference(cfg.Tools))
		b.WriteString("CRITICAL: You can only call one tool at a time.\n")
	}

	return b.String()
}

func chatMessagesFromAssembly(assembled chatAssembly) []ollamaMessage {
	var raw []ollamaMessage
	raw = append(raw, ollamaMessage{Role: "system", Content: assembled.System})
	if assembled.Developer != "" {
		raw = append(raw, ollamaMessage{Role: "developer", Content: assembled.Developer})
	}
	if assembled.User != "" {
		raw = append(raw, ollamaMessage{Role: "user", Content: assembled.User})
	}
	for _, msg := range assembled.History {
		role := msg.Role
		content := msg.Content
		if role == "system" {
			role = "user"
			content = "[SYSTEM CONTEXT]: " + content
		}
		raw = append(raw, ollamaMessage{Role: role, Content: content})
	}

	if len(raw) == 0 {
		return nil
	}

	var consolidated []ollamaMessage
	current := raw[0]

	for i := 1; i < len(raw); i++ {
		next := raw[i]
		if next.Role == current.Role {
			current.Content = current.Content + "\n\n" + next.Content
		} else {
			consolidated = append(consolidated, current)
			current = next
		}
	}
	consolidated = append(consolidated, current)

	return consolidated
}
