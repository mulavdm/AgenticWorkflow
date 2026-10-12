# Ollama Runtime

The **Ollama Runtime** integration allows ArtifactFlow to delegate inference requests to a local or remote Ollama server.

## Integration Architecture

*   **Communication Protocol**: Communicates via standard HTTP REST API endpoints.
*   **Default Connection URL**: `http://localhost:11434`
*   **Registry registration**: Instantiated in Go via:
    `app.Models.RegisterBackend(models.NewOllamaBackend())`

## Model Routing

When a downstream node requests a capability linked to an Ollama backend (for example, generating transcripts or text templates), the model manager makes HTTP requests to Ollama's `/api/generate` or `/api/chat` endpoints. The target model (e.g., `llama3`) is specified in the node configuration.

See also: [Capability Manager](../capability-manager/TDD.md), [VideoScriptor integration](../reference/integrations/videoscriptor.md)
