# TinyAgent Testing Strategy

The goal is not to run "as many cases as possible" but to cover the risk
surface with failure injection. TinyAgent can still fail if a component panics
unexpectedly, a model stream is malformed, a tool hangs, context grows without
bound, or the process-level environment kills it. The test suite is organized
in layers so each failure mode has a deterministic, no-LLM-cost test.

## Test Layers

Runtime lifecycle regressions additionally cover actual sequential execution
order, bounded parallelism, interrupted tool pairing, Hook panic policies and
audit visibility, cancellation safe points/escalation, committed stop conditions,
writeback exclusivity/errors/timeouts, independent session loads, safe cache
unloading, message ownership, bounded offload range reads, cross-session reference
rejection, summaries and audit ring ordering. `e2e/lifecycle_test.go` exercises
HTTP/SSE offload → scoped readback → final answer, including usage-only tail
chunks and Store commit before RunEnd. See [Runtime semantics](RUNTIME.md).

### 1. Protocol / Model Adapter

These tests drive the OpenAI-compatible client against an `httptest` server:

- request wire format, including assistant tool-call `content` field
- non-streaming and streaming responses
- tool-call fragments arriving across multiple SSE chunks
- HTTP 4xx/5xx errors
- malformed SSE payloads
- `finish_reason=length` with empty output (must not end silently)
- default `max_tokens` limits

### 2. Runner / Core Invariants

These tests use scripted models and real tools:

- plain response, tool loop, unknown tool, tool panic recovery
- max iterations, session busy, context cancellation
- parallel tool execution with result ordering
- panic isolation across parallel tools
- hooks, audit, context management
- long tool chains through the public `Agent` facade

### 3. Context Management

- long history is trimmed before the model sees it
- context actions are emitted
- truncation and offload strategies preserve tool pairing
- token counter includes tool schemas

### 4. Tool / CLI Harness

`cmd/tb2agent` and its tools are tested directly:

- shell command success/failure and captured stdout/stderr
- background processes must not block `run_shell`
- command timeout returns partial output instead of hanging
- oversized output is truncated
- file read/write/list/grep/find behavior and soft errors
- max iterations is a soft end for benchmarking
- empty final answer is a failure for benchmarking

### 5. End-to-End Robustness

`e2e/` drives the full public facade:

- streaming tool loop through the real OpenAI-compatible adapter
- multi-turn persistence and restore from store
- panic recovery through the facade
- model HTTP error, malformed stream, and truncated output injection
- long context budget and long tool chain

### 6. Real LLM Benchmark (TB2)

TB2 has a bounded automatic gate plus manual broader evaluation:

- automatic after CI passes: one real-model `fix-git` trial for same-repository
  PRs/main; fork PRs use clearly labeled oracle without credentials
- manual `smoke` preset: three easy/medium short tasks
- `coverage` preset: three longer medium tasks with higher token/iteration/time
  limits
- `oracle` agent mode verifies the Harbor pipeline without an LLM key
- verifier rewards, completion counts and exceptions are checked explicitly
- every run uploads `tb2-results-<run-id>-<attempt>`, including per-trial result.json, exception
  details, trial logs, and the full agent transcript

## Failure Taxonomy

When a benchmark trial fails, classify it before changing code:

- `VerifierFail`: task output is wrong; usually model capability or prompt.
- `AgentException`: agent process exited non-zero; inspect the transcript.
- `ModelTruncation`: `finish_reason=length` or malformed stream; raise the
  output budget or fix the adapter.
- `ToolHang`: a tool waits on a background process or long command; fix the
  tool, not the model.
- `ProcessLevel`: `os.Exit`, OOM, segfault; out of scope for an in-process
  library and must be documented, not "fixed" with recover.

## Adding a New Failure Mode

1. Add a deterministic test at the lowest layer that can reproduce it without
   an LLM (fake server, scripted model, or direct tool call).
2. Add an E2E test through `Agent.Run` when the failure can cross layers.
3. Add a TB2 task to a manual preset only when real-model behavior matters.
4. Keep automatic TB2 small and bounded; expand coverage/hard evaluation manually.
5. Add helper tests whenever input handling or the result gate changes.
