"""The replicate series the figures in this directory are drawn from.

Three full `evaluation_set` runs of one frozen configuration (scripts/benchmark.json,
sha f9e30f4b), with pipeline code identical across them apart from a comment. A
figure drawn from any one of them shows what that run happened to draw: a quarter
of the functions change outcome between runs, and a bucket of 20 moves five
percentage points per function. The thesis reports the series, so every generator
here takes `--runs` and defaults to all three; `--runs <one id>` still draws a
single run. EVALUATION.md "Replicate series" holds the tables these figures
illustrate.

One loader per artifact, shared by every figure, so that "validated", "cost" and
"N*" mean the same thing in all of them:

  * the run log (runs/run-<ts>.jsonl)          -> outcome per function
  * cmd/energy -json                            -> facility joules per attempt,
                                                   failed attempts included
  * cmd/runtime -runlog (evaluation/runtime-*)  -> per-invocation joules of both
                                                   sides, validated translations only

Stdlib only, like the figures themselves.
"""

import json
import math
import os
import sys

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

BUCKETS = ["A", "B", "C", "D+"]

# Failure kinds that mean a fixture never produced a usable answer, as opposed to
# producing the wrong one. "output mismatch" and "side-effect mismatch" are
# deliberately not here: a mismatch is a translation that ran.
EXECUTION_FAILURES = {"execution error", "timeout", "setup failed", "invalid fixture"}

NUMBER = {1: "one", 2: "two", 3: "three", 4: "four", 5: "five"}


class Run:
    def __init__(self, run_id, run_log):
        self.id = run_id
        self.run_log = os.path.join(REPO, "runs", run_log)
        self.energy = os.path.join(REPO, "evaluation", "prediction", "energy-%s.json" % run_id)
        self.runtime = os.path.join(REPO, "evaluation", "runtime-%s.json" % run_id)
        self.report = os.path.join(REPO, "evaluation", "runtime-report-%s.json" % run_id)


# The run id is scripts/run-benchmark.sh's batch id (manifest, packages, runtime
# report); the run log is named after the service's own start time, so the pairing
# has to be written down somewhere. load_runtime() checks it against the report.
KNOWN = [
    Run("20260904-190539", "run-20260904-170428.jsonl"),
    Run("20260911-165103", "run-20260911-144954.jsonl"),
    Run("20260912-172904", "run-20260912-152820.jsonl"),
]
REPLICATES = [r.id for r in KNOWN]
SERIES = "replicates-f9e30f4b"
RESULTS = os.path.join(REPO, "evaluation", "prediction", "results-%s.json" % SERIES)


def add_runs_argument(ap):
    ap.add_argument("--runs", default=",".join(REPLICATES),
                    help="comma-separated run ids (default: the replicate series %s)"
                         % ", ".join(REPLICATES))


def select(spec):
    known = {r.id: r for r in KNOWN}
    ids = [s.strip() for s in spec.split(",") if s.strip()]
    missing = [i for i in ids if i not in known]
    if missing or not ids:
        sys.exit("unknown run id(s) %s; known: %s" % (missing, ", ".join(known)))
    return [known[i] for i in ids]


def stem(prefix, runs):
    ids = [r.id for r in runs]
    return prefix + "-" + (SERIES if ids == REPLICATES else "+".join(ids))


def describe(runs, tex=False):
    """'the three runs of the replicate series' or 'run 20260904-190539'."""
    ids = [r.id for r in runs]
    if ids == REPLICATES:
        return "the %s runs of the replicate series" % NUMBER.get(len(ids), str(len(ids)))
    tt = (lambda s: r"\texttt{%s}" % s) if tex else (lambda s: s)
    if len(ids) == 1:
        return "run " + tt(ids[0])
    return "runs " + ", ".join(tt(i) for i in ids)


def function_order(fid):
    """f2 before f10."""
    digits = "".join(ch for ch in fid if ch.isdigit())
    return (len(fid) - len(digits), int(digits) if digits else 0, fid)


# -------------------------------------------------------------- loaders -----

def load_jobs(run):
    """function_id -> bucket, aws and the three nested outcomes of one run."""
    out = {}
    with open(run.run_log) as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            rec = json.loads(line)
            if rec.get("type") != "job":
                continue
            m = rec.get("metrics") or {}
            meta = m.get("meta") or {}
            outcomes = m.get("test_outcomes") or []
            out[rec["function_id"]] = {
                "bucket": meta.get("bucket"),
                "aws": bool(meta.get("aws")),
                # Reaching the test stage at all is the buildable signal: a package
                # that does not compile is never executed, so it records no outcomes.
                "buildable": bool(outcomes),
                # Every fixture executed cleanly in the final round ([A19]: the
                # outcomes describe the last validation round only).
                "validated": bool(outcomes) and all(
                    o.get("passed") or o.get("kind") not in EXECUTION_FAILURES
                    for o in outcomes),
                # `completed` is pointer-like in the log: absent means true, for the
                # same reason cmd/energy reads it that way.
                "completed": bool(rec.get("completed", True)),
            }
    return out


def load_costs(run):
    """function_id -> facility joules the attempt cost, successes and failures."""
    with open(run.energy) as fh:
        d = json.load(fh)
    return {t["function_id"]: t["facility_joules"]
            for t in d["translations"] + d["failed_attempts"]["translations"]}


def load_runtime(run):
    """function_id -> (python J, go J) per steady-state invocation.

    Validated translations only: a report that was not filtered with
    cmd/runtime -runlog is refused, because it times packages that fail their
    fixtures - fast, since the work they skip is the work they were supposed to do.
    """
    with open(run.report) as fh:
        report = json.load(fh)
    if not report.get("validated_only"):
        sys.exit("%s is not filtered to validated translations; rebuild it with "
                 "cmd/runtime -runlog before plotting" % run.report)
    logs = report.get("run_logs") or []
    log_stem = os.path.basename(run.run_log)[len("run-"):-len(".jsonl")]
    if logs and log_stem not in logs:
        sys.exit("%s was measured against run log(s) %s, not %s"
                 % (run.report, logs, os.path.basename(run.run_log)))
    with open(run.runtime) as fh:
        rt = json.load(fh)
    return {f: (v["python_joules_per_invocation"], v["go_joules_per_invocation"])
            for f, v in rt.items()}


def nstar(jobs, costs, runtime):
    """Break-even per validated, measured translation of one run:

        N* = E_translation / (E_python - E_go)

    math.inf where Go is not faster, i.e. the translation never repays. Validated
    translations without a runtime measurement are left out, as cmd/energy leaves
    them out."""
    out = {}
    for f, j in jobs.items():
        if not j["completed"] or f not in runtime:
            continue
        py, go = runtime[f]
        saving = py - go
        out[f] = costs[f] / saving if saving > 0 else math.inf
    return out


def gate_decisions(runs, results=RESULTS, model="M1 logistic regression"):
    """function_id -> the prediction gate's out-of-fold decision.

    The gate is the model evaluate.py fitted on these very runs, so its decisions
    on them may only come from cross-validation: a function is translated when its
    held-out balanced-point decision says so in at least half of the CV repeats.
    """
    if not os.path.exists(results):
        sys.exit("%s does not exist; run evaluation/prediction/evaluate.py with --json-out "
                 "first (see evaluation/prediction/README.md)" % results)
    with open(results) as fh:
        d = json.load(fh)
    if d.get("runs") != [r.id for r in runs]:
        sys.exit("%s holds out-of-fold decisions for runs %s, not %s"
                 % (results, d.get("runs"), [r.id for r in runs]))
    o = d["oof"][model]
    return {f: freq >= 0.5
            for f, freq in zip(d["function_ids"], o["translate_frequency_balanced"])}


# ---------------------------------------------------------------- stats -----

def median(xs):
    s = sorted(xs)
    n = len(s)
    if n == 0:
        return float("nan")
    return s[n // 2] if n % 2 else (s[n // 2 - 1] + s[n // 2]) / 2


def log_median(xs):
    """Median of positive values on a log scale: for an even count, the geometric
    mean of the two middle values. math.inf (never repays) sorts last and wins a
    tie, so a function that repays in one of two runs is not called cheap."""
    s = sorted(xs)
    n = len(s)
    if n % 2:
        return s[n // 2]
    a, b = s[n // 2 - 1], s[n // 2]
    if math.isinf(a) or math.isinf(b):
        return math.inf
    return math.sqrt(a * b)


def log_quantile(xs, q):
    """Quantile of finite positive values, interpolated in log10 space - the space
    the axis is drawn in."""
    s = sorted(math.log10(v) for v in xs)
    pos = q * (len(s) - 1)
    lo = int(math.floor(pos))
    hi = min(lo + 1, len(s) - 1)
    return 10 ** (s[lo] + (s[hi] - s[lo]) * (pos - lo))


def tex_sci(v, digits=1):
    e = int(math.floor(math.log10(v)))
    return ("%." + str(digits) + "f$\\times$10\\textsuperscript{%d}") % (v / 10 ** e, e)


def plain_sci(v, digits=1):
    e = int(math.floor(math.log10(v)))
    return ("%." + str(digits) + "f x 10^%d") % (v / 10 ** e, e)
