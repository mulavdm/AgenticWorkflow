# VideoScriptor Application

**VideoScriptor** is a downstream application that builds multimodal screenplay scripts from video files. It leverages ArtifactFlow to handle DAG execution, capability matching, and sidecar management.

## Integration Entry Point

The application starts in `cmd/videoscriptor/main.go` of VideoScriptor, a separate repository, and sets up the ArtifactFlow runtime:

1.  **Framework Initialization**: Calls `framework.Init("videoscriptor.db")` which automatically sets up logging and launches the internal Rust compute sidecar.
2.  **Python Sidecar Registration**: Explicitly registers the Python sidecar for ML capability routing:
    ```go
    app.RegisterSidecar("videoscriptor-python", sidecarDir, []string{"uv", "run", "uvicorn", "main:app", "--port", "8000"}, "http://127.0.0.1:8000/health")
    ```
3.  **Model Mapping**: Declares model configurations across local and cloud backends:
    *   `whisper_model`: local GGUF running via `llama.cpp` for audio transcription.
    *   `vision_model`: Hugging Face model running via `transformers` for image description.
    *   `text_model`: local server model running via `ollama` for screenplay generation.

## Custom Processors

VideoScriptor registers its specific processing steps into the orchestrator registry:
*   **`MockVideoProcessor`**: Simulates pipeline video operations.
*   **`TranscribeProcessor`**: Wraps the Whisper model to generate text transcripts from audio.
*   **`VisionCaptionProcessor`**: Calls BLIP vision models to generate captions for frames.
*   **`ScriptMergeProcessor`**: Combines transcripts and frame captions into a screenplay format.

## Execution

Once the configurations and custom processors are registered, VideoScriptor loads a workflow definition (e.g. `workflows/dense-script.yaml`) and executes it:
```go
app.Engine.Run(context.Background(), wf, "video-run-python-sidecar")
```
All intermediate results, statuses, and logs are persisted directly in the `videoscriptor.db` Blackboard database.

See also: [Framework Package Component](../framework/TDD.md), [Python Sidecar Component](../python-sidecar/TDD.md), [Blackboard Architecture](../blackboard/TDD.md)
