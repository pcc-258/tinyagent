"""Harbor adapter for running the TinyAgent-based tb2agent CLI in a task container.

The adapter is executed by Harbor on the host. It uploads the compiled tb2agent
binary into each task environment and then drives one task instruction through
the TinyAgent ReAct loop.
"""

from __future__ import annotations

import os
import shlex
from pathlib import Path
from typing import override

from harbor.agents.installed.base import (
    BaseInstalledAgent,
    CliFlag,
    with_prompt_template,
)
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext


class TinyAgentAgent(BaseInstalledAgent):
    CLI_FLAGS = [
        CliFlag("max_iterations", cli="--max-iterations", type="int"),
        CliFlag("timeout_sec", cli="--timeout-sec", type="int"),
        CliFlag("max_tokens", cli="--max-tokens", type="int"),
        CliFlag("trace_model", cli="--trace-model", type="bool"),
    ]
    _REMOTE_BIN = "/usr/local/bin/tb2agent"
    _ENV_KEYS = (
        "TINYAGENT_API_KEY",
        "TINYAGENT_BASE_URL",
        "TINYAGENT_MODEL",
        "OPENAI_API_KEY",
        "OPENAI_BASE_URL",
        "OPENAI_MODEL",
    )

    def __init__(
        self,
        logs_dir: Path,
        binary_path: str = "/tmp/tb2agent",
        *args,
        **kwargs,
    ):
        self._binary_path = binary_path
        super().__init__(logs_dir, *args, **kwargs)

    @staticmethod
    def name() -> str:
        return "tinyagent-tb2"

    def version(self) -> str | None:
        return self._version or "0.1.0"

    def get_version_command(self) -> str | None:
        return f"{self._REMOTE_BIN} --version"

    async def install(self, environment: BaseEnvironment) -> None:
        binary = Path(self._binary_path)
        if not binary.is_file():
            raise RuntimeError(f"tb2agent binary not found at {binary}")
        await environment.upload_file(binary, self._REMOTE_BIN)
        await self.exec_as_root(environment, f"chmod 0755 {self._REMOTE_BIN}")

    @override
    @with_prompt_template
    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        env = {
            key: os.environ[key]
            for key in self._ENV_KEYS
            if os.environ.get(key)
        }
        parts = [self._REMOTE_BIN]
        flags = self.build_cli_flags()
        if flags:
            parts.append(flags)
        parts.append(f"--instruction {shlex.quote(instruction)}")
        await self.exec_as_agent(
            environment,
            command=" ".join(parts) + " 2>&1 | tee /logs/agent/tb2agent.log",
            env=env,
            cwd="/app",
            timeout_sec=self._resolved_flags.get("timeout_sec"),
        )
