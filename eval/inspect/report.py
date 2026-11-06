"""Turns committed eval results into docs/EVAL.md (6E, 6D, 6F). Reads files only, makes no model
calls, and writes nothing that depends on when it runs, so regenerating it gives no diff."""

import csv
import json
import math
import statistics
import sys
from collections import defaultdict
from pathlib import Path

HERE = Path(__file__).parent
RESULTS = HERE.parent / "results"
LABELS = HERE.parent / "judge" / "labels.csv"
FAILURES = RESULTS / "published" / "failures.csv"
REPORT = HERE.parent.parent / "docs" / "EVAL.md"
KAPPA_THRESHOLD = 0.6


def wilson(successes: int, n: int, z: float = 1.96) -> tuple[float, float]:
    """95% Wilson score interval for a proportion; (0, 1) when there is no data."""
    if n == 0:
        return 0.0, 1.0
    p = successes / n
    centre = (p + z * z / (2 * n)) / (1 + z * z / n)
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / (1 + z * z / n)
    return max(0.0, centre - half), min(1.0, centre + half)


def cohen_kappa(pairs: list[tuple[bool, bool]]) -> float:
    """Agreement beyond chance between two raters' yes/no labels; 1.0 when both never vary together."""
    n = len(pairs)
    observed = sum(a == b for a, b in pairs) / n
    yes_a, yes_b = sum(a for a, _ in pairs) / n, sum(b for _, b in pairs) / n
    expected = yes_a * yes_b + (1 - yes_a) * (1 - yes_b)
    return 1.0 if expected == 1 else (observed - expected) / (1 - expected)


def format_rate(successes: int, n: int) -> str:
    low, high = wilson(successes, n)
    return f"{successes}/{n} = {100 * successes / n:.0f}% [{100 * low:.0f}, {100 * high:.0f}]" if n else "no runs"


def load_rows(folder: str) -> list[dict]:
    rows = []
    for path in sorted((RESULTS / folder).glob("*.jsonl")):
        rows += [{**json.loads(line), "log_dir": folder} for line in path.read_text().splitlines() if line]
    return sorted(rows, key=lambda r: (r["transcript"], r["task"], r["trial"]))


def link_transcript(row: dict) -> str:
    log, _, sample = row["transcript"].partition("#")
    return f"[{sample}](../eval/results/{row['log_dir']}/{log})"


def scored(rows: list[dict], field: str = "passed") -> list[dict]:
    """Rows where the field was graded; a harness failure (None) says nothing about the agent."""
    return [r for r in rows if r[field] is not None]


def build_method(published: list[dict]) -> list[str]:
    dates = sorted({r["transcript"][:10] for r in published})
    models = sorted({r["model"] for r in published})
    tasks = sorted({r["task"] for r in published})
    trials = max((r["trial"] for r in published), default=0)
    return [
        "## Method",
        "",
        "Each task runs travis headless (`travis run`, eval builds only) in a Docker container, with its",
        "model calls routed through Inspect AI's agent bridge. Tasks are in `eval/inspect/tasks.toml`; each",
        "is graded by code on the final files, the tools called and the answer. Approvals and `ask_user`",
        "replies are scripted per task and fail closed once they run out. travis sees today as",
        "2026-10-01 in every run, so fixture dates stay in the future.",
        "",
        f"- Models: {', '.join(models) or 'none yet'}",
        f"- Run dates: {', '.join(dates) or 'none yet'}",
        f"- Tasks: {len(tasks)}, trials per task: {trials}",
    ]


def build_scorecard(published: list[dict]) -> list[str]:
    lines = ["## Results", ""]
    graded = scored(published)
    if not graded:
        return lines + ["No published run is committed yet (`eval/results/published/`)."]
    by_category = defaultdict(list)
    for row in graded:
        by_category[row["category"]].append(row)
    lines += ["| Category | pass@1 [95% CI] | pass^k [95% CI] |", "|---|---|---|"]
    for category, rows in sorted(by_category.items()) + [("**all**", graded)]:
        trials = defaultdict(list)
        for row in rows:
            trials[row["task"]].append(row["passed"])
        all_passed = sum(all(results) for results in trials.values())
        lines.append(f"| {category} | {format_rate(sum(r['passed'] for r in rows), len(rows))} "
                     f"| {format_rate(all_passed, len(trials))} |")
    errors = len(published) - len(graded)
    costs = [r["cost_usd"] for r in published]
    latencies = [r["latency_ms"] / 1000 for r in published]
    lines += [
        "",
        "pass@1 counts every trial; pass^k counts a task only if it passed in all of its trials.",
        f"Harness errors, excluded above: {errors}.",
        "",
        f"Cost: USD {sum(costs):.4f} in total, USD {statistics.mean(costs):.4f} per run. "
        f"Latency: median {statistics.median(latencies):.1f} s, max {max(latencies):.1f} s. "
        f"Steps: mean {statistics.mean(r['steps'] for r in published):.1f}.",
    ]
    return lines


def build_judge(rows: list[dict]) -> tuple[list[str], set[str]]:
    """The agreement table, and the criteria that pass the threshold and so count."""
    lines = ["## Judge calibration", ""]
    verdicts = {r["transcript"]: r["judge"] for r in rows if r["judge"]}
    pairs = defaultdict(list)
    if LABELS.exists():
        for label in csv.DictReader(LABELS.open()):
            judged = verdicts.get(label["transcript"], {})
            # An unparsed verdict (None) or an unlabelled row says nothing about agreement.
            if judged.get(label["criterion"]) is not None and label["human"] in ("0", "1"):
                pairs[label["criterion"]].append((label["human"] == "1", judged[label["criterion"]]))
    counted = set()
    if pairs:
        lines += [f"Threshold: Cohen's κ ≥ {KAPPA_THRESHOLD}. Only criteria that pass count in the scorecard.", "",
                  "| Criterion | Labelled | Agreement | κ | Counts |", "|---|---|---|---|---|"]
        for criterion, criterion_pairs in sorted(pairs.items()):
            kappa = cohen_kappa(criterion_pairs)
            agreement = sum(a == b for a, b in criterion_pairs) / len(criterion_pairs)
            if kappa >= KAPPA_THRESHOLD:
                counted.add(criterion)
            lines.append(f"| {criterion} | {len(criterion_pairs)} | {100 * agreement:.0f}% | {kappa:.2f} "
                         f"| {'yes' if kappa >= KAPPA_THRESHOLD else 'no'} |")
    else:
        lines.append("No transcript is hand-labelled yet (a 0 or 1 in `human` in `eval/judge/labels.csv`), "
                     "so no judge criterion counts.")
    known_bad = [r for r in rows if r["task"].startswith("known_bad/") and r["judge"]]
    if known_bad:
        failed = sum(all(verdict is False for verdict in r["judge"].values()) for r in known_bad)
        lines += ["", f"Known-bad answers the judge failed: {failed}/{len(known_bad)}."]
    published = [r for r in rows if r["log_dir"] == "published" and r["judge"]]
    if counted:
        lines.append("")
    for criterion in sorted(counted):
        judged = [r["judge"][criterion] for r in published if r["judge"].get(criterion) is not None]
        lines.append(f"- {criterion} on the published run: {format_rate(sum(judged), len(judged))}")
    return lines, counted


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


def build_ablations(ablations: list[dict], published: list[dict]) -> list[str]:
    lines = ["## Ablations", ""]
    baseline = scored([r for r in published if r["trial"] == 1])
    groups = defaultdict(list)
    for row in scored(ablations):
        changed = {k: v for k, v in row["condition"].items() if (k, v) in (("hide_tools", "fs_edit"), ("skills", "off"))}
        groups[", ".join(f"{k}={v}" for k, v in sorted(changed.items())) or "baseline"].append(row)
    lines += ["One trial per task. The baseline is the published run's first trial.", "",
              "| Condition | pass@1 [95% CI] |", "|---|---|",
              f"| baseline | {format_rate(sum(r['passed'] for r in baseline), len(baseline))} |"]
    for name, rows in sorted(groups.items()):
        lines.append(f"| {name} | {format_rate(sum(r['passed'] for r in rows), len(rows))} |")
    lines += ["",
              "- `hide_tools=fs_edit`: the model has `fs_write` only (D-08).",
              "- `skills=off`: no user skills are installed.",
              "- Policy and untrusted marking: see the injection table above.",
              "- Not run: pruning (it never engages below `max_exposed` = 60 tools, and this set registers",
              "  at most 3 MCP tools) and supervised vs collaborative autonomy (D-09; travis has no",
              "  supervised mode to switch to)."]
    return lines


def build_failures(published: list[dict]) -> list[str]:
    lines = ["## Error analysis", ""]
    if not FAILURES.exists():
        return lines + ["Failures are not yet coded (`eval/results/published/failures.csv`)."]
    by_transcript = {r["transcript"]: r for r in published}
    by_category = defaultdict(list)
    for failure in csv.DictReader(FAILURES.open()):
        by_category[failure["category"]].append(failure)
    lines += ["| Failure | Count | Example |", "|---|---|---|"]
    for category, failures in sorted(by_category.items(), key=lambda item: (-len(item[1]), item[0])):
        example = failures[0]
        row = by_transcript.get(example["transcript"])
        link = link_transcript(row) if row else example["transcript"]
        lines.append(f"| {category} | {len(failures)} | {link}: {example['note']} |")
    return lines


LIMITATIONS = [
    "## Limitations",
    "",
    "- These results describe travis on DeepSeek V4.1 Flash only, not travis in general.",
    "- At 30 tasks the 95% interval is about ±17 points, and at 10 injection cases about ±30.",
    "  Differences smaller than the intervals are not claimed.",
    "- The judge is the model under test, grading its own answers; criteria count only after",
    "  calibration against hand labels.",
    "- \"Policy off\" lifts the approval gate, not hard denials; \"marking off\" strips delimiters",
    "  in the bridge, after travis has marked the step, so step marking stays on and is counted",
    "  under policy.",
    "- Scripted approvals and replies stand in for a person, so a run never shows how a real user",
    "  would have answered an unexpected question.",
]


def build_report() -> str:
    published, injection, ablations = load_rows("published"), load_rows("injection"), load_rows("ablations")
    judge_lines, _ = build_judge(published + injection + ablations + load_rows("judge"))
    sections = [
        ["# travis evaluation", "", "Generated by `make eval-report` from the results committed under",
         "`eval/results/`. Do not edit by hand."],
        build_method(published),
        build_scorecard(published),
        judge_lines,
        build_injection(injection),
        build_ablations(ablations, published),
        build_failures(published),
        LIMITATIONS,
    ]
    return "\n\n".join("\n".join(section) for section in sections) + "\n"


def main() -> int:
    REPORT.write_text(build_report())
    print(f"wrote {REPORT}")
    return 0


if __name__ == "__main__":
    assert wilson(0, 0) == (0.0, 1.0)
    assert [round(x, 3) for x in wilson(15, 30)] == [0.332, 0.668], wilson(15, 30)
    assert cohen_kappa([(True, True), (False, False)]) == 1.0
    assert cohen_kappa([(True, False), (False, True)]) == -1.0
    sys.exit(main())
