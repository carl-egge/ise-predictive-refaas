# Energy Modelling Approach

Reference document for the energy-consumption estimation of the Python→Go
translation pipeline. Records the decisions, formulas, constants and open
tasks agreed for the thesis.

**Status:** constants are provisional defaults. A request for the real values
has been sent to GWDG support and is pending. See [Open Questions](#open-questions).

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
CO2e       [g] = E_facility / 3.6e6 × I_grid
```

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

With 4 × H100 PCIe (756 TFLOP/s BF16 dense each), MFU 0.40, 123e9 params:

```
T_prefill ≈ 1210e12 / 246e9 ≈ 4,900 tokens/s
e_in      ≈ 2000 W / 4900   ≈ 0.41 J per input token
```

**`e_out` — energy per output token**

```
t_step = weight_bytes / (n_gpu × hbm_bw × bw_eff)
e_out  = P_node × t_step / B
```

With 246 GB weights (123B × 2 bytes, BF16), 4 × 2 TB/s at 75% efficiency:

```
t_step ≈ 246e9 / 6.0e12 ≈ 41 ms
```

| Concurrency `B` | `e_out` (J/token) |
|---|---|
| 8 | 10.3 |
| 16 | 5.1 |
| **32 (central)** | **2.6** |
| 64 | 1.3 |
| 128 | 0.64 |

`B` is the single largest unknown. Everything else is pinned down by hardware
documentation.

### Worked example

5 calls averaging 6,000 prompt / 1,500 output tokens:

```
prefill: 30,000 × 0.41 =  12.3 kJ
decode:   7,500 × 2.6  =  19.5 kJ
total:                    31.8 kJ × PUE 1.05 ≈ 33 kJ ≈ 9.3 Wh
```

Range over `B` ∈ [8, 128]: roughly **4–20 Wh per translation**.

---

## 4. Constants

Provisional until GWDG replies. Any value replaced by GWDG data should be
marked as such in the thesis constants table.

> **These values live in [`energy.config.json`](energy.config.json)**, which
> `go run ./cmd/energy` reads. The tool has no compiled-in fallback, so that
> file is the single source of truth: update it when GWDG replies, and every
> figure recomputes. The table below documents the same values with their
> provenance for the write-up.

| Parameter | Symbol | Default | Source |
|---|---|---|---|
| GPUs per node | `n_gpu` | 4 | KISSKI inference platform page |
| GPU model | — | H100 PCIe 80 GB HBM2e | KISSKI inference platform page |
| GPU TDP | — | 350 W | NVIDIA H100 PCIe datasheet |
| Node power under load | `P_node` | 2000 W | 4 × 350 W GPU + ~600 W host/network |
| Peak BF16 throughput | `peak_flops` | 756 TFLOP/s per GPU | NVIDIA H100 PCIe datasheet |
| Model FLOP utilization | `mfu` | 0.40 | conventional inference-serving assumption |
| Parameters | `n_params` | 123e9 | model card |
| Bytes per parameter | — | 2 (BF16) | **assumption — verify** |
| HBM bandwidth | `hbm_bw` | 2.0e12 B/s per GPU | H100 PCIe datasheet |
| Achieved bandwidth fraction | `bw_eff` | 0.75 | standard assumption |
| Concurrency | `B` | 32 (range 8–128) | **assumption — largest uncertainty** |
| PUE | `PUE` | 1.05 (range 1.03–1.2) | GWDG press release 4/2021 (Emmy) |
| Grid CO₂ intensity | `I_grid` | 363 gCO₂e/kWh | Umweltbundesamt, German average |

Two assumptions dominate the result: **precision** (FP8 instead of BF16 would
roughly halve `e_out`) and **concurrency `B`**.

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
   Devstral. Note that ML.ENERGY measures H100/B200 **SXM** parts (700 W, HBM3)
   whereas GWDG runs H100 **PCIe** (350 W, HBM2e) — adjust accordingly.
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

| Parameter varied | Range | Effect on E per translation |
|---|---|---|
| Concurrency `B` | 8 → 128 | 20 Wh → 4 Wh |
| Precision | BF16 → FP8 | ÷ ~2 |
| MFU | 0.30 → 0.50 | prefill term ±25% |
| PUE | 1.03 → 1.2 | +16% |

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

- Unknown server concurrency (`B`) — the dominant uncertainty.
- Assumed model precision (BF16 vs. FP8).
- Token counts used as a proxy for computational work.
- Prefix caching, if active, makes the estimate conservative — and it cannot
  be quantified here, since SAIA does not expose `cached_tokens` (verified).
- Marginal-cost framing excludes idle and embodied energy.
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
   (PUE up to 1.03 on Emmy; 2021, Emmy-specific — replace with GWDG's reply)
5. GWDG SAIA API documentation — https://docs.hpc.gwdg.de/services/ai-services/saia/index.html
6. NVIDIA H100 PCIe datasheet — obtain the official PDF from nvidia.com

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

Awaiting GWDG support reply. On receipt, update Section 4 and mark the
superseded defaults.

- [ ] GPU type and count serving Devstral 2 123B (assumed: 4 × H100 PCIe 80 GB)
- [ ] Model precision — BF16 or quantized (FP8 / INT4)? **Halves `e_out` if FP8**
- [ ] Typical node power draw under inference load
- [ ] Typical aggregate output-token throughput and concurrent request count
      → **would eliminate `B` entirely**, reducing `e_out` to `P_node / T_aggregate`
- [ ] Current measured PUE for the hall hosting the inference platform
- [ ] Grid CO₂ intensity used for reporting, or renewable procurement status
- [x] ~~Whether vLLM prefix caching is enabled and `cached_tokens` is passed
      through the SAIA gateway~~ — **answered by API experiment (2026-07-04):**
      `usage` reports `prompt_tokens` / `completion_tokens` / `total_tokens`
      consistently in both streaming and non-streaming mode (streaming needs
      `stream_options: {include_usage: true}`), but `prompt_tokens_details` is
      **always `null`** — no cached/uncached breakdown is obtainable. Whether
      caching is *active* server-side remains unknown; if it is, the estimate
      is an overestimate. Ask GWDG only if the distinction becomes load-bearing.

If no reply within ~2 weeks, proceed with the documented defaults and note the
attempt in the thesis.

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
- [ ] Constants table with sources, marking GWDG-provided vs. assumed values
- [ ] Verify sources 7–10 before citing
- [ ] Replace defaults with GWDG values once the reply arrives (Section 4)