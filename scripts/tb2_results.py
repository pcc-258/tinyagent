"""Fail closed on missing, errored or low-reward Harbor trial results."""
import argparse
import json
import math
import os
from pathlib import Path


def check(root, expected, minimum):
    trials, failures = [], []
    seen_tasks = set()
    for path in sorted(root.rglob("result.json")):
        try:
            result = json.loads(path.read_text())
        except (ValueError, OSError):
            failures.append(f"Unreadable result: {path}")
            continue
        if not isinstance(result, dict):
            failures.append(f"Invalid result shape: {path}")
            continue
        if "task_name" not in result:  # The job-level aggregate is not a trial.
            continue
        if not isinstance(result["task_name"], str) or not result["task_name"]:
            failures.append(f"Invalid task identity: {path}")
            continue
        if result["task_name"] in seen_tasks:
            failures.append(f"Duplicate task result: {result['task_name']}")
        seen_tasks.add(result["task_name"])
        exception = result.get("exception_info")
        if exception is not None and not isinstance(exception, dict):
            failures.append(f"Malformed exception metadata: {result['task_name']}")
            exception = {}
        trial = dict(task=result["task_name"], reward=None,
                     exception=(exception or {}).get("exception_type", ""))
        if not result.get("finished_at"):
            failures.append(f"Incomplete trial: {trial['task']}")
        if trial["exception"]:
            failures.append(f"Trial exception: {trial['task']} ({trial['exception']})")
        verifier = result.get("verifier_result")
        if not isinstance(verifier, dict):
            verifier = {}
        rewards = verifier.get("rewards")
        reward = rewards.get("reward") if isinstance(rewards, dict) else None
        if isinstance(reward, bool) or not isinstance(reward, (int, float)) or not math.isfinite(reward) or not 0 <= reward <= 1:
            failures.append(f"Missing or invalid verifier reward: {trial['task']}")
        else:
            trial["reward"] = reward
        trials.append(trial)
    if len(trials) != expected:
        failures.append(f"Expected {expected} trials, found {len(trials)}")
    valid = [trial["reward"] for trial in trials if trial["reward"] is not None]
    mean = sum(valid) / len(valid) if valid else None
    if mean is not None and mean < minimum:
        failures.append(f"Mean reward {mean:.4f} is below {minimum:.4f}")
    return trials, mean, failures


def report(config, trials, mean, failures):
    mode = "oracle infrastructure check (does not evaluate TinyAgent)" if config["agent"] == "oracle" else "TinyAgent real-model benchmark"
    lines = ["## TB2 result", "", f"Mode: {mode}", f"Preset: {config['preset']}",
             f"Expected trials: {config['n_tasks']}", f"Mean reward: {mean if mean is not None else 'unavailable'}",
             f"Required mean reward: {config['minimum_reward']}", "", "| Task | Reward | Exception |", "|---|---:|---|"]
    for trial in trials:
        task = trial["task"].replace("|", "\\|").replace("\n", " ")
        lines.append(f"| {task} | {trial['reward']} | {trial['exception'] or '-'} |")
    lines += ["", f"Gate: {'FAIL' if failures else 'PASS'}"]
    lines += [f"- {failure}" for failure in failures]
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--results", type=Path, required=True)
    parser.add_argument("--summary", type=Path, required=True)
    args = parser.parse_args()
    config = json.loads(args.config.read_text())
    trials, mean, failures = check(args.results, config["n_tasks"], config["minimum_reward"])
    summary = report(config, trials, mean, failures)
    args.summary.write_text(summary)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as stream:
            stream.write(summary)
    print(summary)
    raise SystemExit(bool(failures))


if __name__ == "__main__":
    main()
