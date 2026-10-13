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
- `.github/workflows/tb2.yml` is reusable by CI and also supports manual runs.

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
uv tool install harbor==0.18.0

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
run. Automatic smoke runs one easy task (`fix-git`). Manual smoke defaults to three
tasks. Both enforce verifier results rather than trusting Harbor's exit status.

## GitHub Actions

Every pull request and push to `main` runs these stages:

1. `lint`: Go formatting, actionlint v1.7.7, and benchmark helper regression tests.
2. `unit-tests`: build, vet, race tests and coverage on Go 1.26.
3. `e2e-tests`: race-enabled HTTP/SSE E2E tests with no model credentials.
4. `go-compatibility`: build and race tests on Go 1.27.
5. `tb2`: after all four stages pass, call the reusable TB2 workflow.

Same-repository PRs and main pushes automatically evaluate **TinyAgent with a
real model**, using `fix-git`, one trial, one concurrent task, no Harbor retries,
12 iterations, 2048 output tokens per call and a 900-second agent timeout. The
benchmark execution step allows 25 minutes including environment/verifier
headroom; the whole automatic job allows 45 minutes including installation and
artifact upload. These bounds limit runtime and output generation; they do not
cap input tokens, provider reasoning billing, or dollar cost.

Fork PRs run **oracle** without model credentials. This checks the task/container/
verifier infrastructure only, not TinyAgent's model or tool behavior. The summary
labels oracle separately. Missing credentials for a real-agent run fail explicitly;
the workflow never silently substitutes oracle for a same-repository PR.

CI can also be started manually. New commits cancel superseded runs for the same
PR/ref. Existing `unit-tests` and `e2e-tests` check names are retained. Every test
job uploads JSON logs and coverage where applicable, including on failure.

The reusable benchmark has an independent concurrency group. Automatic runs
cancel older automatic runs for the same PR/ref; manual runs are serialized in
a separate group. Manual `TB2` still supports `smoke`, `coverage` and `hard`.
Manual smoke defaults to these three short tasks:

- `fix-git` (easy)
- `git-leak-recovery` (medium)
- `openssl-selfsigned-cert` (medium)

Manual smoke inputs allow task globs and bounded task/iteration/output/time
limits. Inputs are validated by `scripts/tb2_config.py`; `scripts/tb2_run.py`
constructs an argument array without interpolating inputs into shell source.
The combined requested agent-time budget plus execution headroom must fit in
230 minutes; manual jobs are limited to 250 minutes.

The `coverage` preset targets longer medium tasks and raises the limits:

- `constraints-scheduling`
- `kv-store-grpc`
- `large-scale-text-editing`

It runs with `max_iterations=32`, `max_tokens=8192`, and
`timeout_sec=1800`, so it costs more tokens and time. It is manual only and
uploads the same diagnostic artifact format for per-trial comparison.

The `hard` preset targets hard tasks and raises the limits further:

- `configure-git-webserver`
- `llm-inference-batching-scheduler`
- `train-fasttext`

It runs with `max_iterations=64`, `max_tokens=16384`, and
`timeout_sec=3600`, so it is the most expensive preset and is manual only.

## Historical Evidence (before the runtime changes)

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

Harbor is pinned to 0.18.0, with Python 3.12. The workflow imports the adapter
against that version before running tasks. `TINYAGENT_MODEL` may also be a
repository variable; precedence is manual model input, repository variable,
repository model secret, then `deepseek-v4-flash`.

Every run uploads `tb2-results-<run-id>-<attempt>` containing `tb2-jobs/**`, the
normalized public budget configuration, and a Markdown gate summary. Per-trial
result.json, exceptions, trial logs and agent logs remain available for diagnosis.
The adapter uses `pipefail` so `tee` cannot hide a nonzero agent exit.

`scripts/tb2_results.py` requires exactly the requested number of completed,
distinct task results, valid verifier rewards in [0,1], and no trial exceptions.
Missing/malformed results fail even if Harbor exits successfully. Automatic runs
require mean reward 1.0; manual runs expose `minimum_reward` (default 1.0) for
explicit exploratory thresholds. Exceptions always fail, even with a lower
threshold. Zero matching tasks and duplicate/retried task results cannot pass.
The summary is also published to the Actions job summary.

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
