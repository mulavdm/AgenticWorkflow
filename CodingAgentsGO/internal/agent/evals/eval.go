package evals

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type Fixture struct {
	Name           string
	Role           types.Role
	Response       string
	AllowedTools   []string
	RequiredTags   []string
	ForbiddenTexts []string
}

type Result struct {
	Name   string
	Passed bool
	Errors []string
}

func Run(fixtures []Fixture) []Result {
	results := make([]Result, 0, len(fixtures))
	for _, fixture := range fixtures {
		results = append(results, Evaluate(fixture))
	}
	return results
}

func Evaluate(fixture Fixture) Result {
	var errs []string
	for _, tag := range fixture.RequiredTags {
		if !strings.Contains(fixture.Response, tag) {
			errs = append(errs, fmt.Sprintf("missing required tag %q", tag))
		}
	}
	for _, forbidden := range fixture.ForbiddenTexts {
		if strings.Contains(fixture.Response, forbidden) {
			errs = append(errs, fmt.Sprintf("contains forbidden text %q", forbidden))
		}
	}
	for _, toolName := range extractToolCalls(fixture.Response) {
		if !contains(fixture.AllowedTools, toolName) {
			errs = append(errs, fmt.Sprintf("tool %q is not allowed for role %s", toolName, fixture.Role))
		}
	}
	if !hasTerminalAction(fixture.Response) {
		errs = append(errs, "response must end with a tool call or explicit [NEXT: ...] handoff")
	}
	return Result{Name: fixture.Name, Passed: len(errs) == 0, Errors: errs}
}

func extractToolCalls(response string) []string {
	re := regexp.MustCompile(`(?i)\[\s*TOOL:\s*(\w+)`)
	matches := re.FindAllStringSubmatch(response, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			out = append(out, strings.ToLower(match[1]))
		}
	}
	return out
}

func hasTerminalAction(response string) bool {
	trimmed := strings.TrimSpace(response)
	if regexp.MustCompile(`(?i)\[\s*NEXT:\s*\w+[^\]]*\]\s*$`).MatchString(trimmed) {
		return true
	}
	return regexp.MustCompile(`(?is)\[\s*TOOL:\s*\w+.*\]\s*$`).MatchString(trimmed)
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if strings.EqualFold(value, needle) {
			return true
		}
	}
	return false
}
