"""The one solver every live task runs: travis headless in its sandbox, model calls bridged through Inspect."""

from inspect_ai.agent import Agent, AgentState, agent, sandbox_agent_bridge
from inspect_ai.log import transcript
from inspect_ai.util import sandbox

# Exit codes of `travis run` (turnExitCodes in cmd/travis/run.go). A turn that stopped badly is
# scored like any other; 1, or any code not listed, means travis itself could not run.
STOP_REASONS = {0: "end_turn", 2: "error", 3: "budget", 4: "length"}


@agent
def travis() -> Agent:
    async def execute(state: AgentState) -> AgentState:
        async with sandbox_agent_bridge(state) as bridge:
            result = await sandbox().exec(
                [
                    "travis", "run",
                    "--base-url", f"http://localhost:{bridge.port}/v1",
                    "--model", "inspect",
                    "--answers", "/eval/answers.json",
                    "--prompt", state.messages[-1].text,
                ],
                cwd="/workspace",
                timeout=600,
            )
            stop = STOP_REASONS.get(result.returncode)
            if stop is None:
                raise RuntimeError(f"travis run failed ({result.returncode}): {result.stderr}")
            transcript().info(f"travis stopped: {stop}")
        return bridge.state

    return execute
