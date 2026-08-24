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

- A translation is a **task graph**, not a fixed call sequence: the canonical
  evaluation pipeline (`default.json`) runs `cleaner` → `coder` →
  `goBuilder` ⇄ `gollmRecovery` (fixer) → `goTester` ⇄ `testRecovery`
  (realign) → `testRecoveryBuild`. Retries and recovery hops mean the number
  of LLM calls per function is **variable and outcome-dependent** — which is
  exactly why per-stage attribution matters more than a per-call average.
- Every stage's LLM parameters (`model_name`, `temperature`, …) can be
  overridden **per task**, so a run is not necessarily single-model. Energy
  coefficients are per-model; see [H3] in `TODO.md`.
- The pipeline also performs **local compute** — `go mod init/tidy`,
  `go build`, one `./fn` execution per fixture per test round, optionally
  Floci containers — which the LLM-only energy model does not currently
  capture. See [Local compute](#local-compute-non-llm-pipeline-energy).
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

### Local compute (non-LLM pipeline energy)

`E_translation` as defined in section 3 counts LLM inference only. The
pipeline additionally runs, per attempt: `go mod init`/`go mod tidy` (network
+ CPU), `go build`, and one `./fn` process per fixture per test round — plus
Floci emulator containers when the integration route is enabled. On repeated
repair loops this is not obviously negligible relative to a few thousand
tokens.

`per_task[...].duration` already gives the wall-clock of the build and test
stages, so this can be bounded cheaply: multiply measured stage duration by a
measured host power draw, or measure a representative build/test round with
`perf stat` (the same instrument used in section 6) and scale. Decide between
*measure* and *declare as an excluded, bounded term* — either is defensible,
silently ignoring it is not. → [H5]

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
| Concurrency `B` | 8 → 128 | ×2.6 → ×0.6 (10 Wh → 3 Wh) | **assumed; declined by GWDG — permanent** |
| Node power `P_node` | 1400 → 2550 W | ×0.82 → ×1.50 | **assumed; unanswered** |
| Prefill peak | 989 → 1979 TFLOP/s | ×1.45 → ×1.00 | W8A16 vs W8A8 ambiguity inside the FP8 answer |
| Precision | FP8 → BF16 | ×1.00 → ×1.55 | **settled: FP8** — kept as a counterfactual |
| MFU | 0.30 → 0.50 | ×1.15 → ×0.91 | assumed |
| PUE | 1.03 → 1.2 | ×0.98 → ×1.14 | assumed; unanswered |

The multipliers above are from the `run-20260807-132133` archive; they shift
slightly with the prompt/output token mix of a given run, since `B` and
precision act on the decode term while MFU and the prefill peak act on
prefill. Regenerate the table from the batch actually reported.

Two rows changed character with the reply. **Precision** is no longer an
unknown — it is a resolved constant, and its row now shows what the
confirmation was worth (a 1.55× overestimate avoided). **Concurrency** is no
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
- Token counts used as a proxy for computational work.
- Prefix caching, if active, makes the estimate conservative — and it cannot
  be quantified here, since SAIA does not expose `cached_tokens` (verified).
- Marginal-cost framing excludes idle and embodied energy — and GWDG's
  carbon-neutral status does not change that, since it covers operational
  emissions only.
- **Local pipeline compute (builds, test executions, Floci containers) is
  excluded from `E_translation`**, or included only as a bounded estimate —
  state which, and give the bound (see [Local compute](#local-compute-non-llm-pipeline-energy)).
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

> These were drawn from background knowledge and an automated research pass,
> not from documents opened and checked directly. Open each one, confirm
> authors, year, venue and figures, and only then add it to the bibliography.

7. Kaplan et al. (2020), "Scaling Laws for Neural Language Models",
   arXiv:2001.08361 — the 2N FLOPs-per-token approximation behind `e_in`
8. Luccioni, Jernite & Strubell (2024), "Power Hungry Processing", ACM FAccT 2024
9. ML.ENERGY Leaderboard — https://ml.energy/leaderboard
10. Umweltbundesamt — German grid CO₂ intensity, most recent annual figure

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