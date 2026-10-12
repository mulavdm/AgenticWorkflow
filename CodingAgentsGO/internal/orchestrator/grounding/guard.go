package grounding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
)

// Guard enforces security policies, grounding rules, and anti-hallucination gates.
type Guard struct {
	tools       *tool.Registry
	broadcaster types.Broadcaster
	policy      ToolPolicy
}

func NewGuard(tools *tool.Registry, bc types.Broadcaster) *Guard {
	return &Guard{
		tools:       tools,
		broadcaster: bc,
		policy:      DefaultToolPolicy(),
	}
}

func (g *Guard) SetToolPolicyConfig(cfg ToolPolicyConfig) {
	g.policy = ToolPolicyFromConfig(cfg)
}

func (g *Guard) GetToolPolicyConfig() ToolPolicyConfig {
	return g.policy.Config()
}

// ValidateResponse checks an agent response for tool calls, hallucinations, and protocol compliance.
func (g *Guard) ValidateResponse(ctx context.Context, state *types.CouncilState, role types.Role, content string, metadata map[string]interface{}) (string, error) {
	toolName, toolArgs, cleanContent, err := g.ParseAndValidateResponse(state, role, content)
	if err != nil {
		return cleanContent, err
	}

	if toolName != "" {
		state.AddMessage(role, strings.TrimSpace(cleanContent), metadata, g.broadcaster)
		state.AddEvent(types.EventToolRequested, map[string]string{
			"role": string(role),
			"tool": toolName,
			"args": toolArgs,
		})

		approvalID := fmt.Sprintf("%s-%s-%d", state.SessionID, toolName, time.Now().UnixNano())
		_, pErr := g.CheckPolicy(state, role, toolName, toolArgs, approvalID)
		if pErr != nil {
			return "", pErr
		}

		_, eErr := g.ExecuteTool(ctx, state, role, toolName, toolArgs)
		if eErr != nil {
			return "", eErr
		}
		return "", types.ErrToolExecuted
	}

	return cleanContent, nil
}

// ParseAndValidateResponse parses an agent's response for tool calls or handoffs, and validates syntax, RBAC, and write scopes.
func (g *Guard) ParseAndValidateResponse(state *types.CouncilState, role types.Role, content string) (toolName, toolArgs, cleanContent string, err error) {
	// 1. Prefix Stripping
	content = g.stripHistoryMarkers(content)

	// 2. Tool Call Detection
	toolName, toolArgs, fullMatch := g.ExtractFirstToolCall(content)
	if strings.ToLower(toolName) == "none" {
		toolName = ""
		toolArgs = ""
		fullMatch = ""
	}

	// 3. Truncation to first action
	if fullMatch != "" {
		endIdx := strings.Index(content, fullMatch) + len(fullMatch)
		content = content[:endIdx]
	}

	// 4. Anti-Hallucination
	if err := g.detectHallucinations(content, toolName); err != nil {
		return "", "", content, err
	}

	// 5. Handoff Validation
	if err := g.validateHandoff(content, fullMatch == ""); err != nil {
		return "", "", content, err
	}

	// 6. RBAC and Scope Validation if a tool call is present
	if fullMatch != "" {
		if role == types.RoleContext && toolName != "explorer" && toolName != "memory_retriever" && toolName != "codebase_search" {
			err := fmt.Errorf("security violation: your role (%s) is restricted to read-only tools", role)
			return toolName, toolArgs, content, err
		}
		if err := validateToolArgsJSON(toolArgs); err != nil {
			return toolName, toolArgs, content, err
		}
		if err := g.enforceWriterScope(state, toolName, toolArgs); err != nil {
			return toolName, toolArgs, content, err
		}
	} else {
		// 7. Context-Specific Enforcement
		if role == types.RoleContext {
			if !g.hasSeenObservation(state, "explorer") && !g.hasSeenObservation(state, "memory_retriever") && !g.hasSeenObservation(state, "codebase_search") {
				return "", "", content, fmt.Errorf("you MUST use the 'explorer', 'memory_retriever', or 'codebase_search' tool to inspect the project structure or codebase facts before providing a summary. DO NOT guess.")
			}
		}
	}

	return toolName, toolArgs, strings.TrimSpace(content), nil
}

func (g *Guard) stripHistoryMarkers(content string) string {
	historyMarkers := []string{"### SYSTEM", "[OBSERVATION", "### USER", "### ASSISTANT"}
	lines := strings.Split(content, "\n")
	firstRealLine := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isMarker := false
		for _, marker := range historyMarkers {
			if strings.HasPrefix(trimmed, marker) {
				isMarker = true
				break
			}
		}
		if !isMarker && trimmed != "" {
			firstRealLine = i
			break
		}
	}
	return strings.Join(lines[firstRealLine:], "\n")
}

func (g *Guard) detectHallucinations(content, toolName string) error {
	if strings.ToLower(toolName) == "none" {
		// Treat as no tool, allowing handoff validation to trigger if [NEXT: ...] is missing.
		return nil
	}

	markers := []string{"[SYSTEM RESPONSE]", "[OBSERVATION from", "[LISTED DIRECTORIES", "[FILE CONTENT", "### CONTEXT"}
	for _, m := range markers {
		if strings.Contains(content, m) {
			return fmt.Errorf("hallucination detected: do not simulate system responses (detected: %s)", m)
		}
	}
	return nil
}

func (g *Guard) validateHandoff(content string, noTool bool) error {
	nextRe := regexp.MustCompile(`(?i)\[?NEXT:\s*(\w+)[^\]]*\]?`)
	if !nextRe.MatchString(content) && noTool {
		if strings.Contains(content, "[TOOL:") {
			return fmt.Errorf("invalid tool syntax. Ensure you use EXACTLY: [TOOL: name, ARGS: {\"key\": \"value\"}]")
		}
		return fmt.Errorf("missing explicit handoff: final response MUST include [NEXT: role] or [NEXT: completed]")
	}
	return nil
}

// CheckPolicy evaluates the tool call against safety policies and returns an error (e.g., ApprovalRequiredError) if blocked.
func (g *Guard) CheckPolicy(state *types.CouncilState, role types.Role, toolName, toolArgs, approvalID string) (ToolPolicyDecision, error) {
	t, ok := g.tools.Get(toolName)
	if !ok {
		err := fmt.Errorf("unknown tool: %s", toolName)
		g.recordToolDenied(state, role, toolName, toolArgs, err)
		return ToolPolicyDecision{Decision: ToolDecisionDeny, Reason: err.Error()}, err
	}

	decision := g.policy.Decide(role, toolName, toolArgs)
	g.recordToolPolicyDecision(state, role, toolName, toolArgs, decision)
	switch decision.Decision {
	case ToolDecisionDeny:
		err := errors.New(decision.Reason)
		g.recordToolDenied(state, role, toolName, toolArgs, err)
		return decision, err
	case ToolDecisionRequiresApproval:
		// Shadow workspace bypass for writer outside scratch
		if toolName == "writer" && strings.Contains(decision.Reason, "outside scratch or shadow workspace") {
			if fw, ok := t.(*tool.FileWriter); ok && fw.BasePath != "" && strings.Contains(filepath.ToSlash(fw.BasePath), ".shadow_workspaces") {
				return ToolPolicyDecision{Decision: ToolDecisionAllow}, nil
			}
		}

		// Record approval with the provided deterministic ID
		payload := map[string]string{
			"approval_id": approvalID,
			"role":        string(role),
			"tool":        toolName,
			"args":        toolArgs,
			"reason":      decision.Reason,
			"status":      string(types.ApprovalPending),
		}
		state.AddEvent(types.EventApprovalRequested, payload)
		state.AddEvent(types.EventRunPaused, map[string]string{
			"approval_id": approvalID,
			"reason":      decision.Reason,
		})
		state.AddMessage(types.RoleSystem, fmt.Sprintf("[APPROVAL REQUIRED]: %s (approval_id: %s)", decision.Reason, approvalID), nil, g.broadcaster)

		err := &types.ApprovalRequiredError{
			ApprovalID: approvalID,
			Tool:       toolName,
			Reason:     decision.Reason,
		}
		return decision, err
	}

	return decision, nil
}

func (g *Guard) recordToolPolicyDecision(state *types.CouncilState, role types.Role, toolName, toolArgs string, decision ToolPolicyDecision) {
	state.AddEvent(types.EventToolPolicyDecided, map[string]string{
		"role":     string(role),
		"tool":     toolName,
		"args":     toolArgs,
		"decision": string(decision.Decision),
		"reason":   decision.Reason,
	})
}

// ExecuteTool runs the tool after grounding and loop prevention checks are passed.
func (g *Guard) ExecuteTool(ctx context.Context, state *types.CouncilState, role types.Role, toolName, toolArgs string) (string, error) {
	t, ok := g.tools.Get(toolName)
	if !ok {
		err := fmt.Errorf("unknown tool: %s", toolName)
		g.recordToolDenied(state, role, toolName, toolArgs, err)
		return "", err
	}

	// 1. Loop Detection (Canonicalization)
	var argsObj map[string]interface{}
	canonicalArgs := toolArgs
	if err := json.Unmarshal([]byte(toolArgs), &argsObj); err == nil {
		if p, ok := argsObj["path"].(string); ok {
			argsObj["path"] = filepath.ToSlash(filepath.Clean(p))
		}
		if normalized, err := json.Marshal(argsObj); err == nil {
			canonicalArgs = string(normalized)
		}
	}
	canonicalCall := fmt.Sprintf("%s:%s", toolName, canonicalArgs)

	for _, pastCall := range state.ToolHistory {
		if pastCall == canonicalCall {
			hint := "\n[SYSTEM HINT]: You are repeating yourself. If you can't find a path, use 'explorer:tree' or 'explorer:search' instead of 'list'. If you have enough info, summarize and handoff."
			if state.RetryCount >= 3 {
				state.FlushLastMessages(4)
				hint = "\n[CRITICAL LOOP DETECTED]: I have surgically removed your recent failing thoughts. DO NOT repeat your previous actions. Try a completely different strategy (e.g. search for code patterns or handoff if you are stuck)."
			}
			err := fmt.Errorf("loop detected: you have already called %s with these exact arguments. Progression is REQUIRED. Adjust your strategy.%s", toolName, hint)
			g.recordToolDenied(state, role, toolName, toolArgs, err)
			return "", err
		}
	}

	// 2. Grounding Gate
	if err := g.enforceGrounding(state, toolName, toolArgs); err != nil {
		state.ToolHistory = append(state.ToolHistory, canonicalCall)
		g.recordToolDenied(state, role, toolName, toolArgs, err)
		return "", err
	}

	output, err := t.Execute(ctx, toolArgs)
	state.ToolHistory = append(state.ToolHistory, canonicalCall)
	if err != nil {
		output = g.formatToolError(toolName, err, output)
		state.AddEvent(types.EventToolResult, map[string]string{
			"role":   string(role),
			"tool":   toolName,
			"status": "error",
			"error":  err.Error(),
		})
	} else {
		output = g.applyDiscoveryGuidance(toolName, output)
		state.AddEvent(types.EventToolResult, map[string]string{
			"role":   string(role),
			"tool":   toolName,
			"status": "ok",
		})
	}

	g.broadcastObservation(toolName, output)
	state.AddMessage(types.RoleSystem, fmt.Sprintf("[OBSERVATION from %s]: %s", toolName, output), nil, g.broadcaster)

	return output, nil
}

func validateToolArgsJSON(toolArgs string) error {
	var args map[string]any
	if err := json.Unmarshal([]byte(toolArgs), &args); err != nil {
		return fmt.Errorf("malformed tool JSON: %w", err)
	}
	return nil
}

func (g *Guard) enforceWriterScope(state *types.CouncilState, toolName, toolArgs string) error {
	if toolName != "writer" {
		return nil
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(toolArgs), &args); err != nil {
		return nil
	}
	path := normalizeScopePath(args.Path)
	if path == "" || isScratchOrShadowPath(path) {
		return nil
	}
	allowed := g.collectWriteScopePaths(state)
	if len(allowed) == 0 {
		return nil
	}
	for _, scopePath := range allowed {
		if pathWithinScope(path, scopePath) {
			return nil
		}
	}
	return fmt.Errorf("writer path %q is outside the declared task scope; use only paths named in the user request or approved technical plan", args.Path)
}

func (g *Guard) collectWriteScopePaths(state *types.CouncilState) []string {
	var scopes []string
	for _, msg := range state.SnapshotMessages() {
		if msg.Role == types.RoleUser || msg.Role == types.RolePlanner {
			scopes = append(scopes, extractPathMentions(msg.Content)...)
		}
	}
	if state.Artifacts.TechnicalPlan != "" {
		scopes = append(scopes, extractPathMentions(state.Artifacts.TechnicalPlan)...)
	}
	seen := make(map[string]bool, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, path := range scopes {
		path = normalizeScopePath(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func extractPathMentions(content string) []string {
	pathRe := regexp.MustCompile(`(?i)(?:[\w.-]+[\\/])+[\w.-]+|[\w.-]+\.(?:go|md|txt|json|yaml|yml|toml|css|js|ts|tsx|html|log)`)
	matches := pathRe.FindAllString(content, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		match = strings.Trim(match, "`'\"“”‘’.,;:()[]{}<>")
		if normalized := normalizeScopePath(match); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func normalizeScopePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\"“”‘’.,;:()[]{}<>")
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimLeft(path, "/\\")
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}
	return path
}

func pathWithinScope(path, scopePath string) bool {
	if path == scopePath {
		return true
	}
	if strings.HasSuffix(scopePath, "/") {
		return strings.HasPrefix(path, scopePath)
	}
	ext := filepath.Ext(scopePath)
	return ext == "" && strings.HasPrefix(path, scopePath+"/")
}

func (g *Guard) ExecuteApprovedTool(ctx context.Context, state *types.CouncilState, role types.Role, toolName, toolArgs, approvalID string) error {
	t, ok := g.tools.Get(toolName)
	if !ok {
		err := fmt.Errorf("unknown tool: %s", toolName)
		g.recordToolDenied(state, role, toolName, toolArgs, err)
		return err
	}
	state.AddEvent(types.EventRunResumed, map[string]string{
		"approval_id": approvalID,
		"role":        string(role),
		"tool":        toolName,
		"args":        toolArgs,
	})
	_, err := g.executeTool(ctx, state, role, toolName, toolArgs, t)
	if err == types.ErrToolExecuted {
		return nil
	}
	return err
}

func (g *Guard) executeTool(ctx context.Context, state *types.CouncilState, role types.Role, toolName, toolArgs string, t tool.Tool) (string, error) {
	// 1. Loop Detection (Canonicalization)
	var argsObj map[string]interface{}
	canonicalArgs := toolArgs
	if err := json.Unmarshal([]byte(toolArgs), &argsObj); err == nil {
		// Normalize path if present to detect semantic loops (e.g. ./foo vs foo)
		if p, ok := argsObj["path"].(string); ok {
			argsObj["path"] = filepath.ToSlash(filepath.Clean(p))
		}
		if normalized, err := json.Marshal(argsObj); err == nil {
			canonicalArgs = string(normalized)
		}
	}
	canonicalCall := fmt.Sprintf("%s:%s", toolName, canonicalArgs)

	for _, pastCall := range state.ToolHistory {
		if pastCall == canonicalCall {
			hint := "\n[SYSTEM HINT]: You are repeating yourself. If you can't find a path, use 'explorer:tree' or 'explorer:search' instead of 'list'. If you have enough info, summarize and handoff."

			// Loop Mitigation: Surgical Flush
			if state.RetryCount >= 3 {
				state.FlushLastMessages(4) // Wipe the last 2 rounds of failure
				hint = "\n[CRITICAL LOOP DETECTED]: I have surgically removed your recent failing thoughts. DO NOT repeat your previous actions. Try a completely different strategy (e.g. search for code patterns or handoff if you are stuck)."
			}

			err := fmt.Errorf("loop detected: you have already called %s with these exact arguments. Progression is REQUIRED. Adjust your strategy.%s", toolName, hint)
			g.recordToolDenied(state, role, toolName, toolArgs, err)
			return "", err
		}
	}

	// 2. Grounding Gate
	if err := g.enforceGrounding(state, toolName, toolArgs); err != nil {
		state.ToolHistory = append(state.ToolHistory, canonicalCall)
		g.recordToolDenied(state, role, toolName, toolArgs, err)
		return "", err
	}

	output, err := t.Execute(ctx, toolArgs)
	state.ToolHistory = append(state.ToolHistory, canonicalCall) // Track all attempted tool calls to prevent infinite error loops
	if err != nil {
		output = g.formatToolError(toolName, err, output)
		state.AddEvent(types.EventToolResult, map[string]string{
			"role":   string(role),
			"tool":   toolName,
			"status": "error",
			"error":  err.Error(),
		})
	} else {
		output = g.applyDiscoveryGuidance(toolName, output)
		state.AddEvent(types.EventToolResult, map[string]string{
			"role":   string(role),
			"tool":   toolName,
			"status": "ok",
		})
	}

	g.broadcastObservation(toolName, output)
	state.AddMessage(types.RoleSystem, fmt.Sprintf("[OBSERVATION from %s]: %s", toolName, output), nil, g.broadcaster)

	return "", types.ErrToolExecuted
}

func (g *Guard) recordToolDenied(state *types.CouncilState, role types.Role, toolName, toolArgs string, err error) {
	state.AddEvent(types.EventToolDenied, map[string]string{
		"role":  string(role),
		"tool":  toolName,
		"args":  toolArgs,
		"error": err.Error(),
	})
}

func (g *Guard) enforceGrounding(state *types.CouncilState, toolName, toolArgs string) error {
	if toolName != "explorer" && toolName != "writer" {
		return nil
	}

	var argMap map[string]interface{}
	if err := json.Unmarshal([]byte(toolArgs), &argMap); err != nil {
		return nil
	}

	// Only enforce grounding for file reads (explorer:read) and file writes (writer).
	// Read-only discovery actions (list, tree, search) in explorer do not require grounding.
	if toolName == "explorer" {
		action, _ := argMap["action"].(string)
		if action == "tree" {
			path, _ := argMap["path"].(string)
			cleanPath := filepath.ToSlash(path)
			if strings.HasPrefix(cleanPath, "/") || strings.HasPrefix(cleanPath, "\\") {
				cleanPath = strings.TrimLeft(cleanPath, "/\\")
			}
			if cleanPath == "" {
				cleanPath = "."
			}
			absPath, err := filepath.Abs(cleanPath)
			if err == nil {
				wd, errWd := os.Getwd()
				if errWd == nil {
					absWd, errAbsWd := filepath.Abs(wd)
					if errAbsWd == nil && filepath.Clean(absPath) == filepath.Clean(absWd) {
						return fmt.Errorf("security violation: running 'tree' on the root directory is forbidden to prevent context window overflow. Use 'list' to examine the root, then run 'tree' on a specific subfolder.")
					}
				}
			}
		}
		if action == "list" || action == "tree" || action == "search" {
			return nil
		}
	}

	path, _ := argMap["path"].(string)
	path = filepath.Clean(path)
	if path == "" || path == "." || path == "/" || path == "\\" {
		return nil
	}

	if strings.Contains(path, "*") || strings.Contains(path, "?") {
		path = filepath.Dir(path)
		if path == "." || path == "/" || path == "\\" {
			return nil
		}
	}

	// Auto-correct leading slashes for grounding validation
	cleanPath := filepath.ToSlash(path)
	if strings.HasPrefix(cleanPath, "/") || strings.HasPrefix(cleanPath, "\\") {
		cleanPath = strings.TrimLeft(cleanPath, "/\\")
	}

	// 1. Absolute Path Check (Generic - only block true absolute paths like drive letters or network shares)
	isAbs := filepath.IsAbs(path) || (len(cleanPath) > 1 && cleanPath[1] == ':') || strings.HasPrefix(cleanPath, "//")
	if isAbs {
		return fmt.Errorf("PATH ERROR: '%s' is a forbidden absolute path. The tool is AUTHORIZED, but you must use a relative path from the project root.", path)
	}

	base := filepath.Base(cleanPath)
	parent := filepath.ToSlash(filepath.Dir(cleanPath))
	if parent == "." {
		parent = "the project root"
	}

	// 2. Search History for discovery
	for _, m := range state.Messages {
		if (m.Role == types.RoleSystem && strings.Contains(m.Content, "[OBSERVATION")) ||
			(m.Role == types.RoleUser && strings.Contains(m.Content, cleanPath)) {
			norm := filepath.ToSlash(m.Content)
			// Match if the base name exists in list/tree/search output OR in instructions
			if strings.Contains(norm, base) || strings.Contains(norm, cleanPath) {
				return nil
			}
		}
	}

	// 3. Special handling for '..' (Discovery is implicit for parents if we aren't at root)
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return nil
	}

	if parent == "" || parent == "." {
		parent = "."
	}

	// 3. Relaxed Grounding: If the path was mentioned in the initial task, allow it.
	for _, msg := range state.Messages {
		if msg.Role == types.RoleUser && (strings.Contains(msg.Content, path) || strings.Contains(msg.Content, filepath.Base(path))) {
			return nil
		}
	}

	// 4. Smart Recovery Hint (Avoid inducing loops)
	recoveryAction := "list"
	canonicalRecovery := fmt.Sprintf("explorer:{\"action\":\"%s\",\"path\":\"%s\"}", recoveryAction, parent)
	for _, past := range state.ToolHistory {
		if strings.Contains(past, canonicalRecovery) || strings.Contains(past, parent) {
			recoveryAction = "tree" // Escalation
			break
		}
	}

	return fmt.Errorf("PATH ERROR: I have no record of '%s' existing. The tool is AUTHORIZED, but you must 'discover' this path first. RECOVERY: Call [TOOL: explorer, ARGS: {\"action\": \"%s\", \"path\": \"%s\"}] to verify this directory.", path, recoveryAction, parent)
}

func (g *Guard) recordApprovalRequired(state *types.CouncilState, role types.Role, toolName, toolArgs, reason string) error {
	approvalID := fmt.Sprintf("%s-%s-%d", state.SessionID, toolName, time.Now().UnixNano())
	payload := map[string]string{
		"approval_id": approvalID,
		"role":        string(role),
		"tool":        toolName,
		"args":        toolArgs,
		"reason":      reason,
		"status":      string(types.ApprovalPending),
	}
	state.AddEvent(types.EventApprovalRequested, payload)
	state.AddEvent(types.EventRunPaused, map[string]string{
		"approval_id": approvalID,
		"reason":      reason,
	})
	state.AddMessage(types.RoleSystem, fmt.Sprintf("[APPROVAL REQUIRED]: %s (approval_id: %s)", reason, approvalID), nil, g.broadcaster)
	return &types.ApprovalRequiredError{
		ApprovalID: approvalID,
		Tool:       toolName,
		Reason:     reason,
	}
}

func (g *Guard) formatToolError(name string, err error, output string) string {
	hint := ""
	errMsg := err.Error()
	if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "no such file") {
		hint = "\n[SYSTEM HINT]: Path not found. Use 'explorer:list' to verify the project structure."
	}
	if strings.Contains(errMsg, "target string is required") {
		hint = "\n[SYSTEM HINT]: For action 'patch', you MUST include the 'target' string to match and 'content' string to replace it with."
	}
	return fmt.Sprintf("Error executing tool %s: %v\nOutput: %s%s", name, err, output, hint)
}

func (g *Guard) applyDiscoveryGuidance(name, output string) string {
	if name != "explorer" {
		return output
	}
	if strings.Contains(output, "Directory listing") && !strings.Contains(output, "[FILE] ") && !strings.Contains(output, "[DIR]  ") {
		return output + "\n\n[GROUNDING WARNING]: This listing is EMPTY. Adjust your plan."
	}
	return output
}

func (g *Guard) broadcastObservation(name, output string) {
	if g.broadcaster != nil {
		g.broadcaster.Broadcast(types.UIEvent{
			Type:    "observation",
			Payload: map[string]string{"tool": name, "output": output},
		})
	}
}

func (g *Guard) hasSeenObservation(state *types.CouncilState, toolName string) bool {
	for _, m := range state.Messages {
		if strings.Contains(m.Content, fmt.Sprintf("[OBSERVATION from %s]", toolName)) {
			return true
		}
	}
	return false
}

func (g *Guard) ExtractFirstToolCall(content string) (name, args, fullMatch string) {
	// 1. Find the tool name and the start of the tool call
	re := regexp.MustCompile(`(?si)\[\s*TOOL:\s*(\w+)`)
	locs := re.FindStringSubmatchIndex(content)
	if len(locs) == 0 {
		return "", "", ""
	}

	name = content[locs[2]:locs[3]]
	startIdx := locs[0] // Start of "[TOOL:"

	// 2. Find the ARGS portion
	args = "{}"
	argsStart := -1
	argsEnd := -1

	// Look for ARGS: after the tool name
	afterTool := content[locs[3]:]
	argsKeywordIdx := strings.Index(strings.ToUpper(afterTool), "ARGS:")
	if argsKeywordIdx != -1 {
		// Absolute index of ARGS:
		absArgsKeywordIdx := locs[3] + argsKeywordIdx
		// Find the first '{' after ARGS:
		firstBraceIdx := strings.Index(content[absArgsKeywordIdx:], "{")
		if firstBraceIdx != -1 {
			argsStart = absArgsKeywordIdx + firstBraceIdx

			// Scan forward to find the matching closing brace, respecting string literals
			depth := 0
			inString := false
			escaped := false

			for i := argsStart; i < len(content); i++ {
				ch := content[i]
				if inString {
					if escaped {
						escaped = false
					} else if ch == '\\' {
						escaped = true
					} else if ch == '"' {
						inString = false
					}
				} else {
					if ch == '"' {
						inString = true
					} else if ch == '{' {
						depth++
					} else if ch == '}' {
						depth--
						if depth == 0 {
							argsEnd = i + 1
							break
						}
					}
				}
			}
		}
	}

	// 3. Extract args and determine fullMatch
	if argsStart != -1 {
		if argsEnd != -1 {
			args = content[argsStart:argsEnd]
			// Find the closing bracket ']' after the args block
			closingBracketIdx := strings.Index(content[argsEnd:], "]")
			if closingBracketIdx != -1 {
				fullMatch = content[startIdx : argsEnd+closingBracketIdx+1]
			} else {
				fullMatch = content[startIdx:argsEnd]
			}
		} else {
			// Incomplete args block (e.g. unclosed '{')
			args = content[argsStart:]
			closingBracketIdx := strings.Index(content[argsStart:], "]")
			if closingBracketIdx != -1 {
				fullMatch = content[startIdx : argsStart+closingBracketIdx+1]
			} else {
				fullMatch = content[startIdx:]
			}
		}
	} else {
		// No args block (no '{' found after ARGS:)
		closingBracketIdx := strings.Index(content[locs[3]:], "]")
		if closingBracketIdx != -1 {
			fullMatch = content[startIdx : locs[3]+closingBracketIdx+1]
		} else {
			fullMatch = content[startIdx:]
		}
	}

	return name, args, fullMatch
}
