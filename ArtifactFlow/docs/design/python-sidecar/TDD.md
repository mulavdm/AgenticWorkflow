# Python Sidecar

The **Python Sidecar** (`python-sidecar`) houses the machine learning and deep learning capabilities of the ArtifactFlow platform, providing interfaces for model loading, embedding generation, text generation, and image captioning.

## Dependency Management

*   **UV Integration**: The sidecar uses the `uv` package manager to maintain fast, isolated, and reproducible dependency state.
*   **Configuration**: Configured via `pyproject.toml` and locked using `uv.lock`.
*   **Virtual Environment**: A local virtual environment (`.venv`) is initialized inside the `python-sidecar/` directory to isolate dependencies. This folder is ignored in version control.

## Service Runtime

*   **API Interface**: The sidecar is structured as a **FastAPI** web application (`main.py` and `model_manager.py`).
*   **Execution Command**: Typically launched using Uvicorn:
    `uv run uvicorn main:app --port 8000`
*   **Ready Polling**: When registered, the Go orchestrator blocks execution and polls the `http://127.0.0.1:8000/health` endpoint. Once it returns a status of 200, the sidecar is flagged as `ready`.

## Downstream ML Processing

The Python server acts as a backend service for ML processors. When Go registers an ML processor (e.g. transcription, image description), the processor delegates the actual tensor computation to the Python REST server endpoints, keeping Go code lightweight and free from heavy model dependencies.

See also: [Capability Manager](../capability-manager/TDD.md), [Transformers Runtime](../reference/runtimes/transformers.md), [VideoScriptor integration](../reference/integrations/videoscriptor.md)
