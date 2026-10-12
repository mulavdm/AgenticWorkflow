package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
)

// Processor defines how a single step in a workflow executes.
// It receives a slice of Artifacts and produces a new slice of Artifacts.
// It should read from URIs and write to new URIs, never passing large files in memory.
type Processor interface {
	Name() string
	Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error)
}

// CapabilityProvider can be implemented by processors that require runtime
// capabilities such as a sidecar-backed service.
type CapabilityProvider interface {
	RequiredCapabilities() []string
}

// CapabilityManager owns lazy capability startup and validation.
type CapabilityManager interface {
	Ensure(ctx context.Context, name string) error
}

// CapabilityStatus describes the lifecycle state of a lazy local runtime.
type CapabilityStatus string

const (
	CapabilityRegistered CapabilityStatus = "registered"
	CapabilityMissing    CapabilityStatus = "missing"
	CapabilityStarting   CapabilityStatus = "starting"
	CapabilityReady      CapabilityStatus = "ready"
	CapabilityFailed     CapabilityStatus = "failed"
	CapabilityCancelled  CapabilityStatus = "cancelled"
)

// CapabilityState is a snapshot suitable for downstream status panels.
type CapabilityState struct {
	Name            string           `json:"name"`
	Status          CapabilityStatus `json:"status"`
	Error           string           `json:"error,omitempty"`
	UpdatedAt       time.Time        `json:"updated_at"`
	PID             int              `json:"pid,omitempty"`
	EndpointURL     string           `json:"endpoint_url,omitempty"`
	HealthURL       string           `json:"health_url,omitempty"`
	LogPath         string           `json:"log_path,omitempty"`
	StartupDuration string           `json:"startup_duration,omitempty"`
	RuntimeArgs     []string         `json:"runtime_args,omitempty"`
	Metadata        map[string]any   `json:"metadata,omitempty"`
}

// CapabilityStatusProvider exposes lazy runtime status without handing
// lifecycle control to downstream applications.
type CapabilityStatusProvider interface {
	CapabilityStatus(name string) CapabilityState
	CapabilityStatuses() []CapabilityState
}

// Registry holds the mappings of all available steps/plugins in the engine.
type Registry struct {
	processors   map[string]Processor
	capabilities CapabilityManager
}

func NewRegistry() *Registry {
	return &Registry{
		processors: make(map[string]Processor),
	}
}

func (r *Registry) Register(p Processor) {
	r.processors[p.Name()] = p
}

func (r *Registry) SetCapabilityManager(manager CapabilityManager) {
	r.capabilities = manager
}

func (r *Registry) Get(name string) (Processor, error) {
	p, ok := r.processors[name]
	if !ok {
		return nil, fmt.Errorf("processor '%s' not found in registry", name)
	}
	return p, nil
}

func (r *Registry) EnsureCapabilities(ctx context.Context, p Processor) error {
	if r.capabilities == nil {
		if cp, ok := p.(CapabilityProvider); ok && len(cp.RequiredCapabilities()) > 0 {
			return fmt.Errorf("processor %s requires capabilities but no capability manager is configured", p.Name())
		}
		return nil
	}
	cp, ok := p.(CapabilityProvider)
	if !ok {
		return nil
	}
	for _, capability := range cp.RequiredCapabilities() {
		if err := r.capabilities.Ensure(ctx, capability); err != nil {
			return fmt.Errorf("capability %s unavailable for processor %s: %w", capability, p.Name(), err)
		}
	}
	return nil
}

func (r *Registry) CapabilityStatus(name string) CapabilityState {
	if statusProvider, ok := r.capabilities.(CapabilityStatusProvider); ok {
		return statusProvider.CapabilityStatus(name)
	}
	return CapabilityState{Name: name, Status: CapabilityMissing, UpdatedAt: time.Now()}
}

func (r *Registry) CapabilityStatuses() []CapabilityState {
	if statusProvider, ok := r.capabilities.(CapabilityStatusProvider); ok {
		return statusProvider.CapabilityStatuses()
	}
	return nil
}

type CapabilityFunc func(context.Context) error

// LlamaCppConfig describes the local llama.cpp runtime ArtifactFlow owns.
// Downstream applications may inspect the resulting status metadata, but they
// should not use this as permission to supervise the process themselves.
type LlamaCppConfig struct {
	EndpointURL    string `json:"endpoint_url"`
	ModelPath      string `json:"model_path"`
	ModelName      string `json:"model_name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	ContextSize    int    `json:"context_size"`
	FlashAttention string `json:"flash_attention"`
	GPUOffload     string `json:"gpu_offload"`
}

const DefaultLlamaCppContextSize = 24576

func DefaultLlamaCppConfig() LlamaCppConfig {
	return LlamaCppConfig{
		Host:           "127.0.0.1",
		Port:           8081,
		ContextSize:    DefaultLlamaCppContextSize,
		FlashAttention: "auto",
		GPUOffload:     "999",
	}
}

func (c LlamaCppConfig) Metadata() map[string]any {
	return map[string]any{
		"endpoint_url":    c.EndpointURL,
		"model_path":      c.ModelPath,
		"model_name":      c.ModelName,
		"host":            c.Host,
		"port":            c.Port,
		"context_size":    c.ContextSize,
		"flash_attention": c.FlashAttention,
		"gpu_offload":     c.GPUOffload,
	}
}

type LocalCapabilityManager struct {
	mu       sync.Mutex
	started  map[string]bool
	handlers map[string]CapabilityFunc
	states   map[string]CapabilityState
}

func NewLocalCapabilityManager() *LocalCapabilityManager {
	return &LocalCapabilityManager{
		started:  make(map[string]bool),
		handlers: make(map[string]CapabilityFunc),
		states:   make(map[string]CapabilityState),
	}
}

func (m *LocalCapabilityManager) Register(name string, fn CapabilityFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[name] = fn
	m.states[name] = CapabilityState{Name: name, Status: CapabilityRegistered, UpdatedAt: time.Now()}
}

func (m *LocalCapabilityManager) RegisterWithMetadata(name string, metadata map[string]any, fn CapabilityFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[name] = fn
	m.states[name] = CapabilityState{Name: name, Status: CapabilityRegistered, UpdatedAt: time.Now(), Metadata: cloneCapabilityMetadata(metadata)}
}

func (m *LocalCapabilityManager) Ensure(ctx context.Context, name string) error {
	m.mu.Lock()
	if m.started[name] {
		state := m.states[name]
		state.Name = name
		state.Status = CapabilityReady
		state.Error = ""
		state.UpdatedAt = time.Now()
		m.states[name] = state
		m.mu.Unlock()
		return nil
	}
	fn, ok := m.handlers[name]
	if ok {
		state := m.states[name]
		state.Name = name
		state.Status = CapabilityStarting
		state.Error = ""
		state.UpdatedAt = time.Now()
		m.states[name] = state
	}
	m.mu.Unlock()
	if !ok {
		m.setState(name, CapabilityMissing, fmt.Sprintf("capability not registered: %s", name))
		return fmt.Errorf("capability not registered: %s", name)
	}
	if err := fn(ctx); err != nil {
		if ctx.Err() != nil {
			m.setState(name, CapabilityCancelled, ctx.Err().Error())
			return err
		}
		m.setState(name, CapabilityFailed, err.Error())
		return err
	}
	m.mu.Lock()
	m.started[name] = true
	state := m.states[name]
	state.Name = name
	state.Status = CapabilityReady
	state.Error = ""
	state.UpdatedAt = time.Now()
	m.states[name] = state
	m.mu.Unlock()
	return nil
}

func (m *LocalCapabilityManager) CapabilityStatus(name string) CapabilityState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state, ok := m.states[name]; ok {
		return cloneCapabilityState(state)
	}
	return CapabilityState{Name: name, Status: CapabilityMissing, UpdatedAt: time.Now()}
}

func (m *LocalCapabilityManager) CapabilityStatuses() []CapabilityState {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := make([]CapabilityState, 0, len(m.states))
	for _, state := range m.states {
		states = append(states, cloneCapabilityState(state))
	}
	return states
}

func (m *LocalCapabilityManager) UpdateCapabilityDetails(name string, details CapabilityState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[name]
	state.Name = name
	if details.PID != 0 {
		state.PID = details.PID
	}
	if details.EndpointURL != "" {
		state.EndpointURL = details.EndpointURL
	}
	if details.HealthURL != "" {
		state.HealthURL = details.HealthURL
	}
	if details.LogPath != "" {
		state.LogPath = details.LogPath
	}
	if details.StartupDuration != "" {
		state.StartupDuration = details.StartupDuration
	}
	if len(details.RuntimeArgs) > 0 {
		state.RuntimeArgs = append([]string(nil), details.RuntimeArgs...)
	}
	if len(details.Metadata) > 0 {
		if state.Metadata == nil {
			state.Metadata = map[string]any{}
		}
		for k, v := range details.Metadata {
			state.Metadata[k] = v
		}
	}
	state.UpdatedAt = time.Now()
	m.states[name] = state
}

func (m *LocalCapabilityManager) setState(name string, status CapabilityStatus, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[name]
	state.Name = name
	state.Status = status
	state.Error = msg
	state.UpdatedAt = time.Now()
	m.states[name] = state
}

func cloneCapabilityMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneCapabilityState(state CapabilityState) CapabilityState {
	state.Metadata = cloneCapabilityMetadata(state.Metadata)
	if len(state.RuntimeArgs) > 0 {
		state.RuntimeArgs = append([]string(nil), state.RuntimeArgs...)
	}
	return state
}

// UnmarshalConfig takes a raw map[string]any (parsed from YAML) and strictly
// unmarshals it into a target Go struct via JSON serialization.
func UnmarshalConfig(raw map[string]any, dest any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("failed to marshal raw config: %w", err)
	}

	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("failed to unmarshal into target config struct: %w", err)
	}

	return nil
}

func (m *LocalCapabilityManager) StopOtherLlamaModels(currentName string, stopFn func(name string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for activeCapName, isStarted := range m.started {
		if isStarted && strings.HasPrefix(activeCapName, "llama.cpp:") && activeCapName != currentName {
			// Extract model/repo name
			modelName := strings.TrimPrefix(activeCapName, "llama.cpp:")
			// Skip embedding models
			if strings.Contains(strings.ToLower(modelName), "embedding") {
				continue
			}

			// Stop the sidecar
			if err := stopFn(activeCapName); err == nil {
				m.started[activeCapName] = false
				state := m.states[activeCapName]
				state.Status = CapabilityRegistered
				state.UpdatedAt = time.Now()
				m.states[activeCapName] = state
			}
		}
	}
}
