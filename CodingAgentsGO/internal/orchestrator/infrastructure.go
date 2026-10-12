package orchestrator

import (
	"github.com/user/cognitive-orchestrator/internal/workspace"
	"log"
)

func (c *Coordinator) EnableShadowMode(ctx string) error {
	sw, err := workspace.NewShadowWorkspace(ctx)
	if err != nil {
		return err
	}
	c.Shadow = sw
	return nil
}

func (c *Coordinator) CleanupShadow() {
	if c.Shadow != nil {
		log.Printf("[Coordinator] Cleaning up shadow workspace: %s", c.Shadow.ShadowPath)
		c.Shadow.Cleanup()
		c.Shadow = nil
	}
}
