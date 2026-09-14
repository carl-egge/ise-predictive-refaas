# Prediction dataset and offline gate evaluation (TODO.md section I, items I4–I8)

Offline analysis tooling, deliberately kept out of `go build` — the same separation
`cmd/energy` and `cmd/runtime` keep. **Nothing here runs during a conversion, and nothing
here re-runs one.**

## Why this can be answered without another translation run

The replicate series — runs `20260904-190539`, `20260911-165103` and `20260912-172904` —
translated all 95 `evaluation_set` functions three times on one frozen configuration
(`scripts/benchmark.json` sha `f9e30f4b`, pipeline code identical apart from a comment). For every
(function *i*, run *k*) the repo therefore already holds three measured quantities:

| symbol | meaning | source |
|:---|:---|:---|
| `y_ki`  | did that translation pass all its tests | `runs/run-<ts>.jsonl` |
| `E_ki`  | facility joules that attempt actually cost — **failures included** | `go run ./cmd/energy -json` → `energy-<run-id>.json` |
| `ΔE_ki` | per-invocation joules the Go version saves over Python | `evaluation/runtime-<run-id>.json` ([H6], RAPL, bare metal) |

`ΔE` exists only for translations that **passed their fixtures**: the runtime files are built with
`cmd/runtime -runlog`, because `scripts/run-benchmark.sh` archives the package of a failed job too,
and timing a translation that computes the wrong answer measures how fast the wrong answer is
produced.

A prediction gate is then a decision vector `d ∈ {translate, skip}^95` — the features are
deterministic, so one decision per function serves every run — and its effect in run *k* is a sum
over already-measured numbers:

```
spend_k(d)      = Σ dᵢ · E_ki
benefit_k(d, N) = Σ dᵢ · y_ki · N · ΔE_ki
net_k(d, N)     = benefit_k(d, N) − spend_k(d)
```

Skipping a function removes its measured cost and, if it would have succeeded in that run, forfeits
its measured benefit. No simulation, no re-translation. Every reported figure is computed per run
and averaged over the runs and the CV repeats. The one quantity this replay cannot supply is the
predictor's own cost, which `predictor_energy.py` measures directly.

**Why three runs rather than one.** A quarter of the corpus (23 of 95 functions) changes outcome
between runs of one configuration; the runs agree pairwise on 82–85% of labels (Fleiss κ 0.68). A
model trained on one run learns that run's draw. `evaluate.py` therefore takes one `--dataset` per
run and trains on every (function, run) row: a function that passed twice and failed once is seen
as exactly that.

The earlier single-run analysis on `20260831-190900` — a *different* pipeline version (`cleaner` →
`coder`, before [C8a]/[C11a]/[C13]/[C14]/[C15]) — is kept as provenance: `dataset-20260831-190900.csv`,
`results-20260831-190900.*` and `model-20260831-190900.json`. With a single `--dataset`,
`evaluate.py` reproduces those results exactly (checked against the committed single-run
implementation: identical decisions and probabilities to 2 × 10⁻¹⁶).

## Reproducing

```sh
pip install -r evaluation/prediction/requirements.txt
P=evaluation/prediction

# 1. ex-ante features + the [I11] near-duplicate group_id. Deterministic: the vector every
#    replicate run log recorded is identical to this table, value for value.
go run ./cmd/pyscan evaluation/evaluation_set/*.zip > $P/features.csv 2> $P/features.stderr.txt

# 2. measured per-attempt translation energy, one report per run (successes and failures)
go run ./cmd/energy -json -runtime evaluation/runtime-20260904-190539.json \
    runs/run-20260904-170428.jsonl > $P/energy-20260904-190539.json
go run ./cmd/energy -json -runtime evaluation/runtime-20260911-165103.json \
    runs/run-20260911-144954.jsonl > $P/energy-20260911-165103.json
go run ./cmd/energy -json -runtime evaluation/runtime-20260912-172904.json \
    runs/run-20260912-152820.jsonl > $P/energy-20260912-172904.json

# 3. one table per run  [I4]
python3 $P/build_dataset.py --features $P/features.csv --run-id 20260904-190539 \
    --run-log runs/run-20260904-170428.jsonl --energy $P/energy-20260904-190539.json \
    --runtime evaluation/runtime-20260904-190539.json
python3 $P/build_dataset.py --features $P/features.csv --run-id 20260911-165103 \
    --run-log runs/run-20260911-144954.jsonl --energy $P/energy-20260911-165103.json \
    --runtime evaluation/runtime-20260911-165103.json
python3 $P/build_dataset.py --features $P/features.csv --run-id 20260912-172904 \
    --run-log runs/run-20260912-152820.jsonl --energy $P/energy-20260912-172904.json \
    --runtime evaluation/runtime-20260912-172904.json

# 3b. the external corroboration corpus, run on the same frozen configuration
go run ./cmd/pyscan evaluation/function_set/*.zip \
    > $P/features-functionset.csv 2> $P/features-functionset.stderr.txt
go run ./cmd/energy -json -runtime evaluation/runtime-functionset-20260913-120600.json \
    runs/run-20260913-100522.jsonl > $P/energy-functionset-20260913-120600.json
python3 $P/build_dataset.py --features $P/features-functionset.csv \
    --run-id functionset-20260913-120600 --run-log runs/run-20260913-100522.jsonl \
    --energy $P/energy-functionset-20260913-120600.json \
    --runtime evaluation/runtime-functionset-20260913-120600.json

# 3c. confirm the two corpora share no near-duplicate group before using 3b as "external"
go run ./cmd/pyscan evaluation/evaluation_set/*.zip evaluation/function_set/*.zip \
    2>/dev/null | cut -d, -f1,5   # no group_id may contain both an f* and a pf*

# 4. baselines, models, energy sweep, breakdowns, cost-sensitive variants, model export
#    [I5]/[I6]/[I7]/[I9]/[I10] -- about an hour, dominated by the random forests
D="--dataset $P/dataset-20260904-190539.csv --dataset $P/dataset-20260911-165103.csv \
   --dataset $P/dataset-20260912-172904.csv"
python3 $P/evaluate.py $D --horizon 1e6 --permutations 200 --breakdown \
    --external $P/dataset-functionset-20260913-120600.csv \
    --json-out $P/results-replicates-f9e30f4b.json \
    --export-model $P/model-replicates-f9e30f4b.json \
    > $P/results-replicates-f9e30f4b.txt

# 4a. the second horizon, for the bounded-useful-range finding in [I9]
python3 $P/evaluate.py $D --horizon 1e8 --json-out $P/results-replicates-f9e30f4b-N1e8.json \
    > $P/results-replicates-f9e30f4b-N1e8.txt

# 4b. refresh the Go-side parity fixture whenever the model is re-exported
cp $P/model-replicates-f9e30f4b.json internal/predictor/testdata/model.json
python3 $P/export_parity.py $D --model $P/model-replicates-f9e30f4b.json
go test ./internal/predictor/...   # asserts the Go reader matches scikit-learn to 1e-9

# 5. the predictor's own energy, in energy.config.json's units  [I8]
go build -o /tmp/pyscan ./cmd/pyscan
python3 $P/predictor_energy.py --pyscan-bin /tmp/pyscan \
    --artifacts 'evaluation/evaluation_set/*.zip' \
    --energy-json $P/energy-20260904-190539.json --energy-json $P/energy-20260911-165103.json \
    --energy-json $P/energy-20260912-172904.json

# 6. the figures (evaluation/figures, stdlib only; --runs defaults to the replicate series)
python3 evaluation/figures/pipeline_funnel.py
python3 evaluation/figures/savings_histogram.py
python3 evaluation/figures/nstar_distribution.py
python3 evaluation/figures/amortisation_spread.py   # reads step 4's out-of-fold gate decisions
```

Steps 1, 2 and 5 need the Go toolchain and a `python3` on PATH for the embedded scanner;
steps 3, 4 and 6 need only Python (scikit-learn for 4).

## Protocol notes that are load-bearing

- **Grouping is mandatory, and it is over functions.** Splits use `StratifiedGroupKFold` on
  `group_id`, not `function_id` and not `repo_uri` — [I11] measured 16 functions in 7 near-duplicate
  groups, four of which cross repository boundaries. Effective N is **86, not 95**. Folds are drawn
  over functions and every replicate row of a function follows it, so no function — and no
  near-duplicate of it — is ever on both sides of a split.
- **Replicates are stacked, not averaged.** Each function contributes one training row per run with
  that run's label; folds are stratified on whether a function passed in at least half the runs. A
  model sees label noise as it is instead of a majority vote that hides it. Reported AUC is the mean
  of the per-run AUCs; `evaluate.py` also prints the AUC against the majority label and the
  **replicate ceiling** — the AUC of predicting each run's labels from the mean of the other runs,
  roughly the best any per-function score can do (0.88–0.91 on this series).
- **Every fitted quantity lives inside the training fold**, including the decision threshold,
  which is chosen by an inner 5-fold CV on the training fold only, against every run's outcomes at
  once. Choosing an operating point on the test fold is the standard way a study like this
  invalidates itself quietly.
- **Two operating points are reported** because they optimise different things: `balanced`
  maximises balanced accuracy against the label the model was trained on, and `energy` maximises net
  joules at the stated horizon (always against the real outcomes and measured energies, whatever the
  model was trained on). They differ a lot.
- **Three training targets are reported per model** ([I9]). The plain rows train on
  `all_tests_passed`; `[cost-weighted]` keeps that label but weights each example by the regret of
  getting it wrong, `|v_ki|` where `v_ki = y_ki·N·ΔE_ki − E_ki`; `[energy-target]` relabels to
  `z_ki = 1{v_ki > 0}` — the decision that would have been right in that run — and weights the same
  way. Every term of `v` is measured for every row, so no value is imputed.
- **Read AUC(tgt), not AUC(y), for the energy-target rows.** They were trained to predict
  worthwhileness, so scoring them against success measures a question they were told to ignore.
  The table prints both, with that warning inline.
- **`--horizon N` is a reported parameter, not a tuned one.** It defines the `[energy-target]`
  label and both energy operating points, so results travel as a curve over `N`.
- **Out-of-fold decisions are what the figures use.** `--json-out` records, per function, the mean
  held-out probability and how often its held-out decision was "translate" over the repeats.
  `evaluation/figures/amortisation_spread.py` draws the gate from those; a gate evaluated on the runs
  it was fitted on may use nothing else.
- **`function_set` is corroboration, never the headline.** n = 14, its expectations were never
  executed against the Python originals, and it contains no AWS function. Its run
  (`functionset-20260913-120600`) used the same frozen configuration from a clean tree, so it is a
  cross-corpus test only, no longer a cross-configuration one.
- **`f50`/`f59` are structurally identical source** and disagree in 3 of the 4 runs recorded so far,
  so the ceiling on any deterministic ex-ante predictor is demonstrably below 100% — see [I11]'s
  closure note for the caveat that their fixture sets also differ.
