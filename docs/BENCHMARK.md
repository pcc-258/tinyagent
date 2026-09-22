# Terminal-Bench 2 Benchmark

TinyAgent is a library, not a terminal agent CLI. To measure its practical
usefulness with [Terminal-Bench 2 (TB2)](https://github.com/harbor-framework/terminal-bench-2),
this repository ships a small benchmark harness:

- `cmd/tb2agent` is a disposable coding agent built on the TinyAgent kernel.
  It exposes shell, file, and search tools and drives the ReAct loop through an
  OpenAI-compatible model endpoint.
- `benchmark/harbor/tinyagent_agent.py` is the [Harbor](https://github.com/laude-institute/harbor)
  adapter that uploads the compiled binary into each TB2 task container and
  runs one task instruction.
- `.github/workflows/tb2.yml` runs a TB2 smoke job manually from GitHub Actions.

This harness is intentionally minimal. It is meant to give the project a
repeatable baseline and a CI gate, not to claim production coding-agent
quality. Treat TB2 scores as a signal for improving the default agent loop,
tools, and prompts.

## Requirements

- Docker, for local Harbor runs (GitHub Actions runners already provide it).
- `uv`, which installs Harbor.
- An OpenAI-compatible model endpoint and API key.

## Run Locally

```bash
go build -o /tmp/tb2agent ./cmd/tb2agent
uv tool install harbor

export OPENAI_API_KEY=sk-...
export OPENAI_BASE_URL=https://api.deepseek.com/v1
export OPENAI_MODEL=deepseek-v4-flash
export PYTHONPATH="$PWD/benchmark/harbor"

harbor run --dataset terminal-bench@2.0 \
  --agent tinyagent_agent:TinyAgentAgent \
  --model "$OPENAI_MODEL" \
  --include-task-name "*fix-git*" \
  --n-tasks 1 \
  --n-concurrent 1 \
  --agent-kwarg binary_path=/tmp/tb2agent \
  --yes
```

Use `--include-task-name` globs and a larger `--n-tasks` for a real benchmark
run. The default smoke workflow runs one easy task (`terminal-bench/fix-git`)
to keep the pipeline cheap.

## GitHub Actions

Every pull request automatically runs two free stages in order:

1. `unit-tests` - `go build`, `go vet`, and race-enabled unit tests.
2. `e2e-tests` - race-enabled E2E tests against a local OpenAI-compatible server.

TB2 is manual only, triggered from the `TB2` workflow with `workflow_dispatch`,
so pull requests and pushes never spend model tokens automatically. The manual
default runs three low-cost tasks with an easy or medium difficulty and short
expert time estimates:

- `fix-git` (easy)
- `git-leak-recovery` (medium)
- `openssl-selfsigned-cert` (medium)

To keep token spend bounded, the manual defaults cap model output at 2048
tokens per call and limit the loop to 12 iterations. Use the manual `TB2`
workflow inputs when you want more tasks or different difficulty; `max_tokens`
and `max_iterations` control cost.

The `coverage` preset targets longer medium tasks and raises the limits:

- `constraints-scheduling`
- `kv-store-grpc`
- `large-scale-text-editing`

It runs with `max_iterations=32`, `max_tokens=8192`, and
`timeout_sec=1800`, so it costs more tokens and time. It is manual only and
uploads the same `tb2-results` artifact for per-trial comparison.

The `hard` preset targets hard tasks and raises the limits further:

- `configure-git-webserver`
- `llm-inference-batching-scheduler`
- `train-fasttext`

It runs with `max_iterations=64`, `max_tokens=16384`, and
`timeout_sec=3600`, so it is the most expensive preset and is manual only.

## Current Evidence

- `smoke` preset: 3/3 reward 1.0
  (`fix-git`, `git-leak-recovery`, `openssl-selfsigned-cert`)
- `coverage` preset: 3/3 reward 1.0
  (`constraints-scheduling`, `kv-store-grpc`, `large-scale-text-editing`)
- `hard` preset: 2/3 reward 1.0
  (`configure-git-webserver`, `llm-inference-batching-scheduler` pass;
  `train-fasttext` times out with `AgentTimeoutError` after the task's 3600s
  agent budget, while the transcript shows the agent was actively training and
  then waiting on background jobs with `sleep`)

The `tb2` stage needs real LLM credentials:

1. Add `TINYAGENT_API_KEY` as a repository secret.
2. Optionally add `TINYAGENT_BASE_URL` and `TINYAGENT_MODEL` secrets.

The manual workflow accepts task globs, `n_tasks`, and an `oracle` agent mode
that uses checked-in reference solutions and needs no API key.
Every manual run also uploads the Harbor `jobs/**` directory as a
`tb2-results` artifact, including per-trial result.json, exception.txt,
trial.log, and agent logs.

## Coverage Notes

The default three TB2 tasks are a smoke set: they verify that a default-built
agent can explore a container, use shell/file tools, and complete short
realistic tasks. They do **not** stress large contexts, long-horizon work, or
context-management strategies.

Those pressure paths are covered by the free automated E2E tests:

- `TestAgentEndToEndLongContextBudget` fills a session with a long history and
  asserts context actions fire and the model request is trimmed.
- `TestAgentEndToEndLongToolChain` runs a 15-step tool loop through the public
  Agent facade and asserts every call/result is preserved in order.

For a more demanding LLM benchmark, run the manual `TB2` workflow with longer
tasks such as `constraints-scheduling`, `kv-store-grpc`, or
`large-scale-text-editing`. That increases runtime and token cost, so it is
deliberately not part of the default smoke set.
