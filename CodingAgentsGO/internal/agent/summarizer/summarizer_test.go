package summarizer

import (
	"strings"
	"testing"
)

func TestDeterministicFallback_DirectoryListing(t *testing.T) {
	s := &Summarizer{}
	input := `[OBSERVATION from explorer]: Directory listing for .
[DIR]  .agents/
[DIR]  .git/
[FILE] AGENTS.md (7236 bytes)
[DIR]  AlgoTrade/
[DIR]  ArtifactFlow/
[DIR]  CentralLogger/
[DIR]  CodingAgentsGO/
[DIR]  Open Knowledge Format(OKF)/
[FILE] ROADMAP.md (3239 bytes)
[DIR]  VideoScriptor/
[DIR]  WritingStudio/
[DIR]  bin/
[DIR]  docs/
[FILE] go.mod (34 bytes)
[FILE] go.work (223 bytes)
[FILE] go.work.sum (1921 bytes)
[FILE] install.ps1 (8980 bytes)
[DIR]  logs/
[DIR]  scratch/
[FILE] workspace_test.go (584 bytes)`

	summary := s.DeterministicFallback(input)

	if !strings.Contains(summary, "Deterministic listing of [OBSERVATION from explorer]: Directory listing for .") {
		t.Errorf("Expected directory listing header match, got: %s", summary)
	}

	if !strings.Contains(summary, "[TRUNCATED:") {
		t.Errorf("Expected truncated notice for >15 entries, got: %s", summary)
	}

	if strings.Contains(summary, "workspace_test.go") {
		t.Errorf("Expected workspace_test.go to be truncated (16th file), but it was present: %s", summary)
	}
}

func TestDeterministicFallback_Errors(t *testing.T) {
	s := &Summarizer{}
	input := `some setup line
another line
Failed: dial tcp 127.0.0.1:8080 connectex
another successful action
Error: database connection lost
panic: runtime error: invalid memory address
exit status 1`

	summary := s.DeterministicFallback(input)

	if !strings.Contains(summary, "Execution failed with errors") {
		t.Errorf("Expected execution failed message, got: %s", summary)
	}

	if !strings.Contains(summary, "dial tcp 127.0.0.1:8080") {
		t.Errorf("Expected relevant error log line, got: %s", summary)
	}

	if !strings.Contains(summary, "panic: runtime error") {
		t.Errorf("Expected panic log line, got: %s", summary)
	}
}

func TestDeterministicFallback_GenericTruncate(t *testing.T) {
	s := &Summarizer{}
	lines := make([]string, 30)
	for i := 0; i < 30; i++ {
		lines[i] = string(rune('A' + i))
	}
	input := strings.Join(lines, "\n")

	summary := s.DeterministicFallback(input)

	if !strings.Contains(summary, "Truncated large output") {
		t.Errorf("Expected truncated notice, got: %s", summary)
	}

	if !strings.Contains(summary, "... [TRUNCATED 10 lines]") {
		t.Errorf("Expected count of truncated lines to be 10, got: %s", summary)
	}
}
