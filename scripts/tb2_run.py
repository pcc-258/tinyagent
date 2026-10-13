"""Construct Harbor argv directly; workflow inputs never enter shell source."""
import argparse
import json
import os
import re
import subprocess
from pathlib import Path


def command(config, harbor, model):
    if model.startswith("-") or not re.fullmatch(r"[A-Za-z0-9_.:/-]{1,200}", model):
        raise ValueError("invalid model identifier")
    args = [harbor, "run", "--dataset", "terminal-bench@2.0", "--agent", config["agent"],
            "--jobs-dir", "tb2-jobs", "--n-tasks", str(config["n_tasks"]),
            "--n-concurrent", "1", "--n-attempts", "1", "--max-retries", "0", "--yes"]
    for task in config["tasks"]:
        args.extend(["--include-task-name", task])
    if config["agent"] != "oracle":
        args.extend(["--model", model, "--agent-kwarg", "binary_path=/tmp/tb2agent"])
        for key in ("max_iterations", "timeout_sec", "max_tokens", "trace_model"):
            value = config[key]
            if isinstance(value, bool):
                value = str(value).lower()
            args.extend(["--agent-kwarg", f"{key}={value}"])
    return args


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--harbor", required=True)
    args = parser.parse_args()
    config = json.loads(args.config.read_text())
    raise SystemExit(subprocess.call(command(config, args.harbor, os.environ.get("OPENAI_MODEL", "deepseek-v4-flash"))))


if __name__ == "__main__":
    main()
