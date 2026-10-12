package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type SessionLogger struct {
	BaseDir string
}

func NewSessionLogger(baseDir string) *SessionLogger {
	_ = os.MkdirAll(baseDir, 0755)
	return &SessionLogger{BaseDir: baseDir}
}

func (l *SessionLogger) LogSession(state *types.CouncilState) {
	filename := fmt.Sprintf("session_%s_%s.json", state.SessionID, time.Now().Format("20060102_150405"))
	path := filepath.Join(l.BaseDir, filename)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("[Logger] Error marshaling state: %v", err)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[Logger] Error writing session log: %v", err)
	} else {
		log.Printf("[Logger] Session trace saved to: %s", path)
	}
}
