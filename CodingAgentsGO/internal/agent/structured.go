package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type structuredAgentEnvelope struct {
	Kind     string          `json:"kind"`
	Tool     string          `json:"tool,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	NextRole string          `json:"next_role,omitempty"`
	Content  string          `json:"content,omitempty"`
	Summary  string          `json:"summary,omitempty"`
	Error    string          `json:"error,omitempty"`
	Reason   string          `json:"reason,omitempty"`
}

func structuredOutputEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CODINGAGENTS_STRUCTURED_OUTPUT")))
	return v == "1" || v == "true" || v == "json" || v == "schema"
}

func structuredResponseFormat() map[string]any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "codingagents_response",
			"strict": false,
			"schema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"kind"},
				"properties": map[string]any{
					"kind": map[string]any{
						"type": "string",
						"enum": []string{"tool_call", "handoff", "final", "error"},
					},
					"tool":      map[string]any{"type": "string"},
					"args":      map[string]any{"type": "object"},
					"next_role": map[string]any{"type": "string"},
					"content":   map[string]any{"type": "string"},
					"summary":   map[string]any{"type": "string"},
					"error":     map[string]any{"type": "string"},
					"reason":    map[string]any{"type": "string"},
				},
			},
		},
	}
}

func structuredOutputInstructions() string {
	return `STRUCTURED OUTPUT MODE:
Return exactly one JSON object with one of these shapes:
- {"kind":"tool_call","tool":"explorer|writer|coderunner|dockerrunner","args":{...},"reason":"..."}
- {"kind":"handoff","next_role":"planner|coder|critic|security|synthesizer|completed","summary":"..."}
- {"kind":"final","content":"..."}
- {"kind":"error","error":"..."}
Do not wrap the JSON in Markdown.`
}

func normalizeStructuredAgentOutput(content string) (string, bool, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return content, false, nil
	}
	var env structuredAgentEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return content, false, fmt.Errorf("malformed structured agent JSON: %w", err)
	}
	switch env.Kind {
	case "tool_call":
		if env.Tool == "" {
			return "", true, fmt.Errorf("structured tool_call missing tool")
		}
		args := strings.TrimSpace(string(env.Args))
		if args == "" || args == "null" {
			args = "{}"
		}
		return fmt.Sprintf("[TOOL: %s, ARGS: %s]", env.Tool, args), true, nil
	case "handoff":
		next := strings.TrimSpace(env.NextRole)
		if next == "" {
			return "", true, fmt.Errorf("structured handoff missing next_role")
		}
		prefix := strings.TrimSpace(env.Summary)
		if prefix == "" {
			prefix = strings.TrimSpace(env.Content)
		}
		if prefix != "" {
			return fmt.Sprintf("%s\n[NEXT: %s]", prefix, next), true, nil
		}
		return fmt.Sprintf("[NEXT: %s]", next), true, nil
	case "final":
		content := strings.TrimSpace(env.Content)
		if content == "" {
			content = strings.TrimSpace(env.Summary)
		}
		if content == "" {
			content = "Done."
		}
		return content + "\n[NEXT: completed]", true, nil
	case "error":
		if strings.TrimSpace(env.Error) == "" {
			return "", true, fmt.Errorf("structured error missing error")
		}
		return env.Error + "\n[NEXT: completed]", true, nil
	default:
		return "", true, fmt.Errorf("unknown structured agent output kind %q", env.Kind)
	}
}
