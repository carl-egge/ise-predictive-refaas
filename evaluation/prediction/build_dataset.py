#!/usr/bin/env python3
"""[I4] Build the single feature/label/cost table every prediction method consumes.

Joins three artifacts that already exist for a completed run -- no translation is
re-executed:

  * ex-ante features + group_id   -- `go run ./cmd/pyscan -json <artifacts>`
  * outcome labels                -- the run log (`runs/run-<ts>.jsonl`)
  * measured translation energy   -- `go run ./cmd/energy -json -runtime ... <run log>`
  * per-invocation delta-E        -- `evaluation/runtime-<run-id>.json` ([H6])

Output: evaluation/prediction/dataset-<run-id>.csv, one row per function.
"""
import argparse
import csv
import json
import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def load_features(path):
    """function_id -> dict of the pyscan CSV row (features + bucket/aws/group_id)."""
    with open(path, newline="") as fh:
        return {r["function_id"]: r for r in csv.DictReader(fh)}


def load_labels(run_log):
    """function_id -> label dict, from the run log's job records.

    `all_tests_passed` is the settled [I1] label. `completed`, `pass_fraction` and
    `shape_only_fraction` are the secondary columns [I1] asked to carry along.
    Test outcomes describe the *last* validation round only ([A19]).
    """
    out = {}
    with open(run_log) as fh:
        for line in fh:
            rec = json.loads(line)
            if rec.get("type") != "job":
                continue
            m = rec["metrics"]
            fid = m.get("function_id") or rec.get("function_id")
            outcomes = m.get("test_outcomes") or []
            cases = m.get("test_cases") or {}
            if outcomes:
                n = len(outcomes)
                passed = sum(1 for o in outcomes if o.get("passed"))
                shape = sum(1 for o in outcomes if o.get("output_mode") == "shape")
                routes = sorted({o.get("route", "") for o in outcomes})
            else:
                # Pre-[A19] logs, and jobs that died before validation, only have
                # the last-write-wins map.
                n = len(cases)
                passed = sum(1 for v in cases.values() if v)
                shape = 0
                routes = []
            out[fid] = {
                # meta.type is the dataset's workload-character axis (pure/compute vs
                # network/io); [I9] step 2 groups the measured delta-E on it.
                "meta_type": (m.get("meta") or {}).get("type", ""),
                "completed": int(bool(rec.get("completed", True))),
                "all_tests_passed": int(n > 0 and passed == n),
                "n_tests_run": n,
                "pass_fraction": round(passed / n, 6) if n else 0.0,
                "shape_only_fraction": round(shape / n, 6) if n else 0.0,
                "route": "|".join(r for r in routes if r),
                "reached_validation": int(n > 0),
            }
    return out


def load_costs(energy_json):
    """function_id -> measured facility joules for that translation attempt."""
    d = json.load(open(energy_json))
    rows = list(d["translations"]) + list(d.get("failed_attempts", {}).get("translations", []))
    costs = {}
    for t in rows:
        costs[t["function_id"]] = {
            "e_translation_joules": t["facility_joules"],
            "e_translation_compute_joules": t["compute_joules"],
            "repair_joules": t.get("repair_joules", 0.0),
            "prompt_tokens": t.get("prompt_tokens", 0),
            "eval_tokens": t.get("eval_tokens", 0),
            "co2e_grams": t.get("co2e_grams", 0.0),
        }
    break_even = d.get("break_even", {}) or {}
    return costs, break_even.get("per_function", {}) or {}


def load_runtime(path):
    """function_id -> per-invocation joules for both sides ([H6]); successes only."""
    if not path or not os.path.exists(path):
        return {}
    return json.load(open(path))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--features", required=True, help="cmd/pyscan CSV")
    ap.add_argument("--run-log", required=True)
    ap.add_argument("--energy", required=True, help="cmd/energy -json output")
    ap.add_argument("--runtime", default="", help="[H6] runtime-<run-id>.json")
    ap.add_argument("--run-id", required=True)
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    feats = load_features(args.features)
    labels = load_labels(args.run_log)
    costs, nstar = load_costs(args.energy)
    runtime = load_runtime(args.runtime)

    missing_label = sorted(set(feats) - set(labels))
    missing_feat = sorted(set(labels) - set(feats))
    if missing_feat:
        sys.exit(f"functions in the run log with no feature row: {missing_feat}")
    if missing_label:
        print(f"warning: {len(missing_label)} scanned functions absent from the run log: "
              f"{missing_label}", file=sys.stderr)

    feature_cols = [c for c in next(iter(feats.values()))
                    if c not in ("function_id", "artifact", "bucket", "aws", "group_id")]
    label_cols = ["all_tests_passed", "completed", "pass_fraction", "shape_only_fraction",
                  "n_tests_run", "reached_validation", "route", "meta_type"]
    cost_cols = ["e_translation_joules", "e_translation_compute_joules", "repair_joules",
                 "prompt_tokens", "eval_tokens", "co2e_grams",
                 "python_joules_per_invocation", "go_joules_per_invocation",
                 "delta_e_joules_per_invocation", "n_star", "runtime_measured"]
    header = (["function_id", "artifact", "bucket", "aws", "group_id"]
              + feature_cols + label_cols + cost_cols)

    out = args.out or os.path.join(REPO, "evaluation", "prediction",
                                   f"dataset-{args.run_id}.csv")
    n = 0
    with open(out, "w", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=header)
        w.writeheader()
        for fid in sorted(feats, key=lambda s: (len(s), s)):
            if fid not in labels:
                continue
            row = dict(feats[fid])
            row.update(labels[fid])
            row.update(costs.get(fid, {c: "" for c in cost_cols[:6]}))
            rt = runtime.get(fid)
            if rt:
                py = rt["python_joules_per_invocation"]
                go = rt["go_joules_per_invocation"]
                row.update(python_joules_per_invocation=py,
                           go_joules_per_invocation=go,
                           delta_e_joules_per_invocation=py - go,
                           runtime_measured=1)
            else:
                row.update(python_joules_per_invocation="", go_joules_per_invocation="",
                           delta_e_joules_per_invocation="", runtime_measured=0)
            row["n_star"] = nstar.get(fid, "")
            w.writerow({k: row.get(k, "") for k in header})
            n += 1

    pos = sum(labels[f]["all_tests_passed"] for f in feats if f in labels)
    groups = len({feats[f]["group_id"] for f in feats if f in labels})
    print(f"wrote {out}: {n} rows, {len(header)} columns")
    print(f"  label all_tests_passed: {pos} positive / {n} ({pos / n:.1%})")
    print(f"  independent groups ([I11]): {groups}")
    print(f"  runtime delta-E measured for {sum(1 for f in feats if f in runtime)} functions")


if __name__ == "__main__":
    main()
