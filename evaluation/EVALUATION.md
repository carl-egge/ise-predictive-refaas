# Energy Modelling Approach

Reference document for the energy-consumption estimation of the Python→Go
translation pipeline. Records the decisions, formulas, constants and open
tasks agreed for the thesis.

**Status:** GWDG support replied on **2026-08-22**. Hardware, precision and
carbon accounting are now provider-stated; node power and PUE went unanswered
and concurrency was **declined** (they hold the data but may not release it),
so those three stay assumed and are swept instead. See
[The GWDG reply](#the-gwdg-reply-2026-08-22) and
[Open Questions](#open-questions).

**Results of record (2026-09-14):** three `evaluation_set` runs on one frozen configuration
plus one `function_set` run on it — see
[Replicate series](#replicate-series-results-of-record-2026-09-14). Single-run figures elsewhere
in this document are one column of those tables.

---

## 1. Goal

Estimate the energy consumed by the LLM-based translation pipeline and compare
it against the energy saved by running the translated Go function instead of
the original Python function.

Primary result — the break-even invocation count, computed per function:

```
N* = E_translation / (E_python_per_invocation − E_go_per_invocation)
```

Interpretation: the translation pays for itself after `N*` invocations of the
function.

**Why this framing matters.** `N*` scales linearly with the energy estimate.
An uncertainty of a factor of 3 in `E_translation` produces an uncertainty of a
factor of 3 in `N*`. If `N*` lands far below realistic serverless invocation
counts, the conclusion holds across the entire plausible parameter range. This
must be stated explicitly in the thesis — it converts the weakest part of the
method (absolute accuracy) into a demonstrated non-issue.

---

## 2. Setup

| Item | Value |
|---|---|
| LLM provider | GWDG SAIA / Chat AI (`https://chat-ai.academiccloud.de/v1`) |
| Model | `devstral-2-123b-instruct-2512` (dense, 123B parameters) |
| Serving stack | vLLM 0.22.x |
| Serving hardware | 2 × NVIDIA H200 (141 GB HBM3e), **GWDG-stated** |
| Precision | **FP8**, GWDG-stated (FP16 is their house default; Devstral is one of the exceptions) |
| Evaluation set | **95** curated Python serverless functions (`evaluation_set`, 392 tests) + the legacy **14** paper functions (`function_set`, 41 tests) — see [EVALUATION_DATASET.md](EVALUATION_DATASET.md); report the two separately, `function_set` expectations were never executed |
| Pipeline | Go, multiple LLM calls per translation |
| Signals available | `prompt_tokens`, `completion_tokens` (read directly from the OpenAI-compatible `usage` object by `internal/llmconnector/chatai.go` — no `total − prompt` subtraction needed) |

### Pipeline facts this document must stay aligned with

Verified against the implementation (2026-07-05). These constrain how the
energy model is applied:

- A translation is a **task graph**, not a fixed call sequence: the evaluation
  configuration (`scripts/benchmark.json`, sha `f9e30f4b` since 2026-09-04) runs
  `pyScan` → `summary` (optional) → `coder2` → `goBuilder` ⇄ `gollmRecovery` (fixer) →
  `testRouter` ⇄ `testRecovery` (realign) → `testRecoveryBuild`; run `20260831-190900`
  and earlier opened with `cleaner` → `coder` instead. Retries and recovery hops mean the number
  of LLM calls per function is **variable and outcome-dependent** — which is
  exactly why per-stage attribution matters more than a per-call average.
- Every stage's LLM parameters (`model_name`, `temperature`, …) can be
  overridden **per task**, so a run is not necessarily single-model. Energy
  coefficients are per-model; see [H3] in `TODO.md`.
- The pipeline also performs **local compute** — `go mod init/tidy`,
  `go build`, one `./fn` execution per fixture per test round, optionally
  Floci containers — which is RAPL-measured per job and per stage since
  2026-09-04. See [Local compute](#local-compute-non-llm-pipeline-energy--measured-since-2026-09-04).
- Non-LLM energy aside, translation cost is dominated by retries: the same
  stage can execute up to `maxRetryCount` times, and each execution is a full
  prompt round trip.

### Why Devstral, despite no published energy measurement

Devstral 2 is Mistral's agentic coding model and is the appropriate choice for
a code-translation pipeline on task-suitability grounds. It does not appear on
the ML.ENERGY leaderboard, but the energy model does not depend on that
leaderboard — the coefficients are derived from first principles (parameter
count, weight bytes, memory bandwidth, node power). Selecting a worse-fitting
model purely to obtain a lookup value would degrade the primary research
contribution to marginally improve a secondary estimate that carries a
factor-2–3 uncertainty band regardless.

Validation is handled instead by validating the *method* rather than borrowing
a constant — see [Validation](#7-validation).

---

## 3. Energy model

### Formula

```
E_call     [J] = prompt_tokens × e_in + output_tokens × e_out
E_pipeline [J] = Σ E_call          (over all calls, including retries)
E_facility [J] = E_pipeline × PUE
CO2e       [g] = E_facility / 3.6e6 × I
```

`I` is reported twice: once at the location-based German grid intensity and
once at GWDG’s market-based intensity of zero (they state carbon-neutral
operation). See [The GWDG reply](#the-gwdg-reply-2026-08-22).

Input and output tokens are weighted separately because they are physically
different operations:

- **Prefill (input):** the whole prompt is processed in one parallel pass.
  Compute-bound, saturates the hardware, largely independent of how many other
  users share the server.
- **Decode (output):** the model produces one token at a time, and every step
  requires streaming the entire set of model weights out of GPU memory.
  Memory-bandwidth-bound and strongly dependent on server concurrency.

In a translation pipeline prompts are long (source file + instructions +
examples) and outputs moderate, so a single blended per-token rate would
distort the result materially.

This two-phase split is not our construction: it is the standard roofline
analysis of transformer inference (Pope et al. 2023, source 9), which is what
justifies deriving `e_in` from FLOPs and `e_out` from memory bandwidth rather
than from one blended rate. The `/ B` term in `e_out` follows from the same
analysis and from continuous batching (source 10) — one sweep of the weights
through memory serves every request in the batch.

### Coefficient derivation

**`e_in` — energy per input token**

```
T_prefill = (n_gpu × peak_flops × mfu) / (2 × n_params)
e_in      = P_node / T_prefill
```

With 2 × H200 (1,979 TFLOP/s FP8 dense each), MFU 0.40, 123e9 params:

```
T_prefill ≈ 1583e12 / 246e9 ≈ 6,440 tokens/s
e_in      ≈ 1700 W / 6440   ≈ 0.26 J per input token
```

The FP8 peak is used because the model is *served* in FP8. One assumption
survives inside GWDG's confirmation: whether the deployment quantizes weights
and activations (W8A8, so the matmuls really run on FP8 tensor cores) or only
the weights (W8A16, so prefill still runs at the 989 TFLOP/s BF16 peak). The
latter would halve `T_prefill` and double `e_in`, leaving `e_out` untouched —
which is why `peak_flops_per_gpu` is now one of the swept parameters in
section 8 rather than a silent choice.

**`e_out` — energy per output token**

```
t_step = weight_bytes / (n_gpu × hbm_bw × bw_eff)
e_out  = P_node × t_step / B
```

With 123 GB weights (123B × 1 byte, FP8), 2 × 4.8 TB/s at 75% efficiency:

```
t_step ≈ 123e9 / 7.2e12 ≈ 17.1 ms
```

| Concurrency `B` | `e_out` (J/token) |
|---|---|
| 8 | 3.63 |
| 16 | 1.82 |
| **32 (central)** | **0.91** |
| 64 | 0.45 |
| 128 | 0.23 |

`B` remains the single largest unknown, and after the GWDG reply it is a
**permanent** one rather than a pending one: they hold the throughput and
concurrency data but are not permitted to release it. The sensitivity sweep
over `B` is therefore not a placeholder awaiting an answer — it *is* the
answer, and the thesis should present it that way.

Note the FP8 confirmation cuts the weight traffic per decode step in half
relative to the BF16 assumption, and moving from 4 × H100 PCIe (2.0 TB/s) to
2 × H200 (4.8 TB/s) leaves aggregate bandwidth nearly unchanged (8.0 → 9.6
TB/s) while halving the number of GPUs drawing power. Both effects push the
same way, which is why the estimate fell by roughly a factor of two.

### Worked example

5 calls averaging 6,000 prompt / 1,500 output tokens:

```
prefill: 30,000 × 0.264 =  7.9 kJ
decode:   7,500 × 0.908 =  6.8 kJ
total:                     14.7 kJ × PUE 1.05 ≈ 15.5 kJ ≈ 4.3 Wh
```

Range over `B` ∈ [8, 128]: roughly **3–10 Wh per translation**.

> Before the GWDG reply the same example gave 33 kJ ≈ 9.3 Wh over a 4–20 Wh
> range, on 4 × H100 PCIe at BF16. The formulas did not change — only their
> inputs did. `cmd/energy`'s `TestSupersededCoefficientsStillDerive` pins the
> old figures against the current code so this remains a demonstrable claim
> rather than an assertion.

---

## 4. Constants

Values marked **GWDG** were stated by GWDG support on 2026-08-22 and carry into
the thesis constants table as provider-supplied; everything else remains an
assumption and must be labelled as one.

> **These values live in [`energy.config.json`](energy.config.json)**, which
> `go run ./cmd/energy` reads. The tool has no compiled-in fallback, so that
> file is the single source of truth: update it when GWDG replies, and every
> figure recomputes. The table below documents the same values with their
> provenance for the write-up.

| Parameter | Symbol | Value | Source |
|---|---|---|---|
| GPUs serving Devstral | `n_gpu` | 2 | **GWDG (2026-08-22)** |
| GPU model | — | H200 141 GB HBM3e | **GWDG (2026-08-22)**; SXM/NVL variant not stated |
| GPU TDP | — | 700 W (SXM; NVL would be 600 W) | NVIDIA H200 datasheet |
| Node power under load | `P_node` | 1700 W | **assumption** — 2 × 700 W GPU + 150 W/GPU host share; not answered |
| Peak FP8 throughput | `peak_flops` | 1,979 TFLOP/s per GPU (dense) | NVIDIA H200 datasheet |
| Model FLOP utilization | `mfu` | 0.40 | conventional inference-serving assumption |
| Parameters | `n_params` | 123e9 | model card |
| Bytes per parameter | — | 1 (FP8) | **GWDG (2026-08-22)** |
| HBM bandwidth | `hbm_bw` | 4.8e12 B/s per GPU | NVIDIA H200 datasheet |
| Achieved bandwidth fraction | `bw_eff` | 0.75 | standard assumption |
| Concurrency | `B` | 32 (range 8–128) | **assumption — GWDG declined to release; largest uncertainty** |
| PUE | `PUE` | 1.05 (range 1.03–1.2) | GWDG press release 4/2021 (Emmy); not answered |
| CO₂ intensity, location-based | `I_grid` | 363 gCO₂e/kWh | Umweltbundesamt, German average |
| CO₂ intensity, market-based | `I_market` | 0 gCO₂e/kWh | **GWDG (2026-08-22)** — carbon-neutral operation |

The two assumptions that dominated the result were precision and concurrency.
Precision is now settled (FP8, confirmed), which leaves **concurrency `B`** as
the single dominant uncertainty, with **node power** a distant second.

### The GWDG reply (2026-08-22)

Recorded verbatim in structure because the thesis must distinguish *answered*
from *refused* from *unanswered* — they warrant different treatment.

| Question | Outcome | Effect on the model |
|---|---|---|
| GPU type and count | **Answered.** KISSKI is 4 × H100 PCIe per node — but Devstral specifically "läuft auf 2× H200" | `n_gpu` 4 → 2, H100 PCIe → H200 |
| Precision | **Answered.** FP16 for most models, FP8 for some "wie zum Beispiel bei Devstral" | bytes/param 2 → 1; `e_out` halved |
| Node power | **Not answered** | stays assumed; now swept |
| Throughput / concurrency | **Declined** — the data exists but may not be released | `B` stays assumed *permanently*; the sweep is the result |
| PUE | **Not answered** | 1.03–1.05 from the 2021 Emmy press release stands, still Emmy-specific |
| Electricity | **Answered.** "wir sind tatsächlich CO2-neutral" | market-based intensity 0; location-based retained alongside |

**Reading the hardware answer.** The reply is internally split: the generic
question ("4 × H100 PCIe on KISSKI, correct?") was confirmed with "Richtig",
while the precision answer names our model specifically as running on 2 × H200.
The model-specific statement governs — we are costing Devstral, not the
platform average — and the two are consistent rather than contradictory: the
H100 figure describes the KISSKI inference platform in general, the H200 pair
describes this model's deployment. A sanity check supports the specific
reading: 123B parameters at FP8 is 123 GB of weights, which fits 2 × 141 GB
with ~159 GB left for KV cache; at BF16 it would be 246 GB, leaving 36 GB and
a context far short of the advertised 256K. The stated precision and the
stated GPU count only fit each other.

**Carbon accounting.** "CO₂-neutral" is a statement about procurement, not
about physics: the electricity was still drawn. Following the GHG Protocol's
Scope 2 dual-reporting rule, `cmd/energy` now reports **both** — a
location-based figure at the German grid average (what the draw would emit on
the physical grid) and a market-based figure at GWDG's contractual intensity
of zero. Report both in the thesis. Quoting only the market figure would make
the pipeline look free; quoting only the location figure would misstate what
GWDG reports. Note also that carbon-neutrality typically covers operational
emissions only — embodied GPU manufacturing is excluded from both figures, and
that belongs in section 9's lower-bound caveat.

---

## 5. Instrumentation

> **Revised 2026-07-05 against the implementation.** The originally proposed
> per-call `CallRecord` struct is **not needed** — the pipeline already
> records token spend per stage. What is missing is *identity* and
> *persistence*, not granularity. See the gap list below and section H of
> `TODO.md`.

### Why per-stage aggregates are sufficient

The energy model is **linear in token counts**:

```
E_pipeline = Σ_calls (p_i × e_in + o_i × e_out)
           = (Σ_calls p_i) × e_in + (Σ_calls o_i) × e_out
```

So summing tokens per stage and applying the coefficients afterwards yields
*exactly* the same pipeline total and the same per-stage breakdown as
per-call records would. Per-call granularity would add only three things:
per-call token *distributions*, per-call timestamps for cross-referencing
server load, and per-call model attribution in mixed-model runs. None is
required for `N*`, the per-stage breakdown, or the retry-share finding.

### What the implementation already provides

`internal/domain/types.go` — `Metrics`, attached to every `ConversionRequest`:

| Field | Meaning |
|---|---|
| `conversion_prompt_token_count` / `conversion_eval_token_count` | total input / output tokens for the whole translation |
| `per_task[<task id>]` | per-stage `executions`, `failures`, `duration`, `llm_calls`, `prompt_tokens`, `eval_tokens` |
| `build_error`, `test_error`, `test_cases`, `issues` | outcome and failure detail |
| `StartTime` / `EndTime` / `TotalTime` | wall clock per translation |

Task ids are the pipeline's own (`root`, `convert`, `builder`,
`gollmRecovery`, `goTester`, `testRecovery`, …), so "the repair loop accounts
for X% of pipeline energy" is answerable directly by summing the recovery
tasks' token columns.

Requirements from the original draft, checked against the code:

- **Log failed and retried calls** — ✅ satisfied. `RecordLLMCall` runs
  *before* the error check in `LLMConverter.Apply`, so a truncated response
  (which did consume tokens) is counted; retries add to the same task's
  counters, with `executions` vs. `failures` recording how many there were.
- **Log the stage** — ✅ satisfied, via `per_task` keyed by task id.
- **Store `usage` verbatim** — ~ partially: `chatlogs/<req-id>_<task>_<model>_<ts>.log`
  holds the verbatim prompt and response of every call, but not the raw
  `usage` object. Low value given the parsed counts are already stored.
- **Timestamps in UTC** — per *translation* only, not per call. Only needed
  if GWDG supplies server-side load data; revisit if they do.
- **`prompt_tokens_details.cached_tokens`** — ✅ **question answered**: API
  experiments against SAIA showed `prompt_tokens_details` is always `null`,
  so no cache/uncached split is available and cost tracking uses the three
  top-level counts. The estimate is therefore conservative (an overestimate)
  in the acceptable direction, and prefix caching becomes a threat-to-validity
  entry rather than a modelling term.

### Remaining gaps (tracked in `TODO.md` section H)

1. **Function identity and dataset metadata are not recorded** — metrics are
   keyed by job UUID only. Both available signals are discarded: the uploaded
   filename after its `.zip` check, and the artifact's `meta.json`, which
   matches none of the zip reader's branches and is silently ignored. So a
   metrics dump cannot be attributed to f1…f95, and the `bucket`/`cc`/`aws`
   fields needed for the per-bucket and AWS-vs-non-AWS reporting never reach
   the results. Blocks per-function `N*` and every grouped result. → [H1]

   For the benchmark run `meta.json` is **required**: an upload without one is
   rejected before any LLM call rather than producing an unattributable result
   hours later. It stays optional outside benchmark mode so ad-hoc uploads
   continue to work. → [H1] + [C6]
2. **Metrics are not persisted** — they live in an in-memory map that a crash,
   a restart, or a `/reconfigure` erases; `scripts/store-metrics.sh` is a
   manual `curl` of the whole map after the fact. A 95-function batch is hours
   of LLM time and real energy spend that any of those events destroys. → [H2]
3. **The model is not recorded per stage** — energy coefficients are
   per-model, and pipelines may set `model_name` per task. → [H3]

### Local compute (non-LLM pipeline energy) — **measured since 2026-09-04**

`E_translation` originally counted LLM inference only, while the pipeline also
ran, per attempt: `go mod init`/`go mod tidy` (network + CPU), `go build`, and
one `./fn` process per fixture per test round — plus Floci emulator containers
on the integration route. The visible symptom was that `cmd/energy`’s
per-stage table printed **0.0 J** against `goBuilder`, `goTester` and `pyScan`:
the stages that do all the local work were costed at exactly nothing.

The choice this section demanded — *measure* or *declare as an excluded,
bounded term* — is resolved as **measure**, and directly rather than by
scaling a representative round:

- `internal/hostenergy` reads the RAPL package counters under
  `/sys/class/powercap`. The service samples them either side of every job and
  the pipeline either side of every task attempt, so each run-log record
  carries `host_joules` for the job plus a per-stage breakdown. It is a counter
  difference: **no assumed wattage enters the figure at all.**
- `E_translation = E_inference × PUE + E_host`. PUE is deliberately *not*
  applied to the host term: that machine sits on a desk, not in GWDG’s hall,
  and grossing it up by a datacentre’s cooling overhead would be an invented
  number.
- Where no counter exists (WSL2, macOS, most containers) or the run log
  predates this, `cmd/energy` falls back to
  `host.fallback_power_watts × duration` and tags every resulting figure
  **ESTIMATED**. Both fallback constants default to `0`, so the tool reports
  host energy as `NOT COUNTED` rather than invent it — the same rule §6’s
  meters enforce.

**Gross and marginal are reported side by side.** About **90% of a job’s
wall clock is spent waiting on the remote LLM API**, with the pipeline host
close to idle — measured over run 20260831-190900: 24,101 s of 26,134 s inside
LLM stages (92%), and 89%, 94% and 91% on the three replicate runs. So:

- **gross** = every joule the host drew while the conversion occupied it;
- **marginal** = gross − `idle_watts × duration`, the part the conversion
  *caused*. The service measures its own idle baseline once at startup, before
  taking its first job, and records it on every job.

The marginal figure is the one consistent with the marginal-cost framing this
document already adopts for the inference side; the gross figure is the honest
answer to “what did this machine burn while producing that translation”. The
report prints both, and the write-up must state which it quotes.

**What the measurement found.** Before metering, run 20260831-190900 was re-costed
with the fallback at a nominal 25 W / 11 W idle, which moved mean `E_translation`
from 11.5 kJ to 16.1 kJ per completed translation (+40%). The measured runs do not
bear that out: the host package idles at **0.37–0.40 W** and averages **0.9–1.2 W**
over a run, so the host term is **1.4–2.4% of run spend** (20–27 kJ gross per
95-function run) and gross is only **1.5–1.7×** marginal. The nominal wattage
overstated the host by roughly twentyfold; the term is small but not zero, and it
had to be measured to know that. Caveat: RAPL package counters cover the processor
package, not memory, storage or peripherals, so the figure is a lower bound on what
the machine drew. → [H5], closed.

---

## 6. Go vs. Python runtime measurement

The two sides of the comparison must be methodologically symmetric.

**Reuse the pipeline's own fixtures.** Each uploaded function ships its test
cases in the canonical schema (`internal/fixture.TestCase`: `payload`,
`expectedOutput`, `outputMode`, `env`), and the translated package is executed
through a fixed harness that reads one JSON event on stdin and writes
`{"response": …}` / `{"error": …}` on stdout (`internal/builder/test_handler.txt`).
Driving both the Python original and the Go translation with the *same*
`payload` values through the *same* envelope is what makes the two sides
comparable — and it means the measurement harness needs no new inputs, only a
Python-side equivalent of the existing Go harness.

- Measure both versions on the **same machine**, same workload, same number of
  invocations.
- Use direct CPU energy counters rather than modelling:
  `perf stat -e power/energy-pkg/,power/energy-ram/` on Linux (Intel RAPL / AMD
  equivalent).
- Measure **cold start separately** from steady-state execution — often where
  Go's advantage is largest, and it matters disproportionately in serverless.
- Apply the **same PUE** to both sides so facility overhead does not skew the
  ratio.
- Report the **distribution of `N*` across all 95 functions**, not only the
  mean. Some functions will pay back immediately, some may never. That
  distribution is a more interesting result than a single average.
- Group it the way the dataset intends (EVALUATION_DATASET.md §8–§9): by
  **complexity bucket** (A ≤5, B ≤10, C ≤20, D+ >20 — 25/25/25/20 functions)
  and by **AWS vs. non-AWS** (58 vs. 37). Both axes come from `meta.json` and
  are only available if [H1] carries them into the metrics record. Note the
  Python baseline needs the third-party packages the originals used — `boto3`
  (58 functions), `python-dateutil` (18), `requests` (4) — while the Go side
  needs none of them; that asymmetry is real and belongs in the write-up.

---

### Implementation ([H6], 2026-08-24)

Implemented as `cmd/runtime`, with the two harnesses in `evaluation/harness/`.

```sh
# measure both sides, then turn the result into break-even counts
go run ./cmd/runtime -artifacts evaluation/evaluation_set \
    -packages runs/packages-<run-id>.zip -runlog runs/run-<run-id>.jsonl \
    -out evaluation/runtime.json -report evaluation/runtime-report-<run-id>.json
go run ./cmd/energy -runtime evaluation/runtime.json runs/run-<run-id>.jsonl
```

**`-runlog` is not optional in practice.** A translated Go package on disk is not evidence
that the translation is correct: `scripts/run-benchmark.sh` archives the package of a *failed*
job too (the service returns it with HTTP 406, and it is worth keeping as evidence), so the
packages directory is a superset of the validated translations. Timing one that fails its
fixtures measures how fast the wrong answer is produced — usually fast, since the work it
skips is the work it was supposed to do. `-runlog` restricts the measurement to the functions
the pipeline recorded as completed; without it the tool still runs and says, in both the report
(`validated_only: false`) and the summary (`validated: NO`), that it did not filter.

The flag arrived after the first measurement runs did, so the archived files were rebuilt from
their own reports rather than re-measured — `-from-report` applies the filter to measurements
that were already taken, which keeps the surviving numbers identical to the ones the rest of the
analysis used:

```sh
go run ./cmd/runtime -from-report evaluation/runtime-report-<run-id>.json \
    -runlog runs/run-<run-id>.jsonl -out evaluation/runtime.json
```

How much each file held before the filter: `runtime-20260830-230152.json` 57 → 11 (that run
completed only 11 translations), `runtime-20260831-190900.json` 66 → 42, `runtime.json`
(20260904-190539) 59 → 46, `runtime-functionset.json` 14 → 10. **Break-even `N*` is unaffected**
— `cmd/energy` joins runtime measurements against the run's *completed* translations, so the
extra rows were never costed — but every descriptive Go-vs-Python ratio computed straight from
these files was. On 20260904-190539 the median steady-state *energy* ratio moves from 1.66× (all
59) to 1.28× (the 46 validated), and the *wall-time* ratio from 1.84× to 1.39×; the cold-start
energy ratio is essentially unchanged (46.6× → 46.3×). Say which ratio is quoted — an earlier
revision of this paragraph paired the energy figure before the filter with the time figure after
it.

#### How far a translation gets, by complexity bucket (2026-09-06)

`evaluation/figures/pipeline_funnel.py` draws the three nested outcomes of run
`20260904-190539` per complexity bucket. **buildable** = the pipeline produced Go
that compiles and reached the test stage; **validated** = every fixture also
*executed* cleanly in the final round (no execution error, timeout, setup failure
or unusable fixture); **tested** = every fixture *passed*, which is the pipeline's
own success criterion. They nest by construction, and the run log confirms it in
all four buckets.

| bucket | n | buildable | validated | tested |
|:---|--:|--:|--:|--:|
| A (cc ≤ 5) | 25 | 84% | 72% | 64% |
| B (cc ≤ 10) | 25 | 96% | 72% | 56% |
| C (cc ≤ 20) | 25 | 84% | 80% | 56% |
| D+ (cc > 20) | 20 | 45% | 20% | 15% |
| **all** | 95 | **79%** | **63%** | **49%** |

Two things the middle column buys. First, **A/B/C lose more functions after
compiling than before** — B goes 96 → 72 → 56, so a quarter of the bucket compiles
but does not execute cleanly (an execution error, timeout or setup failure), and a
further sixth executes and returns the wrong answer. That is a different engineering
problem from code that does not compile, and a bare pass/fail rate cannot separate
them. Second, **D+ is the only bucket that mostly fails before it runs**: 55% never
compile at all, and of the 45% that do, more than half then fail to execute.
Complexity is not degrading the *translation quality* here so much as preventing a
translation from existing — which is the shape a pre-translation gate could act on,
and the reason the D+ regression from run `20260831-190900` (7/20 → 3/20 completed)
matters more than a four-function drop suggests. Both statements are re-checked
against the replicates in [Replicate series](#replicate-series-results-of-record-2026-09-14):
B and C hold in all three runs, A is at parity in one, and D+ fails to compile for
14/20 and 8/20 on the other two.

The [A19] caveat applies: `Metrics.TestOutcomes` describes the last validation
round, so "validated" is a property of the final artifact, not a claim that no
fixture ever errored during repair.

#### Where the saving actually is (2026-09-06)

`evaluation/figures/savings_histogram.py` draws the analogue of Werner et al.
Figure 1 — "Consumption Reductions [J]" over the translated functions — for the 46
validated translations of run `20260904-190539`, as two overlapping series split on
the corpus's own AWS axis. It emits TikZ/pgfplots for the write-up, an SVG preview,
and a generated caption; both renderers share one geometry function so the preview
cannot drift from the figure.

The split is the result:

| | n | median | mean | positive | sign test |
|:---|--:|--:|--:|--:|--:|
| AWS | 23 | 21.7 J | 57.4 J | 21/23 | p = 7 × 10⁻⁵ |
| non-AWS | 23 | −0.001 J | 0.10 J | 10/23 | p = 0.68 |

(joules saved over 1,000 invocations; divide by 1,000 for the per-invocation figure
`N*` uses). Mann–Whitney between the groups: p = 1.2 × 10⁻⁵. **Translating a
non-AWS function in this corpus does not save energy** — its savings are a coin
flip around zero, and 13 of the 23 are negative. Every joule of the corpus-level
win comes from the AWS functions, where the Go SDK replaces boto3's import and
client-construction cost. That is also why the corpus median steady-state ratio is
only 1.28×: half the corpus has nothing to win. The split replicates on both further runs of
the same configuration — see [Replicate series](#replicate-series-results-of-record-2026-09-14).

Two things about the comparison to the paper. Its x-axis is labelled per
invocation, but a mean of 201 J per single invocation cannot be reconciled with its
own break-even range of 3,000–10⁶ invocations against a 61-second translation; the
figure is only coherent as savings accumulated over its 1,000-invocation run, which
is the basis used here. And its seven bars sum to 60%, not 100%, so their histogram
is normalised over more data than it draws — it is reproduced in panel (a) as a
shape to compare against, not a distribution to do arithmetic on.

**Symmetry is structural, not conventional.** `evaluation/harness/handler.py` and
`bench_handler.go.txt` read the *same* fixture payloads as JSON Lines on stdin, invoke the
function once per line, and write the *same* envelope (marker + `{"response": …}` / `{"error": …}`)
on stdout, with the same stdout discipline as [A18]. `harness_test.go` pins the marker across all
four files that must agree on it. Both sides run under the same meter, on the same machine, with
the same AWS isolation — `internal/builder.TestExecutionEnv`, the same helper the test stage uses,
so an AWS call cannot resolve differently on the two sides and be recorded as a runtime difference.

**Cold vs. steady comes from a two-point difference, not from a clock inside the harness.** The
same executable is run once with 1 payload and once with N:

```
T(1) = startup + 1 × per_invocation
T(N) = startup + N × per_invocation
```

An in-harness clock would compare Go's runtime clock against Python's `time` module and add a
per-language bias to the very quantity under test. It also puts module import and package-level
statements — which run before the first invocation in both languages, and in real Lambda too — on
the startup side where they belong. Each point is repeated and the **minimum** is kept: noise on a
shared machine only ever adds time, so the minimum is the best available estimate of the true cost
and is far more stable than a mean a single scheduling hiccup can dominate.

**N escalates until the signal clears the noise — for both sides together.** N starts at 1000, the
count the ReFaaS paper's microbenchmark uses. Most functions in this corpus do microseconds of
work against a millisecond of process startup, so at a fixed N the difference `T(N) − T(1)` is
buried in scatter and the naive result is a per-invocation cost of *zero* — which would propagate
into `runtime.json` as "this function is free to run" and make its `N*` infinite. The driver
therefore raises N (×10, to `-max-invocations`) until the difference exceeds the measured
repetition spread. A function that never resolves is reported as `UNRESOLVED` and **omitted** from
`runtime.json`, because `cmd/energy` names a missing function but would cost a zero as free.

The escalation is **joint**: Python and Go are measured at the same N, and a side that resolves
early is carried up to whatever N the other side needs. The two are only ever compared as a ratio,
and a ratio between measurements taken at different N is sound only if per-invocation cost is
exactly linear in N — which GC onset and cache behaviour do not guarantee. Measuring both at one N
removes the assumption instead of resting on it. It was not hypothetical: in
`runtime-report-20260831-190900.json`, 25 functions had Python resolved at N=2000 against a Go side
resolved at N=200.

**CPU time is reported alongside energy and runtime**, as the paper's function-metrics table does.
It comes from the `rusage` the kernel already returns at `wait4`, so it costs nothing and — unlike
an in-process clock — cannot differ between the two runtimes by construction. It is split by the
same two-point difference; peak RSS is reported from the long run rather than differenced, being a
peak. The `perf` backend reports no CPU figure, because there the wrapped process is `perf stat`
rather than the function. A per-invocation CPU delta of zero (the kernel charges CPU in clock
ticks, so a short run can land on the same tick count twice) is dropped rather than reported as
free, exactly as the energy path does.

**No fabricated joules.** Three meters, and every figure carries which produced it:
`rapl` (reads `/sys/class/powercap/intel-rapl:*/energy_uj` — no root, no perf, the primary),
`perf` (`perf stat -e power/energy-pkg/,power/energy-ram/`, as specified above), and `time`
(wall-clock only). The `time` meter reports **no energy at all** unless `-watts` explicitly states a
package power, in which case `E = P·t` is computed and tagged `energy_derived` everywhere it
appears. This is the same assumption `energy.config.json` already makes for the LLM side
(`node_power_watts × time`), so both halves of the comparison stay on one method — but it is an
assumption, and the tool says so rather than letting a derived number read as a measurement.

> **Neither RAPL nor perf is available under WSL2**, which is where this was developed. Measured
> energy therefore requires a bare-metal Linux host with readable powercap counters. **That run has
> since happened** (2026-08-31 and 2026-09-04, host `carl-eikermann-UX310UAK`, `meter: rapl`,
> `energy_derived: false`), so absolute joule figures and `N*` are measured rather than derived.
> The `evaluation_set` results are in "Break-even, per function and per policy" at the end of this
> section; the paper-set table immediately below predates that run and is kept only for the
> cold-versus-steady argument it makes.

**First result (paper set, 14 functions, derived energy at 15 W — timings are measurements, joules
are not):**

| | median | min | max |
|---|---|---|---|
| Go speedup, steady state | **1.9×** | 1.0× | 4.3× |
| Go speedup, cold start | **15.0×** | 3.9× | 21.0× |

> **Superseded by measurement — do not quote the table above.** On RAPL, over validated
> translations only, the paper set gives **1.05× steady-state / 19.0× cold-start energy** (run
> `functionset-20260913-120600`, 11 of 14 translated; 0.96× / 26.3× by wall time) and
> `evaluation_set` gives **1.28× / 46×** (run `20260904-190539`). The qualitative finding below
> survives and strengthens: cold start is 18–36× the steady-state advantage, not 8×.

The gap between the two rows is the finding: this section predicted cold start would be where Go's
advantage is largest, and on this set it is roughly **eight times larger** than the steady-state
advantage. For short serverless invocations, which is what this corpus is, the cold-start column is
the one that governs whether a translation pays back. `runtime.json` nonetheless carries the
**steady-state** figure, because break-even asks how many invocations of a *deployed* function repay
one translation and a function invoked `N*` times is overwhelmingly warm — charging every
invocation a cold start would understate `N*` on both sides at once and flatter the conclusion. The
cold figures are in `-report` for the write-up to use explicitly.

Caveats specific to this first run: the paper set is deliberately trivial (12 of 14 in bucket A,
none using AWS), so its steady-state ratios are dominated by interpreter overhead on functions that
do almost no work, and the resulting `N*` values are correspondingly enormous (median ~2×10⁷). The
`evaluation_set` numbers, below, are the ones to report.

#### Break-even, per function and per policy (2026-09-07)

`evaluation/figures/amortisation_spread.py` draws the analogue of Werner et al. **Figure 6** —
invocations required before a translation repays the energy spent producing it — for the
46 validated translations of run `20260904-190539`. It emits TikZ/pgfplots, an SVG preview and a
generated caption, and consumes `evaluation/prediction/energy-20260904-190539.json` (per-function
facility joules) together with `evaluation/runtime.json` (per-invocation savings).

**Their figure is a bounding box; this one is a distribution.** Werner et al. draw one box per
model, width from best case to worst case, height the number of functions that amortise. Decoding
the vector drawing gives qwq 18 functions over 3.8×10³ to 3.9×10⁵, qwen2.5-coder_32b 9 over
1.9×10³ to 1.9×10⁵, and gemma3_27b 6 over 1.3×10³ to 1.4×10⁵. **All three spans are exactly
101×**, which is the signature of one translation cost per model crossed with a single global
savings range taken from their Figure 1. Here both terms are per function — each translation
carries its own measured facility energy and its own measured per-invocation saving — so
`N*ᵢ = E_translation,ᵢ / ΔEᵢ` is a genuine per-function quantity.

**Panel (a), the per-function spread.** 31 of the 46 repay at some invocation count, spanning
**1.44×10⁴ to 6.65×10⁸, i.e. 4.7 orders of magnitude**, median 1.02×10⁶. The remaining 15 are not
faster than their Python original and never repay at any `N`; they are drawn censored at the right
edge rather than dropped, because dropping them is what makes a median look optimistic. The
AWS structure is stark: **the 15 lowest break-even points are all AWS functions**, and the first
non-AWS function appears at rank 16, already past 10⁶ invocations.

**Panel (b), the same quantity per screening policy**, on their axes, with the box replaced by the
cumulative curve whose bounding rectangle the box would have been:

| policy | translated | succeeded | amortise | portfolio break-even |
|:---|--:|--:|--:|--:|
| translate all | 95 | 47 | 31 | 9.3 × 10⁵ |
| skip AWS functions | 37 | 23 | 10 | **2.0 × 10⁸** |
| prediction gate | 45 | 34 | 20 | 1.1 × 10⁶ |
| oracle | 47 | 47 | 31 | 2.3 × 10⁵ |

The vertical markers are the **portfolio** break-even, which is not the median of the per-function
values: it charges each policy for the attempts that failed, so it is the number a deployment
decision actually turns on.

Two results fall out of that table. **Skipping AWS functions is catastrophic**, two orders of
magnitude worse than translating everything, because it removes precisely the functions where the
saving lives — which is a sharper statement of the asymmetry than the histogram in "Where the
saving actually is" makes on its own. And **the oracle curve coincides exactly with
translate-all**, since both translate every function that succeeds and therefore amortise the same
31; their entire difference is in attempts not paid for, visible only in the portfolio marker.
Perfect screening buys avoided waste, not additional successes.

The portfolio markers also bound the gate. On this run it beats translate-all only below
8.7 × 10⁵ invocations and is net-positive only above 1.1 × 10⁶, so there is no invocation count
at which it beats both baselines — and the replicates do not rescue it (see
[Replicate series](#replicate-series-results-of-record-2026-09-14)).

Two things the write-up must state. The gate curve uses the model fitted on the **previous** run
(`model-20260831-190900.json`) scored against this one, which is the honest transfer setting.
And this corpus sits at 10⁴ to 10⁹ invocations where theirs sits at 10² to 10⁵: explainable rather
than contradictory, since they used 27B to 32B models against this pipeline's 123B, and their
per-invocation savings were larger.

---

## Replicate series: results of record (2026-09-14)

**These are the numbers the Evaluation chapter should quote.** Three full `evaluation_set` runs on
one frozen configuration, plus one `function_set` run on the same configuration, recomputed from
the run logs, `cmd/energy` on the current `energy.config.json`, and the validated-only runtime
reports. The single-run figures earlier in this document (run `20260904-190539`) are one column
of these tables, and the figures in `evaluation/figures/` are drawn for that run only.

| run id | run log | commit | wall clock | completed |
|:---|:---|:---|--:|--:|
| `20260904-190539` | `run-20260904-170428.jsonl` | `8a9cf24` | 5.5 h | 47 / 95 |
| `20260911-165103` | `run-20260911-144954.jsonl` | `437f687` | 8.5 h | 43 / 95 |
| `20260912-172904` | `run-20260912-152820.jsonl` | `f67cd1c` | 6.0 h | 46 / 95 |
| `functionset-20260913-120600` | `run-20260913-100522.jsonl` | `eb48c31` | 0.1 h | 11 / 14 |

All four use `scripts/benchmark.json` sha `f9e30f4b` on the same host with Floci enabled,
`REQUIRE_META` and a 2 s LLM interval, and **the pipeline code is identical across them** — the
only change between the commits is a comment in `internal/floci/deployer.go`. (The `20260911`
manifest counts one of its 52 failures as a client-side error; the run log records all 52 as
failed jobs.) Run `20260831-190900` (config `e82fbaf9`: `cleaner` → `coder`, before [C8a], [C11a],
[C13], [C14], [C15]) is a different pipeline version. It stays the prediction model's training
run and is not pooled with these.

### Outcomes and label stability

| | 0904 | 0911 | 0912 | mean |
|:---|--:|--:|--:|--:|
| completed (= every fixture passed) | 47 | 43 | 46 | 45.3 ± 2.1 (47.7%) |
| bucket A (n = 25) | 16 | 16 | 15 | 62.7% |
| bucket B (n = 25) | 14 | 15 | 13 | 56.0% |
| bucket C (n = 25) | 14 | 12 | 13 | 52.0% |
| bucket D+ (n = 20) | 3 | 0 | 5 | 13.3% |
| AWS (n = 58) | 24 | 25 | 24 | 42.0% |
| non-AWS (n = 37) | 23 | 18 | 22 | 56.8% |
| `goTester` route (n = 55) | 33 | 27 | 32 | 55.8% |
| Floci route (n = 40) | 14 | 16 | 14 | 36.7% |
| Fisher, A vs D+ | p = 0.002 | p < 0.001 | p = 0.034 | |
| Fisher, AWS vs non-AWS | p = 0.06 | p = 0.67 | p = 0.10 | |

- **Stability is measured, no longer assumed.** Pairwise agreement is 85.3% / 84.2% / 82.1%
  (Cohen κ 0.71 / 0.68 / 0.64), Fleiss κ **0.68**. 34 functions always pass, 38 never do, and
  **23 vary** (12 pass once, 11 twice); 57 pass at least once. Predicting one run's labels from
  the mean of the other two reaches AUC 0.91 / 0.89 / 0.88, which is roughly the ceiling for any
  per-function score. Across the configuration change agreement is lower: `20260831-190900`
  against the three runs 75.8% / 69.5% / 72.6% (κ 0.52 / 0.38 / 0.45).
- **A single run is ±2 functions.** A single-run difference of that order is not evidence,
  including the +5 between `20260831-190900` and `20260904-190539`.
- **The signal structure differs from the `20260831-190900` reading.** Per-function success
  probability over the three runs is 0.63 for bucket A against 0.13 for D+ (Mann–Whitney
  p = 0.0002), but 0.42 for AWS against 0.57 for non-AWS (p = 0.13). On `20260831-190900` it was
  the reverse: A vs D+ p = 0.37, AWS 27.6% vs 70.3% (p = 5.5 × 10⁻⁵). That reading belongs to
  that pipeline version — most plausibly the Floci endpoint defect fixed in [C11a], which depressed
  AWS functions there — and must not be reported as a property of the corpus.

### How far a translation gets

| bucket | buildable (0904 / 0911 / 0912) | validated | tested | lost: no build / exec fail / mismatch |
|:---|:---|:---|:---|:---|
| A | 84 / 84 / 80% | 72 / 72 / 76% | 64 / 64 / 60% | 4/3/2 · 4/3/2 · 5/1/4 |
| B | 96 / 96 / 96% | 72 / 84 / 72% | 56 / 60 / 52% | 1/6/4 · 1/3/6 · 1/6/5 |
| C | 84 / 84 / 80% | 80 / 68 / 76% | 56 / 48 / 52% | 4/1/6 · 4/4/5 · 5/1/6 |
| D+ | 45 / 30 / 60% | 20 / 20 / 35% | 15 / 0 / 25% | 11/5/1 · 14/2/4 · 8/5/2 |
| **all** | **79 / 76 / 80%** | **63 / 63 / 66%** | **49 / 45 / 48%** | |

Final-round fixtures passing: 235/313, 224/303, 240/321. B and C lose more functions after
compiling than before in every run; A does in two runs and is at parity (5 vs 5) in the third.
D+ is the only bucket where compilation is the dominant loss, and it is a majority of the
bucket in two of three runs (55%, 70%, 40%).

### Translation energy

| | 0904 | 0911 | 0912 |
|:---|--:|--:|--:|
| total spend (facility, incl. measured host) | 357.9 Wh | 321.4 Wh | 389.4 Wh |
| share spent on failed attempts | 75.5% | 81.8% | 75.9% |
| cost per success, failures amortised | 7.62 Wh | 7.47 Wh | 8.47 Wh |
| per completed translation, mean / median | 1.86 / 1.11 Wh | 1.36 / 0.94 Wh | 2.04 / 1.08 Wh |
| repair share of inference, completed translations only | 47.0% | 37.0% | 49.1% |
| repair share of inference, **all attempts** | 65.9% | 65.3% | 68.3% |
| `summary` stage share, all attempts | 5.1% | 5.7% | 4.7% |
| host energy (RAPL, gross) as share of spend | 1.9% | 2.4% | 1.4% |
| gross / marginal host energy | 1.49 | 1.70 | 1.66 |
| wall clock inside LLM stages | 89.3% | 94.2% | 90.7% |
| tokens, prompt / output | 1.69 M / 0.84 M | 1.54 M / 0.74 M | 1.83 M / 0.92 M |

Mean total spend is 356 ± 34 Wh (1.28 MJ). **The repair share `cmd/energy` prints is of completed
translations only**; over every attempt the two repair stages take two thirds of inference energy,
because failed jobs are the ones that exhaust the repair budget. Say which one is quoted.

### Go vs. Python at runtime (RAPL, validated translations only)

| | 0904 | 0911 | 0912 | 0912 without early exits |
|:---|--:|--:|--:|--:|
| validated translations measured | 46 of 47 | 40 of 43 | 45 of 46 | 41 |
| steady-state energy ratio, median | 1.28× | 1.31× | 1.81× | 1.25× |
| … AWS functions | 2.93× | 2.62× | 3.77× | 3.14× |
| … non-AWS functions | 0.99× | 0.78× | 0.88× | |
| cold-start energy ratio, median | 46.3× | 45.5× | 46.9× | |
| Go faster at steady state | 31 / 46 | 29 / 40 | 29 / 45 | |
| saving per 1,000 invocations, AWS: median (positive) | 21.7 J (21/23) | 28.0 J (21/22) | 57.6 J (21/23) | 36.4 J |
| saving per 1,000 invocations, non-AWS: median (positive) | −0.001 J (10/23) | −0.035 J (8/18) | −0.018 J (8/22) | |
| Mann–Whitney, AWS vs non-AWS savings | 1.2 × 10⁻⁵ | 6.8 × 10⁻⁶ | 1.2 × 10⁻⁵ | |

- Not measured: f72 in 0904 and 0911 (Go side raised on an unset `AWS_LAMBDA_FUNCTION_NAME`),
  f25 and f29 in 0911 and f25 in 0912 (`TIMEOUT`).
- **The AWS asymmetry replicates in every run**, and so does the cold-start figure. 33 functions
  are validated in all three runs; their per-invocation saving has the same sign in 26 and a
  Spearman correlation of 0.80–0.92 between runs.
- **Early-exit translations.** In 0912, f20, f30, f56 and f72 measure below 1 mJ per Go
  invocation against more than 10 mJ for Python; no function does so in the other two runs. No
  fixture sets an environment variable, so f30's suite (every case expects the error response for
  an unset `DESTINATION_BUCKET`) is measured on that error branch. The 0912 translation checks the
  variable before building its S3 client (0.19 mJ); the 0904/0911 translations attempt the call
  first (18 mJ). Observable behaviour is identical. This inflates speedup ratios far more than
  savings, since the Python side dominates the difference.

### Break-even

| | 0904 | 0911 | 0912 | 0912 without early exits |
|:---|--:|--:|--:|--:|
| repay / never repay | 31 / 15 | 29 / 11 | 29 / 16 | 25 / 16 |
| `N*` range | 1.44 × 10⁴ – 6.65 × 10⁸ | 1.48 × 10⁴ – 4.34 × 10⁸ | 1.51 × 10⁴ – 1.74 × 10⁸ | |
| span | 4.7 decades | 4.5 decades | 4.1 decades | |
| `N*` median (repaying functions) | 1.02 × 10⁶ | 4.46 × 10⁵ | 1.28 × 10⁵ | 3.97 × 10⁵ |
| repay within 10⁵ / 10⁶ / 10⁷ | 7 / 15 / 19 | 9 / 17 / 19 | 13 / 18 / 20 | |
| rank of the first non-AWS function | 16 | 17 | 19 | |

The cheapest functions are the same in every run — f76 (1.4–1.5 × 10⁴), f77 (1.6–3.2 × 10⁴),
f45 (2.6–3.9 × 10⁴), f30 (3.3–3.5 × 10⁴) — while the median moves by almost an order of magnitude.
**Quote the distribution, not the median.**

| policy: translated / succeeded / amortise, portfolio `N*` | 0904 | 0911 | 0912 |
|:---|:---|:---|:---|
| translate all | 95 / 47 / 31, 9.3 × 10⁵ | 95 / 43 / 29, 8.6 × 10⁵ | 95 / 46 / 29, 7.9 × 10⁵ |
| skip AWS functions | 37 / 23 / 10, 2.0 × 10⁸ | 37 / 18 / 8, 1.9 × 10⁸ | 37 / 22 / 8, 1.2 × 10⁸ |
| prediction gate (M1 fitted on `20260831-190900`) | 45 / 34 / 20, 1.1 × 10⁶ | 45 / 29 / 19, 7.0 × 10⁵ | 45 / 32 / 17, 9.1 × 10⁵ |
| oracle | 47 / 47 / 31, 2.3 × 10⁵ | 43 / 43 / 29, 1.6 × 10⁵ | 46 / 46 / 29, 1.9 × 10⁵ |

**The gate has no robust useful range.** It beats translate-all only below 8.7 × 10⁵ /
9.4 × 10⁵ / 7.4 × 10⁵ invocations and is net-positive only above 1.1 × 10⁶ / 7.0 × 10⁵ /
9.1 × 10⁵. The window in which it beats both baselines is empty in 0904, 7.0–9.4 × 10⁵ in 0911,
and empty in 0912 (with or without the early exits: 7.9 × 10⁵ against 1.3 × 10⁶). Both ends scale
with the same energy constants, so the window's existence does not depend on §8's assumptions.
Skipping AWS functions is two orders of magnitude worse than translating everything in every run.

### The shipped predictor on the replicates

`model-20260831-190900.json` (M1, threshold 0.465) translates the same 45 functions in every run,
since the features are deterministic.

| | 0904 | 0911 | 0912 |
|:---|--:|--:|--:|
| AUC | 0.78 | 0.70 | 0.74 |
| accuracy | 0.75 | 0.68 | 0.72 |
| TP / FP / FN / TN | 34 / 11 / 13 / 37 | 29 / 16 / 14 / 36 | 32 / 13 / 14 / 36 |
| AUC of the training run's own labels used as the score | 0.76 | 0.69 | 0.73 |
| replicate ceiling (mean of the other two runs) | 0.91 | 0.89 | 0.88 |

Against a later pipeline version the model is only 0.01–0.02 AUC better than simply reusing the
labels it was trained on, and well below what the runs themselves permit. On 0904, 19 of its 24
errors are functions whose label flipped since the training run; 10 of its 13 false negatives
failed there and pass here (9 of those use AWS; 12 of all 13 false negatives do). That is [I10]'s
"a gate learns a pipeline version", now measured three times.

### `function_set` on the frozen configuration

`functionset-20260913-120600`: **11 of 14** (78.6%). pf8, pf10 and pf14 fail, all on output
mismatch, and every function builds. TODO.md open question 1 records pf10 and pf14 as fixtures no
correct translation can satisfy (a live API body, `datetime.now()` timestamps under tolerant
matching) and pf8 as a genuine divergence. The earlier run on the old configuration (dirty tree) scored 10 of 14, and only pf11
changed. Energy 10.30 Wh in total, 49.8% on failures, 0.94 Wh per success. Runtime over the 11
validated translations: **1.05× steady-state, 19.0× cold-start energy**, 6 of 11 faster at steady
state; `N*` computed for 6 (median 3.3 × 10⁷, range 2.9 × 10⁴ – 2.4 × 10⁸), 5 never repay. M1
scores AUC 0.85 and translates 13 of 14, keeping all 11 successes. Report it separately from
`evaluation_set`: its expectations were never executed against the originals, and it contains no
AWS function.

### Reproducing

```sh
go run ./cmd/energy -runtime evaluation/runtime-<run-id>.json runs/run-<log>.jsonl   # add -json / -sweep
```

Everything else is computed from the run logs and the validated-only runtime reports, using the
definitions of `evaluation/figures/pipeline_funnel.py` (buildable / validated / tested) and
`evaluation/figures/amortisation_spread.py` (policies, portfolio break-even).

---

## 7. Validation

No per-token measurement exists for Devstral 2 123B. Validate as follows:

1. **Validate the method, not the model.** Pick a dense model of known size that
   *is* on the ML.ENERGY leaderboard, apply the formulas above to its
   configuration, and compare against their measured value. Agreement within a
   factor of 2 demonstrates the formula works and justifies applying it to
   Devstral. Since GWDG’s reply places Devstral on **H200** (700 W SXM,
   4.8 TB/s HBM3e), the leaderboard’s H100/B200 SXM measurements are now a
   closer hardware match than under the old H100 PCIe assumption — but still
   adjust for the part actually measured rather than assuming equivalence.
   Match dense-to-dense; for MoE models the relevant quantity is *active*
   parameters.
2. **Cross-check against Mistral's LCA** (July 2025, with ADEME and partners)
   for Mistral Large 2 — also a dense 123B model from the same vendor and
   architecture family, therefore a closer match than any leaderboard entry.
   Compare per-response figures after adjusting for their datacenter
   assumptions.

Claimed accuracy: **factor of 2–3 on absolute energy**, better on comparisons
between pipeline configurations, since systematic errors cancel when the model
and hardware are held constant.

---

## 8. Sensitivity analysis

Never report a single number. Report central estimate plus range, and include
a sensitivity table:

`go run ./cmd/energy -sweep runs/*.jsonl` emits this table directly, re-costing
the *measured* token counts under each varied assumption — only the
coefficients are assumptions, the tokens are facts.

| Parameter varied | Range | Effect on E per translation | Status after the reply |
|---|---|---|---|
| Concurrency `B` | 8 → 128 | ×2.8 → ×0.55 | **assumed; declined by GWDG — permanent** |
| Node power `P_node` | 1400 → 2550 W | ×0.82 → ×1.50 | **assumed; unanswered** |
| Prefill peak | 989 → 1979 TFLOP/s | ×1.40 → ×1.00 | W8A16 vs W8A8 ambiguity inside the FP8 answer |
| Precision | FP8 → BF16 | ×1.00 → ×1.60 | **settled: FP8** — kept as a counterfactual |
| MFU | 0.30 → 0.50 | ×1.13 → ×0.92 | assumed |
| PUE | 1.03 → 1.2 | ×0.98 → ×1.14 | assumed; unanswered |

Regenerated 2026-09-14 with `go run ./cmd/energy -sweep` over the three replicate runs
(`20260904-190539`, `20260911-165103`, `20260912-172904`). The multipliers agree to two decimals
across them except `B` = 8 (×2.81 / ×2.75 / ×2.81), the prefill peak (×1.40 / ×1.42 / ×1.40) and
precision (×1.60 / ×1.58 / ×1.60); central mean facility energy per completed translation is
1.84 / 1.33 / 2.02 Wh. They shift slightly with a run's prompt/output token mix, since `B` and
precision act on the decode term while MFU and the prefill peak act on prefill. The earlier
`run-20260807-132133` archive gave ×2.6 → ×0.6, ×1.45, ×1.55 and ×1.15 → ×0.91.

Two rows changed character with the reply. **Precision** is no longer an
unknown — it is a resolved constant, and its row now shows what the
confirmation was worth (a 1.6× overestimate avoided). **Concurrency** is no
longer pending — GWDG holds the data and may not share it, so no future
correspondence will collapse this row, and the sweep is the reported result
rather than a stand-in for one.

Conclude with the payoff statement: *even at the pessimistic end of every
parameter, the break-even point remains below N invocations, so the conclusion
holds across the full plausible range.*

---

## 9. Marginal vs. shared cost

**Decision: report marginal cost as the primary figure.**

- *Marginal energy* — what the requests added to a server that was running
  anyway.
- *Shared (amortized) energy* — total server consumption, including idle time,
  divided over all tokens served.

Justification for marginal, to be argued explicitly in the thesis:

1. SAIA is a shared multi-user research service with continuous demand from the
   Chat AI user base; idle capacity is not attributable to any single user.
2. SAIA is designed specifically to fill Slurm scheduling gaps so the hardware
   is not idle (Doosthosseini et al., 2026).
3. Marginal cost is the decision-relevant quantity — it answers "what does one
   more translation cost?", which is precisely the question the break-even
   analysis poses.

Counter-argument to acknowledge in one paragraph: under low utilization the
amortized cost could be several times higher, and a full attributional LCA
would additionally include embodied emissions from GPU manufacturing. The
reported figure is therefore a **lower bound**. Stating this pre-empts the
obvious examiner question at no cost.

---

## 10. Threats to validity

Write this section. Items to cover:

- Unknown server concurrency (`B`) — the dominant uncertainty, and an
  irreducible one: GWDG holds the measurement and declined to release it
  (2026-08-22). State the refusal, not just the gap.
- ~~Assumed model precision~~ — **resolved**: GWDG confirmed FP8 for Devstral.
  A residual remains: whether the deployment is W8A8 or weight-only W8A16,
  which is a factor of 2 on `e_in` (swept as `peak_flops_per_gpu`).
- Assumed node power (1700 W). GWDG gave no monitoring figure, and the reply
  did not state whether the H200s are SXM (700 W) or NVL (600 W). Swept over
  1400–2550 W, i.e. GPU-only to full-node-share attribution.
- PUE is taken from a 2021 press release about **Emmy**, a different machine
  from the inference platform, and GWDG did not confirm a current value. It is
  the smallest of the uncertainties (±16% across the swept range) but it is not
  a measured value for this hall.
- Carbon-neutrality is a **market-based** claim about procurement. Both
  intensities are reported; neither includes embodied manufacturing emissions.
- Token counts used as a proxy for computational work. Luccioni et al. 2024
  (source 16) is the strongest published evidence against this: measured
  per-inference energy varies by task at comparable token counts.
- **One `P_node` is applied to both phases, and that overstates `e_out`.**
  Splitwise (Patel et al. 2024, source 14) reports that decode draws materially
  less power than prefill, because a memory-bound phase leaves the compute
  units idle while this model charges it the full node draw. Since an output
  token costs ~3.5× an input token here, `e_out` dominates, so the *direction*
  of this error is known even though its size is not: the estimate is
  conservative. Reported as a bounded bias rather than left implicit — the
  alternative would need per-phase power telemetry GWDG has declined to
  release.
- Prefix caching, if active, makes the estimate conservative — and it cannot
  be quantified here, since SAIA does not expose `cached_tokens` (verified).
- Marginal-cost framing excludes idle and embodied energy — and GWDG's
  carbon-neutral status does not change that, since it covers operational
  emissions only.
- **Local pipeline compute (builds, test executions, Floci containers) is now
  measured**, not excluded: RAPL counters are read either side of every job and
  every stage, and `E_translation = E_inference × PUE + E_host`. Two caveats
  remain. The host figure is a whole-machine package counter, so it includes
  whatever else that machine was doing — the run host has to be otherwise
  quiet. And ~90% of a job's wall clock is spent waiting on the LLM API, so
  gross host energy is 1.5–1.7× the marginal figure on the measured runs; both are
  reported, and a quoted figure must say which it is. RAPL package counters also
  exclude memory, storage and peripherals, so the host term is a lower bound (see
  [Local compute](#local-compute-non-llm-pipeline-energy--measured-since-2026-09-04)).
- Single model, single provider, single hardware generation. If any run mixes
  models across stages, per-stage coefficients must be applied — a run-level
  average would be wrong.
- Evaluation set of 95 functions not necessarily representative of serverless
  Python at large — it is a curated slice of `the-stack`, biased toward
  functions that are self-contained, deterministic and fast enough to validate
  (>10 s candidates were rejected). The legacy `function_set` (f1–f14) is
  additionally *unverified* — its expectations were never executed — and
  exercises no side-effect route at all, so failures there mean "investigate",
  not "translation defect".
- Six `evaluation_set` functions contain external HTTP/SMTP call sites that no
  test exercises, so a wrong translation of `requests.post(...)` passes the
  benchmark — do not claim HTTP-integration fidelity (EVALUATION_DATASET.md
  gotcha 5).
- **No fixture sets an environment variable** (0 of 392) although 15 of 95 functions
  read one, so some suites exercise only an error branch — all three f30 fixtures,
  including `happy-path-copy-and-delete`, expect the error response the original
  returns when `DESTINATION_BUCKET` is unset. `cmd/runtime` measures the same branch,
  so a translation that fails fast on the missing variable looks far cheaper than one
  that attempts the call, with identical observable output (f30: 0.19 mJ against
  18 mJ Go steady-state per invocation in runs `20260912-172904` and
  `20260904-190539`). Per-function speedups therefore partly measure how a
  translation handles missing configuration; see
  [Replicate series](#replicate-series-results-of-record-2026-09-14).
- 27 tests across 14 functions use `outputMode: "shape"` (types only, no
  values), so they cannot catch a value regression; exclude or mark them when
  claiming value-level equivalence.
- Tolerant matching is subset matching: a Go translation that returns *more*
  than the Python original still passes.
- Go/Python runtime measurements taken on one hardware platform.
- Retry-driven variance: because the number of LLM calls depends on how many
  repair attempts a function needs, per-function energy is heavy-tailed.
  Report the distribution, not just the mean (this is the same argument as for
  `N*` in section 6).

---

## 11. Sources

**Verified — infrastructure and platform**

1. KISSKI Inference Platform — https://kisski.gwdg.de/leistungen/2-01-02_inferenz/
   (4 × H100 PCIe 80 GB HBM2e per node, Slurm + Kubernetes)
2. GWDG Chat AI Available Models — https://docs.hpc.gwdg.de/services/ai-services/chat-ai/models/index.html
   (Devstral 2 123B, 256K context, linked model repository) — **archive a
   snapshot, this page changes frequently**
3. Doosthosseini, Decker, Nolte & Kunkel (2026), "SAIA: a seamless Slurm-native
   solution for HPC-based services", *The Journal of Supercomputing* 82(7):403 —
   https://doi.org/10.1007/s11227-026-08508-3
   (GWDG's requested citation; supports the marginal-cost argument)
4. GWDG press release 4/2021 — https://gwdg.de/about-us/press-releases/2021/press-release-4-2021/
   (PUE up to 1.03 on Emmy; 2021, Emmy-specific — GWDG did not answer the PUE
   question in the 2026-08-22 reply, so this remains the only source and its
   Emmy-specificity remains a caveat)
4a. **GWDG support correspondence, 2026-08-22** — personal communication.
   Source for: 2 × H200 serving Devstral 2 123B, FP8 precision, carbon-neutral
   operation; and for the *refusal* to release throughput/concurrency data.
   Cite as personal communication with the date; keep the mail archived with
   the thesis artifacts, since three constants in the table above rest on it
   and none is otherwise published.
5. GWDG SAIA API documentation — https://docs.hpc.gwdg.de/services/ai-services/saia/index.html
6. NVIDIA H200 datasheet — obtain the official PDF from nvidia.com
   (141 GB HBM3e, 4.8 TB/s, 1,979 TFLOP/s FP8 dense, up to 700 W SXM). This
   replaces the H100 PCIe datasheet as the hardware source for every
   coefficient; the H100 figures are retained only in the superseded-values
   note of section 3.

**To verify before citing — methodology**

> These were drawn from background knowledge, not from documents opened and
> checked directly. Open each one, confirm authors, year, venue and the
> specific figures, and only then add it to the bibliography.

Grouped by the part of the model each one supports, so a reader can check the
derivation clause by clause rather than against an undifferentiated list.

*§3, the time model — where the coefficients come from*

7. **Kaplan et al. (2020)**, "Scaling Laws for Neural Language Models",
   arXiv:2001.08361 — the 2N FLOPs-per-token approximation behind `T_prefill`.
8. **Chowdhery et al. (2022)**, "PaLM: Scaling Language Modeling with
   Pathways", arXiv:2204.02311 — introduces Model FLOPs Utilization, the metric
   `model_flop_utilization = 0.4` instantiates.
9. **Pope et al. (2023)**, "Efficiently Scaling Transformer Inference", MLSys,
   arXiv:2211.05102 — **the load-bearing reference for this whole section.**
   It is the canonical statement that prefill is compute-bound while decode is
   memory-bandwidth-bound, which is why `e_in` is derived from FLOPs and
   `e_out` from `weight_bytes / HBM bandwidth`. Without it the most distinctive
   choice in the model is unsupported.
10. **Yu et al. (2022)**, "Orca", OSDI, and **Kwon et al. (2023)**, "Efficient
    Memory Management for Large Language Model Serving with PagedAttention"
    (vLLM), SOSP, arXiv:2309.06180 — continuous batching: why one weight sweep
    through memory serves `B` requests, i.e. the `/ concurrency` term in
    `e_out`.

*§3 → §4, turning time into joules*

11. **Patterson et al. (2021)**, "Carbon Emissions and Large Neural Network
    Training", arXiv:2104.10350 — the power × time × PUE methodology this
    document follows, and the source of the convention that PUE is applied to
    the datacentre term only.
12. **Strubell, Ganesh & McCallum (2019)**, "Energy and Policy Considerations
    for Deep Learning in NLP", ACL — the foundational framing.
13. **Henderson et al. (2020)**, "Towards the Systematic Reporting of the
    Energy and Carbon Footprints of Machine Learning", JMLR — reporting
    conventions; relevant to what section 4's constants table must disclose.

*Contradicts an assumption made here — cite it, do not avoid it*

14. **Patel et al. (2024)**, "Splitwise: Efficient Generative LLM Inference
    Using Phase Splitting", ISCA, arXiv:2311.18677 — reports that prefill and
    decode draw **materially different power**, decode being lower because a
    memory-bound phase leaves the compute units idle. This model applies one
    constant `P_node` to both phases, so `e_out` is likely **over**estimated —
    and `e_out` dominates, since an output token costs ~3.5× an input token
    here. The direction of the error is therefore known, which makes it a
    conservative estimate rather than an unquantified assumption. State it that
    way in the threats to validity; an examiner who knows this paper will
    otherwise find the gap unaided.

*Measured per-token energy, for a plausibility check against §3's coefficients*

15. **Samsi et al. (2023)**, "From Words to Watts: Benchmarking the Energy Costs
    of Large Language Model Inference", IEEE HPEC, arXiv:2310.03003 — measured
    joules per token for LLaMA on A100/V100. The closest thing to an
    independent check on `e_in ≈ 0.26 J` and `e_out ≈ 0.91 J`; different
    hardware and model size, so compare orders of magnitude, not values.
16. **Luccioni, Jernite & Strubell (2024)**, "Power Hungry Processing: Watts
    Driving the Cost of AI Deployment?", ACM FAccT, arXiv:2311.16863 — measured
    per-inference energy across tasks and models. Also the strongest published
    challenge to token counts as a proxy for work (section 8's limitation).
17. **ML.ENERGY Leaderboard** — https://ml.energy/leaderboard

*§5, the host-energy term ([H5])*

18. **Khan et al. (2018)**, "RAPL in Action: Experiences in Using RAPL for
    Power Measurements", ACM TOMPECS — accuracy and limitations of the powercap
    counters `internal/hostenergy` and `cmd/runtime` both read. Required
    reading before any RAPL figure is quoted as a measurement.

*§6, the Go vs. Python comparison*

19. **Pereira et al. (2017)**, "Energy Efficiency across Programming
    Languages", SLE; extended in *Science of Computer Programming* (2021) — the
    canonical cross-language energy ranking. The reference point the measured
    Go/Python ratio should be held against.

*§4, carbon accounting and the marginal-cost framing*

20. **GHG Protocol Scope 2 Guidance** (2015) — the dual location-based /
    market-based reporting rule this document follows.
21. **Gupta et al. (2021)**, "Chasing Carbon: The Elusive Environmental
    Footprint of Computing", HPCA, and **Wu et al. (2022)**, "Sustainable AI:
    Environmental Implications, Challenges and Opportunities", MLSys —
    operational vs. embodied emissions; support for excluding embodied
    manufacturing while saying so explicitly.
22. **Umweltbundesamt** — German grid CO₂ intensity, most recent annual figure.

Optional if regulatory or plausibility context is needed: Google's 2025 Gemini
inference-energy paper (~0.24 Wh per median prompt) and EU AI Act Annex XI
energy-documentation requirements. Same verification treatment applies.

---

## Open Questions

GWDG support replied on **2026-08-22**. Section 4 and the constants file are
updated; the superseded defaults are noted in section 3.

- [x] ~~GPU type and count serving Devstral 2 123B~~ — **answered:** 2 × H200
      for this model (KISSKI in general is 4 × H100 PCIe, confirmed separately;
      see the reading note in section 4)
- [x] ~~Model precision~~ — **answered:** FP8. FP16 is the house default,
      Devstral one of the FP8 exceptions. `e_out` halved as predicted.
      Residual: W8A8 vs. weight-only W8A16, which moves `e_in` by 2× and is now
      swept rather than assumed.
- [ ] Typical node power draw under inference load — **not answered.** Stays at
      the assumed 1700 W and is swept over 1400–2550 W. Worth one follow-up:
      it is the only remaining open item that GWDG neither declined nor is
      likely to consider sensitive.
- [x] ~~Typical aggregate output-token throughput and concurrent request
      count~~ — **declined:** "hierzu haben wir zwar Daten, dürfen diese aber
      leider nicht ohne Weiteres rausgeben." This does not become available by
      asking again, so `B` is permanently a swept parameter. Do not present the
      sweep as provisional.
- [ ] Current measured PUE for the hall hosting the inference platform — **not
      answered.** The 2021 Emmy press release remains the only source; keep its
      Emmy-specificity in threats to validity.
- [x] ~~Grid CO₂ intensity used for reporting, or renewable procurement
      status~~ — **answered:** "wir sind tatsächlich CO2-neutral". Reported as
      a market-based intensity of 0 alongside the location-based German grid
      average, per GHG Protocol Scope 2 dual reporting.
- [x] ~~Whether vLLM prefix caching is enabled and `cached_tokens` is passed
      through the SAIA gateway~~ — **answered by API experiment (2026-07-04):**
      `usage` reports `prompt_tokens` / `completion_tokens` / `total_tokens`
      consistently in both streaming and non-streaming mode (streaming needs
      `stream_options: {include_usage: true}`), but `prompt_tokens_details` is
      **always `null`** — no cached/uncached breakdown is obtainable. Whether
      caching is *active* server-side remains unknown; if it is, the estimate
      is an overestimate. Ask GWDG only if the distinction becomes load-bearing.

Remaining follow-up, if any: node power and current PUE. Neither is likely to
move the result far (±50% and ±16% respectively across their swept ranges,
against a factor-2.6 range on `B`), so neither is worth blocking on.

---

## TODO

> **Split 2026-07-05.** Everything requiring a code change now lives in
> **section H (Evaluation) of [`TODO.md`](../TODO.md)**, so implementation work
> is tracked in one place and cannot drift between two lists. What remains
> here is thesis-writing and analysis work that touches no code.

Code-side work, tracked in `TODO.md`:

| Item | What |
|---|---|
| [H1] | Ingest `meta.json`; record function identity + grouping metadata (blocks per-function `N*` and per-bucket reporting) |
| [H1a] | Persist per-test outcome and failure kind, so packaging failures are separable from behavioural ones |
| [H2] | Persist run metrics to disk as they complete, durable against any error (replaces the JSONL/`CallRecord` item) |
| [H3] | Record the model per stage, for per-model coefficients |
| ~~[H4]~~ | ~~Energy-model script over the run logs~~ — **done**: `go run ./cmd/energy runs/*.jsonl`, constants in `evaluation/energy.config.json` |
| [H5] | Account for or bound local compute energy (build/test/Floci) |
| [H6] | Go vs. Python runtime measurement harness reusing the fixture payloads |
| [H7] | Verify token accounting across connector-internal retries |

Resolved while writing this revision:

- ~~Verify whether SAIA passes through `cached_tokens`~~ — answered, see
  [Open Questions](#open-questions). It does not.
- ~~Confirm retried and failed calls are logged, not silently dropped~~ —
  confirmed in code: `RecordLLMCall` precedes the error check, and retries
  accumulate into the same task's counters. One residual check is [H7].
- ~~Add `CallRecord` and JSONL logging~~ — superseded: per-stage aggregates
  are mathematically sufficient (see section 5); the real gaps are [H1]/[H2].

**Analysis** (run after the experiment; the tooling exists, the numbers do not)
- [ ] Compute average energy per translation and the per-stage breakdown — `go run ./cmd/energy runs/*.jsonl`
- [ ] Report the share of energy spent on retries / the repair loop — same command; keep `analysis.repair_stages` in the config in step with the pipeline's task ids
- [ ] Compute `N*` per function; plot the distribution across all ~95 — needs [H6]'s measurements, then `-runtime`; `-json` feeds the plot
- [ ] Build the sensitivity table of Section 8 — `-sweep` emits it directly
- [ ] Run the method-validation comparison of Section 7

**Thesis text**
- [ ] Marginal vs. shared cost section, with the counter-argument paragraph
- [ ] Threats to validity section
- [ ] Constants table with sources, marking GWDG-provided vs. assumed values —
      the section 4 table is now marked; carry the marking through verbatim
- [ ] Verify sources 7–10 before citing
- [x] ~~Replace defaults with GWDG values once the reply arrives (Section 4)~~ —
      done 2026-08-22; `evaluation/energy.config.json` and section 4 updated,
      superseded figures recorded in section 3
- [ ] Report both CO₂ intensities (location- and market-based) and say why,
      rather than quoting GWDG's carbon-neutral status alone
- [ ] Present the `B` sweep as the *result* for concurrency, not as a pending
      unknown — GWDG declined to release the data, and that refusal is itself
      reportable