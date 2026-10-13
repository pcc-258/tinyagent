import json
import tempfile
import unittest
from pathlib import Path

from tb2_config import TINYAGENT, configure
from tb2_results import check, report
from tb2_run import command


class ConfigurationTests(unittest.TestCase):
    def test_automatic_has_fixed_small_real_agent_budget(self):
        cfg = configure({"TB2_AUTOMATIC": "true", "TB2_N_TASKS": "20", "TB2_MAX_TOKENS": "32768"})
        self.assertEqual(cfg["agent"], TINYAGENT)
        self.assertEqual(cfg["n_tasks"], 1)
        self.assertEqual(cfg["tasks"], ["*fix-git*"])
        self.assertEqual(cfg["max_iterations"], 12)
        self.assertEqual(cfg["max_tokens"], 2048)
        self.assertEqual(cfg["run_timeout_minutes"], 25)
        self.assertEqual(cfg["minimum_reward"], 1)

    def test_forks_always_use_oracle(self):
        cfg = configure({"TB2_AUTOMATIC": "true", "TB2_FORK": "true", "TB2_AGENT": TINYAGENT})
        self.assertEqual(cfg["agent"], "oracle")
        args = command(cfg, "harbor", "model")
        self.assertNotIn("--model", args)
        self.assertNotIn("--agent-kwarg", args)

    def test_presets_and_manual_overrides(self):
        hard = configure({"TB2_PRESET": "hard"})
        self.assertEqual((hard["n_tasks"], hard["max_iterations"], hard["max_tokens"], hard["timeout_sec"]), (3, 64, 16384, 3600))
        custom = configure({"TB2_TASKS": "fix-git", "TB2_N_TASKS": "1", "TB2_MAX_TOKENS": "4096", "TB2_MINIMUM_REWARD": "0.5"})
        self.assertEqual(custom["max_tokens"], 4096)
        self.assertEqual(custom["minimum_reward"], 0.5)

    def test_unsafe_or_unbounded_inputs_are_rejected(self):
        for env in ({"TB2_AGENT": "other:Class"}, {"TB2_TASKS": "$(touch /tmp/unwanted)"},
                    {"TB2_TASKS": "--config"}, {"TB2_N_TASKS": "0"},
                    {"TB2_MAX_TOKENS": "0"}, {"TB2_MAX_ITERATIONS": "999"},
                    {"TB2_N_TASKS": "20", "TB2_TIMEOUT_SEC": "7200"},
                    {"TB2_MINIMUM_REWARD": "nan"}, {"TB2_MINIMUM_REWARD": "2"},
                    {"TB2_TRACE_MODEL": "true\nevil"}):
            with self.subTest(env=env), self.assertRaises(ValueError):
                configure(env)

    def test_command_uses_separate_argv_and_disables_retries(self):
        cfg = configure({"TB2_AUTOMATIC": "true"})
        args = command(cfg, "harbor", "provider/model:version")
        self.assertEqual(args[args.index("--model") + 1], "provider/model:version")
        self.assertEqual(args[args.index("--include-task-name") + 1], "*fix-git*")
        self.assertEqual(args[args.index("--max-retries") + 1], "0")
        self.assertEqual(args[args.index("--n-attempts") + 1], "1")
        for value in ("model;touch /tmp/unwanted", "model\nextra", "--flag"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                command(cfg, "harbor", value)


class ResultsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.addCleanup(self.temp.cleanup)

    def trial(self, name="fix-git", reward=1, **extra):
        data = dict(task_name=name, trial_name=name + "__fixture", finished_at="2026-10-13T00:00:00Z",
                    exception_info=None, verifier_result={"rewards": {"reward": reward}})
        data.update(extra)
        dest = self.root / (name + str(len(list(self.root.iterdir()))))
        dest.mkdir()
        (dest / "result.json").write_text(json.dumps(data))

    def test_pass_and_aggregate_not_counted_twice(self):
        self.trial()
        (self.root / "result.json").write_text(json.dumps({"stats": {"n_trials": 1}}))
        trials, mean, failures = check(self.root, 1, 1)
        self.assertEqual(len(trials), 1)
        self.assertEqual(mean, 1)
        self.assertFalse(failures)

    def test_harbor_exit_success_is_not_verifier_success(self):
        self.trial(reward=0)
        self.assertTrue(check(self.root, 1, 1)[2])

    def test_exception_fails_even_if_reward_is_one(self):
        self.trial(exception_info={"exception_type": "AgentTimeoutError"})
        self.assertTrue(check(self.root, 1, 0)[2])

    def test_missing_trials_and_empty_run_fail(self):
        self.assertTrue(check(self.root, 1, 1)[2])
        self.trial()
        self.assertTrue(check(self.root, 2, 1)[2])

    def test_incomplete_and_missing_reward_fail(self):
        self.trial(finished_at=None, verifier_result=None)
        self.assertTrue(check(self.root, 1, 0)[2])

    def test_nan_boolean_or_out_of_range_reward_fail(self):
        for value in (float("nan"), float("inf"), True, 2, -1, "1"):
            with self.subTest(value=value):
                self.trial(name=str(len(list(self.root.iterdir()))), reward=value)
        self.assertTrue(check(self.root, 6, 0)[2])

    def test_duplicate_trials_do_not_fill_missing_tasks(self):
        self.trial()
        self.trial()
        self.assertTrue(check(self.root, 2, 1)[2])

    def test_corrupt_artifact_fails(self):
        (self.root / "result.json").write_text("not json")
        self.assertTrue(check(self.root, 1, 1)[2])

    def test_malformed_nested_metadata_fails_with_diagnostics(self):
        self.trial(verifier_result="broken", exception_info=["broken"])
        trials, mean, failures = check(self.root, 1, 1)
        self.assertEqual(len(trials), 1)
        self.assertIsNone(mean)
        self.assertTrue(failures)

    def test_manual_mean_threshold(self):
        self.trial("first", 1)
        self.trial("second", 0)
        self.assertFalse(check(self.root, 2, 0.5)[2])
        self.assertTrue(check(self.root, 2, 0.6)[2])

    def test_oracle_summary_does_not_claim_tinyagent_quality(self):
        cfg = configure({"TB2_AUTOMATIC": "true", "TB2_FORK": "true"})
        text = report(cfg, [], None, ["missing"])
        self.assertIn("does not evaluate TinyAgent", text)
        self.assertIn("Gate: FAIL", text)


if __name__ == "__main__":
    unittest.main()
