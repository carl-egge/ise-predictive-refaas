#!/usr/bin/env python3
"""[I10] Regenerate internal/predictor's train/serve parity fixture.

The Go side of the gate must reproduce the probabilities scikit-learn actually
produces. Every number in [I7] comes from scikit-learn, so if the shipped reader
disagrees with it the service is deploying a different classifier from the one
that was evaluated -- by a margin far too small to notice in a log and more than
large enough to flip a candidate sitting on the threshold.

This writes the golden file that pins it: for every function in the corpus, the
feature values and the probability scikit-learn assigns them under the exported
model. `go test ./internal/predictor/...` replays it.

Run this whenever a model is re-exported, with the same --dataset list the
export used. It writes internal/predictor/testdata/parity-<kind>.json and copies
the model beside it as model-<kind>.json, for both model kinds the reader
implements.
"""
import argparse
import json
import os
import shutil
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from evaluate import Corpus, make_model  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
KINDS = {"logistic_regression": "lr", "random_forest": "rf"}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dataset", action="append", required=True,
                    help="dataset CSV; repeat once per replicate run, as for the export")
    ap.add_argument("--model", required=True, help="the exported model JSON")
    ap.add_argument("--label", default="all_tests_passed")
    ap.add_argument("--testdata", default=os.path.join(REPO, "internal", "predictor", "testdata"))
    args = ap.parse_args()

    model = json.load(open(args.model))
    kind = KINDS.get(model.get("model"))
    if kind is None:
        raise SystemExit("%s is a %r model; internal/predictor implements %s"
                         % (args.model, model.get("model"), sorted(KINDS)))

    c = Corpus(args.dataset, args.label)
    Xs, Ys, _, _, _ = c.stacked()
    # The same full-corpus refit the export uses (every (function, run) row, seed
    # 0), so the fixture pins the model that was actually shipped rather than a
    # fold's. One case per function: the features are shared by every run.
    fitted = make_model(kind, 0).fit(Xs, Ys)
    probs = fitted.predict_proba(c.X)[:, 1]
    if kind == "rf":
        trees = fitted.named_steps["clf"].estimators_
        if len(trees) != len(model["trees"]) or any(
                e.tree_.node_count != len(t["left"]) for e, t in zip(trees, model["trees"])):
            raise SystemExit("the refit forest does not match the exported one; export and "
                             "parity must use the same datasets")

    out = os.path.join(args.testdata, "parity-%s.json" % kind)
    model_name = "model-%s.json" % kind
    payload = {
        # The Go test resolves this next to itself, so it is a bare filename.
        "model": model_name,
        "feature_names": c.cols,
        "cases": [
            {
                "function_id": fid,
                "values": [float(v) for v in c.X[i]],
                "sklearn_score": float(probs[i]),
                "translate": bool(probs[i] >= model["threshold"]),
            }
            for i, fid in enumerate(c.ids)
        ],
    }
    os.makedirs(args.testdata, exist_ok=True)
    with open(out, "w") as fh:
        json.dump(payload, fh, indent=1)
        fh.write("\n")
    shutil.copyfile(args.model, os.path.join(args.testdata, model_name))
    print("wrote %s: %d cases, scores %.4f .. %.4f, model copied to %s"
          % (out, len(payload["cases"]), probs.min(), probs.max(), model_name))


if __name__ == "__main__":
    main()
