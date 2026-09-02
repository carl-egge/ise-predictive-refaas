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

Run this whenever the model is re-exported, together with

    cp evaluation/prediction/model-<run-id>.json internal/predictor/testdata/model.json
"""
import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from evaluate import load, make_model  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dataset", required=True)
    ap.add_argument("--model", required=True, help="the exported model JSON")
    ap.add_argument("--label", default="all_tests_passed")
    ap.add_argument("--out", default=os.path.join(
        REPO, "internal", "predictor", "testdata", "parity.json"))
    args = ap.parse_args()

    rows, cols, X, y, groups, E, dE, ids, aws = load(args.dataset, args.label)
    # The same full-corpus refit the export uses, so the fixture pins the
    # coefficients that were actually shipped rather than a fold's.
    probs = make_model("lr", 0).fit(X, y).predict_proba(X)[:, 1]
    model = json.load(open(args.model))

    payload = {
        # The Go test resolves this next to itself, so it is a bare filename.
        "model": "model.json",
        "feature_names": cols,
        "cases": [
            {
                "function_id": fid,
                "values": [float(v) for v in X[i]],
                "sklearn_score": float(probs[i]),
                "translate": bool(probs[i] >= model["threshold"]),
            }
            for i, fid in enumerate(ids)
        ],
    }
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w") as fh:
        json.dump(payload, fh, indent=1)
        fh.write("\n")
    print("wrote %s: %d cases, scores %.4f .. %.4f"
          % (args.out, len(payload["cases"]), probs.min(), probs.max()))
    print("  remember to copy the model itself alongside it:")
    print("    cp %s internal/predictor/testdata/model.json" % args.model)


if __name__ == "__main__":
    main()
