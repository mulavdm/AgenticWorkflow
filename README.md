# AgenticWorkflow

> **Archived. Nothing here is actively developed.**
>
> This repository gathers a set of experiments built between May and October
> 2026 around one question: how much of a local, agent-driven application can
> a small Go platform own, so that the application itself stays thin? It is
> published as it stood, for reading. Issues and pull requests are not
> watched.

## What is here

| Folder | What it was |
| --- | --- |
| [`ArtifactFlow/`](ArtifactFlow/) | The platform. It executes a workflow as a DAG of processors, keeps every run on a SQLite "blackboard" so a run can be inspected, paused, resumed and replayed, and starts and supervises the model runtimes the processors need: a Rust sidecar over FlatBuffers IPC, a Python sidecar, llama.cpp and whisper.cpp. |
| [`CodingAgentsGO/`](CodingAgentsGO/) | The largest consumer: a council of specialist LLM agents (analyst, planner, critic, red team, coder, synthesizer) for software tasks, with a tool protocol, approval-gated tool policy and a web dashboard. Its agent loop runs as ArtifactFlow nodes. |
| [`VideoScriptor/`](VideoScriptor/) | A small consumer: a workflow that extracts frames and audio from a video, transcribes and describes them, and writes a script. |
| [`CentralLogger/`](CentralLogger/) | The structured JSONL logging module the others share. |
| [`AlgoTrade/`](AlgoTrade/) | A separate research tool, not built on ArtifactFlow: it tests whether one stock's returns follow another's after a delay, with rolling lagged cross-correlation, a volatility cut-off and a local dashboard. The idea was inspired by [TradingAgents](https://github.com/TauricResearch/TradingAgents); no code or design comes from it. |

The files at the root tie the Go modules together: `go.work`, a test that
runs every module's tests, the Windows installer for CodingAgentsGO, and the
roadmap and notes the projects shared under [`docs/`](docs/).

## What a reader should know

- **It was developed on one Windows machine with an AMD GPU.** Named pipes,
  Windows job objects and the ROCm build notes reflect that. The Go code
  compiles for Windows from the root with `go build ./...` in each module of
  `go.work`; most of it does not compile for Linux.
- **A few things were changed for publication.** The fallbacks that named
  the development machine's own paths were removed from ArtifactFlow and
  CodingAgentsGO, and ArtifactFlow's whisper.cpp call reads its two paths
  from `WHISPER_CPP_CLI` and `WHISPER_CPP_MODEL`. Pasted design conversations,
  a file of stored embeddings, and copies of other projects' documentation
  were left out. Nothing else differs from what ran.
- **`AGENTS.md`, `CLAUDE.md` and `GEMINI.md`**, here and in each folder, are
  the instructions the coding agents working on these projects were given.
  They refer to a private workspace's documents, which are not published.
- **External tools are not included.** llama.cpp, whisper.cpp and
  stable-diffusion.cpp were separate checkouts, and no model files are here.

## License

Licensed under either of

- the Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE) or
  <http://www.apache.org/licenses/LICENSE-2.0>), or
- the MIT license ([LICENSE-MIT](LICENSE-MIT) or
  <http://opensource.org/licenses/MIT>),

at your option.
