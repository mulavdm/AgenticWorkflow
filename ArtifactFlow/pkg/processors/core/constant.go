package core

import (
	"context"
	"fmt"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
)

// ConstantProcessor outputs a constant value defined in its config.
type ConstantProcessor struct{}

func (p *ConstantProcessor) Name() string {
	return "core.constant"
}

func (p *ConstantProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	val, ok := config["value"]
	if !ok {
		return nil, fmt.Errorf("missing 'value' in config")
	}

	// We just pass the value inside an artifact of type Value
	art := artifact.Artifact{
		ID:       fmt.Sprintf("const_%v", val), // unique enough for mock
		Kind:     "Value",
		Metadata: map[string]any{"data": val},
	}

	return []artifact.Artifact{art}, nil
}
