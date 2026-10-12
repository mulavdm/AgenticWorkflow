package agenticworkflow

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestWorkspaceModules(t *testing.T) {
	modules := []string{
		"./ArtifactFlow/...",
		"./CodingAgentsGO/...",
		"./VideoScriptor/...",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", append([]string{"test"}, modules...)...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		t.Fatalf("workspace module tests failed: %v\n%s", err, output.String())
	}
}
