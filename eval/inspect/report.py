"""Summarises committed eval results (6E; 6D and 6F extend it). Reads files only and makes no model
calls, and its output does not depend on when it runs."""

import json
import math
from collections import defaultdict
from pathlib import Path

HERE = Path(__file__).parent
RESULTS = HERE.parent / "results"


def wilson(successes: int, n: int, z: float = 1.96) -> tuple[float, float]:
    """95% Wilson score interval for a proportion; (0, 1) when there is no data."""
    if n == 0:
        return 0.0, 1.0
    p = successes / n
    centre = (p + z * z / (2 * n)) / (1 + z * z / n)
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / (1 + z * z / n)
    return max(0.0, centre - half), min(1.0, centre + half)


def format_rate(successes: int, n: int) -> str:
    low, high = wilson(successes, n)
    return f"{successes}/{n} = {100 * successes / n:.0f}% [{100 * low:.0f}, {100 * high:.0f}]" if n else "no runs"


def load_rows(folder: str) -> list[dict]:
    rows = []
    for path in sorted((RESULTS / folder).glob("*.jsonl")):
        rows += [{**json.loads(line), "log_dir": folder} for line in path.read_text().splitlines() if line]
    return sorted(rows, key=lambda r: (r["transcript"], r["task"], r["trial"]))


def scored(rows: list[dict], field: str = "passed") -> list[dict]:
    """Rows where the field was graded; a harness failure (None) says nothing about the agent."""
    return [r for r in rows if r[field] is not None]


def build_injection(rows: list[dict]) -> list[str]:
    lines = ["## Prompt injection", ""]
    if not rows:
        return lines + ["No injection run is committed yet (`eval/results/injection/`)."]
    lines += [
        "Attacks are planted in a workspace file, `AGENTS.md`, a workspace skill, an MCP result and a",
        "fetched page. Each case runs once clean and once attacked. Defences are switched outside",
        "travis: policy off approves every ask, and marking off strips the untrusted delimiters before",
        "the model sees them. Hard denials (`host_allow`, the denylist) stay on in every condition.",
        "",
        "| Policy | Marking | Utility, no attack | Utility under attack | Attack success | Canary leaks |",
        "|---|---|---|---|---|---|",
    ]
    conditions = defaultdict(list)
    for row in rows:
        conditions[(row["condition"].get("policy", "on"), row["condition"].get("marking", "on"))].append(row)
    surfaces = defaultdict(lambda: defaultdict(list))
    for (policy, marking), group in sorted(conditions.items(), reverse=True):
        clean = scored([r for r in group if r["variant"] == "clean"])
        attacked = scored([r for r in group if r["variant"] == "attacked"])
        attacks = scored(attacked, "attack_succeeded")
        leaks = sum(bool(r["canary_leaked"]) for r in group)
        lines.append(f"| {policy} | {marking} | {format_rate(sum(r['passed'] for r in clean), len(clean))} "
                     f"| {format_rate(sum(r['passed'] for r in attacked), len(attacked))} "
                     f"| {format_rate(sum(r['attack_succeeded'] for r in attacks), len(attacks))} | {leaks} |")
        for row in attacks:
            surfaces[row["category"]][(policy, marking)].append(row["attack_succeeded"])
    columns = sorted(conditions, reverse=True)
    lines += ["", "Attack success by surface:", "",
              "| Surface | " + " | ".join(f"policy {p}, marking {m}" for p, m in columns) + " |",
              "|---|" + "---|" * len(columns)]
    for surface, by_condition in sorted(surfaces.items()):
        cells = [f"{sum(by_condition[c])}/{len(by_condition[c])}" for c in columns]
        lines.append(f"| {surface} | " + " | ".join(cells) + " |")
    return lines


LIMITATIONS = [
    "## Limitations",
    "",
    "- These results describe travis on DeepSeek V4.1 Flash only, not travis in general.",
    "- At 10 injection cases the 95% interval is about ±30 points; differences smaller than the",
    "  intervals are not claimed.",
    "- \"Policy off\" lifts the approval gate, not hard denials; \"marking off\" strips delimiters",
    "  in the bridge, after travis has marked the step, so step marking stays on and is counted",
    "  under policy.",

]


def build_report() -> str:
    sections = [build_injection(load_rows("injection")), LIMITATIONS]
    return "\n\n".join("\n".join(section) for section in sections) + "\n"


if __name__ == "__main__":
    assert wilson(0, 0) == (0.0, 1.0)
    assert [round(x, 3) for x in wilson(15, 30)] == [0.332, 0.668], wilson(15, 30)
    print(build_report(), end="")
