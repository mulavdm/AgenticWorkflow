package consensus

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type testMockAgent struct {
	role  types.Role
	resp  string
	fail  bool
	calls int32
}

func (m *testMockAgent) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	atomic.AddInt32(&m.calls, 1)
	if m.fail {
		return types.AgentResponse{}, context.Canceled
	}
	return types.AgentResponse{Content: m.resp}, nil
}

func (m *testMockAgent) GetRole() types.Role {
	return m.role
}

func TestResolveSwarm_FilteredAndMerged(t *testing.T) {
	agent := &testMockAgent{role: types.RoleCoder, resp: "proposed patch"}

	// Critic returns a score and contradictions in JSON
	critic := &testMockAgent{
		role: types.RoleCritic,
		resp: `{"score": 8, "contradictions": ["missing import"]}`,
	}

	// Synthesizer merges the outputs
	synth := &testMockAgent{
		role: types.RoleSynthesizer,
		resp: "final merged response",
	}

	state := types.NewCouncilState("session-123", "trace-123")
	engine := NewEngine(3)

	resp, err := engine.ResolveSwarm(context.Background(), state, agent, critic, synth, "instructions", 3, nil)
	if err != nil {
		t.Fatalf("ResolveSwarm failed: %v", err)
	}

	if resp.Content != "final merged response" {
		t.Fatalf("expected 'final merged response', got %q", resp.Content)
	}

	if got := atomic.LoadInt32(&agent.calls); got != 3 {
		t.Fatalf("expected agent to be called 3 times, got %d", got)
	}
	if got := atomic.LoadInt32(&critic.calls); got != 3 {
		t.Fatalf("expected critic to be called 3 times, got %d", got)
	}
	if got := atomic.LoadInt32(&synth.calls); got != 1 {
		t.Fatalf("expected synth to be called 1 time, got %d", got)
	}
}

func TestResolveSwarm_LowScoreCollapseToBest(t *testing.T) {
	agent := &testMockAgent{role: types.RoleCoder, resp: "poor response"}
	critic := &testMockAgent{
		role: types.RoleCritic,
		resp: `{"score": 0, "contradictions": ["all wrong"]}`, // score < 4
	}
	synth := &testMockAgent{role: types.RoleSynthesizer, resp: "merged"}

	state := types.NewCouncilState("session-123", "trace-123")
	engine := NewEngine(3)

	// Since all responses are below 4, it should collapse to the single best response instead of calling synthesizer.
	resp, err := engine.ResolveSwarm(context.Background(), state, agent, critic, synth, "instructions", 2, nil)
	if err != nil {
		t.Fatalf("ResolveSwarm failed: %v", err)
	}

	// Should not have called synth since it collapsed to the only best response (which is "poor response")
	if resp.Content != "poor response" {
		t.Fatalf("expected 'poor response' (collapsed), got %q", resp.Content)
	}
	if got := atomic.LoadInt32(&synth.calls); got != 0 {
		t.Fatalf("expected synth to not be called, got %d", got)
	}
}
