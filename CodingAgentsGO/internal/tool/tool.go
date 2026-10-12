package tool

import (
	"context"
	"sync"
)

// Tool defines the interface for any capability provided to the agents.
type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, args string) (string, error)
}

// WorkspaceTool is a tool that operates within a specific directory context.
type WorkspaceTool interface {
	Tool
	SetBasePath(path string)
}

// Registry maintains the available tools for the coordinator.
type Registry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []Tool
	for _, t := range r.tools {
		list = append(list, t)
	}
	return list
}

func (r *Registry) SetBasePath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tools {
		if wt, ok := t.(WorkspaceTool); ok {
			wt.SetBasePath(path)
		}
	}
}
