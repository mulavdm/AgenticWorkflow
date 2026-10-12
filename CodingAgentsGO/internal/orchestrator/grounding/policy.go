package grounding

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type ToolDecision string

const (
	ToolDecisionAllow            ToolDecision = "allow"
	ToolDecisionDeny             ToolDecision = "deny"
	ToolDecisionRequiresApproval ToolDecision = "requires_approval"
)

type ToolPolicyDecision struct {
	Decision ToolDecision
	Reason   string
}

type ToolPolicy struct {
	CodeRunnerApprovalThreshold time.Duration
	SafeWritePrefixes           []string
}

type ToolPolicyConfig struct {
	CodeRunnerApprovalThresholdMS int      `json:"coderunner_approval_threshold_ms,omitempty"`
	SafeWritePrefixes             []string `json:"safe_write_prefixes,omitempty"`
}

func DefaultToolPolicy() ToolPolicy {
	return ToolPolicyFromConfig(DefaultToolPolicyConfig())
}

func DefaultToolPolicyConfig() ToolPolicyConfig {
	return ToolPolicyConfig{
		CodeRunnerApprovalThresholdMS: int((15 * time.Second) / time.Millisecond),
		SafeWritePrefixes:             []string{"scratch", ".shadow_workspaces"},
	}
}

func ToolPolicyFromConfig(cfg ToolPolicyConfig) ToolPolicy {
	defaults := DefaultToolPolicyConfig()
	if cfg.CodeRunnerApprovalThresholdMS <= 0 {
		cfg.CodeRunnerApprovalThresholdMS = defaults.CodeRunnerApprovalThresholdMS
	}
	if len(cfg.SafeWritePrefixes) == 0 {
		cfg.SafeWritePrefixes = defaults.SafeWritePrefixes
	}
	return ToolPolicy{
		CodeRunnerApprovalThreshold: time.Duration(cfg.CodeRunnerApprovalThresholdMS) * time.Millisecond,
		SafeWritePrefixes:           normalizeSafeWritePrefixes(cfg.SafeWritePrefixes),
	}
}

func (p ToolPolicy) Config() ToolPolicyConfig {
	threshold := p.CodeRunnerApprovalThreshold
	if threshold <= 0 {
		threshold = 15 * time.Second
	}
	prefixes := p.SafeWritePrefixes
	if len(prefixes) == 0 {
		prefixes = []string{"scratch", ".shadow_workspaces"}
	}
	return ToolPolicyConfig{
		CodeRunnerApprovalThresholdMS: int(threshold / time.Millisecond),
		SafeWritePrefixes:             append([]string(nil), prefixes...),
	}
}

func (p ToolPolicy) Decide(role types.Role, toolName, toolArgs string) ToolPolicyDecision {
	switch toolName {
	case "explorer":
		return ToolPolicyDecision{Decision: ToolDecisionAllow}
	case "writer":
		if role != types.RoleCoder {
			return ToolPolicyDecision{Decision: ToolDecisionDeny, Reason: "security violation: writer is restricted to the coder role"}
		}
		var args struct {
			Action string `json:"action"`
			Path   string `json:"path"`
		}
		_ = json.Unmarshal([]byte(toolArgs), &args)
		if strings.EqualFold(args.Action, "merge") {
			return ToolPolicyDecision{Decision: ToolDecisionRequiresApproval, Reason: "writer merge requires explicit human promotion"}
		}
		if !p.isSafeWritePath(args.Path) {
			return ToolPolicyDecision{Decision: ToolDecisionRequiresApproval, Reason: fmt.Sprintf("writer path %q is outside scratch or shadow workspace", args.Path)}
		}
		return ToolPolicyDecision{Decision: ToolDecisionAllow}
	case "dockerrunner":
		if role != types.RoleCoder && role != types.RoleRedTeamer && role != types.RoleSecurity {
			return ToolPolicyDecision{Decision: ToolDecisionDeny, Reason: "security violation: dockerrunner is restricted to coder and security roles"}
		}
		return ToolPolicyDecision{Decision: ToolDecisionRequiresApproval, Reason: "dockerrunner requires explicit human approval"}
	case "coderunner":
		if requestedToolTimeout(toolArgs) > p.CodeRunnerApprovalThreshold {
			return ToolPolicyDecision{Decision: ToolDecisionRequiresApproval, Reason: "long-running coderunner command requires explicit human approval"}
		}
		return ToolPolicyDecision{Decision: ToolDecisionAllow}
	case "merge":
		return ToolPolicyDecision{Decision: ToolDecisionRequiresApproval, Reason: "merge requires explicit human promotion"}
	default:
		return ToolPolicyDecision{Decision: ToolDecisionAllow}
	}
}

func isScratchOrShadowPath(path string) bool {
	return DefaultToolPolicy().isSafeWritePath(path)
}

func (p ToolPolicy) isSafeWritePath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	clean = strings.TrimLeft(clean, "/\\")
	prefixes := p.SafeWritePrefixes
	if len(prefixes) == 0 {
		prefixes = []string{"scratch", ".shadow_workspaces"}
	}
	for _, prefix := range prefixes {
		prefix = strings.Trim(strings.TrimSpace(filepath.ToSlash(filepath.Clean(prefix))), "/\\")
		if prefix == "" || prefix == "." || strings.HasPrefix(prefix, "..") {
			continue
		}
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return true
		}
	}
	return false
}

func normalizeSafeWritePrefixes(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	seen := make(map[string]bool, len(prefixes))
	for _, prefix := range prefixes {
		clean := strings.Trim(strings.TrimSpace(filepath.ToSlash(filepath.Clean(prefix))), "/\\")
		if clean == "" || clean == "." || strings.HasPrefix(clean, "..") || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	if len(out) == 0 {
		return []string{"scratch", ".shadow_workspaces"}
	}
	return out
}

func requestedToolTimeout(toolArgs string) time.Duration {
	var args map[string]any
	if err := json.Unmarshal([]byte(toolArgs), &args); err != nil {
		return 0
	}
	for _, key := range []string{"timeout_ms", "timeoutMillis", "timeout_milliseconds"} {
		if duration := durationFromAny(args[key], time.Millisecond); duration > 0 {
			return duration
		}
	}
	for _, key := range []string{"timeout_seconds", "timeoutSec", "timeout"} {
		if duration := durationFromAny(args[key], time.Second); duration > 0 {
			return duration
		}
	}
	return 0
}

func durationFromAny(value any, unit time.Duration) time.Duration {
	switch v := value.(type) {
	case float64:
		return time.Duration(v) * unit
	case int:
		return time.Duration(v) * unit
	case json.Number:
		n, _ := strconv.ParseInt(string(v), 10, 64)
		return time.Duration(n) * unit
	case string:
		if parsed, err := time.ParseDuration(v); err == nil {
			return parsed
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Duration(n) * unit
		}
	}
	return 0
}
