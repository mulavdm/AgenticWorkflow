package memory

import (
	"context"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	"github.com/user/cognitive-orchestrator/internal/types"
)

type ArtifactFlowStore struct {
	board *afstate.Blackboard
}

func NewArtifactFlowStore(board *afstate.Blackboard) *ArtifactFlowStore {
	return &ArtifactFlowStore{board: board}
}

func (s *ArtifactFlowStore) ArtifactFlowBoard() *afstate.Blackboard {
	return s.board
}

func (s *ArtifactFlowStore) SaveLesson(ctx context.Context, lesson Lesson) error {
	return s.board.SaveLesson(afstate.Lesson{
		Task:       lesson.Task,
		Critique:   lesson.Critique,
		Resolution: lesson.Resolution,
		Tags:       lesson.Tags,
	})
}

func (s *ArtifactFlowStore) SaveLessonsFromSession(ctx context.Context, sessionID string, messages []types.Message) error {
	var task, critique, resolution string
	for _, msg := range messages {
		switch msg.Role {
		case types.RoleUser:
			if task == "" {
				task = msg.Content
			}
		case types.RoleCritic:
			critique = msg.Content
		case types.RoleSynthesizer:
			resolution = msg.Content
		}
	}
	if task == "" || critique == "" || resolution == "" {
		return nil
	}
	return s.SaveLesson(ctx, Lesson{Task: task, Critique: critique, Resolution: resolution, Tags: "general"})
}

func (s *ArtifactFlowStore) GetRelevantLessons(ctx context.Context, query string) ([]Lesson, error) {
	lessons, err := s.board.SearchLessons(query, 5)
	if err != nil {
		return nil, err
	}
	out := make([]Lesson, 0, len(lessons))
	for _, lesson := range lessons {
		out = append(out, Lesson{
			ID:         lesson.ID,
			Task:       lesson.Task,
			Critique:   lesson.Critique,
			Resolution: lesson.Resolution,
			Tags:       lesson.Tags,
			CreatedAt:  lesson.CreatedAt,
		})
	}
	return out, nil
}

func (s *ArtifactFlowStore) GetVectorStore() types.VectorStore {
	return &ArtifactFlowVectorStore{board: s.board}
}

func (s *ArtifactFlowStore) Close() error {
	return s.board.Close()
}

type ArtifactFlowVectorStore struct {
	board *afstate.Blackboard
}

func (s *ArtifactFlowVectorStore) IndexMessage(ctx context.Context, msgID string, vector []float32, metadata map[string]string) error {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if metadata["kind"] == "" {
		if metadata["role"] == "code" || strings.HasPrefix(msgID, "code-") {
			metadata["kind"] = "code"
		} else {
			metadata["kind"] = "memory"
		}
	}
	if metadata["source_path"] == "" && metadata["phase"] != "" && metadata["kind"] == "code" {
		metadata["source_path"] = metadata["phase"]
	}
	runID := metadata["run_id"]
	return s.board.SaveVector(afstate.VectorEntry{
		ID:       msgID,
		RunID:    runID,
		TargetID: msgID,
		Vector:   vector,
		Metadata: metadata,
	})
}

func (s *ArtifactFlowVectorStore) SearchMessages(ctx context.Context, queryVector []float32, limit int) ([]string, error) {
	return s.SearchMessagesByKind(ctx, queryVector, limit, "")
}

func (s *ArtifactFlowVectorStore) SearchMessagesByKind(ctx context.Context, queryVector []float32, limit int, kind string) ([]string, error) {
	entries, err := s.board.QueryVectors(afstate.VectorQuery{
		Kind:       kind,
		Dimensions: len(queryVector),
	})
	if err != nil {
		return nil, err
	}
	return s.rankVectorEntries(ctx, entries, queryVector, limit)
}

func (s *ArtifactFlowVectorStore) rankVectorEntries(ctx context.Context, entries []afstate.VectorEntry, queryVector []float32, limit int) ([]string, error) {
	type result struct {
		id    string
		score float64
	}
	results := make([]result, 0, len(entries))
	for _, entry := range entries {
		results = append(results, result{id: entry.TargetID, score: artifactFlowCosineSimilarity(queryVector, entry.Vector)})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	threshold := 0.70
	if envThreshold := os.Getenv("VECTOR_MEM_THRESHOLD"); envThreshold != "" {
		if val, err := strconv.ParseFloat(envThreshold, 64); err == nil {
			threshold = val
		}
	}

	ids := make([]string, 0, limit)
	for i := 0; i < len(results) && len(ids) < limit; i++ {
		if results[i].score < threshold {
			break // Break early as subsequent results have lower scores
		}

		if strings.HasPrefix(results[i].id, "code-") {
			ids = append(ids, results[i].id)
			continue
		}

		// Cross-validate against the database at query time
		msg, err := s.board.GetMessage(results[i].id)
		if err != nil {
			continue // Message row missing/deleted, skip
		}
		if !msg.Visible || msg.Content == "" {
			continue // Message hidden or empty, skip
		}

		ids = append(ids, results[i].id)
	}
	return ids, nil
}

func (s *ArtifactFlowVectorStore) GetMessage(ctx context.Context, id string) (types.Message, error) {
	if strings.HasPrefix(id, "code-") {
		entry, err := s.board.GetVector(id)
		if err == nil {
			return types.Message{
				ID:      entry.ID,
				Role:    types.Role(entry.Metadata["role"]),
				Content: entry.Metadata["content"],
				Visible: true,
				Phase:   entry.Metadata["phase"],
			}, nil
		}
	}

	msg, err := s.board.GetMessage(id)
	if err != nil {
		return types.Message{}, err
	}

	var usage *types.Usage
	if msg.Usage != nil {
		usage = &types.Usage{}
		if td, ok := msg.Usage["total_duration"].(int64); ok {
			usage.TotalDuration = td
		}
		if ld, ok := msg.Usage["load_duration"].(int64); ok {
			usage.LoadDuration = ld
		}
		if pec, ok := msg.Usage["prompt_eval_count"].(float64); ok {
			usage.PromptEvalCount = int(pec)
		}
		if pec, ok := msg.Usage["prompt_eval_count"].(int); ok {
			usage.PromptEvalCount = pec
		}
		if ped, ok := msg.Usage["prompt_eval_duration"].(int64); ok {
			usage.PromptEvalDuration = ped
		}
		if ec, ok := msg.Usage["eval_count"].(float64); ok {
			usage.EvalCount = int(ec)
		}
		if ec, ok := msg.Usage["eval_count"].(int); ok {
			usage.EvalCount = ec
		}
		if ed, ok := msg.Usage["eval_duration"].(int64); ok {
			usage.EvalDuration = ed
		}
	}

	return types.Message{
		ID:        msg.ID,
		TraceID:   msg.RunID,
		Role:      types.Role(msg.Role),
		Model:     msg.Model,
		Content:   msg.Content,
		Visible:   msg.Visible,
		Timestamp: msg.CreatedAt,
		Phase:     msg.NodeID,
		Metadata:  msg.Metadata,
		Usage:     usage,
	}, nil
}

func (s *ArtifactFlowVectorStore) DeletePrefix(ctx context.Context, prefix string) error {
	return s.board.DeleteVectorsByPrefix(prefix)
}

func (s *ArtifactFlowVectorStore) Close() error { return nil }

func artifactFlowCosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
