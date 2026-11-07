"""Live runner (6B): run tasks k times against one model, then write one JSONL row per run beside the log."""

import argparse
import json
import sys
from pathlib import Path

from inspect_ai import eval
from inspect_ai.log import EvalLog, read_eval_log
from inspect_ai.model import ModelCost, ModelInfo, set_model_info
from inspect_ai.scorer import CORRECT, INCORRECT

DEFAULT_MODEL = "openrouter/deepseek/deepseek-v4.1-flash-1017"

# USD per million tokens. Inspect's model database lacks these, and cost_limit refuses a model
# with no price, so a model missing here cannot be run. Cache reads are billed at the full input
# price as an upper bound (phase-6-spike.md). mockllm is free and checks the pipeline offline.
PRICES = {
    DEFAULT_MODEL: ModelCost(input=0.15, output=0.60, input_cache_write=0.15, input_cache_read=0.15),
    # DeepSeek's own API serves the same V4.1-Flash-1017 weights as deepseek-flash (phase-6-spike.md).
    "deepseek/deepseek-flash": ModelCost(input=0.15, output=0.60, input_cache_write=0.15, input_cache_read=0.15),
    "mockllm/model": ModelCost(input=0, output=0, input_cache_write=0, input_cache_read=0),
}


def read_verdict(sample, scorer_name: str) -> bool | None:
    """CORRECT as a bool; None when the scorer did not run or the harness failed, since that
    run says nothing about the agent."""
    score = (sample.scores or {}).get(scorer_name)
    return None if sample.error or score is None else score.value == CORRECT


def build_result_rows(log: EvalLog) -> list[dict]:
    rows = []
    for sample in log.samples or []:
        usage = sample.model_usage.values()
        judged = (sample.scores or {}).get("judge")
        rows.append({
            "task": f"{log.eval.task}/{sample.id}",
            "model": log.eval.model,
            "trial": sample.epoch,
            "condition": log.eval.task_args,
            "variant": (sample.metadata or {}).get("variant"),
            "category": (sample.metadata or {}).get("category") or (sample.metadata or {}).get("surface"),
            "passed": read_verdict(sample, "utility"),
            "attack_succeeded": read_verdict(sample, "attack"),
            "canary_leaked": read_verdict(sample, "canary"),
            # 6D decides which criteria count; until then the judge is recorded, not scored.
            "judge": None if sample.error or not judged or judged.explanation != "judged"
            # A verdict the judge's reply did not state (NaN) is unknown, not a fail.
            else {c: (v == CORRECT) if v in (CORRECT, INCORRECT) else None for c, v in judged.value.items()},
            "steps": sum(1 for event in sample.events if event.event == "model"),
            "tokens": sum(u.total_tokens for u in usage),
            "cost_usd": sum(u.total_cost or 0 for u in usage),
            "latency_ms": round((sample.total_time or 0) * 1000),
            "transcript": f"{Path(log.location).name}#{sample.id}/{sample.epoch}",
        })
    return rows


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tasks", nargs="*", default=["tasks.py@travis_tasks"], help="Inspect tasks")
    parser.add_argument("-T", dest="task_args", action="append", default=[], metavar="NAME=VALUE",
                        help="task argument, e.g. -T split=all -T policy=off")
    parser.add_argument("--log-dir", default="logs", help="where the .eval logs and results JSONL go")
    parser.add_argument("--sample-id", help="run only these samples, comma-separated")
    parser.add_argument("--model", default=DEFAULT_MODEL, choices=sorted(PRICES))
    parser.add_argument("--trials", type=int, default=3, help="k: runs per task")
    # Soft stop. 0.05 is ~17x the spike's measured cost per run, so 30 tasks x 3 trials stays
    # under the USD 5.60 published-run share; the provider key's credit limit is the hard stop.
    parser.add_argument("--max-cost-per-run", type=float, default=0.05, help="USD; Inspect ends a run that passes it")
    args = parser.parse_args()

    set_model_info(args.model, ModelInfo(cost=PRICES[args.model]))
    task_args = dict(arg.split("=", 1) for arg in args.task_args)
    logs = eval(args.tasks, model=args.model, epochs=args.trials, cost_limit=args.max_cost_per_run,
                task_args=task_args, log_dir=args.log_dir,
                sample_id=args.sample_id.split(",") if args.sample_id else None)

    for log in logs:
        results = Path(log.location).with_suffix(".jsonl")
        rows = build_result_rows(read_eval_log(log.location))
        results.write_text("".join(json.dumps(row) + "\n" for row in rows))
        print(f"{len(rows)} results written to {results}")
    return 0 if all(log.status == "success" for log in logs) else 1


if __name__ == "__main__":
    sys.exit(main())
