package workflow

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadFromFile reads a YAML file and parses it into a Workflow struct.
func LoadFromFile(path string) (*Workflow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}

	var wf Workflow
	if err := yaml.Unmarshal(b, &wf); err != nil {
		return nil, fmt.Errorf("failed to parse workflow yaml: %w", err)
	}

	return &wf, nil
}
