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
import os

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


def load(path, label, cols=None):
    """Load a dataset table. `cols` pins the feature order, which an external test
    corpus must inherit from the training corpus rather than re-deriving."""
    rows = list(csv.DictReader(open(path, newline="")))
    if cols is None:
        cols = [c for c in rows[0]
                if c not in NON_FEATURE and c not in LABEL_COLS and c not in COST_COLS]
    missing = [c for c in cols if c not in rows[0]]
    if missing:
        raise SystemExit("%s is missing feature columns %s -- the two tables were "
                         "built by different scanner versions" % (path, missing))
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


def translate_value(y, E, dE, N):
    """v_i = the net energy translating function i actually returned, in joules.

        v_i = y_i * N * dE_i - E_i

    Every term is measured: y and E for all 95 functions (cmd/energy costs the
    failures too), dE for all 42 successes. A failure has v = -E, the energy it
    wasted. A success whose Go version is *slower* also has v < 0 -- 17 of our
    42 are in that position, which is the whole reason this file exists.
    """
    return y * N * dE - E


def energy_label(v):
    """The decision that would have been right, in hindsight: translate exactly
    when doing so returned net energy.

    This is a *relabelling*, not just a reweighting, and it is the point of the
    exercise. `all_tests_passed` says "translate f67, it works"; this says "skip
    f67, it works and it is slower in Go". Training on the first target teaches
    a model to find translatable functions; training on this one teaches it to
    find worthwhile ones. Only the second is the research question.

    Note it depends on N, unlike the feasibility label. That is not a defect --
    whether a translation is worth doing genuinely depends on how often the
    function runs -- but it does mean this target has a parameter, and results
    must be reported as a curve over it rather than as a single number.
    """
    return (v > 0).astype(int)


def cost_weight(v, floor=1e-6):
    """Regret weight: how much getting function i wrong actually costs.

    |v_i| is exactly that. Mislabel a function whose translation returned 70 kJ
    and you lose 70 kJ; mislabel one that wasted 400 J and you lose 400 J. The
    unweighted loss treats those two mistakes as equal, which is what makes a
    plain accuracy-driven gate pick the cheap, easy, worthless functions.

    Normalized to mean 1 so the L2 penalty keeps the same meaning: sklearn
    scales the data-fit term by the weights but not the penalty, so raw joule
    weights (order 10^4) would silently switch regularization off and turn a
    deliberately-untuned model into an unregularized one.
    """
    w = np.abs(v).astype(float)
    w = np.maximum(w, floor)
    return w / w.mean()


def pick_threshold(p, y, E, dE, N, objective, tgt=None):
    """Decision threshold, a fitted quantity -- only ever called on a training
    fold's inner-CV predictions ([I7]: never on the test fold).

    Two operating points are reported because they answer different questions.
    `energy` maximizes net joules at horizon N -- always against the *real*
    outcomes and measured energies, whatever the model was trained on, since
    that is the actual quantity a deployment cares about.
    `balanced` maximizes balanced accuracy against the label the model predicts
    (`tgt`, defaulting to y). Balancing an energy-target model against success
    would place its operating point using a question it was told to ignore.
    """
    t_label = y if tgt is None else tgt
    best_t, best_v = 0.5, -np.inf
    for t in np.unique(np.concatenate([[0.0, 1.01], p])):
        d = (p >= t).astype(int)
        if objective == "energy":
            v = net(d, y, E, dE, N)
        else:
            tpr = d[t_label == 1].mean() if (t_label == 1).any() else 0.0
            tnr = 1.0 - (d[t_label == 0].mean() if (t_label == 0).any() else 0.0)
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


def _fit(kind, seed, X, t, w):
    """Fit one model on target t with optional per-sample weights."""
    m = make_model(kind, seed)
    if w is None:
        m.fit(X, t)
    else:
        m.fit(X, t, clf__sample_weight=w)
    return m


def oof_probs_and_thresholds(kind, X, y, groups, E, dE, N, n_splits, seed,
                             target=None, weight=None):
    """Out-of-fold probabilities plus, per fold, thresholds chosen by an inner CV
    on the training fold only ([I7]: the operating point is a fitted quantity).

    `target` is what the model is trained to predict (defaults to y, the
    feasibility label) and `weight` is the per-sample cost weighting. Both are
    training-side only: folds are stratified on the training target, but every
    threshold and every reported figure is still computed against the *real*
    outcomes y and the *measured* energies, so a cost-sensitive model stays
    directly comparable to the plain one.
    """
    t = y if target is None else target
    p = np.zeros(len(y))
    thr = {"energy": np.zeros(len(y)), "balanced": np.zeros(len(y))}
    outer = StratifiedGroupKFold(n_splits=n_splits, shuffle=True, random_state=seed)
    for tr, te in outer.split(X, t, groups):
        inner_p = np.zeros(len(tr))
        inner = StratifiedGroupKFold(n_splits=5, shuffle=True, random_state=seed + 1)
        for itr, ite in inner.split(X[tr], t[tr], groups[tr]):
            wtr = None if weight is None else weight[tr][itr]
            m = _fit(kind, seed, X[tr][itr], t[tr][itr], wtr)
            inner_p[ite] = m.predict_proba(X[tr][ite])[:, 1]
        for obj in thr:
            thr[obj][te] = pick_threshold(inner_p, y[tr], E[tr], dE[tr], N, obj,
                                          tgt=t[tr])
        m = _fit(kind, seed, X[tr], t[tr], None if weight is None else weight[tr])
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


def report_breakdown(name, p, thr, y, E, dE, rows, horizons):
    """[I7]'s per-group generalisation check: does the model work outside the one
    axis the corpus is stratified on, or has it only learned 'high cc -> fail'?
    Slices are the dataset's own two reporting axes (EVALUATION_DATASET.md 9)."""
    d = (p >= thr).astype(int)
    print("\n%s -- performance by reporting axis (balanced operating point)" % name)
    hdr = ("%-14s%5s%9s%8s%8s%9s%10s%10s"
           % ("slice", "n", "base", "AUC", "acc", "recall", "spend Wh", "Wh/succ"))
    print(hdr)
    print("-" * len(hdr))
    slices = [("all", np.ones(len(y), bool))]
    buckets = sorted({r["bucket"] for r in rows})
    slices += [("bucket " + b, np.array([r["bucket"] == b for r in rows])) for b in buckets]
    slices += [("aws=" + v, np.array([(r["aws"] in ("true", "True", "1")) == (v == "true")
                                      for r in rows])) for v in ("true", "false")]
    for label, m in slices:
        if m.sum() == 0:
            continue
        ys, ds, Es = y[m], d[m], E[m]
        try:
            auc = "%.3f" % roc_auc_score(ys, p[m]) if len(set(ys)) > 1 else "  --"
        except ValueError:
            auc = "  --"
        kept = int(ys[ds.astype(bool)].sum())
        spend = float(Es[ds.astype(bool)].sum()) / WH
        print("%-14s%5d%9.2f%8s%8.3f%9s%10.1f%10s"
              % (label, m.sum(), ys.mean(), auc,
                 float((ds.astype(bool) == ys.astype(bool)).mean()),
                 "%.3f" % (kept / ys.sum()) if ys.sum() else "  --",
                 spend, "%.2f" % (spend / kept) if kept else "inf"))


def report_external(kind, label, Xtr, ytr, gtr, Etr, dEtr, ext, N, folds, seed, horizons,
                    target=None, weight=None):
    """Train on the whole training corpus, test once on a genuinely separate one.
    A different-corpus generalisation check, which is more informative than another
    random slice of the same corpus -- but its labels are noisier, so it is
    corroboration and never the headline ([I7]).

    `target`/`weight` carry the cost-sensitive variants through unchanged, so the
    energy-target models face the same external test as the feasibility ones.
    They need it more, not less: their label is defined by this corpus's own
    delta-E distribution, so "does it transfer" is exactly the open question.
    The external corpus is scored against its OWN measured energies, so nothing
    from the training corpus leaks into the evaluation.
    """
    rows_e, X_e, y_e, E_e, dE_e = ext
    ttr = ytr if target is None else target
    # The operating point is still a fitted quantity: choose it by inner CV on the
    # training corpus only, never on the external set.
    inner_p = np.zeros(len(ytr))
    for itr, ite in StratifiedGroupKFold(n_splits=folds, shuffle=True,
                                         random_state=seed).split(Xtr, ttr, gtr):
        wtr = None if weight is None else weight[itr]
        m = _fit(kind, seed, Xtr[itr], ttr[itr], wtr)
        inner_p[ite] = m.predict_proba(Xtr[ite])[:, 1]
    out = {}
    model = _fit(kind, seed, Xtr, ttr, weight)
    p_e = model.predict_proba(X_e)[:, 1]
    # The external corpus's own worthwhileness label, for an AUC that scores the
    # model on the question it was trained to answer.
    z_e = energy_label(translate_value(y_e, E_e, dE_e, N))
    for obj in ("balanced", "energy"):
        t = pick_threshold(inner_p, ytr, Etr, dEtr, N, obj, tgt=ttr)
        s = summarize((p_e >= t).astype(int), y_e, E_e, dE_e, horizons)
        s["threshold"] = float(t)
        s["roc_auc"] = (float(roc_auc_score(y_e, p_e)) if len(set(y_e)) > 1
                        else float("nan"))
        s["roc_auc_target"] = (s["roc_auc"] if target is None
                               else float(roc_auc_score(z_e, p_e)) if len(set(z_e)) > 1
                               else float("nan"))
        out[obj] = s
    return out, p_e


def export_model(path, X, y, groups, E, dE, cols, N, folds, repeats,
                 schema_version, results, dataset_path):
    """Export M1 for internal/predictor ([I10]).

    The shipped artifact is a vector of coefficients, not a pickled estimator:
    it keeps go.mod free of any ML dependency, it is auditable by reading, and
    it makes the deployed decision boundary a reviewable part of the thesis.

    M1 rather than M2 because [I7] measured the forest failing to transfer -
    AUC 0.525 on function_set against M1's 0.850, and 0.242 on the most
    expensive complexity bucket.

    The exported threshold is the *balanced* operating point, averaged over the
    inner-CV selections. The energy point is deliberately not shipped: it
    encodes this corpus's delta-E distribution as much as its labels, and on
    function_set it degenerated to translating nothing.
    """
    model = make_model("lr", 0).fit(X, y)
    keep = model.named_steps["var"].get_support()
    kept = [c for c, k in zip(cols, keep) if k]
    scaler = model.named_steps["sc"]
    clf = model.named_steps["clf"]

    thresholds = []
    for r in range(repeats):
        _, thr = oof_probs_and_thresholds("lr", X, y, groups, E, dE, N, folds,
                                          seed=100 + r)
        thresholds.append(float(np.mean(thr["balanced"])))

    aucs = [s["roc_auc"] for s in results.get("M1 logistic regression [balanced pt]", [])
            if "roc_auc" in s]
    payload = {
        "model": "logistic_regression",
        "feature_schema_version": schema_version,
        "features": kept,
        "mean": [float(v) for v in scaler.mean_],
        "scale": [float(v) for v in scaler.scale_],
        "coefficients": [float(v) for v in clf.coef_[0]],
        "intercept": float(clf.intercept_[0]),
        "threshold": float(np.mean(thresholds)),
        "provenance": {
            "id": "m1-lr-" + os.path.basename(dataset_path).replace(
                "dataset-", "").replace(".csv", ""),
            "dataset": os.path.basename(dataset_path),
            "trained_on": int(len(y)),
            "positives": int(y.sum()),
            "independent_groups": int(len(set(groups))),
            "dropped_zero_variance_columns": int(len(cols) - len(kept)),
            "cv_roc_auc_mean": float(np.mean(aucs)) if aucs else None,
            "cv_roc_auc_std": float(np.std(aucs)) if aucs else None,
            "threshold_objective": "balanced_accuracy",
            "threshold_horizon_invocations": None,
            "hyperparameters": "C=1.0, L2, class_weight=balanced, fixed a priori (no tuning)",
            "caveat": "labels are single-run and their stability is unmeasured ([I1]); "
                      "this model predicts what THIS pipeline fails at, not translatability",
        },
    }
    with open(path, "w") as fh:
        json.dump(payload, fh, indent=1)
        fh.write("\n")
    print("  %d features kept of %d (%d zero-variance columns dropped), threshold %.3f"
          % (len(kept), len(cols), len(cols) - len(kept), payload["threshold"]))
    return path


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
    ap.add_argument("--breakdown", action="store_true",
                    help="per-bucket and AWS/non-AWS performance of the learned models")
    ap.add_argument("--external", default="",
                    help="a second dataset CSV (e.g. function_set) used as a one-shot "
                         "external corroboration set")
    ap.add_argument("--export-model", default="",
                    help="write M1 as a JSON model for internal/predictor ([I10])")
    ap.add_argument("--feature-schema-version", type=int, default=1,
                    help="pyscan.FeatureSchemaVersion the dataset was built under; "
                         "stamped into the exported model so the Go side can refuse "
                         "a vector from a different schema")
    args = ap.parse_args()

    N = args.horizon
    # The fitted horizon always appears in the sweep: the oracle and the
    # threshold are both defined at N, so omitting it would report a curve that
    # skips the one point every fitted quantity refers to.
    horizons = sorted({1e3, 1e5, 1e6, 1e7, 1e9, N})
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

    # Cost-sensitive variants ([option A]). v is the measured net energy each
    # translation actually returned; z is the decision that would have been
    # right; |v| is what getting it wrong costs.
    v = translate_value(y, E, dE, N)
    z = energy_label(v)
    w = cost_weight(v)
    print("cost-sensitive target at this N: %d of %d functions are worth translating "
          "in hindsight" % (z.sum(), len(z)))
    print("  (%d successes whose Go version does not repay are relabelled 'skip')"
          % int(((y == 1) & (z == 0)).sum()))
    print("  regret weights span %.0f J .. %.0f J (mean-normalized for fitting)\n"
          % (np.abs(v).min(), np.abs(v).max()))

    variants = [
        # (suffix, training target, sample weights)
        ("", None, None),                       # unchanged: feasibility, unweighted
        (" [cost-weighted]", None, w),          # A1: feasibility label, energy-weighted loss
        (" [energy-target]", z, w),             # A2: relabelled + energy-weighted loss
    ]

    coefs = {}
    oof = {}
    for kind, label in [("lr", "M1 logistic regression"), ("rf", "M2 random forest")]:
        for suffix, target, weight in variants:
            if target is not None and (z.sum() < args.folds or (len(z) - z.sum()) < args.folds):
                print("skipping %s%s: only %d positives, too few for %d folds"
                      % (label, suffix, z.sum(), args.folds))
                continue
            reps = {"energy": [], "balanced": []}
            for r in range(args.repeats):
                p, thr = oof_probs_and_thresholds(kind, X, y, groups, E, dE, N,
                                                  args.folds, seed=100 + r,
                                                  target=target, weight=weight)
                if r == 0 and suffix == "":
                    oof[label] = (p, thr["balanced"])
                auc = float(roc_auc_score(y, p))
                # AUC against the label the model was actually trained on. For a
                # feasibility model these coincide; for an energy-target model
                # they must not be confused. Scoring an energy-target model
                # against `y` measures how well it predicts something it was
                # deliberately not asked to predict, and reading that number as
                # its quality is the single easiest way to misread this table.
                tgt = y if target is None else target
                auc_t = (auc if target is None
                         else float(roc_auc_score(tgt, p)) if len(set(tgt)) > 1
                         else float("nan"))
                for obj in reps:
                    s = summarize((p >= thr[obj]).astype(int), y, E, dE, horizons)
                    s["roc_auc"] = auc
                    s["roc_auc_target"] = auc_t
                    s["mean_threshold"] = float(thr[obj].mean())
                    s["trained_on"] = "energy" if target is not None else "feasibility"
                    s["weighted"] = weight is not None
                    reps[obj].append(s)
            results["%s%s [energy pt]" % (label, suffix)] = reps["energy"]
            results["%s%s [balanced pt]" % (label, suffix)] = reps["balanced"]
        if kind == "lr":
            m = make_model("lr", 0).fit(X, y)
            keep = m.named_steps["var"].get_support()
            coefs = dict(zip([c for c, k in zip(cols, keep) if k],
                             m.named_steps["clf"].coef_[0].tolist()))

    w = max(len(k) for k in results) + 1
    hdr = ("%-*s%7s%6s%10s%9s%8s%8s%8s%16s"
           % (w, "policy", "transl", "kept", "spend Wh", "Wh/succ", "recall",
              "AUC(y)", "AUC(tgt)", "net Wh @N"))
    print(hdr)
    print("-" * len(hdr))
    print("  AUC(y) = discrimination of real success; AUC(tgt) = of the label the model was")
    print("  trained on. They differ only for the energy-target rows, and for those AUC(y) is")
    print("  not a quality measure - it scores them on a question they were told to ignore.")
    print("-" * len(hdr))
    for name, reps in results.items():
        def mean(k):
            vals = [r[k] for r in reps if k in r and r[k] is not None]
            return float(np.mean(vals)) if vals else None

        def spread(k):
            vals = [r[k] for r in reps if k in r and r[k] is not None]
            return float(np.std(vals)) if len(vals) > 1 else 0.0
        auc = mean("roc_auc")
        auct = mean("roc_auc_target")
        wps = mean("wh_per_success")
        netk = "net_wh_N%g" % N
        print("%-*s%7.1f%6.1f%10.1f%9s%8.3f%8s%8s%16s"
              % (w, name, mean("translated"), mean("successes_kept"), mean("spend_wh"),
                 "inf" if not np.isfinite(wps) else "%.2f" % wps,
                 mean("recall"),
                 "--" if auc is None else "%.3f" % auc,
                 "--" if auct is None or not np.isfinite(auct) else "%.3f" % auct,
                 "%.1f +-%.0f" % (mean(netk), spread(netk))))

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

    if args.breakdown:
        print("\n(single representative repeat, seed 100 -- slice counts are too small "
              "for the 5-repeat spread to mean much)")
        for label, (p, t) in oof.items():
            report_breakdown(label, p, t, y, E, dE, rows, horizons)

    if args.external:
        rows_e, cols_e, X_e, y_e, g_e, E_e, dE_e, ids_e, aws_e = load(
            args.external, args.label, cols=cols)
        print("\n" + "=" * 78)
        print("EXTERNAL CORROBORATION: trained on %d functions, tested once on %s"
              % (len(y), args.external))
        print("  external corpus: %d functions, %d positive (%.1f%%), %.1f Wh spent"
              % (len(y_e), y_e.sum(), 100 * y_e.mean(), E_e.sum() / WH))
        print("  NOT the headline: a different corpus means different labels, and this "
              "one's\n  expectations were never executed against the Python originals "
              "(EVALUATION_DATASET.md 4).")
        hdr = ("%-34s%8s%7s%6s%10s%9s%8s%9s"
               % ("model / operating point", "thresh", "transl", "kept", "spend Wh",
                  "Wh/succ", "AUC(y)", "AUC(tgt)"))
        print("\n" + hdr)
        print("-" * len(hdr))
        b0e = summarize(np.ones(len(y_e), int), y_e, E_e, dE_e, horizons)
        print("%-34s%8s%7d%6d%10.1f%9.2f%8s%9s"
              % ("B0 always-translate", "--", b0e["translated"], b0e["successes_kept"],
                 b0e["spend_wh"], b0e["wh_per_success"], "--", "--"))
        ext = (rows_e, X_e, y_e, E_e, dE_e)
        z_e = energy_label(translate_value(y_e, E_e, dE_e, N))
        print("  external worthwhileness label at this N: %d of %d worth translating"
              % (z_e.sum(), len(z_e)))
        for kind, label in [("lr", "M1 logistic regression"), ("rf", "M2 random forest")]:
            for suffix, target, weight in variants:
                out, _ = report_external(kind, label, X, y, groups, E, dE, ext, N,
                                         args.folds, 100, horizons,
                                         target=target, weight=weight)
                for obj in ("balanced", "energy"):
                    s = out[obj]
                    auct = s.get("roc_auc_target")
                    print("%-34s%8.3f%7d%6d%10.1f%9s%8.3f%9s"
                          % ("%s%s [%s]" % (label, suffix, obj), s["threshold"],
                             s["translated"], s["successes_kept"], s["spend_wh"],
                             "inf" if not np.isfinite(s["wh_per_success"])
                             else "%.2f" % s["wh_per_success"], s["roc_auc"],
                             "--" if auct is None or not np.isfinite(auct)
                             else "%.3f" % auct))

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

    if args.export_model:
        path = export_model(args.export_model, X, y, groups, E, dE, cols, N,
                            args.folds, args.repeats, args.feature_schema_version,
                            results, args.dataset)
        print("\nwrote %s" % path)

    if args.json_out:
        json.dump({"policies": results, "lr_coefficients": coefs},
                  open(args.json_out, "w"), indent=1)
        print("\nwrote %s" % args.json_out)


if __name__ == "__main__":
    main()
