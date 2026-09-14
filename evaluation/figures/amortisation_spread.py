#!/usr/bin/env python3
"""Invocations required before a translation is net energy saving.

The analogue of Werner et al. Figure 6, with the resolution their data could not
support. Their figure draws one box per model: width is the best to worst case
required invocations, height is how many functions amortise at all. Every one of
their three boxes spans exactly 101x, which is the signature of a single
translation cost per model crossed with a global range of savings potential
taken from their Figure 1.

Here both terms are per function and per run. Each translation carries its own
measured facility energy (cmd/energy) and its own measured per-invocation saving
(cmd/runtime, RAPL, steady state), so

    N*_i,k = E_translation,i,k / (E_python,i,k - E_go,i,k)

is a per-function quantity measured once in every run k of the replicate series in
which function i was validated, and the spread is a real distribution rather than a
bounding box.

  (a) One point per function, at the median of its N* over the runs in which it was
      validated (for two runs, the geometric mean; a run in which it never repays
      counts as infinite). The spread *within* a function across runs is the subject
      of nstar_distribution.py.
  (b) Their axes, with the box replaced by the cumulative curve whose bounding
      rectangle the box would have been, one curve per screening policy - the choice
      we have in place of their choice of model is which candidates to translate at
      all. The line is the mean over the runs, the band the lowest to highest run.

Three things the panels are careful about:

  - A translation that is not faster than its Python original never repays at any
    N. It is drawn as censored at the right edge of (a) rather than dropped,
    because dropping it is what makes a median look optimistic.
  - The ticks at the top of (b) are each run's *portfolio* break-even of a policy,
    which is not the median of its per-function values: it charges the policy for
    the failed attempts it paid for, so it is the number a deployment decision
    actually turns on.
  - The prediction gate is the logistic regression evaluate.py fitted on these same
    runs, so on them it may only act through its out-of-fold decisions
    (replicates.gate_decisions).

Stdlib only. Emits TikZ/pgfplots, an SVG preview, and a caption.

    python3 evaluation/figures/amortisation_spread.py
"""

import argparse
import math
import os

import replicates as rep
from plotkit import Panel, esc, wrap

HERE = os.path.join(rep.REPO, "evaluation", "figures")

AWS_COLOR = "#1f4fd8"
NON_COLOR = "#e8890c"
NEVER_COLOR = "#b0b0b0"

# Screening policies. The two that need no model are kept first, because they
# are the ones a reader can verify without accepting anything about the gate.
#
# The oracle curve coincides exactly with translate-all, because both translate
# every function that succeeds and therefore amortise the same set. That is not
# a defect of the figure, it is the point: the oracle's advantage is entirely in
# the attempts it does not pay for, which is visible only in the portfolio
# marker. It is drawn with a sparse dash so the curve underneath shows through.
SCENARIOS = [
    ("translate all", "#4c72b0", "solid"),
    ("skip cloud functions", "#dd8452", "solid"),
    ("prediction gate", "#55a868", "solid"),
    ("oracle", "#111111", "sparse"),
]

# Axis range, in powers of ten. Chosen to hold the measured span with one
# decade of margin on each side; the censored column sits beyond XMAX.
XMIN, XMAX = 4, 9
CENSORED_AT = 9.55
XLIM = (XMIN - 0.15, 9.95)


# ---------------------------------------------------------------- data ------

def build(runs, results):
    data = []
    for run in runs:
        jobs = rep.load_jobs(run)
        costs = rep.load_costs(run)
        runtime = rep.load_runtime(run)
        data.append((run, jobs, costs, runtime, rep.nstar(jobs, costs, runtime)))
    functions = sorted(data[0][1], key=rep.function_order)
    aws = {f: data[0][1][f]["aws"] for f in functions}

    # Panel (a): every function validated in at least one run, at its median.
    values = {}
    for _, _, _, _, ns in data:
        for f, v in ns.items():
            values.setdefault(f, []).append(v)
    medians = {f: rep.log_median(v) for f, v in values.items()}
    repay = sorted((m, f, aws[f]) for f, m in medians.items() if not math.isinf(m))
    never = sorted((f for f, m in medians.items() if math.isinf(m)), key=rep.function_order)

    # Panel (b): one curve per policy per run.
    gate = rep.gate_decisions(runs, results)
    policies = {
        "translate all": lambda f, jobs: True,
        "skip cloud functions": lambda f, jobs: not aws[f],
        "prediction gate": lambda f, jobs: gate[f],
        "oracle": lambda f, jobs: jobs[f]["completed"],
    }
    curves = {}
    for name, decide in policies.items():
        per_run = []
        for run, jobs, costs, runtime, ns in data:
            chosen = [f for f in functions if decide(f, jobs)]
            spend = sum(costs[f] for f in chosen)
            paying = [f for f in chosen if f in ns and not math.isinf(ns[f])]
            rate = sum(runtime[f][0] - runtime[f][1] for f in paying)
            per_run.append({
                "run": run.id,
                "nstars": sorted(ns[f] for f in paying),
                "translated": len(chosen),
                "succeeded": sum(1 for f in chosen if jobs[f]["completed"]),
                "portfolio": spend / rate if rate > 0 else None,
                "spend": spend,
            })
        curves[name] = per_run
    return repay, never, curves, values


def envelope(per_run):
    """(x, mean, lowest, highest) functions amortised by N = x, over the runs."""
    xs = sorted({n for c in per_run for n in c["nstars"]})
    out = []
    for x in xs:
        counts = [sum(1 for n in c["nstars"] if n <= x) for c in per_run]
        out.append((x, sum(counts) / len(counts), min(counts), max(counts)))
    return out


def step_points(env, idx, x_end):
    """A cumulative step curve through column `idx` of an envelope."""
    if not env:
        return []
    pts = [(env[0][0], 0.0)]
    prev = 0.0
    for row in env:
        pts.append((row[0], prev))
        pts.append((row[0], row[idx]))
        prev = row[idx]
    pts.append((x_end, prev))
    return pts


def band_polygon(env, x_end):
    upper = step_points(env, 3, x_end)
    lower = step_points(env, 2, x_end)
    return upper + list(reversed(lower))


def span(vals, fmt="%d"):
    lo, hi = min(vals), max(vals)
    return fmt % lo if lo == hi else (fmt + "--" + fmt) % (lo, hi)


def y_top(curves):
    top = max(max(len(c["nstars"]) for c in per_run) for per_run in curves.values())
    return int(math.ceil((top + 3) / 5.0)) * 5


# ---------------------------------------------------------------- tikz ------

TIKZ_HEAD = r"""% Invocations until a translation is net energy saving - @DESC@.
% Generated by evaluation/figures/amortisation_spread.py - do not edit by hand.
%
% Requires, in the document preamble:
%   \usepackage{pgfplots}
%   \pgfplotsset{compat=1.18}
%   \usepgfplotslibrary{groupplots}
%
% Then: \input{figures/@STEM@.tex}
\begin{tikzpicture}
\definecolor{awsblue}{HTML}{@AWS@}
\definecolor{nonawsorange}{HTML}{@NON@}
\definecolor{nevergrey}{HTML}{@NEVER@}
@SCENCOLORS@
\pgfplotsset{
  amortaxis/.style={
    width=\linewidth,
    xmode=log, log basis x=10,
    xmin=@XLO@, xmax=@XHI@,
    xtick={@XTICKS@},
    grid=major, grid style={draw=black!10},
    tick align=outside, tick pos=left,
    legend cell align=left,
    legend style={draw=black!25, font=\footnotesize, fill=white,
                  fill opacity=0.92, text opacity=1},
  },
}
\begin{groupplot}[group style={group size=1 by 2, vertical sep=1.7cm}]
"""

TIKZ_DASH = {"solid": "solid", "sparse": "dash pattern=on 1.5pt off 4pt"}

TIKZ_TAIL = r"""\end{groupplot}
\end{tikzpicture}
"""


def coords(pairs):
    return " ".join("(%.6g,%.6g)" % (x, y) for x, y in pairs)


def ticks_every(top, step=10):
    return ",".join(str(v) for v in range(0, top + 1, step))


def write_tikz(path, stem, runs, repay, never, curves):
    scen_colors = "\n".join(
        r"\definecolor{scen%d}{HTML}{%s}" % (i, c[1:])
        for i, (_, c, _) in enumerate(SCENARIOS))
    head = (TIKZ_HEAD.replace("@DESC@", rep.describe(runs)).replace("@STEM@", stem)
            .replace("@AWS@", AWS_COLOR[1:]).replace("@NON@", NON_COLOR[1:])
            .replace("@NEVER@", NEVER_COLOR[1:])
            .replace("@SCENCOLORS@", scen_colors)
            .replace("@XLO@", "%g" % 10 ** XLIM[0]).replace("@XHI@", "%g" % 10 ** XLIM[1])
            .replace("@XTICKS@", ",".join("1e%d" % k for k in range(XMIN, XMAX + 1))))
    out = [head]

    aws_pts = [(n, i + 1) for i, (n, _, a) in enumerate(repay) if a]
    non_pts = [(n, i + 1) for i, (n, _, a) in enumerate(repay) if not a]
    cens = [(10 ** CENSORED_AT, len(repay) + 1 + i) for i in range(len(never))]
    top_a = len(repay) + len(never) + 2
    many = len(runs) > 1

    out.append(r"""% (a) one point per function, sorted by required invocations@MEDIANNOTE@.
% The censored column on the right holds the functions that are not faster than
% their Python original and therefore never repay at any N.
\nextgroupplot[amortaxis, height=7.2cm,
  title={(a) Required invocations per function@TITLENOTE@},
  xlabel={Invocations until net energy saving},
  ylabel={Function (rank)},
  ymin=0, ymax=@TOP@, ytick={@YTICKS@},
  legend pos=north west,
]
\draw[black!35, densely dotted] (axis cs:@CENSLINE@,0) -- (axis cs:@CENSLINE@,@TOP@);
\addplot[only marks, mark=*, mark size=1.5pt, awsblue] coordinates {@AWSPTS@};
\addlegendentry{cloud functions}
\addplot[only marks, mark=*, mark size=1.5pt, nonawsorange] coordinates {@NONPTS@};
\addlegendentry{non-cloud functions}
\addplot[only marks, mark=x, mark size=2.4pt, nevergrey] coordinates {@CENS@};
\addlegendentry{never repays}
\node[anchor=south east, font=\footnotesize, text=black!60]
  at (rel axis cs:0.985,0.03) {median @MEDIAN@};
\draw[black!50, dashed] (axis cs:@MEDIANX@,0) -- (axis cs:@MEDIANX@,@TOP@);
"""
               .replace("@MEDIANNOTE@", ", at its median over the runs" if many else "")
               .replace("@TITLENOTE@", " (median over runs)" if many else "")
               .replace("@TOP@", str(top_a))
               .replace("@YTICKS@", ticks_every(top_a))
               .replace("@CENSLINE@", "%g" % 10 ** (XMAX + 0.3))
               .replace("@AWSPTS@", coords(aws_pts))
               .replace("@NONPTS@", coords(non_pts))
               .replace("@CENS@", coords(cens))
               .replace("@MEDIANX@", "%.6g" % median_of(repay))
               .replace("@MEDIAN@", rep.tex_sci(median_of(repay))))

    top_b = y_top(curves)
    x_end = 10 ** XLIM[1]
    body = []
    for i, (name, _, style) in enumerate(SCENARIOS):
        per_run = curves[name]
        env = envelope(per_run)
        if not env:
            continue
        if many and name != "oracle":
            poly = band_polygon(env, x_end)
            body.append(r"\fill[scen%d, fill opacity=0.13] %s -- cycle;"
                        % (i, " -- ".join("(axis cs:%.6g,%.6g)" % p for p in poly)))
        body.append(r"\addplot[const plot, scen%d, %s, line width=1pt] coordinates {%s};"
                    % (i, TIKZ_DASH[style], coords(step_points(env, 1, x_end))))
        body.append(r"\addlegendentry{%s (%s translated)}"
                    % (name, span([c["translated"] for c in per_run])))
    for i, (name, _, _) in enumerate(SCENARIOS):
        for c in curves[name]:
            if c["portfolio"]:
                body.append(r"\draw[scen%d, line width=0.9pt] (axis cs:%.6g,%.6g) -- "
                            r"(axis cs:%.6g,%d);"
                            % (i, c["portfolio"], top_b * 0.9, c["portfolio"], top_b))

    out.append(r"""
% (b) the same axes as Werner et al. Figure 6, with the box replaced by the
% cumulative curve whose bounding rectangle the box would have been. @BANDNOTE@
% Ticks at the top mark each run's portfolio break-even, which charges the policy
% for the failed attempts it paid for.
\nextgroupplot[amortaxis, height=6.4cm,
  title={(b) Functions amortised, by screening policy},
  xlabel={Invocations until net energy saving},
  ylabel={Functions amortised},
  ymin=0, ymax=@YMAX@, ytick={@YTICKS@},
  % below the band where the portfolio ticks are drawn, so the legend cannot hide them
  legend style={at={(0.015,0.86)}, anchor=north west},
]
@BODY@
"""
               .replace("@BANDNOTE@", "Lines are the mean over the runs,\n% bands the lowest "
                                      "to the highest run." if many else "")
               .replace("@YMAX@", str(top_b))
               .replace("@YTICKS@", ticks_every(top_b))
               .replace("@BODY@", "\n".join(body)))

    out.append(TIKZ_TAIL)
    with open(path, "w") as fh:
        fh.write("".join(out))


def median_of(repay):
    v = [n for n, _, _ in repay]
    k = len(v) // 2
    return v[k] if len(v) % 2 else math.sqrt(v[k - 1] * v[k])


CAPTION = r"""% Suggested caption - the numbers are generated, so edit the prose only.
\caption{Invocations required before a translation repays the energy spent producing
  it, over @DESC@. (a) @PANELA@; @NREPAY@ repay, spanning @SPAN@ orders of magnitude
  from @MINV@ to @MAXV@, while the @NNEVER@ marked at the right are not faster than
  their Python original and never repay at any $N$@NEVERNOTE@. (b) The cumulative form of
  the same quantity under four screening policies, on the axes of Werner et al.\
  Figure~6@BANDS@; ticks at the top mark each run's portfolio break-even, which unlike
  the per-function values also carries the cost of the attempts that failed
  (@PORTFOLIOS@). The prediction gate is the logistic-regression model fitted on
  @THESE@, applied through its out-of-fold decisions.@SKIPNOTE@}
"""


def portfolio_summary(curves, tex):
    parts = []
    for name, _, _ in SCENARIOS:
        vals = [c["portfolio"] for c in curves[name] if c["portfolio"]]
        if not vals:
            continue
        f = rep.tex_sci if tex else rep.plain_sci
        if len(vals) == 1:
            parts.append("%s %s" % (name, f(vals[0])))
        else:
            parts.append("%s %s to %s" % (name, f(min(vals)), f(max(vals))))
    return "; ".join(parts)


def skip_note(curves):
    med = {n: rep.median([c["portfolio"] for c in curves[n] if c["portfolio"]])
           for n in ("translate all", "skip cloud functions")}
    if med["skip cloud functions"] > 10 * med["translate all"]:
        return (" Skipping cloud functions removes most of the achievable saving, since "
                "that is where the saving is.")
    return ""


def write_caption(path, runs, repay, never, curves):
    many = len(runs) > 1
    v = [n for n, _, _ in repay]
    text = (CAPTION
            .replace("@DESC@", rep.describe(runs, tex=True))
            .replace("@PANELA@", "One point per function validated in at least one run, at "
                                 "the median of its break-even over the runs in which it was "
                                 "validated, sorted by requirement" if many else
                     "One point per validated translation, sorted by requirement")
            .replace("@NREPAY@", str(len(repay)))
            .replace("@NNEVER@", str(len(never)))
            .replace("@NEVERNOTE@", " in the median over their runs" if many else "")
            .replace("@SPAN@", "%.1f" % math.log10(v[-1] / v[0]))
            .replace("@MINV@", rep.tex_sci(v[0]))
            .replace("@MAXV@", rep.tex_sci(v[-1]))
            .replace("@BANDS@", "; lines are the mean number of functions amortised over the "
                                "runs, shaded bands the lowest to the highest run"
                     if many else "")
            .replace("@PORTFOLIOS@", portfolio_summary(curves, tex=True))
            .replace("@THESE@", "these runs" if many else "this run")
            .replace("@SKIPNOTE@", skip_note(curves)))
    with open(path, "w") as fh:
        fh.write(text)


# ----------------------------------------------------------------- svg ------

def logfmt(k):
    return "10^%d" % int(round(k))


def write_svg(path, runs, repay, never, curves):
    many = len(runs) > 1
    W, H = 980, 870
    svg = ['<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" '
           'viewBox="0 0 %d %d" font-family="DejaVu Sans, Helvetica, Arial, sans-serif">'
           % (W, H, W, H),
           '<rect width="%d" height="%d" fill="#ffffff"/>' % (W, H)]
    ticks = list(range(XMIN, XMAX + 1))
    top_a = len(repay) + len(never) + 2

    p1 = Panel(80, 52, 820, 290, XLIM, (0, top_a))
    p1.frame("(a) Required invocations per function%s" % (" (median over runs)" if many else ""),
             "Invocations until net energy saving", "Function (rank)",
             ticks, list(range(0, top_a + 1, 10)), xfmt=logfmt)
    p1.vline(XMAX + 0.3, "#bbbbbb", dash="2 3")
    p1.vline(math.log10(median_of(repay)), "#999999", dash="5 3")
    for i, (n, _, a) in enumerate(repay):
        p1.out.append('<circle cx="%.2f" cy="%.2f" r="2.6" fill="%s"/>'
                      % (p1.px(math.log10(n)), p1.py(i + 1),
                         AWS_COLOR if a else NON_COLOR))
    for i in range(len(never)):
        x, y = p1.px(CENSORED_AT), p1.py(len(repay) + 1 + i)
        p1.out.append('<path d="M %.2f %.2f l 5 5 M %.2f %.2f l 5 -5" stroke="%s" '
                      'stroke-width="1.6"/>' % (x - 2.5, y - 2.5, x - 2.5, y + 2.5,
                                                NEVER_COLOR))
    p1.legend([("cloud functions", AWS_COLOR, False),
               ("non-cloud functions", NON_COLOR, False),
               ("never repays", NEVER_COLOR, False)], 0.015, 0.03)
    p1.note([("median %s" % rep.plain_sci(median_of(repay)), "#555")], 0.985, 0.93)
    svg += p1.out

    top_b = y_top(curves)
    p2 = Panel(80, 430, 820, 250, XLIM, (0, top_b))
    p2.frame("(b) Functions amortised, by screening policy",
             "Invocations until net energy saving", "Functions amortised",
             ticks, list(range(0, top_b + 1, 10)), xfmt=logfmt)
    x_end = XLIM[1]
    for name, color, style in SCENARIOS:
        env = envelope(curves[name])
        if not env:
            continue
        logged = [(math.log10(r[0]), r[1], r[2], r[3]) for r in env]
        if many and name != "oracle":
            poly = band_polygon(logged, x_end)
            p2.out.append('<path d="M %s Z" fill="%s" fill-opacity="0.13" stroke="none"/>'
                          % (" L ".join("%.2f %.2f" % (p2.px(x), p2.py(y)) for x, y in poly),
                             color))
        pts = step_points(logged, 1, x_end)
        dash = ' stroke-dasharray="3 6"' if style != "solid" else ""
        p2.out.append('<path d="M %s" fill="none" stroke="%s" stroke-width="1.8"%s/>'
                      % (" L ".join("%.2f %.2f" % (p2.px(x), p2.py(y)) for x, y in pts),
                         color, dash))
        for c in curves[name]:
            if c["portfolio"]:
                xx = p2.px(math.log10(c["portfolio"]))
                p2.out.append('<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="%s" '
                              'stroke-width="2"/>' % (xx, p2.py(top_b), xx,
                                                      p2.py(top_b * 0.9), color))
    p2.legend([("%s (%s translated)" % (n, span([c["translated"] for c in curves[n]])),
                c, False) for n, c, _ in SCENARIOS], 0.015, 0.14)
    svg += p2.out

    v = [n for n, _, _ in repay]
    caption = ("%s. %d functions repay%s, spanning %.1f orders of magnitude (%.3g to %.3g); "
               "%d never repay. In (b) lines are %s; ticks at the top are each run's "
               "portfolio break-even (%s). The gate acts through out-of-fold decisions of the "
               "model fitted on %s."
               % (rep.describe(runs)[0].upper() + rep.describe(runs)[1:], len(repay),
                  " at their median" if many else "", math.log10(v[-1] / v[0]), v[0], v[-1],
                  len(never), "means over runs, bands the lowest to highest run"
                  if many else "cumulative counts",
                  portfolio_summary(curves, tex=False), "these runs" if many else "this run"))
    for i, line in enumerate(wrap(caption, 128)):
        svg.append('<text x="80" y="%d" font-size="11" fill="#444">%s</text>'
                   % (H - 78 + i * 15, esc(line)))
    svg.append("</svg>")
    with open(path, "w") as fh:
        fh.write("\n".join(svg) + "\n")


# ---------------------------------------------------------------- build -----

def main():
    ap = argparse.ArgumentParser()
    rep.add_runs_argument(ap)
    ap.add_argument("--results", default=rep.RESULTS,
                    help="evaluate.py --json-out holding the gate's out-of-fold decisions "
                         "for exactly these runs")
    args = ap.parse_args()
    runs = rep.select(args.runs)
    stem = rep.stem("amortisation-spread", runs)

    repay, never, curves, values = build(runs, args.results)
    os.makedirs(HERE, exist_ok=True)
    write_tikz(os.path.join(HERE, stem + ".tex"), stem, runs, repay, never, curves)
    write_svg(os.path.join(HERE, stem + ".svg"), runs, repay, never, curves)
    write_caption(os.path.join(HERE, stem + "-caption.tex"), runs, repay, never, curves)

    v = [n for n, _, _ in repay]
    print("wrote %s.{tex,svg,-caption.tex}" % os.path.join(HERE, stem))
    print("  runs: %s" % ", ".join(r.id for r in runs))
    print("  functions validated in >=1 run: %d; repay at median %d, never at median %d"
          % (len(values), len(repay), len(never)))
    print("  median-N* min %.4g  median %.4g  max %.4g  (%.2f orders of magnitude)"
          % (v[0], median_of(repay), v[-1], math.log10(v[-1] / v[0])))
    print("  %-22s %-16s %6s %6s %6s %12s" % ("policy", "run", "transl", "succ", "repay",
                                             "portfolio"))
    for name, _, _ in SCENARIOS:
        for c in curves[name]:
            print("  %-22s %-16s %6d %6d %6d %12.4g"
                  % (name, c["run"], c["translated"], c["succeeded"], len(c["nstars"]),
                     c["portfolio"] or float("inf")))


if __name__ == "__main__":
    main()
