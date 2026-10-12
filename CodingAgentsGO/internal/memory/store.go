package memory

import (
	"context"
	"github.com/user/cognitive-orchestrator/internal/types"
	"time"
)

// Lesson represents a specific piece of knowledge learned from a past session.
type Lesson struct {
	ID         int       `json:"id"`
	Task       string    `json:"task"`
	Critique   string    `json:"critique"`
	Resolution string    `json:"resolution"`
	Tags       string    `json:"tags"` // Comma-separated for simple SQL search
	CreatedAt  time.Time `json:"created_at"`
}

// Store defines the interface for persisting and retrieving agent experiences.
type Store interface {
	// SaveLesson persists a new insight.
	SaveLesson(ctx context.Context, lesson Lesson) error

	// SaveLessonsFromSession extracts and saves lessons from a set of session messages.
	SaveLessonsFromSession(ctx context.Context, sessionID string, messages []types.Message) error

	// GetRelevantLessons finds past experiences related to the current task.
	GetRelevantLessons(ctx context.Context, query string) ([]Lesson, error)

	// GetVectorStore returns the underlying vector memory, if available.
	GetVectorStore() types.VectorStore

	// Close releases any underlying resources.
	Close() error
}
