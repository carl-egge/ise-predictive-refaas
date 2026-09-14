#!/usr/bin/env python3
"""[I5]/[I6]/[I7] Offline counterfactual evaluation of an ex-ante prediction gate.

Nothing here re-runs a translation. Every function in the corpus was translated
in each run the tables were built from, so for every (function i, run k) we
already know

  * y_ki  -- whether that translation succeeded (`all_tests_passed`)
  * E_ki  -- what the attempt actually cost in facility joules (cmd/energy)
  * dE_ki -- the per-invocation energy the Go version saves over Python ([H6])

A gate is therefore a *replay*: a decision vector d over the functions, whose
cost and benefit in run k are sums of already-measured quantities. The only
quantity a replay cannot supply is the predictor's own inference cost, which
[I8] measures separately.

    spend_k(d)      = sum_i d_i * E_ki
    benefit_k(d, N) = sum_i d_i * y_ki * N * dE_ki
    net_k(d, N)     = benefit_k(d, N) - spend_k(d)

Replicates. Pass --dataset once per run of the *same* pipeline configuration (the
three frozen-configuration runs 20260904-190539, 20260911-165103 and
20260912-172904). The ex-ante features are deterministic, so the runs share one
feature matrix and differ only in what was measured. Each function contributes
one training row per run: a function that passed twice and failed once is seen as
exactly that, not as whichever outcome one run happened to draw -- which matters
because a quarter of the corpus changes outcome between runs of one
configuration. Every reported figure is computed per run and then averaged, so a
policy is judged against each run's own outcomes and costs, and every spread
includes run-to-run variation as well as split-to-split variation.

Protocol ([I7]): repeated StratifiedGroupKFold (10 folds x 5 repeats) over
*functions*, grouped on `group_id`, so all replicates of a function and all of its
near-duplicates sit on the same side of every split. Every fitted quantity --
zero-variance filtering, standardization, class weighting and the decision
threshold -- is learned inside the training fold, the threshold via an inner CV.
With a single dataset this reduces exactly to the single-run protocol.
"""
import argparse
import csv
import itertools
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


def run_id_of(path):
    base = os.path.basename(path)
    if base.startswith("dataset-") and base.endswith(".csv"):
        return base[len("dataset-"):-len(".csv")]
    return base


class Corpus:
    """One or more runs of one pipeline configuration over the same functions.

    X, groups, ids, aws and rows are per function and shared by construction.
    Y, E and dE are measured per run and have shape (K, n). Stacked arrays list
    run 0's rows first, then run 1's, and so on.
    """

    def __init__(self, paths, label, cols=None):
        loaded = [load(p, label, cols) for p in paths]
        self.rows, self.cols, self.X, _, self.groups, _, _, self.ids, self.aws = loaded[0]
        for p, (_, cols_k, X_k, _, g_k, _, _, ids_k, _) in zip(paths[1:], loaded[1:]):
            if ids_k != self.ids or cols_k != self.cols:
                raise SystemExit("%s does not cover the same functions and columns as %s"
                                 % (p, paths[0]))
            if not np.array_equal(X_k, self.X) or not np.array_equal(g_k, self.groups):
                raise SystemExit(
                    "%s has different feature values or groups than %s: replicates must "
                    "share one deterministic feature table, or they are not the same "
                    "corpus" % (p, paths[0]))
        self.paths = list(paths)
        self.run_ids = [run_id_of(p) for p in paths]
        self.Y = np.stack([l[3] for l in loaded])
        self.E = np.stack([l[5] for l in loaded])
        self.dE = np.stack([l[6] for l in loaded])

    @property
    def K(self):
        return self.Y.shape[0]

    @property
    def n(self):
        return self.Y.shape[1]

    def rows_of(self, idx):
        """Stacked row indices of the functions `idx`, in stacked order."""
        idx = np.asarray(idx)
        return np.concatenate([idx + k * self.n for k in range(self.K)])

    def stacked(self):
        return (np.tile(self.X, (self.K, 1)), self.Y.reshape(-1),
                np.tile(self.groups, self.K), self.E.reshape(-1), self.dE.reshape(-1))


def strat_label(T, n):
    """Function-level stratification label for a stacked target: positive in at
    least half of the runs. With one run it is the target itself."""
    return (np.asarray(T).reshape(-1, n).mean(axis=0) >= 0.5).astype(int)


def auc_or_nan(y, p):
    return float(roc_auc_score(y, p)) if len(set(y)) > 1 else float("nan")


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


def per_run(d, c, horizons, p=None, target=None):
    """One summary per run: a policy's decisions judged against that run's own
    outcomes and measured energies. `target` (stacked) is what a cost-sensitive
    model was trained on; its AUC is reported against that, per run."""
    out = []
    T = None if target is None else np.asarray(target).reshape(c.K, c.n)
    for k in range(c.K):
        s = summarize(d, c.Y[k], c.E[k], c.dE[k], horizons)
        s["run"] = c.run_ids[k]
        if p is not None:
            s["roc_auc"] = auc_or_nan(c.Y[k], p)
            s["roc_auc_target"] = s["roc_auc"] if T is None else auc_or_nan(T[k], p)
        out.append(s)
    return out


def translate_value(y, E, dE, N):
    """v = the net energy translating a function actually returned, in joules.

        v = y * N * dE - E

    Every term is measured: y and E for every attempt (cmd/energy costs the
    failures too), dE for every validated translation. A failure has v = -E, the
    energy it wasted. A success whose Go version is *slower* also has v < 0 --
    about a third of the validated translations are in that position, which is the
    whole reason this function exists. Works elementwise, per run.
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
    """Regret weight: how much getting an example wrong actually costs.

    |v| is exactly that. Mislabel a translation that returned 70 kJ and you lose
    70 kJ; mislabel one that wasted 400 J and you lose 400 J. The unweighted loss
    treats those two mistakes as equal, which is what makes a plain
    accuracy-driven gate pick the cheap, easy, worthless functions.

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
    fold's inner-CV predictions ([I7]: never on the test fold). With replicates
    the arrays are stacked, so the objective is taken over every run at once.

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


def oof_probs_and_thresholds(kind, c, N, n_splits, seed, target=None, weight=None):
    """Out-of-fold probability per function plus, per function, the thresholds
    chosen by an inner CV on its training fold only ([I7]: the operating point is
    a fitted quantity).

    Folds are drawn over functions, so a function's replicates never straddle a
    boundary; models are fitted on the stacked (function, run) rows of the
    training functions. `target` (stacked) is what the model is trained to
    predict, defaulting to the feasibility label, and `weight` (stacked) is the
    per-row cost weighting. Both are training-side only: every threshold and every
    reported figure is still computed against the *real* outcomes and the
    *measured* energies, so a cost-sensitive model stays directly comparable to
    the plain one.
    """
    Xs, Ys, _, Es, dEs = c.stacked()
    T = Ys if target is None else target
    strat = strat_label(T, c.n)
    p = np.zeros(c.n)
    thr = {"energy": np.zeros(c.n), "balanced": np.zeros(c.n)}
    outer = StratifiedGroupKFold(n_splits=n_splits, shuffle=True, random_state=seed)
    for tr, te in outer.split(c.X, strat, c.groups):
        inner_p = np.zeros(c.n)
        inner = StratifiedGroupKFold(n_splits=5, shuffle=True, random_state=seed + 1)
        for itr, ite in inner.split(c.X[tr], strat[tr], c.groups[tr]):
            r = c.rows_of(tr[itr])
            m = _fit(kind, seed, Xs[r], T[r], None if weight is None else weight[r])
            inner_p[tr[ite]] = m.predict_proba(c.X[tr[ite]])[:, 1]
        rt = c.rows_of(tr)
        pin = np.tile(inner_p[tr], c.K)
        for obj in thr:
            thr[obj][te] = pick_threshold(pin, Ys[rt], Es[rt], dEs[rt], N, obj, tgt=T[rt])
        m = _fit(kind, seed, Xs[rt], T[rt], None if weight is None else weight[rt])
        p[te] = m.predict_proba(c.X[te])[:, 1]
    return p, thr


def baseline_decisions(name, c, N, n_splits, seed):
    n = c.n
    if name.startswith("B0"):
        return np.ones(n, int)
    if name.startswith("B1"):
        return np.zeros(n, int)
    if name.startswith("B4"):
        j = c.cols.index("has_infeasible_lib")
        return (c.X[:, j] == 0).astype(int)
    if name.startswith("B5"):
        return (c.aws == 0).astype(int)
    d = np.zeros(n, int)
    Xs, Ys, _, Es, dEs = c.stacked()
    outer = StratifiedGroupKFold(n_splits=n_splits, shuffle=True, random_state=seed)
    for tr, te in outer.split(c.X, strat_label(Ys, n), c.groups):
        if name.startswith("B3"):
            j = c.cols.index("cc")
            rt = c.rows_of(tr)
            best_t, best_v = None, -np.inf
            for t in np.unique(c.X[tr, j]):
                v = net((Xs[rt, j] <= t).astype(int), Ys[rt], Es[rt], dEs[rt], N)
                if v > best_v:
                    best_v, best_t = v, t
            d[te] = (c.X[te, j] <= best_t).astype(int)
    return d


def _oof_auc(c, Y, n_splits, seed):
    """M1's grouped-CV AUC for label matrix Y, averaged over the runs."""
    Xs, Ys = np.tile(c.X, (c.K, 1)), Y.reshape(-1)
    p = np.zeros(c.n)
    for tr, te in StratifiedGroupKFold(n_splits=n_splits, shuffle=True,
                                       random_state=seed).split(c.X, strat_label(Ys, c.n),
                                                                c.groups):
        r = c.rows_of(tr)
        m = make_model("lr", seed).fit(Xs[r], Ys[r])
        p[te] = m.predict_proba(c.X[te])[:, 1]
    return float(np.mean([roc_auc_score(Y[k], p) for k in range(c.K)]))


def permutation_null(c, n_splits, n_perm, seed=0):
    """Null distribution of M1's grouped-CV AUC under label permutation *between
    groups*. Permuting rows instead would break the group structure the splitter
    depends on and give an optimistically narrow null. With replicates a group's
    whole outcome history moves together, so the null keeps the run-to-run
    structure of the labels as well."""
    first = {}
    for i, g in enumerate(c.groups):
        first.setdefault(g, i)
    keys = sorted(first)
    rng = np.random.default_rng(seed)
    out = []
    while len(out) < n_perm:
        mapping = dict(zip(keys, rng.permutation(keys)))
        Yp = c.Y[:, [first[mapping[g]] for g in c.groups]]
        if any(Yp[k].sum() in (0, c.n) for k in range(c.K)):
            continue
        try:
            out.append(_oof_auc(c, Yp, n_splits, seed + 100))
        except ValueError:
            continue
    return np.array(out)


def replicate_ceiling(c):
    """AUC of predicting each run's labels from the mean label of the other runs.

    No deterministic ex-ante score can do systematically better than knowing how
    often the function passed elsewhere, so this is the reference any model AUC
    should be read against."""
    if c.K < 2:
        return []
    out = []
    for k in range(c.K):
        others = np.delete(c.Y, k, axis=0).mean(axis=0)
        out.append(auc_or_nan(c.Y[k], others))
    return out


def report_breakdown(name, p, thr, c):
    """[I7]'s per-group generalisation check: does the model work outside the one
    axis the corpus is stratified on, or has it only learned 'high cc -> fail'?
    Slices are the dataset's own two reporting axes (EVALUATION_DATASET.md 9).
    Every column is a mean over the runs."""
    d = (p >= thr).astype(int)
    print("\n%s -- performance by reporting axis (balanced operating point, mean over "
          "%d run%s)" % (name, c.K, "" if c.K == 1 else "s"))
    hdr = ("%-14s%5s%9s%8s%8s%9s%10s%10s"
           % ("slice", "n", "base", "AUC", "acc", "recall", "spend Wh", "Wh/succ"))
    print(hdr)
    print("-" * len(hdr))
    rows = c.rows
    slices = [("all", np.ones(c.n, bool))]
    buckets = sorted({r["bucket"] for r in rows})
    slices += [("bucket " + b, np.array([r["bucket"] == b for r in rows])) for b in buckets]
    slices += [("aws=" + v, np.array([(r["aws"] in ("true", "True", "1")) == (v == "true")
                                      for r in rows])) for v in ("true", "false")]
    for label, m in slices:
        if m.sum() == 0:
            continue
        aucs, accs, recs, spend, kept = [], [], [], 0.0, 0
        for k in range(c.K):
            ys, ds, Es = c.Y[k][m], d[m].astype(bool), c.E[k][m]
            if len(set(ys)) > 1:
                aucs.append(roc_auc_score(ys, p[m]))
            accs.append(float((ds == ys.astype(bool)).mean()))
            k_kept = int(ys[ds].sum())
            if ys.sum():
                recs.append(k_kept / ys.sum())
            spend += float(Es[ds].sum()) / WH
            kept += k_kept
        print("%-14s%5d%9.2f%8s%8.3f%9s%10.1f%10s"
              % (label, m.sum(), c.Y[:, m].mean(),
                 "%.3f" % np.mean(aucs) if aucs else "  --", np.mean(accs),
                 "%.3f" % np.mean(recs) if recs else "  --",
                 spend / c.K, "%.2f" % (spend / kept) if kept else "inf"))


def report_external(kind, c, ext, N, folds, seed, horizons, target=None, weight=None):
    """Train on the whole training corpus, test once on a genuinely separate one.
    A different-corpus generalisation check, which is more informative than another
    random slice of the same corpus -- but its labels are noisier, so it is
    corroboration and never the headline ([I7]).

    `target`/`weight` carry the cost-sensitive variants through unchanged, so the
    energy-target models face the same external test as the feasibility ones.
    The external corpus is scored against its OWN measured energies, so nothing
    from the training corpus leaks into the evaluation. Figures are a mean over
    the external corpus's runs (usually one).
    """
    Xs, Ys, _, Es, dEs = c.stacked()
    T = Ys if target is None else target
    strat = strat_label(T, c.n)
    # The operating point is still a fitted quantity: choose it by inner CV on the
    # training corpus only, never on the external set.
    inner_p = np.zeros(c.n)
    for itr, ite in StratifiedGroupKFold(n_splits=folds, shuffle=True,
                                         random_state=seed).split(c.X, strat, c.groups):
        r = c.rows_of(itr)
        m = _fit(kind, seed, Xs[r], T[r], None if weight is None else weight[r])
        inner_p[ite] = m.predict_proba(c.X[ite])[:, 1]
    model = _fit(kind, seed, Xs, T, weight)
    p_e = model.predict_proba(ext.X)[:, 1]
    pin = np.tile(inner_p, c.K)
    out = {}
    for obj in ("balanced", "energy"):
        t = pick_threshold(pin, Ys, Es, dEs, N, obj, tgt=T)
        reps = []
        for k in range(ext.K):
            y_e, E_e, dE_e = ext.Y[k], ext.E[k], ext.dE[k]
            # The external corpus's own worthwhileness label, for an AUC that scores
            # the model on the question it was trained to answer.
            z_e = energy_label(translate_value(y_e, E_e, dE_e, N))
            s = summarize((p_e >= t).astype(int), y_e, E_e, dE_e, horizons)
            s["threshold"] = float(t)
            s["roc_auc"] = auc_or_nan(y_e, p_e)
            s["roc_auc_target"] = s["roc_auc"] if target is None else auc_or_nan(z_e, p_e)
            reps.append(s)
        out[obj] = {k: (float(np.mean([r[k] for r in reps]))
                        if isinstance(reps[0][k], (int, float)) else reps[0][k])
                    for k in reps[0]}
    return out, p_e


def export_model(path, c, N, folds, repeats, schema_version, results):
    """Export M1 for internal/predictor ([I10]).

    The shipped artifact is a vector of coefficients, not a pickled estimator:
    it keeps go.mod free of any ML dependency, it is auditable by reading, and
    it makes the deployed decision boundary a reviewable part of the thesis.

    M1 rather than M2: the linear model gives the calibrated probability [I9]
    composes with and is the arm that transferred to function_set; the per-model
    comparison is re-reported in the results this export accompanies.

    The model is fitted on every (function, run) row. The exported threshold is
    the *balanced* operating point, averaged over the inner-CV selections of every
    repeat. The energy point is deliberately not shipped: it encodes this
    corpus's delta-E distribution as much as its labels, and on function_set it
    degenerated to translating nothing.
    """
    Xs, Ys, _, _, _ = c.stacked()
    model = make_model("lr", 0).fit(Xs, Ys)
    keep = model.named_steps["var"].get_support()
    kept = [col for col, k in zip(c.cols, keep) if k]
    scaler = model.named_steps["sc"]
    clf = model.named_steps["clf"]

    thresholds = []
    for r in range(repeats):
        _, thr = oof_probs_and_thresholds("lr", c, N, folds, seed=100 + r)
        thresholds.append(float(np.mean(thr["balanced"])))

    aucs = [s["roc_auc"] for s in results.get("M1 logistic regression [balanced pt]", [])
            if "roc_auc" in s]
    ident = c.run_ids[0] if c.K == 1 else "replicates-" + "+".join(c.run_ids)
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
            "id": "m1-lr-" + ident,
            "datasets": [os.path.basename(p) for p in c.paths],
            "runs": c.run_ids,
            "trained_on": int(c.n),
            "trained_on_rows": int(c.K * c.n),
            "positives_per_run": [int(v) for v in c.Y.sum(axis=1)],
            "independent_groups": int(len(set(c.groups))),
            "dropped_zero_variance_columns": int(len(c.cols) - len(kept)),
            "cv_roc_auc_mean": float(np.mean(aucs)) if aucs else None,
            "cv_roc_auc_std": float(np.std(aucs)) if aucs else None,
            "cv_protocol": "StratifiedGroupKFold over functions on group_id, %d folds x %d "
                           "repeats; AUC per run, averaged" % (folds, repeats),
            "threshold_objective": "balanced_accuracy",
            "threshold_horizon_invocations": None,
            "hyperparameters": "C=1.0, L2, class_weight=balanced, fixed a priori (no tuning)",
            "caveat": "labels are the per-run outcomes of %d run%s of one pipeline "
                      "configuration; this model predicts what THIS pipeline fails at, "
                      "not translatability" % (c.K, "" if c.K == 1 else "s"),
        },
    }
    with open(path, "w") as fh:
        json.dump(payload, fh, indent=1)
        fh.write("\n")
    print("  %d features kept of %d (%d zero-variance columns dropped), threshold %.3f"
          % (len(kept), len(c.cols), len(c.cols) - len(kept), payload["threshold"]))
    return path


BASELINES = ["B0 always-translate", "B1 never-translate",
             "B3 cc threshold", "B4 infeasible-lib blocklist", "B5 skip-AWS"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dataset", action="append", required=True,
                    help="dataset CSV of one run; repeat once per replicate run of the "
                         "same pipeline configuration")
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
    c = Corpus(args.dataset, args.label)
    n, K = c.n, c.K

    print("corpus: %d functions x %d run%s, %d independent groups, %d features"
          % (n, K, "" if K == 1 else "s", len(set(c.groups)), len(c.cols)))
    for k in range(K):
        y, E, dE = c.Y[k], c.E[k], c.dE[k]
        pos_dE = int((dE[y == 1] > 0).sum())
        print("  run %s: %d positive (%.1f%%); spend %.1f Wh (%.1f on successes, %.1f on "
              "failures); delta-E > 0 for %d/%d successes"
              % (c.run_ids[k], y.sum(), 100 * y.mean(), E.sum() / WH, E[y == 1].sum() / WH,
                 E[y == 0].sum() / WH, pos_dE, y.sum()))
    ceiling = replicate_ceiling(c)
    if K > 1:
        agree = [float((c.Y[a] == c.Y[b]).mean()) for a, b in itertools.combinations(range(K), 2)]
        flips = int((c.Y.min(axis=0) != c.Y.max(axis=0)).sum())
        print("  label agreement between runs: %s (mean %.3f); %d of %d functions change "
              "outcome at least once" % (", ".join("%.3f" % a for a in agree),
                                          np.mean(agree), flips, n))
        print("  replicate ceiling -- AUC of each run's labels from the mean of the others: "
              "%s (mean %.3f)" % (", ".join("%.3f" % v for v in ceiling), np.mean(ceiling)))
    print("threshold horizon N = %g invocations\n" % N)

    results = {}
    # The oracle is horizon- and run-specific: with perfect ex-post knowledge of a
    # run's y and dE it translates exactly the functions that repay at that N in
    # that run. Evaluating one fixed oracle across the sweep would understate it
    # everywhere but its own horizon.
    orc = []
    for k in range(K):
        y, E, dE = c.Y[k], c.E[k], c.dE[k]
        by_h = {h: ((y == 1) & (h * dE > E)).astype(int) for h in horizons}
        s = summarize(by_h[N], y, E, dE, horizons)
        for h in horizons:
            s["net_wh_N%g" % h] = net(by_h[h], y, E, dE, h) / WH
        s["run"] = c.run_ids[k]
        orc.append(s)
    results["ORACLE (upper bound)"] = orc

    for name in BASELINES:
        results[name] = [s for r in range(args.repeats)
                         for s in per_run(baseline_decisions(name, c, N, args.folds,
                                                             seed=100 + r), c, horizons)]

    # Cost-sensitive variants ([option A]). V is the measured net energy each
    # translation actually returned, per run; z is the decision that would have
    # been right; |V| is what getting it wrong costs.
    V = translate_value(c.Y, c.E, c.dE, N)
    z = energy_label(V).reshape(-1)
    w = cost_weight(V.reshape(-1))
    print("cost-sensitive target at this N: %d of %d (function, run) pairs are worth "
          "translating in hindsight (per run: %s)"
          % (z.sum(), len(z), ", ".join(str(int(v)) for v in energy_label(V).sum(axis=1))))
    print("  (%d successful translations whose Go version does not repay are relabelled "
          "'skip')" % int(((c.Y.reshape(-1) == 1) & (z == 0)).sum()))
    print("  regret weights span %.0f J .. %.0f J (mean-normalized for fitting)\n"
          % (np.abs(V).min(), np.abs(V).max()))

    variants = [
        # (suffix, training target, sample weights)
        ("", None, None),                       # unchanged: feasibility, unweighted
        (" [cost-weighted]", None, w),          # A1: feasibility label, energy-weighted loss
        (" [energy-target]", z, w),             # A2: relabelled + energy-weighted loss
    ]

    coefs = {}
    first_rep = {}
    oof = {}
    majority = strat_label(c.Y.reshape(-1), n)
    for kind, label in [("lr", "M1 logistic regression"), ("rf", "M2 random forest")]:
        for suffix, target, weight in variants:
            zf = strat_label(z, n)
            if target is not None and (zf.sum() < args.folds or (n - zf.sum()) < args.folds):
                print("skipping %s%s: only %d functions positive in a majority of runs, "
                      "too few for %d folds" % (label, suffix, zf.sum(), args.folds))
                continue
            reps = {"energy": [], "balanced": []}
            p_sum = np.zeros(n)
            dec = {"energy": np.zeros(n), "balanced": np.zeros(n)}
            auc_major = []
            for r in range(args.repeats):
                p, thr = oof_probs_and_thresholds(kind, c, N, args.folds, seed=100 + r,
                                                  target=target, weight=weight)
                if r == 0 and suffix == "":
                    first_rep[label] = (p, thr["balanced"])
                p_sum += p
                auc_major.append(auc_or_nan(majority, p))
                # AUC against the label the model was actually trained on. For a
                # feasibility model these coincide; for an energy-target model
                # they must not be confused. Scoring an energy-target model
                # against `y` measures how well it predicts something it was
                # deliberately not asked to predict, and reading that number as
                # its quality is the single easiest way to misread this table.
                for obj in reps:
                    d = (p >= thr[obj]).astype(int)
                    dec[obj] += d
                    for s in per_run(d, c, horizons, p=p, target=target):
                        s["repeat"] = r
                        s["mean_threshold"] = float(thr[obj].mean())
                        s["trained_on"] = "energy" if target is not None else "feasibility"
                        s["weighted"] = weight is not None
                        reps[obj].append(s)
            results["%s%s [energy pt]" % (label, suffix)] = reps["energy"]
            results["%s%s [balanced pt]" % (label, suffix)] = reps["balanced"]
            # Out-of-fold scores and decisions per function, for the figures: a gate
            # evaluated on the corpus it was fitted on may only use these.
            oof["%s%s" % (label, suffix)] = {
                "p_mean": (p_sum / args.repeats).tolist(),
                "translate_frequency_balanced": (dec["balanced"] / args.repeats).tolist(),
                "translate_frequency_energy": (dec["energy"] / args.repeats).tolist(),
                "roc_auc_majority_label": float(np.nanmean(auc_major)),
            }
        if kind == "lr":
            Xs, Ys, _, _, _ = c.stacked()
            m = make_model("lr", 0).fit(Xs, Ys)
            keep = m.named_steps["var"].get_support()
            coefs = dict(zip([col for col, k in zip(c.cols, keep) if k],
                             m.named_steps["clf"].coef_[0].tolist()))

    width = max(len(k) for k in results) + 1
    hdr = ("%-*s%7s%6s%10s%9s%8s%8s%8s%16s"
           % (width, "policy", "transl", "kept", "spend Wh", "Wh/succ", "recall",
              "AUC(y)", "AUC(tgt)", "net Wh @N"))
    print("means over %s; +- is the standard deviation over %s"
          % ("%d repeats x %d runs" % (args.repeats, K),
             "repeats and runs" if K > 1 else "repeats"))
    print(hdr)
    print("-" * len(hdr))
    print("  AUC(y) = discrimination of real success; AUC(tgt) = of the label the model was")
    print("  trained on. They differ only for the energy-target rows, and for those AUC(y) is")
    print("  not a quality measure - it scores them on a question they were told to ignore.")
    print("-" * len(hdr))
    for name, reps in results.items():
        def mean(key):
            vals = [r[key] for r in reps if key in r and r[key] is not None]
            return float(np.mean(vals)) if vals else None

        def spread(key):
            vals = [r[key] for r in reps if key in r and r[key] is not None]
            return float(np.std(vals)) if len(vals) > 1 else 0.0
        auc = mean("roc_auc")
        auct = mean("roc_auc_target")
        kept_total = sum(r["successes_kept"] for r in reps)
        wps = (sum(r["spend_wh"] for r in reps) / kept_total) if kept_total else float("inf")
        netk = "net_wh_N%g" % N
        print("%-*s%7.1f%6.1f%10.1f%9s%8.3f%8s%8s%16s"
              % (width, name, mean("translated"), mean("successes_kept"), mean("spend_wh"),
                 "inf" if not np.isfinite(wps) else "%.2f" % wps,
                 mean("recall"),
                 "--" if auc is None else "%.3f" % auc,
                 "--" if auct is None or not np.isfinite(auct) else "%.3f" % auct,
                 "%.1f +-%.0f" % (mean(netk), spread(netk))))
    if K > 1:
        print("\n  AUC(y) is the mean per-run AUC. Against the majority label (passed in at "
              "least half the runs):")
        for name, o in oof.items():
            print("    %-52s %.3f" % (name, o["roc_auc_majority_label"]))
        print("  replicate ceiling for comparison: %.3f" % np.mean(ceiling))

    hs = "".join("%14s" % ("N=" + format(h, ".0e")) for h in horizons)
    for title, rel in (("net energy at horizon N (Wh; benefit - spend, positive = worth doing)",
                        False),
                       ("net energy saved versus B0 always-translate "
                        "(Wh; positive = the gate helps)", True)):
        print("\n" + title)
        print("%-*s%s" % (width, "policy", hs))
        print("-" * (width + 14 * len(horizons)))
        b0 = {h: np.mean([r["net_wh_N%g" % h] for r in results["B0 always-translate"]])
              for h in horizons}
        for name, reps in results.items():
            if rel and name == "B0 always-translate":
                continue
            cells = "".join(
                "%14.1f" % (np.mean([r["net_wh_N%g" % h] for r in reps])
                            - (b0[h] if rel else 0.0))
                for h in horizons)
            print("%-*s%s" % (width, name, cells))

    if args.breakdown:
        print("\n(single representative repeat, seed 100 -- slice counts are too small "
              "for the 5-repeat spread to mean much)")
        for label, (p, t) in first_rep.items():
            report_breakdown(label, p, t, c)

    external = {}
    if args.external:
        ext = Corpus([args.external], args.label, cols=c.cols)
        print("\n" + "=" * 78)
        print("EXTERNAL CORROBORATION: trained on %d functions x %d run%s, tested once on %s"
              % (n, K, "" if K == 1 else "s", args.external))
        print("  external corpus: %d functions, %d positive (%.1f%%), %.1f Wh spent"
              % (ext.n, ext.Y[0].sum(), 100 * ext.Y[0].mean(), ext.E[0].sum() / WH))
        print("  NOT the headline: a different corpus means different labels, and this "
              "one's\n  expectations were never executed against the Python originals "
              "(EVALUATION_DATASET.md 4).")
        hdr = ("%-34s%8s%7s%6s%10s%9s%8s%9s"
               % ("model / operating point", "thresh", "transl", "kept", "spend Wh",
                  "Wh/succ", "AUC(y)", "AUC(tgt)"))
        print("\n" + hdr)
        print("-" * len(hdr))
        b0e = summarize(np.ones(ext.n, int), ext.Y[0], ext.E[0], ext.dE[0], horizons)
        print("%-34s%8s%7d%6d%10.1f%9.2f%8s%9s"
              % ("B0 always-translate", "--", b0e["translated"], b0e["successes_kept"],
                 b0e["spend_wh"], b0e["wh_per_success"], "--", "--"))
        z_e = energy_label(translate_value(ext.Y[0], ext.E[0], ext.dE[0], N))
        print("  external worthwhileness label at this N: %d of %d worth translating"
              % (z_e.sum(), len(z_e)))
        for kind, label in [("lr", "M1 logistic regression"), ("rf", "M2 random forest")]:
            for suffix, target, weight in variants:
                out, p_e = report_external(kind, c, ext, N, args.folds, 100, horizons,
                                           target=target, weight=weight)
                external["%s%s" % (label, suffix)] = {"results": out, "p": p_e.tolist(),
                                                       "function_ids": ext.ids}
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

    permutation = None
    if args.permutations:
        obs = float(np.mean([r["roc_auc"]
                             for r in results["M1 logistic regression [balanced pt]"]]))
        null = permutation_null(c, args.folds, args.permutations)
        pval = (1 + int((null >= obs).sum())) / (1 + len(null))
        permutation = {"observed": obs, "null_mean": float(null.mean()),
                       "null_std": float(null.std()), "permutations": int(len(null)),
                       "p": pval}
        print("\ngroup-level label permutation test on M1's AUC "
              "(labels shuffled between groups, so the null keeps the group structure)")
        print("  observed %.3f | null %.3f +- %.3f over %d permutations | p = %.4f"
              % (obs, null.mean(), null.std(), len(null), pval))

    if coefs:
        print("\nM1 logistic-regression coefficients (standardized features, full-corpus "
              "refit on every (function, run) row -- descriptive only, not an out-of-fold "
              "quantity)")
        top = sorted(coefs.items(), key=lambda kv: -abs(kv[1]))[:12]
        for key, v in top:
            print("  %-28s %+.3f  (%s translation success)"
                  % (key, v, "raises" if v > 0 else "lowers"))

    if args.export_model:
        path = export_model(args.export_model, c, N, args.folds, args.repeats,
                            args.feature_schema_version, results)
        print("\nwrote %s" % path)

    if args.json_out:
        json.dump({"runs": c.run_ids, "datasets": [os.path.basename(p) for p in c.paths],
                   "horizon": N, "function_ids": c.ids, "replicate_ceiling_auc": ceiling,
                   "policies": results, "lr_coefficients": coefs, "oof": oof,
                   "external": external, "permutation": permutation},
                  open(args.json_out, "w"), indent=1)
        print("\nwrote %s" % args.json_out)


if __name__ == "__main__":
    main()
