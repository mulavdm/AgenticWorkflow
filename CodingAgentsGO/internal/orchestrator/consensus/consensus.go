package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type Agent interface {
	Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error)
	GetRole() types.Role
}

// Engine handles multi-agent swarm logic and consensus merging.
type Engine struct {
	CouncilSize int
}

type evaluationResult struct {
	Score          int      `json:"score"`
	Contradictions []string `json:"contradictions"`
}

func NewEngine(size int) *Engine {
	return &Engine{CouncilSize: size}
}

// ResolveSwarm executes multiple agents in parallel, evaluates them using a critic + programmatic checks, and uses a synthesizer to merge them.
func (e *Engine) ResolveSwarm(ctx context.Context, state *types.CouncilState, agent Agent, critic Agent, synth Agent, instructions string, swarmSize int, bc types.Broadcaster) (types.AgentResponse, error) {
	if swarmSize <= 0 {
		swarmSize = e.CouncilSize
	}
	log.Printf("[Consensus] Spawning swarm for %s (size: %d)", agent.GetRole(), swarmSize)

	results := make(chan types.AgentResponse, swarmSize)
	errors := make(chan error, swarmSize)

	for i := 0; i < swarmSize; i++ {
		go func(id int) {
			localState := state.Clone()
			r, err := agent.Respond(ctx, localState, instructions)
			if err != nil {
				errors <- err
				return
			}
			results <- r
		}(i)
	}

	var rawResponses []types.AgentResponse
	for i := 0; i < swarmSize; i++ {
		select {
		case res := <-results:
			rawResponses = append(rawResponses, res)
		case err := <-errors:
			log.Printf("[Consensus] Swarm agent failed: %v", err)
		case <-time.After(45 * time.Second):
			log.Printf("[Consensus] Swarm agent timed out")
		}
	}

	if len(rawResponses) == 0 {
		return types.AgentResponse{}, fmt.Errorf("swarm failed: no agents returned valid responses")
	}

	// 1. Evaluate responses in parallel
	type scoredResponse struct {
		content        string
		score          int
		contradictions []string
		metadata       map[string]interface{}
		usage          *types.Usage
	}

	var wg sync.WaitGroup
	scoredChan := make(chan scoredResponse, len(rawResponses))

	for _, resp := range rawResponses {
		wg.Add(1)
		go func(r types.AgentResponse) {
			defer wg.Done()
			score, contradictions := e.evaluate(ctx, state, critic, agent.GetRole(), r.Content)
			scoredChan <- scoredResponse{
				content:        r.Content,
				score:          score,
				contradictions: contradictions,
				metadata:       r.Metadata,
				usage:          r.Usage,
			}
		}(resp)
	}

	wg.Wait()
	close(scoredChan)

	var evaluated []scoredResponse
	var best scoredResponse
	hasBest := false

	for s := range scoredChan {
		evaluated = append(evaluated, s)
		if !hasBest || s.score > best.score {
			best = s
			hasBest = true
		}
	}

	// 2. Filter low-confidence responses (score < 4), but keep at least the best one if all are filtered out
	var filtered []scoredResponse
	var contradictionMatrix []string

	for _, s := range evaluated {
		if s.score >= 4 {
			filtered = append(filtered, s)
		}
		if len(s.contradictions) > 0 {
			contradictionMatrix = append(contradictionMatrix, s.contradictions...)
		}
	}
	if len(filtered) == 0 && hasBest {
		filtered = append(filtered, best)
	}

	// 3. Synthesize
	var mergePrompt string
	if len(filtered) == 1 {
		// Only one response survived, or they all collapsed to the best one.
		log.Printf("[Consensus] Swarm resolved to single highest-scoring response (score: %d)", filtered[0].score)
		if bc != nil {
			state.AddMessage(types.RoleSystem, "[COUNCIL CONSENSUS]: "+filtered[0].content, nil, bc)
		}
		return types.AgentResponse{Content: filtered[0].content, Metadata: filtered[0].metadata, Usage: filtered[0].usage}, nil
	}

	var sb strings.Builder
	for i, f := range filtered {
		sb.WriteString(fmt.Sprintf("RESPONSE #%d (Score: %d):\n%s\n---\n", i+1, f.score, f.content))
	}

	matrixStr := "None identified."
	if len(contradictionMatrix) > 0 {
		matrixStr = "- " + strings.Join(contradictionMatrix, "\n- ")
	}

	mergePrompt = fmt.Sprintf("The following are parallel agent responses for the %s phase. RESOLVE all contradictions and produce a SINGLE unified high-quality response.\n\nCONTRADICTIONS IDENTIFIED:\n%s\n\nRESPONSES:\n%s",
		agent.GetRole(), matrixStr, sb.String())

	merged, err := synth.Respond(ctx, state, mergePrompt)
	if err != nil {
		return types.AgentResponse{}, fmt.Errorf("consensus resolution failed: %w", err)
	}

	if bc != nil {
		state.AddMessage(types.RoleSystem, "[COUNCIL CONSENSUS]: "+merged.Content, nil, bc)
	}

	return types.AgentResponse{Content: merged.Content, Metadata: merged.Metadata, Usage: merged.Usage}, nil
}

func (e *Engine) evaluate(ctx context.Context, state *types.CouncilState, critic Agent, role types.Role, content string) (int, []string) {
	// Programmatic checks
	score := 10
	var contradictions []string

	// Check JSON syntax in tool call
	if strings.Contains(content, "[TOOL:") {
		re := regexp.MustCompile(`(?si)\[?\s*TOOL:\s*(\w+)\s*(?:,?\s*ARGS:\s*(\{.*?\}))?.*?\]?`)
		match := re.FindStringSubmatch(content)
		if len(match) > 2 && match[2] != "" {
			var temp map[string]interface{}
			if err := json.Unmarshal([]byte(match[2]), &temp); err != nil {
				score -= 4
				contradictions = append(contradictions, fmt.Sprintf("Malformed tool JSON arguments: %v", err))
			}
		}
	} else {
		// Missing handoff check (if no tool, we must have [NEXT: ...])
		if !strings.Contains(strings.ToLower(content), "[next:") {
			score -= 3
			contradictions = append(contradictions, "Missing explicit handoff [NEXT: role] or [NEXT: completed]")
		}
	}

	// Basic matching brackets/parentheses count
	brackets := map[rune]int{'{': 0, '}': 0, '[': 0, ']': 0, '(': 0, ')': 0}
	for _, char := range content {
		if _, ok := brackets[char]; ok {
			brackets[char]++
		}
	}
	if brackets['{'] != brackets['}'] || brackets['['] != brackets[']'] || brackets['('] != brackets[')'] {
		score -= 2
		contradictions = append(contradictions, fmt.Sprintf("Unbalanced parentheses/brackets (curly: %d/%d, square: %d/%d, round: %d/%d)",
			brackets['{'], brackets['}'], brackets['['], brackets[']'], brackets['('], brackets[')']))
	}

	// Critic LLM scoring (if available)
	if critic != nil {
		criticPrompt := fmt.Sprintf("You are the Swarm Critic. Evaluate the following proposed response for the %s phase. Rate its semantic quality and logical correctness on a scale from 1 to 10. Detect any logical contradictions or factual errors, and list them.\n\nRESPONSE TO EVALUATE:\n%s\n\nOutput your evaluation in JSON format exactly as follows:\n{\n  \"score\": <integer 1-10>,\n  \"contradictions\": [\"contradiction 1\", \"contradiction 2\"]\n}", role, content)
		criticResp, err := critic.Respond(ctx, state, criticPrompt)
		if err == nil {
			// Extract JSON from response
			var eval evaluationResult
			cleaned := criticResp.Content
			if idx := strings.Index(cleaned, "{"); idx != -1 {
				if endIdx := strings.LastIndex(cleaned, "}"); endIdx != -1 && endIdx > idx {
					cleaned = cleaned[idx : endIdx+1]
				}
			}
			if err := json.Unmarshal([]byte(cleaned), &eval); err == nil {
				// Critic LLM score replaces or adjusts the base score
				score = (score + eval.Score) / 2
				if len(eval.Contradictions) > 0 {
					contradictions = append(contradictions, eval.Contradictions...)
				}
			} else {
				log.Printf("[Consensus] Critic returned invalid JSON: %s", criticResp.Content)
			}
		} else {
			log.Printf("[Consensus] Critic failed to respond: %v", err)
		}
	}

	if score < 1 {
		score = 1
	}
	if score > 10 {
		score = 10
	}
	return score, contradictions
}

