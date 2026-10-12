# llama.cpp Runtime

The **llama.cpp Runtime** executes GGUF models locally. It is managed by ArtifactFlow as an external compute capability.

## Folder Boundaries

To keep the repository clean, all code builds and model weight files are kept in directories ignored by version control:
*   **Source Checkout**: `bin/llama.cpp/`
*   **ROCm Build Path (Windows/AMD)**: `bin/llama.cpp/build-gfx1101-vs2022/`
*   **Model Weights**: `models/`
*   **Server Logs**: `logs/`

## Configuration Defaults

To prevent truncated generation during reasoning loops, the default local runner sets the following parameters:
*   **Context Window**: `--ctx-size 24576` (explicit token budget).
*   **Attention Mode**: `--flash-attn auto` (optimizes throughput).
*   **GPU Layer Offload**: `-ngl 999` (offloads as many layers as possible to VRAM).

## ROCm GPU Build on Windows

To compile llama.cpp with ROCm 7.1 support for AMD GPU hardware (e.g. `gfx1101`), developers run build steps detailing compiler variables and target graphics card models. The detailed guide is documented in [BUILDING_ROCM_GPU.md](/docs/BUILDING_ROCM_GPU.md).

See also: [Capability Manager](../capability-manager/TDD.md), [Python Sidecar Component](../python-sidecar/TDD.md)
