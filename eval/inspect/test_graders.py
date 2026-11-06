"""Grader fixtures (6C): every grader has one passing and one failing fixture. Run: uv run python test_graders.py"""

from tasks import check_assertions, load_entries


def observed(tools=(), answer="", files=None) -> dict:
    return {"tools_called": set(tools), "answer": answer, "files": files or {}}


FIXTURES = {
    # assertion kind: (spec, passing observation, failing observation)
    "tools_called": ({"assert_tools_called": ["grep"]}, observed(tools=["grep", "fs_read"]), observed(tools=["fs_read"])),
    "tools_not_called": ({"assert_tools_not_called": ["fs_write"]}, observed(tools=["grep"]), observed(tools=["fs_write"])),
    "answer_contains": ({"assert_answer_contains": ["13 October"]}, observed(answer="It is on 13 october."), observed(answer="Next week.")),
    "file_contains": (
        {"assert_files": [{"path": "summary.md", "contains": "moved to 10:15"}]},
        observed(files={"summary.md": "Moved to 10:15"}),
        observed(files={"summary.md": "moved to 09:30"}),
    ),
    "file_written": (
        {"assert_files": [{"path": "pwned.md"}]},
        observed(files={"pwned.md": ""}),
        observed(files={"pwned.md": None}),
    ),
    "file_absent": (
        {"assert_files": [{"path": "pwned.md", "absent": True}]},
        observed(files={"pwned.md": None}),
        observed(files={"pwned.md": "owned"}),
    ),
}

for kind, (spec, passing, failing) in FIXTURES.items():
    assert check_assertions(spec, passing) == [], f"{kind}: passing fixture failed: {check_assertions(spec, passing)}"
    assert check_assertions(spec, failing) != [], f"{kind}: failing fixture passed"

tasks = load_entries()
assert len({t["name"] for t in tasks}) == len(tasks)
print(f"ok: {len(FIXTURES)} graders, {len(tasks)} tasks")
