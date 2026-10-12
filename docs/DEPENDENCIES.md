# DEPENDENCIES.md

## Dependency Policy

This document defines when to use external dependencies and when to write code ourselves.

The goal is not to avoid dependencies completely. The goal is to avoid unnecessary dependency risk while still relying on mature, battle-tested implementations for hard, security-sensitive, or standards-heavy problems.

A good engineering culture should be able to say both:

> Do not reinvent cryptography.

and:

> Do not import a package for ten lines of ordinary application logic.

## Core Principle

Use external dependencies when the problem requires deep expertise, complex standards compliance, security guarantees, or large amounts of testing that would be unrealistic to reproduce internally.

Write the code yourself when the logic is small, project-specific, central to your architecture, or important for long-term understanding and control.

The basic question is:

> Is this implementing a difficult external standard, or is this implementing our own system?

If it is a difficult external standard, prefer a mature dependency.

If it is our own system, prefer writing it ourselves unless there is a strong reason not to.

## Almost Always Use a Mature Dependency

These areas are dangerous or complex enough that writing them yourself is usually a mistake outside of education or research.

### Cryptography

Use established libraries for encryption, signing, hashing, key exchange, random number generation, and secure protocols.

Do not implement your own cryptographic algorithms.

Examples:

- AES
- ChaCha20-Poly1305
- Ed25519
- RSA
- ECDSA
- HMAC
- HKDF
- Secure random number generation

Reason: cryptography fails silently. A system can appear to work while being completely insecure.

### TLS, HTTPS, and Certificate Validation

Use existing TLS stacks and standard HTTPS implementations.

Do not write your own TLS protocol handling or certificate validation logic.

Reason: TLS involves protocol details, downgrade attacks, certificate chains, hostname validation, expiration, revocation, and many subtle edge cases.

### Password Hashing

Use established password hashing algorithms and libraries.

Acceptable examples:

- Argon2id
- bcrypt
- scrypt

Do not store plaintext passwords. Do not use plain SHA-256, SHA-512, or MD5 for passwords.

Reason: password hashing requires salts, memory hardness, configurable cost parameters, and resistance against GPU/ASIC attacks.

### Compression

Use established implementations for compression formats.

Examples:

- gzip
- zstd
- brotli
- deflate
- xz

Reason: compression formats are full of edge cases and can expose security issues such as decompression bombs or parser bugs.

### Image, Audio, and Video Codecs

Use mature libraries for complex media formats.

Examples:

- PNG
- JPEG
- WebP
- AV1
- H.264/H.265
- MP3
- AAC
- Opus

Reason: media formats are complex, often attacker-controlled, and historically common sources of vulnerabilities.

### Database Engines

Use existing database engines.

Examples:

- SQLite
- PostgreSQL
- MySQL/MariaDB
- DuckDB
- Redis

Do not write your own database for production storage unless the database itself is the project.

Reason: databases require years of work around indexing, transactions, crash recovery, locking, query planning, durability, corruption handling, and concurrency.

### Complex Parsers for Public Standards

Use mature parsers for large, messy, or security-sensitive public formats.

Examples:

- HTML
- XML
- PDF
- Unicode
- JSON Web Tokens
- ASN.1
- SQL
- Email/MIME

Reason: public formats have large specifications, real-world inconsistencies, malicious inputs, and many compatibility edge cases.

### WebAssembly Runtimes and Sandboxes

Use existing runtimes for executing untrusted or semi-trusted code.

Examples:

- Wasmtime
- Wasmer
- V8
- QuickJS, when JavaScript embedding is needed

Reason: sandboxing is security-sensitive and extremely difficult to get right.

## Usually Use a Dependency, But Consider the Context

These areas can be written internally, especially for learning or small systems, but using a dependency is often practical.

### HTTP Servers and Routers

Use the standard library where possible, especially in Go.

External routers or frameworks are acceptable when they add clear value.

Use a dependency when you need:

- Mature middleware ecosystem
- Complex routing
- WebSocket support
- OpenAPI integration
- Authentication integrations
- Production-proven framework conventions

Write it yourself when:

- The routing is simple
- You want full control
- The standard library is enough
- You are building a small service or internal tool

### CLI Parsers

Use a dependency for complex command-line tools.

Write it yourself for small tools with a few flags.

Examples of when a library helps:

- Subcommands
- Help generation
- Shell completion
- Validation
- Configuration integration

### Logging

Use simple standard-library logging for small projects.

Use a mature structured logger when logs are important for production operations.

Use a dependency when you need:

- Structured logs
- JSON output
- Log levels
- Context fields
- Production observability integration

### Configuration

Write simple configuration loading yourself when using a small config file or environment variables.

Use a dependency when you need:

- Multiple config sources
- Validation
- Hot reload
- Secrets integration
- Complex file formats

### Template Engines

Use the standard library if it is good enough.

In Go, `html/template` is often enough and has strong escaping behavior.

Use an external template engine only when you need major ergonomic or architectural benefits.

### ORMs and Query Builders

Prefer writing SQL directly for small and medium projects.

Use an ORM or query builder when:

- The domain model is large
- Migrations are complex
- You need portability across databases
- The team benefits from conventions

Avoid ORMs when they hide important database behavior or make performance harder to reason about.

### Message Queues

Use existing message brokers for production distributed systems.

Examples:

- NATS
- RabbitMQ
- Kafka
- Redis streams

Writing a tiny in-process queue is fine.

Writing a durable distributed queue is usually not.

### Search Engines

Use existing search systems for production-grade search.

Examples:

- PostgreSQL full-text search
- Meilisearch
- Typesense
- OpenSearch/Elasticsearch

Writing a small search index is good for learning or specialized local systems.

## Often Better to Write Yourself

These areas are commonly over-dependency-heavy. Prefer internal code unless a dependency provides clear and substantial value.

### Small Utility Functions

Do not add dependencies for tiny helpers.

Examples:

- String trimming helpers
- Simple validation
- Small collection transformations
- Basic formatting
- Small math helpers

If the code is short, obvious, and stable, write it yourself.

### Project-Specific Business Logic

Write your own business logic.

Examples:

- User workflow rules
- Project permissions
- Internal scoring systems
- Domain-specific validation
- Application-specific state transitions

Reason: this is where your system's identity lives. Hiding it inside generic frameworks can make the architecture harder to understand.

### Workflow Engines for Your Own Architecture

If the workflow model is central to the project, write it yourself or keep dependencies minimal.

Examples:

- DAG execution
- Agent orchestration
- Task scheduling logic
- Pipeline definitions
- Local AI workflow control
- Artifact routing

Reason: the workflow engine is often the core intellectual property and architectural heart of the system.

### Plugin Architecture

Use dependencies for low-level loading or sandboxing if needed, but design the plugin model yourself.

Examples:

- Tool interfaces
- Capability declarations
- Input/output contracts
- Versioning rules
- Permission boundaries

Reason: plugin architecture shapes the whole system. It should match the project, not a generic framework's assumptions.

### Internal Data Structures

Write simple internal structures yourself.

Examples:

- Graphs
- DAGs
- Trees
- Small indexes
- Registries
- In-memory caches

Use libraries only when the data structure is highly specialized, performance-critical, or difficult to implement correctly.

### Validation Rules

Prefer writing validation rules directly unless they involve a major external standard.

Write yourself:

- Username rules
- Project naming rules
- Internal schema checks
- Business-specific constraints

Use dependencies for:

- Email parsing
- URL parsing
- Unicode normalization
- International phone numbers
- Complex public identifiers

## Dependency Decision Checklist

Before adding a dependency, ask:

1. Is this solving a hard security problem?
2. Is this implementing a complex public standard?
3. Is this likely to have many edge cases?
4. Is this dependency actively maintained?
5. Is the API stable?
6. Is the license acceptable?
7. How many transitive dependencies does it add?
8. Could this be replaced by the standard library?
9. Could we write this ourselves in less than a few hundred lines?
10. Would writing it ourselves increase understanding and control?
11. Would writing it ourselves create serious hidden risk?
12. Is this dependency central to the architecture or just a helper?
13. If the dependency disappeared tomorrow, how painful would replacement be?
14. Does this dependency force a framework or worldview onto the project?
15. Can we isolate it behind our own interface?

## Dependency Risk Levels

### Low Risk

Usually acceptable.

Characteristics:

- Mature
- Widely used
- Maintained by reputable developers or organizations
- Few transitive dependencies
- Stable API
- Clear license
- Easy to replace

Examples:

- Well-known database drivers
- Official SDKs
- Standard crypto/TLS libraries
- Mature parser libraries

### Medium Risk

Accept with caution.

Characteristics:

- Useful but not essential
- Moderate transitive dependency tree
- Maintained but not critical infrastructure
- Can be isolated behind an interface

Examples:

- CLI frameworks
- Logging libraries
- Web routers
- Configuration libraries

### High Risk

Avoid unless strongly justified.

Characteristics:

- Unmaintained
- Unknown maintainer
- Large dependency tree
- Runs build scripts
- Security-sensitive
- Hard to replace
- Takes over project architecture
- Poor documentation
- Unclear license

Examples:

- Random authentication libraries
- Tiny packages for trivial utilities
- Abandoned wrappers around important systems
- Frameworks that force total lock-in

## Language-Specific Notes

### Go

Go has a strong standard library. Prefer it by default.

Good standard-library areas:

- HTTP servers
- Templates
- JSON
- Cryptography primitives
- TLS
- Testing
- Contexts
- File handling
- Basic logging
- SQL abstraction

In Go, avoid dependencies unless they clearly improve the project.

Good reasons to add Go dependencies:

- Database driver
- WebSocket support
- Structured logging
- Migrations
- Specific API SDK
- Serious CLI framework
- Production metrics/tracing

### Rust

Rust has a powerful crate ecosystem, but dependency trees can grow quickly.

Common core crates:

- `serde` for serialization
- `tokio` for async runtime
- `clap` for CLI parsing
- `anyhow`/`thiserror` for error handling
- `tracing` for structured diagnostics

Be especially careful with:

- Large transitive dependency trees
- Abandoned crates
- Crates using significant `unsafe`
- Build scripts
- Security-sensitive crates

Rust is strongest when dependencies are chosen deliberately and audited, not imported casually.

### Zig

Zig encourages minimal dependencies and direct control.

Prefer writing small pieces yourself.

Use external code mainly for:

- C library interop
- Mature codecs
- Cryptography
- Database clients
- Platform-specific bindings

Zig is a good fit when you want a self-contained system and are willing to own more code.

### C and C++

C and C++ often use fewer package-manager dependencies, but they still rely heavily on libraries.

Common dependencies:

- OpenSSL/BoringSSL
- Boost
- Qt
- fmt
- spdlog
- protobuf
- gRPC
- SQLite
- libcurl

C++ can feel more self-contained, but dependency management, ABI issues, build systems, and memory safety risks must be considered.

## When Reinventing Is Good

Reinventing is good when the goal is learning, control, or architecture.

Good educational projects:

- Interpreter
- Compiler
- HTTP server
- Template engine
- Small database
- Search index
- Vector index
- Message queue
- Entity-component system
- DAG executor

These should not necessarily be used in production immediately, but they deepen understanding.

## When Reinventing Is Dangerous

Reinventing is dangerous when failure is subtle, security-critical, or standards-heavy.

Avoid reinventing:

- Cryptography
- TLS
- Password hashing
- Certificate validation
- Authentication protocols
- Complex parsers for hostile input
- Sandboxes
- Database storage engines
- Media codecs
- Compression formats

## Preferred Project Philosophy

For long-term maintainable systems:

1. Start with the standard library.
2. Add dependencies only when the value is clear.
3. Prefer fewer, better dependencies.
4. Avoid tiny dependencies for trivial code.
5. Avoid framework lock-in unless the framework is the point.
6. Isolate important dependencies behind internal interfaces.
7. Audit dependencies before using them in security-sensitive code.
8. Treat dependencies as part of the codebase, not magic.
9. Use mature libraries for dangerous standards-heavy problems.
10. Write project-specific architecture yourself.

## Practical Rule of Thumb

Use a dependency when the problem is:

- Security-sensitive
- Standards-heavy
- Algorithmically subtle
- Performance-critical and already solved well
- Too large to maintain yourself
- Not central to your project's unique architecture

Write it yourself when the problem is:

- Small
- Clear
- Project-specific
- Central to your architecture
- Important for learning
- Easy to test
- Better served by direct control

## Final Summary

Dependencies are not bad. Unexamined dependencies are bad.

Writing everything yourself is not noble if it creates fragile, insecure code.

Importing everything is not efficient if it creates dependency rot, supply-chain risk, and architectural confusion.

The right approach is selective dependency minimalism:

> Use mature libraries for hard external problems.  
> Write your own code for the system that makes your project unique.
