#!/usr/bin/env python3
"""[I5]/[I6]/[I7] Offline counterfactual evaluation of an ex-ante prediction gate.

Nothing here re-runs a translation. Every function in the corpus was translated
once ([I1], run 20260831-190900), so for each one we already know

  * y_i  -- whether the translation succeeded (`all_tests_passed`)
  * E_i  -- what the attempt actually cost in facility joules (cmd/energy)
  * dE_i -- the per-invocation energy the Go version saves over Python ([H6])

A gate is therefore a *replay*: a decision vector d over the 95 rows, whose cost
and benefit are sums of already-measured quantities. The only quantity a replay
cannot supply is the predictor's own inference cost, which [I8] measures separately.

    spend(d)      = sum_i d_i * E_i
    benefit(d, N) = sum_i d_i * y_i * N * dE_i
    net(d, N)     = benefit(d, N) - spend(d)

Protocol ([I7]): repeated StratifiedGroupKFold (5 x 10) on `group_id`, with every
fitted quantity -- zero-variance filtering, standardization, class weighting and
the decision threshold -- learned inside the training fold via an inner CV.
"""
import argparse
import csv
import json

import numpy as np
from sklearn.ensemble import RandomForestClassifier
from sklearn.feature_selection import VarianceThreshold
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import roc_auc_score
from sklearn.model_selection import StratifiedGroupKFold
from sklearn.pipeline import Pipeline
from sklearn.preprocessing import StandardScaler

WH = 3600.0  # joules per watt-hour

NON_FEATURE = {"function_id", "artifact", "bucket", "aws", "group_id"}
LABEL_COLS = {"all_tests_passed", "completed", "pass_fraction", "shape_only_fraction",
              "n_tests_run", "reached_validation", "route", "meta_type"}
COST_COLS = {"e_translation_joules", "e_translation_compute_joules", "repair_joules",
             "prompt_tokens", "eval_tokens", "co2e_grams",
             "python_joules_per_invocation", "go_joules_per_invocation",
             "delta_e_joules_per_invocation", "n_star", "runtime_measured"}


def load(path, label):
    rows = list(csv.DictReader(open(path, newline="")))
    cols = [c for c in rows[0]
            if c not in NON_FEATURE and c not in LABEL_COLS and c not in COST_COLS]
    X = np.array([[float(r[c]) for c in cols] for r in rows])
    y = np.array([int(r[label]) for r in rows])
    groups = np.array([r["group_id"] for r in rows])
    E = np.array([float(r["e_translation_joules"]) for r in rows])
    dE = np.array([float(r["delta_e_joules_per_invocation"] or 0.0) for r in rows])
    ids = [r["function_id"] for r in rows]
    aws = np.array([1 if r["aws"] in ("true", "1", "True") else 0 for r in rows])
    return rows, cols, X, y, groups, E, dE, ids, aws


def net(d, y, E, dE, N):
    m = d.astype(bool)
    return float((y[m] * N * dE[m]).sum() - E[m].sum())


def summarize(d, y, E, dE, horizons):
    m = d.astype(bool)
    kept = int(y[m].sum())
    spend = float(E[m].sum())
    out = {
        "translated": int(m.sum()),
        "successes_kept": kept,
        "successes_forfeited": int(y.sum() - kept),
        "spend_wh": spend / WH,
        "wh_per_success": (spend / WH / kept) if kept else float("inf"),
        "accuracy": float((m == y.astype(bool)).mean()),
        "precision": float(y[m].mean()) if m.sum() else float("nan"),
        "recall": float(kept / y.sum()) if y.sum() else float("nan"),
    }
    for N in horizons:
        out["net_wh_N%g" % N] = net(d, y, E, dE, N) / WH
    return out


def pick_threshold(p, y, E, dE, N, objective):
    """Decision threshold, a fitted quantity -- only ever called on a training
    fold's inner-CV predictions ([I7]: never on the test fold).

    Two operating points are reported because they answer different questions.
    `energy` maximizes net joules at horizon N: the gate as an energy instrument.
    `balanced` maximizes balanced accuracy: the gate as a feasibility classifier,
    which is what "does the predictor work?" usually means.
    """
    best_t, best_v = 0.5, -np.inf
    for t in np.unique(np.concatenate([[0.0, 1.01], p])):
        d = (p >= t).astype(int)
        if objective == "energy":
            v = net(d, y, E, dE, N)
        else:
            tpr = d[y == 1].mean() if (y == 1).any() else 0.0
            tnr = 1.0 - (d[y == 0].mean() if (y == 0).any() else 0.0)
            v = 0.5 * (tpr + tnr)
        if v > best_v:
            best_v, best_t = v, t
    return best_t


def make_model(kind, seed):
    if kind == "lr":
        return Pipeline([
            ("var", VarianceThreshold(0.0)),
            ("sc", StandardScaler()),
            ("clf", LogisticRegression(C=1.0, class_weight="balanced",
                                       max_iter=5000, random_state=seed)),
        ])
    return Pipeline([
        ("var", VarianceThreshold(0.0)),
        ("clf", RandomForestClassifier(n_estimators=500, min_samples_leaf=2,
                                       class_weight="balanced_subsample",
                                       random_state=seed, n_jobs=-1)),
    ])


def oof_probs_and_thresholds(kind, X, y, groups, E, dE, N, n_splits, seed):
    """Out-of-fold probabilities plus, per fold, thresholds chosen by an inner CV
    on the training fold only ([I7]: the operating point is a fitted quantity)."""
    p = np.zeros(len(y))
    thr = {"energy": np.zeros(len(y)), "balanced": np.zeros(len(y))}
    outer = StratifiedGroupKFold(n_splits=n_splits, shuffle=True, random_state=seed)
    for tr, te in outer.split(X, y, groups):
        inner_p = np.zeros(len(tr))
        inner = StratifiedGroupKFold(n_splits=5, shuffle=True, random_state=seed + 1)
        for itr, ite in inner.split(X[tr], y[tr], groups[tr]):
            m = make_model(kind, seed)
            m.fit(X[tr][itr], y[tr][itr])
            inner_p[ite] = m.predict_proba(X[tr][ite])[:, 1]
        for obj in thr:
            thr[obj][te] = pick_threshold(inner_p, y[tr], E[tr], dE[tr], N, obj)
        m = make_model(kind, seed)
        m.fit(X[tr], y[tr])
        p[te] = m.predict_proba(X[te])[:, 1]
    return p, thr


def baseline_decisions(name, X, cols, y, groups, E, dE, aws, N, n_splits, seed):
    n = len(y)
    if name.startswith("B0"):
        return np.ones(n, int)
    if name.startswith("B1"):
        return np.zeros(n, int)
    if name.startswith("B4"):
        j = cols.index("has_infeasible_lib")
        return (X[:, j] == 0).astype(int)
    if name.startswith("B5"):
        return (aws == 0).astype(int)
    d = np.zeros(n, int)
    outer = StratifiedGroupKFold(n_splits=n_splits, shuffle=True, random_state=seed)
    for tr, te in outer.split(X, y, groups):
        if name.startswith("B2"):
            d[te] = 1 if y[tr].mean() >= 0.5 else 0
        elif name.startswith("B3"):
            j = cols.index("cc")
            best_t, best_v = None, -np.inf
            for t in np.unique(X[tr, j]):
                v = net((X[tr, j] <= t).astype(int), y[tr], E[tr], dE[tr], N)
                if v > best_v:
                    best_v, best_t = v, t
            d[te] = (X[te, j] <= best_t).astype(int)
    return d


def _oof_auc(X, y, groups, n_splits, seed):
    p = np.zeros(len(y))
    for tr, te in StratifiedGroupKFold(n_splits=n_splits, shuffle=True,
                                       random_state=seed).split(X, y, groups):
        m = make_model("lr", seed).fit(X[tr], y[tr])
        p[te] = m.predict_proba(X[te])[:, 1]
    return roc_auc_score(y, p)


def permutation_null(X, y, groups, n_splits, n_perm, seed=0):
    """Null distribution of M1's grouped-CV AUC under label permutation *between
    groups*. Permuting rows instead would break the group structure the splitter
    depends on and give an optimistically narrow null."""
    by_group = {}
    for g, l in zip(groups, y):
        by_group.setdefault(g, []).append(int(l))
    keys = sorted(by_group)
    rng = np.random.default_rng(seed)
    out = []
    while len(out) < n_perm:
        mapping = dict(zip(keys, rng.permutation(keys)))
        yp = np.array([by_group[mapping[g]][0] for g in groups])
        if yp.sum() in (0, len(yp)):
            continue
        try:
            out.append(_oof_auc(X, yp, groups, n_splits, seed + 100))
        except ValueError:
            continue
    return np.array(out)


BASELINES = ["B0 always-translate", "B1 never-translate", "B2 majority-class",
             "B3 cc threshold", "B4 infeasible-lib blocklist", "B5 skip-AWS"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dataset", required=True)
    ap.add_argument("--label", default="all_tests_passed")
    ap.add_argument("--horizon", type=float, default=1e6,
                    help="invocation count N used to fit thresholds and headline net energy")
    ap.add_argument("--folds", type=int, default=10)
    ap.add_argument("--repeats", type=int, default=5)
    ap.add_argument("--json-out", default="")
    ap.add_argument("--permutations", type=int, default=0,
                    help="group-level label permutation test on M1's AUC (0 = skip)")
    args = ap.parse_args()

    horizons = [1e3, 1e5, 1e6, 1e7, 1e9]
    N = args.horizon
    rows, cols, X, y, groups, E, dE, ids, aws = load(args.dataset, args.label)

    print("corpus: %d functions, %d positive (%.1f%%), %d independent groups, %d features"
          % (len(y), y.sum(), 100 * y.mean(), len(set(groups)), len(cols)))
    print("measured spend: %.1f Wh total (%.1f Wh on successes, %.1f Wh on failures)"
          % (E.sum() / WH, E[y == 1].sum() / WH, E[y == 0].sum() / WH))
    pos_dE = int((dE[y == 1] > 0).sum())
    print("delta-E > 0 for %d/%d successes (%d translations are slower in Go and never repay)"
          % (pos_dE, y.sum(), y.sum() - pos_dE))
    print("threshold horizon N = %g invocations\n" % N)

    results = {}
    # The oracle is horizon-specific: with perfect ex-post knowledge of y and dE it
    # translates exactly the functions that repay at that N. Evaluating one fixed
    # oracle across the sweep would understate it everywhere but its own horizon.
    oracle_by_h = {h: ((y == 1) & (h * dE > E)).astype(int) for h in horizons}
    orc = summarize(oracle_by_h[N], y, E, dE, horizons)
    for h in horizons:
        orc["net_wh_N%g" % h] = net(oracle_by_h[h], y, E, dE, h) / WH
    results["ORACLE (upper bound)"] = [orc]

    for name in BASELINES:
        results[name] = [
            summarize(baseline_decisions(name, X, cols, y, groups, E, dE, aws, N,
                                         args.folds, seed=100 + r),
                      y, E, dE, horizons)
            for r in range(args.repeats)]

    coefs = {}
    for kind, label in [("lr", "M1 logistic regression"), ("rf", "M2 random forest")]:
        reps = {"energy": [], "balanced": []}
        for r in range(args.repeats):
            p, thr = oof_probs_and_thresholds(kind, X, y, groups, E, dE, N,
                                              args.folds, seed=100 + r)
            auc = float(roc_auc_score(y, p))
            for obj in reps:
                s = summarize((p >= thr[obj]).astype(int), y, E, dE, horizons)
                s["roc_auc"] = auc
                s["mean_threshold"] = float(thr[obj].mean())
                reps[obj].append(s)
        results["%s [energy pt]" % label] = reps["energy"]
        results["%s [balanced pt]" % label] = reps["balanced"]
        if kind == "lr":
            m = make_model("lr", 0).fit(X, y)
            keep = m.named_steps["var"].get_support()
            coefs = dict(zip([c for c, k in zip(cols, keep) if k],
                             m.named_steps["clf"].coef_[0].tolist()))

    w = max(len(k) for k in results) + 1
    hdr = ("%-*s%7s%6s%10s%9s%7s%8s%7s%12s"
           % (w, "policy", "transl", "kept", "spend Wh", "Wh/succ", "acc", "recall",
              "AUC", "net Wh @N"))
    print(hdr)
    print("-" * len(hdr))
    for name, reps in results.items():
        def mean(k):
            vals = [r[k] for r in reps if k in r]
            return float(np.mean(vals)) if vals else None
        auc = mean("roc_auc")
        wps = mean("wh_per_success")
        print("%-*s%7.1f%6.1f%10.1f%9s%7.3f%8.3f%7s%12.1f"
              % (w, name, mean("translated"), mean("successes_kept"), mean("spend_wh"),
                 "inf" if not np.isfinite(wps) else "%.2f" % wps,
                 mean("accuracy"), mean("recall"),
                 "--" if auc is None else "%.3f" % auc,
                 mean("net_wh_N%g" % N)))

    hs = "".join("%14s" % ("N=" + format(h, ".0e")) for h in horizons)
    for title, rel in (("net energy at horizon N (Wh; benefit - spend, positive = worth doing)",
                        False),
                       ("net energy saved versus B0 always-translate "
                        "(Wh; positive = the gate helps)", True)):
        print("\n" + title)
        print("%-*s%s" % (w, "policy", hs))
        print("-" * (w + 14 * len(horizons)))
        b0 = {h: np.mean([r["net_wh_N%g" % h] for r in results["B0 always-translate"]])
              for h in horizons}
        for name, reps in results.items():
            if rel and name == "B0 always-translate":
                continue
            cells = "".join(
                "%14.1f" % (np.mean([r["net_wh_N%g" % h] for r in reps])
                            - (b0[h] if rel else 0.0))
                for h in horizons)
            print("%-*s%s" % (w, name, cells))

    if args.permutations:
        obs = float(np.mean([r["roc_auc"]
                             for r in results["M1 logistic regression [balanced pt]"]]))
        null = permutation_null(X, y, groups, args.folds, args.permutations)
        p = (1 + int((null >= obs).sum())) / (1 + len(null))
        print("\ngroup-level label permutation test on M1's AUC "
              "(labels shuffled between groups, so the null keeps the group structure)")
        print("  observed %.3f | null %.3f +- %.3f over %d permutations | p = %.4f"
              % (obs, null.mean(), null.std(), len(null), p))

    if coefs:
        print("\nM1 logistic-regression coefficients (standardized features, full-corpus "
              "refit -- descriptive only, not an out-of-fold quantity)")
        top = sorted(coefs.items(), key=lambda kv: -abs(kv[1]))[:12]
        for k, v in top:
            print("  %-28s %+.3f  (%s translation success)"
                  % (k, v, "raises" if v > 0 else "lowers"))

    if args.json_out:
        json.dump({"policies": results, "lr_coefficients": coefs},
                  open(args.json_out, "w"), indent=1)
        print("\nwrote %s" % args.json_out)


if __name__ == "__main__":
    main()
