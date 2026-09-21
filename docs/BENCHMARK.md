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

The `TB2` workflow is manually triggered:

1. Add `TINYAGENT_API_KEY` as a repository secret.
2. Optionally add `TINYAGENT_BASE_URL` and `TINYAGENT_MODEL` secrets.
3. Open Actions -> `TB2` -> Run workflow, choose model/task/limit, and start it.

The workflow builds `cmd/tb2agent` for Linux, installs Harbor, and runs
`harbor run` against `terminal-bench@2.0`. The job fails if the API key secret
is missing. To verify the Harbor/TB2 pipeline itself without spending API
calls, set the `agent` input to `oracle`: the oracle solves tasks from the
checked-in solutions and needs no API key. The normal `go test` CI remains
independent of benchmark cost.
