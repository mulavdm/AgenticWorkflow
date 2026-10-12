package processor

import (
	"context"
	"errors"
	"testing"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
)

type capabilityProcessor struct{}

func (p capabilityProcessor) Name() string { return "needs.capability" }

func (p capabilityProcessor) RequiredCapabilities() []string { return []string{"rust.compute"} }

func (p capabilityProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	return nil, nil
}

func TestRegistryEnsuresProcessorCapabilities(t *testing.T) {
	registry := NewRegistry()
	manager := NewLocalCapabilityManager()
	called := false
	manager.Register("rust.compute", func(ctx context.Context) error {
		called = true
		return nil
	})
	registry.SetCapabilityManager(manager)

	if err := registry.EnsureCapabilities(context.Background(), capabilityProcessor{}); err != nil {
		t.Fatalf("EnsureCapabilities failed: %v", err)
	}
	if !called {
		t.Fatal("capability handler was not called")
	}
}

func TestRegistryFailsMissingCapability(t *testing.T) {
	registry := NewRegistry()
	registry.SetCapabilityManager(NewLocalCapabilityManager())

	if err := registry.EnsureCapabilities(context.Background(), capabilityProcessor{}); err == nil {
		t.Fatal("expected missing capability to fail")
	}
}

func TestRegistryPropagatesCapabilityError(t *testing.T) {
	registry := NewRegistry()
	manager := NewLocalCapabilityManager()
	want := errors.New("sidecar failed")
	manager.Register("rust.compute", func(ctx context.Context) error {
		return want
	})
	registry.SetCapabilityManager(manager)

	if err := registry.EnsureCapabilities(context.Background(), capabilityProcessor{}); !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

func TestRegistryExposesReadOnlyCapabilityStatuses(t *testing.T) {
	registry := NewRegistry()
	manager := NewLocalCapabilityManager()
	manager.Register("llama.cpp:model.gguf", func(ctx context.Context) error { return nil })
	registry.SetCapabilityManager(manager)

	states := registry.CapabilityStatuses()
	if len(states) != 1 || states[0].Name != "llama.cpp:model.gguf" || states[0].Status != CapabilityRegistered {
		t.Fatalf("unexpected capability statuses: %#v", states)
	}
	if got := registry.CapabilityStatus("llama.cpp:model.gguf"); got.Status != CapabilityRegistered {
		t.Fatalf("unexpected capability status: %#v", got)
	}
	if got := registry.CapabilityStatus("missing.runtime"); got.Status != CapabilityMissing {
		t.Fatalf("expected missing status without mutating manager, got %#v", got)
	}
}

func TestLocalCapabilityManagerStatusLifecycle(t *testing.T) {
	manager := NewLocalCapabilityManager()

	manager.Register("rust.compute", func(ctx context.Context) error { return nil })
	if got := manager.CapabilityStatus("rust.compute"); got.Status != CapabilityRegistered {
		t.Fatalf("expected registered status, got %s", got.Status)
	}

	if err := manager.Ensure(context.Background(), "rust.compute"); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	if got := manager.CapabilityStatus("rust.compute"); got.Status != CapabilityReady {
		t.Fatalf("expected ready status, got %s", got.Status)
	}

	if err := manager.Ensure(context.Background(), "missing.runtime"); err == nil {
		t.Fatal("expected missing runtime to fail")
	}
	if got := manager.CapabilityStatus("missing.runtime"); got.Status != CapabilityMissing {
		t.Fatalf("expected missing status, got %s", got.Status)
	}
}

func TestLocalCapabilityManagerPreservesStatusMetadata(t *testing.T) {
	manager := NewLocalCapabilityManager()
	cfg := DefaultLlamaCppConfig()
	cfg.EndpointURL = "http://127.0.0.1:8081/v1/chat/completions"
	cfg.ModelName = "Gemma-4-E4B-Q8_K_P.gguf"

	manager.RegisterWithMetadata("llama.cpp", cfg.Metadata(), func(ctx context.Context) error { return nil })
	if got := manager.CapabilityStatus("llama.cpp"); got.Status != CapabilityRegistered {
		t.Fatalf("expected registered status, got %s", got.Status)
	} else if got.Metadata["context_size"] != DefaultLlamaCppContextSize {
		t.Fatalf("expected context metadata to survive registration, got %#v", got.Metadata)
	}

	if err := manager.Ensure(context.Background(), "llama.cpp"); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	got := manager.CapabilityStatus("llama.cpp")
	if got.Status != CapabilityReady {
		t.Fatalf("expected ready status, got %s", got.Status)
	}
	if got.Metadata["flash_attention"] != "auto" || got.Metadata["gpu_offload"] != "999" {
		t.Fatalf("expected llama.cpp runtime metadata, got %#v", got.Metadata)
	}
}

func TestLocalCapabilityManagerFailureAndCancellationStatus(t *testing.T) {
	manager := NewLocalCapabilityManager()
	want := errors.New("boot failed")
	manager.Register("python.media", func(ctx context.Context) error { return want })
	if err := manager.Ensure(context.Background(), "python.media"); !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
	if got := manager.CapabilityStatus("python.media"); got.Status != CapabilityFailed || got.Error == "" {
		t.Fatalf("expected failed status with error, got %#v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager.Register("llama.cpp", func(ctx context.Context) error { return ctx.Err() })
	if err := manager.Ensure(ctx, "llama.cpp"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if got := manager.CapabilityStatus("llama.cpp"); got.Status != CapabilityCancelled {
		t.Fatalf("expected cancelled status, got %#v", got)
	}
}

func TestLocalCapabilityManagerUpdateCapabilityDetails(t *testing.T) {
	manager := NewLocalCapabilityManager()
	manager.Register("llama.cpp:model.gguf", func(ctx context.Context) error { return nil })
	if err := manager.Ensure(context.Background(), "llama.cpp:model.gguf"); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}

	manager.UpdateCapabilityDetails("llama.cpp:model.gguf", CapabilityState{
		PID:             1234,
		EndpointURL:     "http://127.0.0.1:8081/v1",
		HealthURL:       "http://127.0.0.1:8081/health",
		LogPath:         "logs/sidecar.log",
		StartupDuration: "1s",
		RuntimeArgs:     []string{"--embeddings"},
		Metadata:        map[string]any{"model_id": "model.gguf"},
	})

	got := manager.CapabilityStatus("llama.cpp:model.gguf")
	if got.PID != 1234 || got.EndpointURL == "" || got.HealthURL == "" || got.LogPath == "" || got.StartupDuration != "1s" {
		t.Fatalf("details were not merged into capability status: %#v", got)
	}
	if len(got.RuntimeArgs) != 1 || got.RuntimeArgs[0] != "--embeddings" {
		t.Fatalf("unexpected runtime args: %#v", got.RuntimeArgs)
	}
	got.RuntimeArgs[0] = "mutated"
	if again := manager.CapabilityStatus("llama.cpp:model.gguf"); again.RuntimeArgs[0] != "--embeddings" {
		t.Fatalf("runtime args snapshot was mutable: %#v", again.RuntimeArgs)
	}
}
