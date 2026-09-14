#!/usr/bin/env python3
"""Figure 1 analogue: the distribution of per-function energy savings from
translating Python to Go, split into AWS and non-AWS functions.

Werner et al. (2025), "Code once, Run Green", Figure 1 plots one histogram of
"Consumption Reductions [J]" over their 14 manually translated functions. This
draws the same chart for our *validated* translations across the replicate series
(see replicates.py), as two overlapping series, because the AWS/non-AWS split is
the axis this corpus was sampled on (EVALUATION_DATASET.md) and - as the figure
shows - it is where the entire effect lives.

One value per function. A function contributes the median of its saving over the
runs in which it was a validated, measured translation. Pooling every (function,
run) measurement instead would count the functions that validate in every run
three times, weighting the sample toward the easy ones, and would break the
one-observation-per-function assumption of the tests below. The median also damps a
single run's outlier: run 20260912-172904 has four translations that return early
on an unset environment variable (EVALUATION.md "Replicate series").

Units. The paper's caption says "per invocation" but its method invokes each
function 1,000 times and "accumulated the results in Figure 1", and a mean of
201 J for functions it calls "rather basic" is only coherent on the accumulated
reading. Its own break-even discussion agrees: savings of 201 J per single
invocation against a 61-second translation would amortize in tens of
invocations, not the 3,000-5,000 at best and nearly 10^6 at worst it reports. So
the x-axis here is **joules saved over 1,000 invocations** = 1000 x (E_python -
E_go) per invocation, which is the basis that puts the two figures on one scale.
Divide by 1,000 for the per-invocation figure cmd/energy uses for N*.

Outputs a TikZ/pgfplots figure for the write-up and an SVG to look at without
compiling. Both are drawn from one geometry function, so the preview cannot
drift from the figure that goes in the thesis. Stdlib only, on purpose: there is
no matplotlib and no LaTeX on this machine, and a figure that regenerates
anywhere is worth more than one that needs an environment.

    python3 evaluation/figures/savings_histogram.py                          # the series
    python3 evaluation/figures/savings_histogram.py --runs 20260904-190539   # one run
"""

import argparse
import math
import os
import sys

import replicates as rep
from plotkit import Panel, esc, fmt, rects, steps, wrap

HERE = os.path.join(rep.REPO, "evaluation", "figures")

INVOCATIONS = 1000  # the paper's N, and the basis of the x-axis

# Werner et al. Figure 1, recovered from the PDF's vector drawing (the figure is
# a matplotlib Form XObject, so its geometry is exact rather than eyeballed):
# the axis transform was solved from the tick-label anchors and the bars read
# off as paths. Seven bins of 306.8 J starting at -426.3 J, heights in percent.
# Those heights sum to 60, not 100, so their histogram is normalised over more
# data than it draws - it is reproduced here as a shape to compare against, not
# as a distribution to do arithmetic on.
PAPER_BIN_LO = -426.3
PAPER_BIN_W = 306.8
PAPER_HEIGHTS = [3.0, 39.0, 9.0, 4.0, 3.0, 1.0, 1.0]
PAPER_XLIM = (-533.2, 1828.8)
PAPER_XTICKS = [-500, 0, 500, 1000, 1500]
PAPER_MEAN = 201.0

# Signed order of magnitude. The paper's linear scale cannot resolve this
# corpus, whose savings span four orders of magnitude: on any axis wide enough
# for the AWS tail, every non-AWS function collapses onto the zero line. These
# bins are the smallest change that makes both groups visible, and they keep the
# sign - which is the whole question for a third of the translations.
MAG_EDGES = [-100, -10, -1, -0.1, -0.01, 0.01, 0.1, 1, 10, 100, 1000]

# The two series land in the same bin on the paper's scale, so equal-width
# overlapping bars would hide one behind the other entirely. Nesting the second
# series inside the first keeps both readable without dodging them apart, which
# would misrepresent them as occupying different bins.
AWS_FRAC = 0.98
NON_FRAC = 0.50

AWS_COLOR = "#1f4fd8"
NON_COLOR = "#e8890c"
PAPER_COLOR = "#8a8a8a"


# ---------------------------------------------------------------- data ------

def load(runs):
    """Per-function savings over INVOCATIONS invocations, split by AWS usage.

    Returns (aws, non, per_run): aws and non are sorted (function_id, value)
    lists, one entry per function at the median of its per-run savings; per_run
    holds, per run, the AWS and non-AWS values that run contributed.
    """
    values, aws, per_run = {}, {}, []
    for run in runs:
        jobs = rep.load_jobs(run)
        runtime = rep.load_runtime(run)
        a, n = [], []
        for f, (py, go) in runtime.items():
            if not jobs.get(f, {}).get("completed"):
                sys.exit("run %s: %s has a runtime row but is not a validated translation"
                         % (run.id, f))
            v = (py - go) * INVOCATIONS
            values.setdefault(f, []).append(v)
            aws[f] = jobs[f]["aws"]
            (a if aws[f] else n).append(v)
        per_run.append((run, a, n))
    agg = [(f, rep.median(v)) for f, v in values.items()]
    return (sorted([r for r in agg if aws[r[0]]], key=lambda r: r[1]),
            sorted([r for r in agg if not aws[r[0]]], key=lambda r: r[1]),
            per_run)


def mean(xs):
    return sum(xs) / len(xs)


def sign_test(xs):
    """Two-sided sign test for "savings differ from zero".

    A sign test rather than a t-test because these values span four orders of
    magnitude and are nowhere near normal; it asks only the question the figure
    is about - does translating this class of function save energy at all.
    """
    nonzero = [x for x in xs if x != 0]
    n = len(nonzero)
    k = sum(1 for x in nonzero if x > 0)
    tail = min(k, n - k)
    p = 2 * sum(math.comb(n, i) for i in range(tail + 1)) / 2 ** n
    return k, n, min(p, 1.0)


def mann_whitney(a, b):
    """Rank-sum test that the two groups come from the same distribution."""
    pooled = sorted([(v, 0) for v in a] + [(v, 1) for v in b])
    ranks = [0.0] * len(pooled)
    i = 0
    while i < len(pooled):
        j = i
        while j + 1 < len(pooled) and pooled[j + 1][0] == pooled[i][0]:
            j += 1
        for t in range(i, j + 1):
            ranks[t] = (i + j) / 2 + 1
        i = j + 1
    ra = sum(ranks[t] for t in range(len(pooled)) if pooled[t][1] == 0)
    na, nb = len(a), len(b)
    u = ra - na * (na + 1) / 2
    z = (u - na * nb / 2) / math.sqrt(na * nb * (na + nb + 1) / 12)
    return u, z, math.erfc(abs(z) / math.sqrt(2))


def histogram(values, edges):
    """Percent per bin; the last bin is closed so the maximum is not lost."""
    counts = [0] * (len(edges) - 1)
    for v in values:
        for i in range(len(counts)):
            last = i == len(counts) - 1
            if edges[i] <= v < edges[i + 1] or (last and v == edges[i + 1]):
                counts[i] += 1
                break
    return [100.0 * c / len(values) for c in counts]


def zero_bins_note(values):
    """How many of our values sit in the paper-scale bin holding zero and its
    right-hand neighbour - the visual claim panel (a) makes."""
    lin_edges = [PAPER_BIN_LO + i * PAPER_BIN_W for i in range(len(PAPER_HEIGHTS) + 1)]
    z = next(i for i in range(len(lin_edges) - 1) if lin_edges[i] <= 0 < lin_edges[i + 1])
    lo, hi = lin_edges[z], lin_edges[min(z + 2, len(lin_edges) - 1)]
    k = sum(1 for v in values if lo <= v < hi)
    if k == len(values):
        return "all %d of ours fall in two adjacent bins at zero" % k
    return "%d of our %d fall in the two bins at zero" % (k, len(values))


def maglabel(v, tex):
    if v in (-0.01, 0.01):
        return ("$%g$" % v) if tex else "%g" % v
    e = round(math.log10(abs(v)))
    if tex:
        return ("$-10^{%d}$" % e) if v < 0 else "$10^{%d}$" % e
    return ("-10^%d" % e) if v < 0 else "10^%d" % e


def what(runs, total, tex):
    if len(runs) == 1:
        return "%d validated translations of %s" % (total, rep.describe(runs, tex))
    return ("%d functions validated in at least one of %s, each at the median of its "
            "saving over the runs in which it was validated" % (total, rep.describe(runs, tex)))


# ---------------------------------------------------------------- tikz ------

# Substituted with str.replace rather than %-formatting: these templates are
# mostly LaTeX comments and percent signs, and escaping every one of them for
# the formatter is how a template like this earns a silent typo.

TIKZ_HEAD = r"""% Energy savings from Python->Go translation, @DESC@.
% Generated by evaluation/figures/savings_histogram.py - do not edit by hand.
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
\definecolor{papergrey}{HTML}{@PAPER@}
\tikzset{
  awsbar/.style={draw=awsblue, fill=awsblue, fill opacity=0.55, draw opacity=1},
  nonbar/.style={draw=nonawsorange, fill=nonawsorange, fill opacity=0.6,
                 draw opacity=1},
}
\pgfplotsset{
  savingsaxis/.style={
    width=\linewidth, height=5.6cm,
    ylabel={Frequency [\%]},
    ymin=0, axis on top=false,
    tick align=outside, tick pos=left,
    grid=major, grid style={draw=black!10},
    legend cell align=left,
    legend style={draw=black!25, font=\footnotesize, fill=white,
                  fill opacity=0.92, text opacity=1},
  },
}
\begin{groupplot}[group style={group size=1 by 2, vertical sep=1.7cm}]
"""

TIKZ_TAIL = r"""\end{groupplot}
\end{tikzpicture}
"""


def tikz_bars(bars, style):
    return "\n".join(
        r"\filldraw[%s] (axis cs:%s,0) rectangle (axis cs:%s,%s);"
        % (style, fmt(x0), fmt(x1), fmt(h)) for x0, x1, h in bars)


def write_tikz(path, stem, runs, aws, non, a_lin, n_lin, a_mag, n_mag):
    lin_edges = [PAPER_BIN_LO + i * PAPER_BIN_W for i in range(len(PAPER_HEIGHTS) + 1)]
    idx = list(range(len(MAG_EDGES)))

    head = (TIKZ_HEAD.replace("@DESC@", rep.describe(runs)).replace("@STEM@", stem)
            .replace("@AWS@", AWS_COLOR[1:])
            .replace("@NON@", NON_COLOR[1:])
            .replace("@PAPER@", PAPER_COLOR[1:]))
    out = [head]

    # -- panel (a): the paper's own scale ----------------------------------
    paper_path = " -- ".join("(axis cs:%s,%s)" % (fmt(x), fmt(y))
                             for x, y in steps(lin_edges, PAPER_HEIGHTS))
    out.append(r"""% (a) drawn on the axis of Werner et al. Figure 1, so the two are directly
% superimposable. Their bars are the dashed outline; ours are the filled series.
\nextgroupplot[savingsaxis,
  title={(a) On the scale of Werner et al.\ Figure~1},
  xlabel={Consumption Reductions [J], over @N@ invocations},
  xmin=@XMIN@, xmax=@XMAX@, xtick={@XTICK@},
  ymax=108, ytick={0,20,40,60,80,100},
  legend pos=north east,
]
\addlegendimage{area legend, awsbar}\addlegendentry{AWS ($n=@NA@$)}
\addlegendimage{area legend, nonbar}\addlegendentry{non-AWS ($n=@NN@$)}
\addlegendimage{papergrey, dashed, line width=1pt}\addlegendentry{Werner et al.\ ($n=14$)}
\draw[papergrey, dashed, line width=1pt] @PAPERPATH@;
\draw[papergrey, dashed, line width=1pt]
  (axis cs:@PMEAN@,0) -- (axis cs:@PMEAN@,108);
@AWSBARS@
@NONBARS@
\node[anchor=north east, font=\footnotesize, align=right, text=black!65]
  at (rel axis cs:0.985,0.62)
  {Werner et al.: mean $201$\,J\\
   ours: AWS mean $@AMEAN@$\,J, non-AWS mean $@NMEAN@$\,J\\
   @ZERONOTE@};
"""
               .replace("@N@", str(INVOCATIONS))
               .replace("@XMIN@", fmt(PAPER_XLIM[0]))
               .replace("@XMAX@", fmt(PAPER_XLIM[1]))
               .replace("@XTICK@", ",".join(str(t) for t in PAPER_XTICKS))
               .replace("@PAPERPATH@", paper_path)
               .replace("@PMEAN@", fmt(PAPER_MEAN))
               .replace("@AWSBARS@", tikz_bars(rects(lin_edges, a_lin, AWS_FRAC), "awsbar"))
               .replace("@NONBARS@", tikz_bars(rects(lin_edges, n_lin, NON_FRAC), "nonbar"))
               .replace("@NA@", str(len(aws))).replace("@NN@", str(len(non)))
               .replace("@AMEAN@", "%.0f" % mean(aws))
               .replace("@NMEAN@", "%.1f" % mean(non))
               .replace("@ZERONOTE@", zero_bins_note(aws + non)))

    # -- panel (b): signed order of magnitude ------------------------------
    out.append(r"""
% (b) the same @TOTAL@ values, binned by signed order of magnitude. Panel (a) is the
% honest comparison but cannot resolve this corpus; see the note in the
% generator about why the sign is kept rather than plotting |savings|.
\nextgroupplot[savingsaxis,
  title={(b) The same data, binned by signed order of magnitude},
  xlabel={Consumption Reductions [J], over @N@ invocations},
  xmin=0, xmax=@XMAX@,
  xtick={@XTICK@}, xticklabels={@XTICKLABELS@},
  x tick label style={font=\footnotesize},
  ymax=@YMAX@, ytick={0,10,20,30,40,50,60},
  legend pos=north west,
]
\addlegendimage{area legend, awsbar}\addlegendentry{AWS ($n=@NA@$)}
\addlegendimage{area legend, nonbar}\addlegendentry{non-AWS ($n=@NN@$)}
\draw[black!45, densely dotted, line width=1pt]
  (axis cs:@ZERO@,0) -- (axis cs:@ZERO@,@YMAX@);
@AWSBARS@
@NONBARS@
\node[anchor=north, font=\footnotesize, text=black!60]
  at (rel axis cs:0.30,0.99) {Go worse $\leftarrow$};
\node[anchor=north, font=\footnotesize, text=black!60]
  at (rel axis cs:0.70,0.99) {$\rightarrow$ Go better};
"""
               .replace("@TOTAL@", str(len(aws) + len(non)))
               .replace("@N@", str(INVOCATIONS))
               .replace("@XMAX@", str(len(MAG_EDGES) - 1))
               .replace("@XTICK@", ",".join(str(i) for i in idx))
               .replace("@XTICKLABELS@", ",".join(maglabel(v, True) for v in MAG_EDGES))
               .replace("@YMAX@", "%d" % mag_ymax(a_mag, n_mag))
               .replace("@ZERO@", fmt(MAG_EDGES.index(0.01) - 0.5))
               .replace("@AWSBARS@", tikz_bars(rects(idx, a_mag, AWS_FRAC), "awsbar"))
               .replace("@NONBARS@", tikz_bars(rects(idx, n_mag, NON_FRAC), "nonbar"))
               .replace("@NA@", str(len(aws))).replace("@NN@", str(len(non))))

    out.append(TIKZ_TAIL)
    with open(path, "w") as fh:
        fh.write("".join(out))


def mag_ymax(a_mag, n_mag):
    return max(62, int(math.ceil(max(a_mag + n_mag) / 10.0)) * 10 + 2)


CAPTION = r"""% Suggested caption - the numbers are generated, so edit the prose only.
\caption{Energy saved by translating Python to Go, over @N@ invocations, for the
  @WHAT@ (RAPL, steady state). Negative values mean the Python version was more
  energy efficient, as in Werner et al.\ Figure~1, whose bars are reproduced as the
  dashed outline in~(a). The saving is confined to the AWS functions (median
  $@AMED@$\,J, $@AK@/@AN@$ positive, sign test $p=@AP@$); for the non-AWS functions it
  is indistinguishable from zero (median $@NMED@$\,J, $@NK@/@NN@$ positive,
  $p=@NP@$). The two groups differ at Mann--Whitney $p=@PG@$.@PERRUN@}
"""


def texsci(v):
    """Scientific notation as LaTeX math. "%.0e" would typeset 7e-05 as
    "7e - 05" inside $...$, which is not a number anyone reads."""
    if v >= 0.01:
        return "%.2f" % v
    exp = math.floor(math.log10(v))
    mant = v / 10 ** exp
    return r"%.0f \times 10^{%d}" % (mant, exp)


def per_run_note(per_run, tex):
    if len(per_run) < 2:
        return ""
    meds = ["%.1f" % rep.median(a) for _, a, _ in per_run]
    unit = r"\,J" if tex else " J"
    return (" Per run, the AWS median is %s and %s%s."
            % (", ".join(meds[:-1]), meds[-1], unit))


def write_caption(path, runs, aws, non, stats, per_run):
    ka, na_, pa, kn, nn_, pn, pg = stats
    text = (CAPTION
            .replace("@N@", str(INVOCATIONS))
            .replace("@WHAT@", what(runs, len(aws) + len(non), tex=True))
            .replace("@AMED@", "%.1f" % rep.median(aws))
            .replace("@AK@", str(ka)).replace("@AN@", str(na_))
            .replace("@AP@", texsci(pa))
            .replace("@NMED@", "%.3f" % rep.median(non))
            .replace("@NK@", str(kn)).replace("@NN@", str(nn_))
            .replace("@NP@", "%.2f" % pn)
            .replace("@PG@", texsci(pg))
            .replace("@PERRUN@", per_run_note(per_run, tex=True)))
    with open(path, "w") as fh:
        fh.write(text)


# ----------------------------------------------------------------- svg ------

def write_svg(path, runs, aws, non, a_lin, n_lin, a_mag, n_mag, stats, per_run):
    ka, na_, pa, kn, nn_, pn, pg = stats
    lin_edges = [PAPER_BIN_LO + i * PAPER_BIN_W for i in range(len(PAPER_HEIGHTS) + 1)]
    idx = list(range(len(MAG_EDGES)))
    ymax_b = mag_ymax(a_mag, n_mag)

    W, H = 1000, 740
    svg = ['<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" '
           'viewBox="0 0 %d %d" font-family="DejaVu Sans, Helvetica, Arial, sans-serif">'
           % (W, H, W, H),
           '<rect width="%d" height="%d" fill="#ffffff"/>' % (W, H)]

    p1 = Panel(80, 52, 838, 232, PAPER_XLIM, (0, 108))
    p1.frame("(a) On the scale of Werner et al. Figure 1",
             "Consumption Reductions [J], over %d invocations" % INVOCATIONS,
             "Frequency [%]", PAPER_XTICKS, [0, 20, 40, 60, 80, 100])
    p1.outline(steps(lin_edges, PAPER_HEIGHTS), PAPER_COLOR)
    p1.vline(PAPER_MEAN, PAPER_COLOR)
    p1.bars(rects(lin_edges, a_lin, AWS_FRAC), AWS_COLOR, 0.55)
    p1.bars(rects(lin_edges, n_lin, NON_FRAC), NON_COLOR, 0.60)
    p1.legend([("AWS  (n = %d)" % len(aws), AWS_COLOR, False),
               ("non-AWS  (n = %d)" % len(non), NON_COLOR, False),
               ("Werner et al. Fig. 1  (n = 14)", PAPER_COLOR, True)], 0.735, 0.03)
    p1.note([("Werner et al.: mean 201 J", PAPER_COLOR),
             ("ours:  AWS mean %.0f J,  non-AWS mean %.1f J" % (mean(aws), mean(non)),
              "#333"),
             (zero_bins_note(aws + non), "#333")], 0.985, 0.44)
    svg += p1.out

    p2 = Panel(80, 400, 838, 214, (0, len(MAG_EDGES) - 1), (0, ymax_b))
    p2.frame("(b) The same data, binned by signed order of magnitude",
             "Consumption Reductions [J], over %d invocations" % INVOCATIONS,
             "Frequency [%]", idx, list(range(0, ymax_b, 10)),
             xfmt=lambda i: maglabel(MAG_EDGES[int(i)], False))
    p2.vline(MAG_EDGES.index(0.01) - 0.5, "#999", dash="2 3")
    p2.bars(rects(idx, a_mag, AWS_FRAC), AWS_COLOR, 0.55)
    p2.bars(rects(idx, n_mag, NON_FRAC), NON_COLOR, 0.60)
    p2.legend([("AWS  (n = %d)" % len(aws), AWS_COLOR, False),
               ("non-AWS  (n = %d)" % len(non), NON_COLOR, False)], 0.015, 0.03)
    p2.note([("Go worse  <-", "#777")], 0.40, 0.09)
    p2.note([("->  Go better", "#777")], 0.72, 0.09, anchor="start")
    svg += p2.out

    caption = (
        "%s; RAPL-measured steady state, savings over %d invocations.   "
        "AWS: median %.1f J, %d/%d positive (sign test p = %.1g).   "
        "non-AWS: median %.3f J, %d/%d positive (p = %.2f).   "
        "AWS vs non-AWS: Mann-Whitney p = %.1g.%s"
        % (what(runs, len(aws) + len(non), tex=False)[0].upper()
           + what(runs, len(aws) + len(non), tex=False)[1:], INVOCATIONS,
           rep.median(aws), ka, na_, pa, rep.median(non), kn, nn_, pn, pg,
           per_run_note(per_run, tex=False)))
    for i, line in enumerate(wrap(caption, 130)):
        svg.append('<text x="80" y="%d" font-size="11" fill="#444">%s</text>'
                   % (H - 64 + i * 15, esc(line)))
    svg.append("</svg>")

    with open(path, "w") as fh:
        fh.write("\n".join(svg) + "\n")


# ---------------------------------------------------------------- build -----

def main():
    ap = argparse.ArgumentParser()
    rep.add_runs_argument(ap)
    args = ap.parse_args()
    runs = rep.select(args.runs)
    stem = rep.stem("energy-savings", runs)

    aws_rows, non_rows, per_run = load(runs)
    aws = [v for _, v in aws_rows]
    non = [v for _, v in non_rows]

    ka, na_, pa = sign_test(aws)
    kn, nn_, pn = sign_test(non)
    _, _, pg = mann_whitney(aws, non)
    stats = (ka, na_, pa, kn, nn_, pn, pg)

    lin_edges = [PAPER_BIN_LO + i * PAPER_BIN_W for i in range(len(PAPER_HEIGHTS) + 1)]
    a_lin, n_lin = histogram(aws, lin_edges), histogram(non, lin_edges)
    a_mag, n_mag = histogram(aws, MAG_EDGES), histogram(non, MAG_EDGES)

    os.makedirs(HERE, exist_ok=True)
    tex = os.path.join(HERE, stem + ".tex")
    svg = os.path.join(HERE, stem + ".svg")
    cap = os.path.join(HERE, stem + "-caption.tex")
    write_tikz(tex, stem, runs, aws, non, a_lin, n_lin, a_mag, n_mag)
    write_svg(svg, runs, aws, non, a_lin, n_lin, a_mag, n_mag, stats, per_run)
    write_caption(cap, runs, aws, non, stats, per_run)

    print("wrote %s.{tex,svg,-caption.tex}" % os.path.join(HERE, stem))
    print("  runs: %s" % ", ".join(r.id for r in runs))
    print("  AWS      n=%2d  mean %8.2f J  median %8.3f J  positive %2d/%2d  p=%.3g"
          % (len(aws), mean(aws), rep.median(aws), ka, na_, pa))
    print("  non-AWS  n=%2d  mean %8.2f J  median %8.3f J  positive %2d/%2d  p=%.3g"
          % (len(non), mean(non), rep.median(non), kn, nn_, pn))
    print("  Mann-Whitney AWS vs non-AWS: p=%.3g" % pg)
    for run, a, n in per_run:
        print("  run %s: AWS n=%d median %.3f J | non-AWS n=%d median %.4f J"
              % (run.id, len(a), rep.median(a), len(n), rep.median(n)))
    print("  paper-scale bins  AWS %%: %s" % [round(v, 1) for v in a_lin])
    print("  paper-scale bins  non %%: %s" % [round(v, 1) for v in n_lin])
    print("  magnitude bins    AWS %%: %s" % [round(v, 1) for v in a_mag])
    print("  magnitude bins    non %%: %s" % [round(v, 1) for v in n_mag])


if __name__ == "__main__":
    main()
