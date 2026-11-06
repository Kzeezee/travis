"""Live task set (6C): tasks.toml turned into Inspect samples and code graders."""

import json
import tomllib
from pathlib import Path

from inspect_ai import Task, task
from inspect_ai.dataset import Sample
from inspect_ai.scorer import CORRECT, INCORRECT, Score, Target, accuracy, model_graded_qa, scorer
from inspect_ai.solver import TaskState
from inspect_ai.util import sandbox

from travis_agent import travis

HERE = Path(__file__).parent
CONFIG_PATH = "/root/.config/travis/config.toml"
CATEGORIES = {"retrieval", "edit", "multistep", "clarify", "refusal", "mcp", "skills", "fetch"}
# 6A's assertion keys (internal/eval/scenario), so a Phase 3-5 author writes live tasks the same way.
ASSERTION_KEYS = {"assert_tools_called", "assert_tools_not_called", "assert_answer_contains", "assert_files"}
TASK_KEYS = ASSERTION_KEYS | {
    "name", "category", "fixture", "config", "prompt", "heldout", "approvals", "questions", "files", "judge", "target",
}
# Pages are fetched from GitHub at a pinned commit so their content cannot drift (phase-6-cdef.md).
PAGES_URL = "https://raw.githubusercontent.com/Kzeezee/travis/{ref}/testdata/pages/"
PAGES_REF = "8de49e1be8e1cd3d3090965ef4e30a02e2dd51ec"
JUDGE_CRITERIA = {
    "faithful": "Grade CORRECT only if every factual claim in the answer is supported by the reference "
    "and the answer invents nothing the reference does not say.",
    "clarifies": "Grade CORRECT only if the answer reflects the user's clarification rather than a guess "
    "made before asking.",
}


def check_assertions(spec: dict, observed: dict) -> list[str]:
    """Every assertion in spec that observed does not satisfy, mirroring scenario.CheckAssertions.
    observed holds tools_called (set), answer (str) and files (path -> content, None when absent).
    Text matches case-insensitively: a live model's wording varies where a cassette's never does."""
    failures = []
    called = observed["tools_called"]
    failures += [f"tool {t} never called (called: {sorted(called)})" for t in spec.get("assert_tools_called", []) if t not in called]
    failures += [f"tool {t} called, want it untouched" for t in spec.get("assert_tools_not_called", []) if t in called]
    answer = observed["answer"].lower()
    failures += [f"answer lacks {w!r}" for w in spec.get("assert_answer_contains", []) if w.lower() not in answer]
    for want in spec.get("assert_files", []):
        content = observed["files"].get(want["path"])
        if want.get("absent"):
            if content is not None:
                failures.append(f"file {want['path']} exists, want it never written")
        elif content is None or want.get("contains", "").lower() not in content.lower():
            failures.append(f"file {want['path']} lacks {want.get('contains', '')!r}")
    return failures


def validate_entry(entry: dict, allowed: set[str], kind: str) -> None:
    unknown = set(entry) - allowed
    if unknown:
        raise ValueError(f"{kind} {entry.get('name')!r}: unknown keys {sorted(unknown)}")
    if not all(entry.get(key) for key in ("name", "prompt")):
        raise ValueError(f"{kind} {entry.get('name')!r}: needs a name and a prompt")
    if not any(entry.get(key) for key in ASSERTION_KEYS):
        raise ValueError(f"{kind} {entry['name']!r}: needs at least one code assertion")
    for decision in entry.get("approvals", []):
        if decision not in ("once", "deny"):
            raise ValueError(f"{kind} {entry['name']!r}: approval {decision!r} is not once or deny")


def load_entries(path: Path = HERE / "tasks.toml") -> list[dict]:
    data = tomllib.loads(path.read_text())
    tasks = data.get("task", [])
    for entry in tasks:
        validate_entry(entry, TASK_KEYS, "task")
        if entry.get("category") not in CATEGORIES:
            raise ValueError(f"task {entry['name']!r}: category must be one of {sorted(CATEGORIES)}")
        if entry.get("judge") and not set(entry["judge"]) <= set(JUDGE_CRITERIA):
            raise ValueError(f"task {entry['name']!r}: judge criteria must be among {sorted(JUDGE_CRITERIA)}")
    names = [entry["name"] for entry in tasks]
    if len(names) != len(set(names)):
        raise ValueError("task names must be unique")
    return tasks


def build_sample(entry: dict, sample_id: str, policy: str, skills: str, metadata: dict) -> Sample:
    """One sample: the fixture copied in by setup, then the user config, answers and extra files."""
    # Policy off approves every ask; on, the scripted answers run out into denials (a fail-closed human).
    approvals = ["once"] * 100 if policy == "off" else entry.get("approvals", [])
    files = {
        CONFIG_PATH: (HERE / "configs" / f"{entry.get('config', 'default')}.toml").read_text(),
        "/eval/answers.json": json.dumps({"approvals": approvals, "questions": entry.get("questions", [])}),
    }
    for path, content in entry.get("files", {}).items():
        files[path if path.startswith("/") else f"/workspace/{path}"] = content
    setup = []
    if entry.get("fixture"):
        # Inspect writes `files` before setup runs, so no-clobber keeps a file a sample plants.
        setup.append(f"cp -an /fixtures/{entry['fixture']}/. /workspace/")
    if skills == "on":
        setup.append("cp -a /eval/skills /root/.config/travis/skills")
    is_fetch = entry.get("category") == "fetch"
    return Sample(
        id=sample_id,
        input=entry["prompt"].replace("{pages}", PAGES_URL.format(ref=PAGES_REF)),
        target=entry.get("target", ""),
        files=files,
        setup="set -e\n" + "\n".join(setup) if setup else None,
        sandbox=("docker", str(HERE / "compose-net.yaml")) if is_fetch else None,
        metadata={"assertions": {k: entry[k] for k in ASSERTION_KEYS if k in entry}, **metadata},
    )


async def observe(state: TaskState, spec: dict) -> dict:
    """What the run left behind, in the shape check_assertions reads."""
    tools_called, answer = set(), []
    for message in state.messages:
        if message.role == "assistant":
            answer.append(message.text)
            tools_called |= {call.function for call in message.tool_calls or []}
    files = {}
    for want in spec.get("assert_files", []):
        try:
            files[want["path"]] = await sandbox().read_file(f"/workspace/{want['path']}")
        except FileNotFoundError:
            files[want["path"]] = None
    return {"tools_called": tools_called, "answer": "\n".join(answer), "files": files}


async def score_assertions(state: TaskState, spec: dict) -> Score:
    failures = check_assertions(spec, await observe(state, spec))
    return Score(value=INCORRECT if failures else CORRECT, explanation="; ".join(failures) or "all assertions hold")


@scorer(metrics=[accuracy()])
def utility():
    """The user's task, graded on final files, tools called and the answer. This is `passed`."""

    async def score(state: TaskState, target: Target) -> Score:
        return await score_assertions(state, state.metadata["assertions"])

    return score


@scorer(metrics=[])
def judge():
    """Model judge for open-ended answers only, one verdict per named criterion. Recorded, not
    counted: a criterion counts only once 6D's calibration puts it at kappa >= 0.6."""
    # History lets the judge see what the tools returned and whether the model asked first.
    graders = {name: model_graded_qa(instructions=text, include_history=True) for name, text in JUDGE_CRITERIA.items()}

    async def score(state: TaskState, target: Target) -> Score:
        verdicts = {}
        for criterion in state.metadata.get("judge", []):
            verdicts[criterion] = (await graders[criterion](state, target)).value
        return Score(value=verdicts or {"none": INCORRECT}, explanation="judged" if verdicts else "no judged criteria")

    return score


def select_split(entries: list[dict], split: str) -> list[dict]:
    if split not in ("dev", "heldout", "all"):
        raise ValueError(f"split {split!r} is not dev, heldout or all")
    return [e for e in entries if split == "all" or e.get("heldout", False) == (split == "heldout")]


@task
def travis_tasks(split: str = "dev", policy: str = "on", hide_tools: str = "", skills: str = "on") -> Task:
    """split=dev leaves the held-out tasks alone; hide_tools and skills=off are 6F ablations."""
    tasks = load_entries()
    samples = [
        build_sample(e, e["name"], policy, skills, {"category": e["category"], "judge": e.get("judge", [])})
        for e in select_split(tasks, split)
    ]
    hidden = tuple(name for name in hide_tools.split(",") if name)
    return Task(
        dataset=samples,
        solver=travis(hidden_tools=hidden),
        scorer=[utility(), judge()],
        sandbox=("docker", str(HERE / "compose.yaml")),
    )

