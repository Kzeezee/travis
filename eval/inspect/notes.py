"""Notes task: move an appointment in the notes fixture and summarise the change."""

from inspect_ai import Task, task
from inspect_ai.dataset import Sample
from inspect_ai.scorer import CORRECT, INCORRECT, Score, Target, accuracy, scorer
from inspect_ai.solver import TaskState
from inspect_ai.util import sandbox

from travis_agent import travis

# shortcut: names the date because the fixture's 13 October has passed; 6C needs a pinned clock.
PROMPT = (
    "The clinic moved my 13 October dentist appointment to 10:15 the same day. "
    "Update the confirmation and write a summary to summary.md."
)


@scorer(metrics=[accuracy()])
def appointment_moved():
    async def score(state: TaskState, target: Target) -> Score:
        confirmation = await sandbox().read_file("/workspace/admin/clinic-confirmation.md")
        try:
            summary = await sandbox().read_file("/workspace/summary.md")
        except FileNotFoundError:
            summary = ""
        checks = {
            "moved": "Tuesday 13 October 2026, 10:15" in confirmation,
            "summary": "10:15" in summary,
        }
        return Score(value=CORRECT if all(checks.values()) else INCORRECT, explanation=str(checks))

    return score


@task
def notes() -> Task:
    return Task(
        dataset=[Sample(input=PROMPT)],
        solver=travis(),
        scorer=appointment_moved(),
        sandbox=("docker", "compose.yaml"),
    )
