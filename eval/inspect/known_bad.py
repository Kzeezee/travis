"""Known-bad answers (6D): each judged task answered wrongly on purpose, with no agent run. The
judge must grade every one INCORRECT. Run: make eval-live ARGS="known_bad.py --trials 1 --log-dir ../results/judge"."""

from inspect_ai import Task, task
from inspect_ai.dataset import Sample
from inspect_ai.model import ChatMessageAssistant, ModelOutput
from inspect_ai.solver import Generate, TaskState, solver

from tasks import judge, load_entries

WRONG_ANSWERS = {
    "notes-dentist-date": "Your dentist appointment is on Tuesday 2 September, according to journal/2026-09-02.md.",
    "household-grocery-total": "You spent $201.35 on groceries in September.",
    "household-next-event": "Next up is the book club at Ana's on 24 September, reading The Remains of the Day.",
    "household-which-appointment": "I moved your dentist appointment on 13 October to 11:00.",
    "household-which-chris": "Chris's number is 0498 004 211.",
    "fetch-bus-after-library": "The library closes at 17:00 on Friday, so take the 17:05 bus home to Hill Street.",
}


@solver
def answer_wrongly():
    async def solve(state: TaskState, generate: Generate) -> TaskState:
        text = WRONG_ANSWERS[str(state.sample_id)]
        state.messages.append(ChatMessageAssistant(content=text))
        state.output = ModelOutput.from_content(str(state.model), text)
        return state

    return solve


@task
def known_bad() -> Task:
    tasks = {entry["name"]: entry for entry in load_entries()[0]}
    samples = [
        Sample(id=name, input=tasks[name]["prompt"].replace("{pages}", "the pages "), target=tasks[name]["target"],
               metadata={"judge": tasks[name]["judge"]})
        for name in WRONG_ANSWERS
    ]
    return Task(dataset=samples, solver=answer_wrongly(), scorer=judge())
