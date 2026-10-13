"""Normalize workflow inputs without evaluating them as shell code."""
import argparse
import json
import math
import os
import re
from pathlib import Path

TINYAGENT = "tinyagent_agent:TinyAgentAgent"
SMOKE_TASKS = ["*fix-git*", "*git-leak-recovery*", "*openssl-selfsigned-cert*"]
PRESETS = {
    "coverage": (["*constraints-scheduling*", "*kv-store-grpc*", "*large-scale-text-editing*"], 32, 8192, 1800),
    "hard": (["*configure-git-webserver*", "*llm-inference-batching-scheduler*", "*train-fasttext*"], 64, 16384, 3600),
}


def boolean(value):
    if value not in ("true", "false", ""):
        raise ValueError("boolean inputs must be true or false")
    return value == "true"


def integer(env, key, default, low, high):
    try:
        value = int(env.get(key) or default)
    except ValueError as error:
        raise ValueError(f"{key} must be an integer") from error
    if not low <= value <= high:
        raise ValueError(f"{key} must be between {low} and {high}")
    return value


def configure(env):
    automatic = boolean(env.get("TB2_AUTOMATIC", ""))
    fork = boolean(env.get("TB2_FORK", ""))
    agent = "oracle" if fork else (TINYAGENT if automatic else env.get("TB2_AGENT") or TINYAGENT)
    if agent not in (TINYAGENT, "oracle"):
        raise ValueError("agent must be TinyAgent or oracle")
    preset = "automatic" if automatic else env.get("TB2_PRESET") or "smoke"
    if preset == "automatic":
        tasks, n_tasks, iterations, tokens, timeout = ["*fix-git*"], 1, 12, 2048, 900
    elif preset in PRESETS:
        tasks, iterations, tokens, timeout = PRESETS[preset]
        n_tasks = 3
    elif preset == "smoke":
        tasks = (env.get("TB2_TASKS") or ",".join(SMOKE_TASKS)).split(",")
        n_tasks = integer(env, "TB2_N_TASKS", 3, 1, 20)
        iterations = integer(env, "TB2_MAX_ITERATIONS", 12, 1, 256)
        tokens = integer(env, "TB2_MAX_TOKENS", 2048, 1, 32768)
        timeout = integer(env, "TB2_TIMEOUT_SEC", 900, 60, 7200)
    else:
        raise ValueError("unknown task preset")
    tasks = [task.strip() for task in tasks]
    if not tasks or any(not task or task.startswith("-") or not re.fullmatch(r"[A-Za-z0-9_*?./\[\]-]+", task) for task in tasks):
        raise ValueError("invalid task glob")
    reward = 1.0 if automatic else float(env.get("TB2_MINIMUM_REWARD") or 1.0)
    if not math.isfinite(reward) or not 0 <= reward <= 1:
        raise ValueError("minimum reward must be finite and between 0 and 1")
    run_minutes = math.ceil(n_tasks * timeout / 60) + 10
    if run_minutes > 230:
        raise ValueError("combined task/time budget exceeds 230 minutes")
    return dict(agent=agent, preset=preset, tasks=tasks, n_tasks=n_tasks,
                max_iterations=iterations, max_tokens=tokens, timeout_sec=timeout,
                trace_model=boolean(env.get("TB2_TRACE_MODEL", "")),
                minimum_reward=reward, run_timeout_minutes=run_minutes)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    config = configure(os.environ)
    args.output.write_text(json.dumps(config, indent=2) + "\n")
    output = os.environ.get("GITHUB_OUTPUT")
    if output:
        with open(output, "a") as stream:
            stream.write(f"agent={config['agent']}\nrun_timeout_minutes={config['run_timeout_minutes']}\n")
    print(f"TB2 mode={config['agent']} preset={config['preset']} tasks={config['n_tasks']} run_timeout_minutes={config['run_timeout_minutes']}")


if __name__ == "__main__":
    main()
