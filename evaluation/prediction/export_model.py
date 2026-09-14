#!/usr/bin/env python3
"""[I10]/[I12] Export a model for internal/predictor from an evaluation that has
already run, without running it again.

evaluate.py --export-model does the same at the end of a full evaluation, which
takes the better part of half an hour. This reads the per-run datasets and the
--json-out results of that evaluation instead: the model is refitted on every
(function, run) row exactly as evaluate.py would fit it (seed 0), and the
operating point is the balanced-point threshold evaluate.py selected by inner CV,
read from the results. So the exported model and the numbers reported for it
cannot drift apart, and the results must come from the same runs.

    python3 evaluation/prediction/export_model.py --kind rf \
        --dataset evaluation/prediction/dataset-20260904-190539.csv \
        --dataset evaluation/prediction/dataset-20260911-165103.csv \
        --dataset evaluation/prediction/dataset-20260912-172904.csv \
        --results evaluation/prediction/results-replicates-f9e30f4b.json \
        --out evaluation/prediction/model-replicates-f9e30f4b-rf.json
"""
import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from evaluate import EXPORT_KINDS, Corpus, export_model  # noqa: E402


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dataset", action="append", required=True,
                    help="dataset CSV; repeat once per run, as for the evaluation")
    ap.add_argument("--results", required=True, help="evaluate.py --json-out of those runs")
    ap.add_argument("--kind", choices=sorted(EXPORT_KINDS), required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--label", default="all_tests_passed")
    ap.add_argument("--feature-schema-version", type=int, default=1)
    args = ap.parse_args()

    c = Corpus(args.dataset, args.label)
    res = json.load(open(args.results))
    if res.get("runs") != c.run_ids:
        raise SystemExit("%s was computed on runs %s, not %s"
                         % (args.results, res.get("runs"), c.run_ids))
    label = EXPORT_KINDS[args.kind][0]
    rows = res["policies"].get("%s [balanced pt]" % label, [])
    if not rows:
        raise SystemExit("%s holds no %s results to take the threshold from" % (args.results, label))
    repeats = len({r.get("repeat", 0) for r in rows})
    export_model(args.out, c, res["horizon"], 10, repeats, args.feature_schema_version,
                 res["policies"], kind=args.kind)
    print("wrote %s" % args.out)


if __name__ == "__main__":
    main()
