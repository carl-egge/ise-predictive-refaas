# Prediction dataset and offline gate evaluation (TODO.md section I, items I4–I8)

Offline analysis tooling, deliberately kept out of `go build` — the same separation
`cmd/energy` and `cmd/runtime` keep. **Nothing here runs during a conversion, and nothing
here re-runs one.**

## Why this can be answered without another translation run

Run `20260831-190900` translated all 95 `evaluation_set` functions once. For every function
the repo therefore already holds three measured quantities:

| symbol | meaning | source |
|:---|:---|:---|
| `y_i`  | did the translation pass all its tests | `runs/run-20260831-170746.jsonl` |
| `E_i`  | facility joules that attempt actually cost — **failures included** | `go run ./cmd/energy -json` |
| `ΔE_i` | per-invocation joules the Go version saves over Python | `evaluation/runtime-20260831-190900.json` ([H6], RAPL, bare metal) |

A prediction gate is then a decision vector `d ∈ {translate, skip}^95` and its effect is a
sum over already-measured numbers:

```
spend(d)      = Σ dᵢ · Eᵢ
benefit(d, N) = Σ dᵢ · yᵢ · N · ΔEᵢ
net(d, N)     = benefit(d, N) − spend(d)
```

Skipping a function removes its measured cost and, if it would have succeeded, forfeits its
measured benefit. No simulation, no re-translation. The one quantity this replay cannot
supply is the predictor's own cost, which `predictor_energy.py` measures directly.

## Reproducing

```sh
pip install -r evaluation/prediction/requirements.txt

# 1. ex-ante features + the [I11] near-duplicate group_id
go run ./cmd/pyscan evaluation/evaluation_set/*.zip > evaluation/prediction/features.csv \
    2> evaluation/prediction/features.stderr.txt

# 2. measured per-function translation energy (successes and failures)
go run ./cmd/energy -json -runtime evaluation/runtime-20260831-190900.json \
    runs/run-20260831-170746.jsonl > evaluation/prediction/energy-20260831-190900.json

# 3. the one table every method consumes  [I4]
python3 evaluation/prediction/build_dataset.py \
    --features evaluation/prediction/features.csv \
    --run-log runs/run-20260831-170746.jsonl \
    --energy  evaluation/prediction/energy-20260831-190900.json \
    --runtime evaluation/runtime-20260831-190900.json \
    --run-id  20260831-190900

# 3b. the same table for the external corroboration corpus
go run ./cmd/pyscan evaluation/function_set/*.zip \
    > evaluation/prediction/features-functionset.csv \
    2> evaluation/prediction/features-functionset.stderr.txt
go run ./cmd/energy -json -runtime evaluation/runtime-functionset.json \
    runs/run-20260831-084331.jsonl \
    > evaluation/prediction/energy-functionset-20260831.json
python3 evaluation/prediction/build_dataset.py \
    --features evaluation/prediction/features-functionset.csv \
    --run-log runs/run-20260831-084331.jsonl \
    --energy  evaluation/prediction/energy-functionset-20260831.json \
    --runtime evaluation/runtime-functionset.json \
    --run-id  functionset-20260831

# 3c. confirm the two corpora share no near-duplicate group before using 3b as "external"
go run ./cmd/pyscan evaluation/evaluation_set/*.zip evaluation/function_set/*.zip \
    2>/dev/null | cut -d, -f1,5   # no group_id may contain both an f* and a pf*

# 4. baselines, models, energy sweep, breakdowns, cost-sensitive variants
#    [I5]/[I6]/[I7]/[I9] -- ~20 min, dominated by the random forests
python3 evaluation/prediction/evaluate.py \
    --dataset evaluation/prediction/dataset-20260831-190900.csv \
    --horizon 1e6 --permutations 200 --breakdown \
    --external evaluation/prediction/dataset-functionset-20260831.csv \
    --json-out evaluation/prediction/results-20260831-190900.json \
    | tee evaluation/prediction/results-20260831-190900.txt

# 4a. the second horizon, for the bounded-useful-range finding in [I9]
python3 evaluation/prediction/evaluate.py \
    --dataset evaluation/prediction/dataset-20260831-190900.csv --horizon 1e8

# 4b. export the shipped model for internal/predictor  [I10]
python3 evaluation/prediction/evaluate.py \
    --dataset evaluation/prediction/dataset-20260831-190900.csv \
    --horizon 1e6 \
    --export-model evaluation/prediction/model-20260831-190900.json

# 4c. refresh the Go-side parity fixture whenever the model is re-exported
cp evaluation/prediction/model-20260831-190900.json internal/predictor/testdata/model.json
python3 evaluation/prediction/export_parity.py \
    --dataset evaluation/prediction/dataset-20260831-190900.csv \
    --model   evaluation/prediction/model-20260831-190900.json
go test ./internal/predictor/...   # asserts the Go reader matches scikit-learn to 1e-9

# 5. the predictor's own energy, in energy.config.json's units  [I8]
go build -o /tmp/pyscan ./cmd/pyscan
python3 evaluation/prediction/predictor_energy.py --pyscan-bin /tmp/pyscan \
    --artifacts 'evaluation/evaluation_set/*.zip' \
    --energy-json evaluation/prediction/energy-20260831-190900.json
```

Steps 1, 2 and 5 need the Go toolchain and a `python3` on PATH for the embedded scanner;
steps 3 and 4 need only the two Python packages.

## Protocol notes that are load-bearing

- **Grouping is mandatory.** Splits use `StratifiedGroupKFold` on `group_id`, not
  `function_id` and not `repo_uri` — [I11] measured 16 functions in 7 near-duplicate groups,
  four of which cross repository boundaries. Effective N is **86, not 95**.
- **Every fitted quantity lives inside the training fold**, including the decision threshold,
  which is chosen by an inner 5-fold CV on the training fold only. Choosing an operating
  point on the test fold is the standard way a study like this invalidates itself quietly.
- **Two operating points are reported** because they optimise different things: `balanced`
  maximises balanced accuracy against the label the model was trained on, and `energy` maximises
  net joules at the stated horizon (always against the real outcomes and measured energies,
  whatever the model was trained on). They differ a lot.
- **Three training targets are reported per model** ([I9]). The plain rows train on
  `all_tests_passed`; `[cost-weighted]` keeps that label but weights each example by the regret
  of getting it wrong, `|v_i|` where `v_i = y_i·N·ΔE_i − E_i`; `[energy-target]` relabels to
  `z_i = 1{v_i > 0}` — the decision that would have been right — and weights the same way. Every
  term of `v` is measured for every row, so no value is imputed.
- **Read AUC(tgt), not AUC(y), for the energy-target rows.** They were trained to predict
  worthwhileness, so scoring them against success measures a question they were told to ignore.
  The table prints both, with that warning inline.
- **`--horizon N` is a reported parameter, not a tuned one.** It defines the `[energy-target]`
  label and both energy operating points, so results travel as a curve over `N`. Reporting one
  horizon hides the range finding: below ~10⁵ nothing repays, above ~10⁷–10⁸ everything does, and
  a gate only earns its keep in between.
- **`function_set` is corroboration, never the headline.** n = 14, its expectations were never
  executed against the Python originals, and its run used a slightly different
  `scripts/benchmark.json` (repair-stage `temperature`/`top_p`) from a dirty tree. It is a
  cross-corpus *and* cross-configuration test.
- **Labels are single-run and their stability is unmeasured** ([I1]). `f50`/`f59` are
  structurally identical source with opposite labels, so the ceiling is demonstrably below
  100% — see [I11]'s closure note for the caveat that their fixture sets also differ.
