"""Re-grade stored logs with the current judge, without re-running the agent, then rewrite each
log and its results JSONL in place. Run: uv run --env-file .env python rescore.py ../results/judge/*.eval
`inspect score` cannot do this: overwrite drops the utility and canary scores, and append keeps
the old verdict and files the new one as judge-1. Rewriting in place keeps the log's name, so
the transcript ids in eval/judge/labels.csv still match."""

import sys

from inspect_ai import score
from inspect_ai.log import read_eval_log, write_eval_log

from live import write_result_rows
from tasks import judge


def main(locations: list[str]) -> int:
    for location in locations:
        log = read_eval_log(location)
        for sample in log.samples or []:
            (sample.scores or {}).pop("judge", None)
        write_eval_log(score(log, judge(), action="append", display="none"), location)
        write_result_rows(location)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
