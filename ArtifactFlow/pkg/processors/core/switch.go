package core

import (
	"context"
	"fmt"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
)

// SwitchProcessor outputs one of its config branches based on a condition.
// For ArtifactFlow, the config dynamic interpolator handles fetching the
// actual Artifact if `on_true` or `on_false` is an interpolation string,
// OR if the interpolated value itself is raw data.
// Wait, if `on_true` evaluates to an Artifact, it will be injected into `config` as the Artifact object,
// or as the data. Let's make it so the interpolator injects the Artifact object if it resolves to a node!
type SwitchProcessor struct{}

func (p *SwitchProcessor) Name() string {
	return "core.switch"
}

func (p *SwitchProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	cond, ok := config["condition"]
	if !ok {
		return nil, fmt.Errorf("missing 'condition' in config")
	}

	var condition bool
	switch v := cond.(type) {
	case bool:
		condition = v
	case string:
		condition = (v == "true")
	default:
		return nil, fmt.Errorf("condition must be a boolean, got %T", cond)
	}

	var result any
	if condition {
		result = config["on_true"]
	} else {
		result = config["on_false"]
	}

	// If the result is ALREADY an Artifact (because the Interpolator injected it), just return it!
	if art, ok := result.(artifact.Artifact); ok {
		return []artifact.Artifact{art}, nil
	}

	// Otherwise, wrap the primitive value into a new Artifact
	art := artifact.Artifact{
		ID:       "switch_output",
		Kind:     "Value",
		Metadata: map[string]any{"data": result},
	}

	return []artifact.Artifact{art}, nil
}
