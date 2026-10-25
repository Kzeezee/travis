"""Live runner (6B): run tasks k times against one model, then write one JSONL row per run beside the log."""

import argparse
import json
import sys
from pathlib import Path

from inspect_ai import eval
from inspect_ai.log import EvalLog, read_eval_log
from inspect_ai.model import ModelCost, ModelInfo, set_model_info
from inspect_ai.scorer import CORRECT

DEFAULT_MODEL = "openrouter/deepseek/deepseek-v4.1-flash-1017"

# USD per million tokens. Inspect's model database lacks these, and cost_limit refuses a model
# with no price, so a model missing here cannot be run. Cache reads are billed at the full input
# price as an upper bound (phase-6-spike.md). mockllm is free and checks the pipeline offline.
PRICES = {
    DEFAULT_MODEL: ModelCost(input=0.15, output=0.60, input_cache_write=0.15, input_cache_read=0.15),
    "mockllm/model": ModelCost(input=0, output=0, input_cache_write=0, input_cache_read=0),
}


def build_result_rows(log: EvalLog) -> list[dict]:
    rows = []
    for sample in log.samples or []:
        score = next(iter((sample.scores or {}).values()), None)
        usage = sample.model_usage.values()
        rows.append({
            "task": f"{log.eval.task}/{sample.id}",
            "model": log.eval.model,
            "trial": sample.epoch,
            # None when the harness failed: that run says nothing about the agent.
            "passed": None if sample.error or score is None else score.value == CORRECT,
            "steps": sum(1 for event in sample.events if event.event == "model"),
            "tokens": sum(u.total_tokens for u in usage),
            "cost_usd": sum(u.total_cost or 0 for u in usage),
            "latency_ms": round((sample.total_time or 0) * 1000),
            "transcript": f"{Path(log.location).name}#{sample.id}/{sample.epoch}",
        })
    return rows


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tasks", nargs="*", default=["notes.py"], help="Inspect task files")
    parser.add_argument("--model", default=DEFAULT_MODEL, choices=sorted(PRICES))
    parser.add_argument("--trials", type=int, default=3, help="k: runs per task")
    # Soft stop. 0.05 is ~17x the spike's measured cost per run, so 30 tasks x 3 trials stays
    # under the USD 5.60 published-run share; the provider key's credit limit is the hard stop.
    parser.add_argument("--max-cost-per-run", type=float, default=0.05, help="USD; Inspect ends a run that passes it")
    args = parser.parse_args()

    set_model_info(args.model, ModelInfo(cost=PRICES[args.model]))
    logs = eval(args.tasks, model=args.model, epochs=args.trials, cost_limit=args.max_cost_per_run)

    for log in logs:
        results = Path(log.location).with_suffix(".jsonl")
        rows = build_result_rows(read_eval_log(log.location))
        results.write_text("".join(json.dumps(row) + "\n" for row in rows))
        print(f"{len(rows)} results written to {results}")
    return 0 if all(log.status == "success" for log in logs) else 1


if __name__ == "__main__":
    sys.exit(main())
