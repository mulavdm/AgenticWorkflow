# Software Engineering Principles for Agents

This document describes practical software engineering principles that coding agents should follow when building, modifying, reviewing, or maintaining software. The goal is to produce code that is idiomatic, reliable, understandable, testable, secure, and easy for humans to keep evolving.

## 1. Understand Before Changing

- Read the existing code, tests, documentation, and project structure before making edits.
- Prefer the project's established patterns over inventing a new style.
- Identify the real problem being solved, not only the visible symptom.
- Keep the scope of a change as small as the task allows.
- Avoid unrelated refactors while fixing a bug or adding a feature.
- Preserve user or teammate changes unless explicitly asked to replace them.

## 2. Use Idiomatic Language and Framework Conventions

- Write code in the idiomatic style of the language being used.
- Use standard formatters, linters, package managers, and project tooling where available.
- Follow common naming, layout, error handling, dependency, and module conventions for the language.
- Prefer standard library features and well-maintained ecosystem libraries over custom implementations.
- Avoid translating patterns from one language into another when they do not fit naturally.
- Respect framework lifecycle, state management, routing, rendering, dependency injection, and configuration patterns.

## 3. Optimize for Maintainability

- Make code easy to read before making it clever.
- Use clear names that describe intent and domain meaning.
- Keep functions, classes, modules, and files focused on one coherent responsibility.
- Separate business logic from infrastructure, presentation, persistence, and transport concerns.
- Avoid unnecessary abstraction, but introduce one when it removes real duplication or complexity.
- Make invalid states hard to represent where the language allows it.
- Keep public APIs stable and documented when other code depends on them.

## 4. Design Modular Systems

- Prefer small, composable units with explicit inputs and outputs.
- Minimize hidden global state and implicit coupling.
- Encapsulate implementation details behind clear interfaces.
- **Hide Infrastructure Complexity:** A well-designed framework should encapsulate messy infrastructure (like spawning background daemons, managing IPC, or opening databases). Expose a clean, high-level API to downstream consumers so they can focus entirely on business logic rather than polyglot deployment.
- Keep dependencies flowing in sensible directions.
- Avoid circular dependencies.
- Design modules so they can be tested independently.
- Make integration boundaries clear: database, network, filesystem, queue, cache, UI, external service.
- **Embrace Raw Code APIs Over UI Wrappers:** When building complex local ML pipelines, prefer dropping down to raw library primitives (e.g., `transformers`, `diffusers`, `bitsandbytes`) instead of relying on high-level visual node abstractions. This allows total structural control over latents, quantizations, and inference loops.

## 5. Write Correct and Robust Code

- Handle errors deliberately instead of ignoring them.
- Validate inputs at trust boundaries.
- Be explicit about nullability, optional values, empty states, and default behavior.
- Consider edge cases such as zero items, duplicate values, large inputs, timeouts, retries, partial failure, and concurrent access.
- Avoid race conditions, resource leaks, deadlocks, and unbounded memory growth.
- Make operations idempotent when retries are expected.
- Prefer deterministic behavior for core logic.

## 6. Test Meaningfully

- Add or update tests when behavior changes.
- Prefer tests that verify observable behavior over implementation details.
- Cover the important paths: success, failure, edge cases, regressions, and integration boundaries.
- Keep unit tests fast and focused.
- Use integration or end-to-end tests when correctness depends on multiple components working together.
- Avoid brittle tests that rely on timing, ordering, private internals, or excessive mocking.
- When fixing a bug, add a regression test when practical.
- Run the relevant test suite before declaring work complete.

## 7. Profile and Benchmark Before Optimizing

- Do not optimize based only on intuition when measurement is possible.
- Establish a baseline before making performance changes.
- Use profilers, benchmarks, traces, logs, or metrics appropriate to the language and runtime.
- Optimize the bottleneck, not the most visible code.
- Keep benchmarks reproducible and representative of real workloads.
- Consider algorithmic complexity before micro-optimizations.
- After optimizing, verify both performance improvement and behavioral correctness.
- Document important performance assumptions.

## 8. Build for Observability

- Add useful logs, metrics, traces, and diagnostics where they help operate the system.
- Log events with enough context to debug issues without exposing secrets.
- Prefer structured logging where supported.
- Surface actionable error messages to users and operators.
- Make failures visible instead of silently swallowing them.
- Include health checks, readiness checks, or status reporting for long-running services where appropriate.

## 9. Treat Security as a Default Requirement

- Never hard-code secrets, credentials, tokens, or private keys.
- Validate and sanitize untrusted input.
- Use parameterized queries or safe database APIs.
- Avoid unsafe deserialization and dynamic code execution.
- Apply least privilege to files, network access, credentials, and service permissions.
- Keep dependencies updated and avoid abandoned packages when alternatives exist.
- Handle authentication, authorization, and session state carefully.
- Avoid leaking sensitive data through logs, errors, telemetry, caches, or test fixtures.
- **Safetensors and Checkpoint Security:** Avoid legacy Python pickle (`.bin` or `.pt`) loading when possible. Always prefer `.safetensors` for ML model weights to prevent arbitrary code execution and sidestep aggressive framework vulnerability mitigations (e.g., PyTorch CVE lockouts).

## 10. Manage Dependencies Carefully

- Add dependencies only when they provide clear value.
- Prefer mature, maintained, well-documented libraries.
- Check license, maintenance activity, security posture, size, and transitive dependencies when relevant.
- Keep dependency versions consistent with the project.
- Avoid duplicating libraries that solve the same problem.
- Do not introduce a heavy framework for a narrow utility need.
- Account for OS-specific limitations and native API availability before strictly enforcing a zero-dependency rule.

## 11. Preserve Compatibility and Migration Safety

- Understand callers, consumers, data formats, and deployment environments before changing contracts.
- Avoid breaking public APIs unless the task explicitly requires it.
- Use migrations for schema changes and make them reversible where practical.
- Plan data migrations for large or production datasets.
- Support backward and forward compatibility during rolling deploys when needed.
- Handle old configuration, cached data, and persisted state gracefully.

## 12. Keep Configuration Explicit

- Separate configuration from code.
- Use environment variables, config files, or project-standard configuration systems.
- Provide safe defaults where appropriate.
- Validate configuration early at startup.
- Document required configuration values.
- Avoid hidden behavior controlled by undocumented flags.

## 13. Respect Data Integrity

- Treat persisted data carefully.
- Use transactions when multiple writes must succeed or fail together.
- Enforce constraints at the right layer: type system, validation, database, or domain model.
- Avoid lossy transformations unless they are intentional and documented.
- Be careful with time zones, locales, currency, precision, encoding, and ordering.
- Make backups, rollback paths, or dry runs available for risky data operations.

## 14. Favor Simple, Clear User Experiences

- Build workflows that match the user's real task.
- Make errors recoverable and understandable.
- Avoid surprising state changes.
- Preserve user input where possible.
- Keep interfaces accessible, responsive, and consistent.
- Make loading, empty, disabled, and failure states explicit.
- Avoid adding visible instructional clutter when the interface can be self-explanatory.

## 15. Document What Matters

- Write documentation for decisions, contracts, setup, deployment, operations, and non-obvious behavior.
- Keep comments close to code and use them to explain why, not what.
- Update documentation when behavior, configuration, or workflows change.
- **Open Knowledge Format (OKF)**: Use OKF bundles (`okf/` directories) to maintain a living, machine-readable and human-browsable wiki of the codebase's architecture, components, runtimes, and integrations. Maintain strict parity between codebase updates and OKF concepts in the same commit.
- Prefer examples for APIs and complex workflows.
- Record tradeoffs when a decision has meaningful alternatives.

## 16. Review With Care

- Review for correctness, maintainability, security, performance, tests, and user impact.
- Prioritize concrete risks over style preferences.
- Point to specific files, lines, behaviors, or scenarios.
- Distinguish blocking issues from suggestions.
- Check that tests cover the changed behavior.
- Look for missing migrations, compatibility risks, error handling gaps, and observability gaps.

## 17. Automate Repetitive Quality Checks

- Use formatting, linting, type checking, tests, dependency checks, and build validation in automation.
- Prefer repeatable commands over manual verification.
- Keep continuous integration fast enough that developers use it.
- Make failures easy to understand and reproduce locally.
- Avoid flaky automation; fix or quarantine unreliable checks.
- Be aware of OS execution environments and security policies when building cross-platform automation scripts.

## 18. Work Incrementally

- Make small, reviewable changes.
- Verify after each meaningful step.
- Prefer working software over large unfinished rewrites.
- **Architectural Mocking First:** When building complex orchestrations (like DAG workflows or multi-agent pipelines), implement the core structural routing first using *mocked* processors (e.g. relying on `time.Sleep` and static data). Only wire up expensive, slow, or complex real APIs once the structural mechanics are proven end-to-end.
- Keep commits focused when commits are requested.
- When a task is risky, create checkpoints through tests, feature flags, or staged rollout.
- Communicate assumptions, limitations, and verification performed.

## 19. Be Performance-Aware by Design

- Choose data structures and algorithms appropriate to expected scale.
- Avoid unnecessary network calls, database queries, rendering work, serialization, and allocation.
- Batch or cache work when it improves real workloads without harming correctness.
- Set timeouts for external calls.
- Avoid unbounded queues, recursive calls, retries, and fan-out.
- Consider startup time, memory usage, latency, throughput, and resource cleanup.

## 20. Prefer Clarity in Agent Behavior

- Explain important assumptions before acting on them when they affect the result.
- Ask questions only when the answer cannot be inferred and the risk of guessing is high.
- Keep changes aligned with the user's actual request.
- Verify work with available tools before presenting it as done.
- Report what changed, how it was tested, and any remaining risks.
- Do not hide failed commands, skipped tests, or uncertain behavior.

## Practical Completion Checklist

Before considering a change complete, an agent should usually confirm:

- The code follows the idioms and tooling of the language and framework.
- The change is scoped to the requested behavior.
- The design remains modular and maintainable.
- Important edge cases and error paths are handled.
- Relevant tests were added or updated.
- Relevant tests, builds, formatters, linters, or type checks were run.
- Performance-sensitive work was measured or justified.
- Security and data integrity risks were considered.
- Documentation or comments were updated where useful.
- Any limitations, skipped verification, or follow-up risks are clearly stated.

