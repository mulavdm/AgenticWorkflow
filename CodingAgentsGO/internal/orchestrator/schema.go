package orchestrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// SchemaProvider is an interface for roles that need to output structured data.
type SchemaProvider interface {
	GetSchema() string
}

// FlexibleList is a custom type to handle both string arrays and object arrays from LLMs.
type FlexibleList []string

func (f *FlexibleList) UnmarshalJSON(data []byte) error {
	// Try array of strings
	var strings []string
	if err := json.Unmarshal(data, &strings); err == nil {
		*f = strings
		return nil
	}

	// Try array of objects with a "step" or "description" field
	var objects []map[string]interface{}
	if err := json.Unmarshal(data, &objects); err == nil {
		for _, obj := range objects {
			for _, v := range obj {
				if s, ok := v.(string); ok {
					*f = append(*f, s)
					break
				}
			}
		}
		return nil
	}

	// Try single string
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*f = []string{single}
		return nil
	}

	return fmt.Errorf("failed to parse FlexibleList from %s", string(data))
}

// PlanAction represents the structured output expected from a Planner.
type PlanAction struct {
	Reasoning string       `json:"reasoning" description:"Internal reasoning about the task"`
	Plan      FlexibleList `json:"plan" description:"The actual plan or response to the user"`
	Steps     FlexibleList `json:"steps" description:"Sequential logical steps to complete the task"`
	ToolName  string       `json:"tool_name,omitempty" description:"Optional tool to execute"`
	ToolArgs  string       `json:"tool_args,omitempty" description:"Arguments for the tool"`
}

// CritiqueAction represents the structured output expected from a Critic.
type CritiqueAction struct {
	Reasoning   string       `json:"reasoning" description:"Analysis of the plan"`
	Score       int          `json:"score" description:"Confidence score from 1-10"`
	Weaknesses  FlexibleList `json:"weaknesses" description:"List of potential issues or flaws"`
	Suggestions FlexibleList `json:"suggestions" description:"Actionable improvements"`
}

// VerificationAction represents the structured output expected from a Red Teamer.
type VerificationAction struct {
	Reasoning       string       `json:"reasoning" description:"Security and logical analysis"`
	Vulnerabilities FlexibleList `json:"vulnerabilities" description:"List of security or logical flaws"`
	FailureModes    FlexibleList `json:"failure_modes" description:"Scenarios where the plan will break"`
	RiskLevel       string       `json:"risk_level" description:"Low, Medium, High, or Critical"`
}

// ValidateAction unmarshals and validates agent output against a specific Go struct.
func ValidateAction[T any](raw string) (T, error) {
	cleaned := CleanJSON(raw)
	repaired := RepairJSON(cleaned)
	var action T
	if err := json.Unmarshal([]byte(repaired), &action); err != nil {
		return action, fmt.Errorf("JSON validation failed: %w. Raw output was: %s", err, raw)
	}
	return action, nil
}

// RepairJSON attempts to fix common small syntax errors in LLM-generated JSON.
func RepairJSON(jsonStr string) string {
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" {
		return "{}"
	}

	// Remove trailing commas before closing braces/brackets
	reTrailingComma := regexp.MustCompile(`,(\s*[}\]])`)
	jsonStr = reTrailingComma.ReplaceAllString(jsonStr, "$1")

	// Balance brackets if the model cut off early
	openBraces := strings.Count(jsonStr, "{")
	closeBraces := strings.Count(jsonStr, "}")
	for i := 0; i < openBraces-closeBraces; i++ {
		jsonStr += "}"
	}

	openBrackets := strings.Count(jsonStr, "[")
	closeBrackets := strings.Count(jsonStr, "]")
	for i := 0; i < openBrackets-closeBrackets; i++ {
		jsonStr += "]"
	}

	return jsonStr
}

// CleanJSON attempts to extract the first valid JSON object from a response.
func CleanJSON(raw string) string {
	// 1. Try to extract from markdown code blocks first as a hint
	re := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")
	match := re.FindStringSubmatch(raw)
	if len(match) > 1 {
		raw = match[1]
	}

	// 2. Find the first '{'
	start := strings.Index(raw, "{")
	if start == -1 {
		return strings.TrimSpace(raw)
	}

	// 3. Find the balancing '}' for the first '{'
	// Simple bracket counting to isolate the first object
	count := 0
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case '{':
			count++
		case '}':
			count--
			if count == 0 {
				return raw[start : i+1]
			}
		}
	}

	return strings.TrimSpace(raw[start:])
}

// GetSchemaJSON returns a JSON string representing the expected structure for prompts.
func GetSchemaJSON[T any]() string {
	var instance T
	data, _ := json.MarshalIndent(instance, "", "  ")
	return string(data)
}
