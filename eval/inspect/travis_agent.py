"""The one solver every live task runs: travis headless in its sandbox, model calls bridged through Inspect."""

from inspect_ai.agent import Agent, AgentState, agent, sandbox_agent_bridge
from inspect_ai.log import transcript
from inspect_ai.model import ChatMessage, GenerateConfig, GenerateInput, Model
from inspect_ai.tool import ToolChoice, ToolInfo
from inspect_ai.util import sandbox

# Exit codes of `travis run` (turnExitCodes in cmd/travis/run.go). A turn that stopped badly is
# scored like any other; 1, or any code not listed, means travis itself could not run.
STOP_REASONS = {0: "end_turn", 2: "error", 3: "budget", 4: "length"}

# Every run happens on this day, so fixture dates (the notes dentist appointment is 13 October
# 2026) stay in the future however late the eval is run.
EVAL_DATE = "2026-10-01"

@agent
def travis(hidden_tools: tuple[str, ...] = ()) -> Agent:
    """hidden_tools is an ablation applied in the bridge, outside travis, so no shipped code gains
    an off switch for a tool."""

    async def ablate(
        _: Model, messages: list[ChatMessage], tools: list[ToolInfo], choice: ToolChoice | None, config: GenerateConfig
    ) -> GenerateInput | None:
        if not hidden_tools:
            return None
        return GenerateInput(
            input=messages,
            tools=[tool for tool in tools if tool.name not in hidden_tools],
            tool_choice=choice,
            config=config,
        )

    async def execute(state: AgentState) -> AgentState:
        async with sandbox_agent_bridge(state, filter=ablate) as bridge:
            result = await sandbox().exec(
                [
                    "travis", "run",
                    "--base-url", f"http://127.0.0.1:{bridge.port}/v1",
                    "--model", "inspect",
                    "--answers", "/eval/answers.json",
                    "--now", EVAL_DATE,
                    "--prompt", state.messages[-1].text,
                ],
                cwd="/workspace",
                env={"OPENAI_API_KEY": "unused-the-bridge-holds-the-key"},
                timeout=600,
            )
            stop = STOP_REASONS.get(result.returncode)
            if stop is None:
                raise RuntimeError(f"travis run failed ({result.returncode}): {result.stderr}")
            transcript().info(f"travis stopped: {stop}\n\n{result.stderr}")
        return bridge.state

    return execute
