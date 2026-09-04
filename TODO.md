# Audit & TODO List — Python→Go Serverless Translation Pipeline (ReFaaS)

## Progress overview

**A. Open bugs**
- [x] [A1] Inverted pass/fail logic in `SimilarityValidation`
- [x] [A2] `compareMap` returns early, skipping sibling keys
- [x] [A3] Unchecked type assertions in the validator panic and abort the conversion
- [x] [A4] Broken `go.mod` recovery in `GolangBuilder.doBuild`
- [x] [A5] Uploaded/per-test environment variables never reach test execution
- [x] [A6] `UndeterministicResults` JSON tag is semantically inverted
- [x] [A7] `compilePipeline` loops forever on unknown or cyclic task references
- [x] [A8] `/reconfigure` races against an in-flight conversion
- [x] [A9] `pollHandler` mutates the results map under a read lock
- [x] [A10] `log.Fatal` in library code kills the whole service on config mistakes
- [x] [A11] Tasks with omitted `maxRetryCount` silently never execute
- [x] [A12] `LogResponse` panics when `model_name` is absent
- [x] [A13] Ollama connector goroutine leak on mid-stream errors
- [x] [A14] Gemini connector panics on safety-blocked/empty responses
- [x] [A15] `BasicLLMDeploymentReader` nondeterministic "main" pick / empty content
- [x] [A16] `Metrics.AddMetric` clobbers `StartTime` to the zero value
- [x] [A17] Zip ingestion: last-`.py`-wins, macOS junk entries, CRLF `.env` parsing
- [x] [A18] Test harness shares stdout with the translated function; any output corrupts the envelope **(P0)**
- [x] [A19] `Metrics.TestOutcomes` accumulates across retry rounds, inflating persisted test counts **(P0)**

**B. Software quality**
- [x] [B1] Unify the two output-comparison implementations (+ type-shape-only mode)
- [x] [B2] Tests for orchestration and validation semantics
- [X] [B3] Error taxonomy unused; raw error strings muddle diagnostics
- [X] [B4] Job status conflates "in progress" and "unknown"
- [x] [B5] Observability: chatlog correlation + per-stage metrics **(P0)**
- [X] [B6] Documentation drift on prompt wiring and Floci examples
- [X] [B7] `goTester` runs `go run .` in the service CWD when `WorkingDir` unset
- [x] [B8] `Metrics.Issues` records the same error once per recursion level
- [x] [B9] `go test ./...` is not green: a live, billable ChatAI test never skips

**C. Pipeline features**
- [x] [C1] Structured per-test failure evidence into the repair/align loop **(P0)**
- [x] [C2] Fix the dev pipeline's test-failure dead-end (keep it short)
- [x] [C3] Deterministic `go.mod`; LLM returns only `main.go`
- [x] [C4] Deterministic Go post-processing (package clause, goimports)
- [x] [C5] Detect repair-loop stagnation (narrowed — see item)
- [x] [C6] Validate uploads and fixtures before spending LLM tokens
- [x] [C7] Deterministic and complete test context for prompts
- [x] [C8] Python feature pre-scan feeding the translate prompt — **shipped 2026-08-24 as `internal/pyscan`, jointly with [I3]**
- [~] [C9] ~~Support multi-file Python inputs~~ — **dropped**, single-file is the contract
- [x] [C10] Unified fixture schema + per-job validation routing (goTester vs. flociTester)
- [x] [C11] Prevent AWS leakage: always resolve to the Floci harness
- [x] [C12] Unify the two test JSON shapes into one canonical fixture schema

**D. Prompts**
- [x] [D1] Convert prompt: fix broken few-shot; state the harness contract **(P0)**
- [x] [D2] Align prompt: failure evidence + checkable equivalence **(P0)**
- [x] [D3] Fix-errors prompt: structured compiler errors, minimal-change directive
- [x] [D4] All prompts: remove "step by step" vs. "JSON only" contradiction
- [ ] [D5] Document stage: scope to translation-relevant facts (or merge with summarize)

**E. Small-model robustness**
- [x] [E1] Per-task output schemas for code-producing stages
- [x] [E2] Deterministic JSON extraction fallback before failing a parse
- [x] [E3] Vary sampling on resample-style retries
- [x] [E4] Truncate feedback to the first compiler errors
- [x] [E5] Stop sending junk params to Ollama; set explicit `num_predict`

**F. Fault tolerance**
- [x] [F1] Per-test and per-build-command timeouts **(P0)**
- [x] [F2] Retry transient LLM API failures at the connector
- [x] [F3] Detect truncated LLM responses via finish/done reason
- [x] [F4] Upload handler must not block on a full queue
- [x] [F5] Configurable minimum delay between LLM calls (rate-limit throttle)
- [ ] [F6] Stop logging API keys in plaintext at startup

**G. Efficiency & token economy**
- [x] [G1] Build once, run the binary per test
- [ ] [G2] Right-size the repair/align prompt payloads
- [ ] [G3] Make the cleaner stage skippable and measure its contribution
- [ ] [G4] Reuse the Go module cache across builds and jobs
- [ ] [G5] Experiment: continued LLM conversation across stages

**H. Evaluation** (energy study — see [evaluation/EVALUATION.md](evaluation/EVALUATION.md) and [evaluation/EVALUATION_DATASET.md](evaluation/EVALUATION_DATASET.md))
- [x] [H1] Ingest `meta.json` and record function identity + grouping metadata per job **(P0 — blocks per-function `N*` and all per-bucket reporting)**
- [x] [H1a] Emit a per-function result summary (outcome + failure kind) alongside the metrics
- [x] [H2] Persist run metrics to disk as jobs complete **(P0 — a batch currently survives neither a crash, a restart, nor a `/reconfigure`)**
- [x] [H3] Record the model per stage for per-model energy coefficients
- [x] [H4] Energy-model script over the run logs, constants in one config file
- [x] [H5] Account for local compute energy (build/test/Floci) — **measured per job via RAPL since 2026-09-04**
- [x] [H6] Go vs. Python runtime measurement harness — **shipped 2026-08-24 as `cmd/runtime` + `evaluation/harness`**
- [ ] [H7] Verify token accounting across connector-internal retries
- [x] [H8] `cmd/energy` reports a coefficient assumption for stages that consumed no tokens
- [x] [H9] Integrate the GWDG infrastructure reply into the energy constants
- [x] [H10] `cmd/runtime` invocation timeout — **implemented 2026-09-02**; budget scales with N, kills the process group, reports `TIMEOUT`

**I. Prediction & candidate selection** (new 2026-08-24, decisions settled the same day — *nothing here exists in code yet*)
*Order: [C8]/[I3] scanner + [H6] runtime harness → [I1] one run (+[I11] in parallel) → [I2] → [I4]–[I7].*
- [x] [I3] Deterministic ex-ante feature extractor — **shipped 2026-08-24**; cc 92/95 exact vs `meta.json` (r=0.9998), 56 columns
- [x] [I1] Labelled corpus: one full `evaluation_set` pass; label = `all_tests_passed` — **use `run-20260831-170746`, 42/95 (44.2%)**; first pass 11/95 kept as provenance
- [x] [I11] Leakage audit — **done 2026-08-26**: 16 functions in 7 groups, effective corpus size **86 not 95**; repo grouping alone would miss 4 of the 7
- [x] [I1a] Bare-metal run checklist — done 2026-08-30 on the Ubuntu host (real RAPL, venv, Floci, module cache)
- [x] [I2] Signal check — **answered 2026-08-31 on the second pass: criterion PASSES.** Base rate 44.2%; complexity still no signal (A vs D+ p=0.37), AWS strong (27.6% vs 70.3%, p=0.0001). Modelling is worth doing; beat the skip-AWS baseline, not always-translate
- [ ] [I4] One feature/label table every method and baseline consumes **(P0)**
- [ ] [I5] Baselines: always-translate, never, majority, `cc` threshold, infeasibility rule list
- [ ] [I6] Candidate methods: M1 logistic regression + M2 random forest (M3 LLM-judge optional; MLP deferred) **(P0)**
- [ ] [I7] Split protocol (repeated stratified *group* k-fold, not one holdout) + net energy vs. always-translate **(P0)**
- [ ] [I8] Measure the predictor's own energy in the same units as the pipeline
- [ ] [I9] Secondary objective (energy-saving potential): compose from `P(success)` × [H6]'s ΔE — **[H6] is done; unblocked**
- [ ] [I10] Service integration: `internal/predictor` + `predictGate` converter, off by default

---

> Produced by a full read of the orchestration (`internal/pipeline`), all prompt templates and
> their embed wiring, the build/test harness (`internal/builder`), the LLM connectors, the
> service layer, the Floci stage, the paper fixture set (`examples/paper/f1–f14`), and the
> recorded failure evidence in `examples/metrics/`. Items are grouped by category (A–I) and
> ordered by priority within each group. Checkboxes track implementation status.

## Executive summary

The single biggest threat to translation success rate is not the LLM stages — it is the
**validation and feedback machinery around them**. The default string-similarity validator is
inverted (`sim < 0.9` passes *dissimilar* output, `internal/builder/validator.go`), the
JSON-aware validator has an early-return bug that skips sibling keys and panics on type
mismatches, and together these make the pipeline's pass/fail signal unreliable in both
directions — a translated function can "pass" while wrong and "fail" while right, which
corrupts every retry decision built on top. Second, the repair loops are starved of
information: `goTester` reduces all failure detail to `"N tests failed"`, and the align prompt
(`3-stage-align.md`) references neither `{{ .issue }}` nor any test data, so the realignment
stage is asked to fix behavior it has never seen fail. Third, the embedded default pipeline
(`default.yaml`) structurally cannot recover from test failures — validation failure retries
the same `goBuilder` task on unchanged code, so `realign` is unreachable and retries are pure
waste (per maintainer this file is a deliberately short dev pipeline, which softens but does
not remove the issue — see the reframed [C2]; the canonical evaluation pipeline is
`default.json`). Fourth, the recorded metrics (`examples/metrics/`) show that the dominant real failure
classes — missing `package` clauses, malformed `go.mod` files repeated identically across four
fix attempts — are exactly the ones a deterministic pre/post-processing step would eliminate
without any LLM call, which is also the cheapest way to help a ~30B model. Finally, the wired
translate prompt's own few-shot example teaches the model to emit non-JSON response bodies
(`fmt.Sprintf("%v", map…)`), which directly contradicts what the test fixtures (e.g. `f2`:
`"body": "{\"result\": 3}"`) expect. Fixing the validator, feeding structured failure evidence
to the fix/align stages, regenerating `go.mod` deterministically, and repairing the convert
few-shot are the four highest-leverage changes; most are small, local patches.

---

## A. Open bugs

> **Status 2026-07-04 (branch `validator-2`):** all A items are now resolved. Second pass:
> [A15]'s remaining leftover-key pruning was folded into [C3] by maintainer decision, and
> [A17]'s multi-source policy was decided (reject at upload) and implemented, with tests in
> `internal/inputhandler/reader_test.go`. Regression tests for the first pass were added in
> `internal/builder/validator_test.go`, `internal/domain/types_test.go`,
> `internal/pipeline/pipeline_io_test.go`, and `internal/pipeline/pipeline_default_test.go`.
> Verified with `go build ./...`, `go vet ./...`, `gofmt`, `go test ./...`, and
> `go test -race` on the service + new tests.
>
> **Status 2026-08-07 (batch run `run-20260807-132133`):** [A1]–[A17] remain resolved, but the
> first full `function_set` pass surfaced two new bugs. [A18] — the test harness shares stdout
> with the translated function — fails correct translations *and* feeds the repair loop a defect
> that does not exist, which is the most expensive possible failure mode now that realign is
> ~48% of inference energy. [A19] — per-test outcomes accumulate across retry rounds — writes
> wrong test counts into the primary evaluation artifact and into `cmd/energy`'s grouped
> reporting. Both were P0 and **both are fixed and verified the same day**: pf13 and pf9 flip to
> fully passing and pf10 gains a case ([A18]); pf14's 4 validation rounds now record 3 outcomes
> for 3 fixtures instead of 15 ([A19]). The batch archived before those fixes keeps the numbers
> it was recorded with — see [A19]'s status note before reading `test_outcomes` out of
> `runs/run-20260807-132133.jsonl`.

### [x] [A1] Inverted pass/fail logic in `SimilarityValidation`
- Category: Bug
- Affected component(s): `internal/builder/validator.go` (`SimilarityValidation.validate` / `validateUndeterministic`)
- Problem / current state: `validate` returns `sim < 0.9`. The overlap coefficient returns 1.0 for identical strings, so an output identical to the expected fixture *fails* and a completely different output *passes*. This is the default validator when no `strategy` is configured, and it is the `fallBackValidation` inside `JsonAwareSimilarityValidation` whenever either side fails to parse as JSON (truncated output, malformed fixture, empty output). Concrete consequence: uploading `examples/input/1-floci-s3.zip` (whose test file is in Floci format, so `TestFile.Output` is empty) makes `goTester` *pass* spuriously. The sibling method `JsonAwareSimilarityValidation.fallback` has the *correct* direction, confirming the intent.
- Proposed change: Flip both comparisons to `sim >= threshold` (0.9 deterministic / 0.6 nondeterministic); add a unit test asserting identical strings pass and disjoint strings fail.
- Why: The validator is the pipeline's ground truth; while inverted, retries/recovery trigger on correct output and skip wrong output. Based on the overlap-coefficient semantics of `github.com/adrg/strutil` (1.0 = identical).
- Architecture impact: Local | Effort: S | Priority: **P0**

### [x] [A2] `compareMap` returns early on the first nested object, skipping sibling keys
- Category: Bug
- Affected component(s): `internal/builder/validator.go` (`JsonAwareSimilarityValidation.compareMap`)
- Problem / current state: In the `map[string]interface{}` and object-vs-JSON-string branches, the function does `return vs.compareMap(...)` inside the key loop instead of continuing. Since Go map iteration order is randomized, if the first key visited is a nested object that matches, all remaining expected keys are silently accepted — validation becomes nondeterministically lenient.
- Proposed change: Replace the `return` with `if !vs.compareMap(...) { return false }` and continue the loop; same for the object-vs-string branch.
- Why: Removes a nondeterministic false-pass path; based on Go's randomized map iteration order.
- Architecture impact: Local | Effort: S | Priority: **P0**

### [x] [A3] Unchecked type assertions in the validator panic and abort the whole conversion
- Category: Bug
- Affected component(s): `internal/builder/validator.go` (`actualJSON["response"].(map[string]interface{})`; `compareSimple`'s assertions when expected and actual have different JSON types)
- Problem / current state: If the handler returns a non-object, or expected has `"statusCode": 200` (float64) while actual has `"statusCode": "200"` (string), a type assertion panics. The pipeline-level `recover()` turns this into a run-ending error — the request fails outright instead of the test failing and routing to `realign`.
- Proposed change: Comma-ok assertions everywhere in `validate`/`compareSimple`/`compareMap`; treat a type mismatch as a normal comparison failure (respecting the `valueValidation` flag).
- Why: A common LLM mistake (stringified numbers, scalar responses) currently kills the job instead of triggering the repair loop that exists precisely for that case.
- Architecture impact: Local | Effort: S | Priority: **P1**

### [x] [A4] Broken `go.mod` recovery in `GolangBuilder.doBuild`
- Category: Bug
- Affected component(s): `internal/builder/builder.go`
- Problem / current state: On a build error containing `" unknown revision"`, the code deletes `go.mod` from `BuildFiles` and rewrites `code.BuildCmd`, but then re-runs the *old failed command* in the *same directory where the bad `go.mod` still exists on disk* — the new command list is never executed in this attempt. The trigger string also misses the actually-observed failure modes (`errors parsing go.mod`, `unknown directive`, `invalid version` — see `examples/metrics/metrics-20260701122938.json`, where the same `go.mod` error failed four consecutive times).
- Proposed change: On any `go.mod`-related failure, delete the file from disk *and* from `BuildFiles`, then execute the full replacement command list. (Superseded entirely if [C3] is implemented.)
- Why: The recorded metrics prove this recovery path fires on real runs and currently does nothing, burning all retries on an unfixable state.
- Architecture impact: Local | Effort: S | Priority: **P1**

### [x] [A5] Uploaded/per-test environment variables never reach test execution
- Category: Bug
- Affected component(s): `internal/translator/readers.go` (both readers construct a fresh `DeploymentPackage` without copying `original.Env`); `internal/domain/types.go` (`GetTestFiles` sets `file.Env = dp.Env` *after* unmarshalling, clobbering the fixture's own `"env"` field)
- Problem / current state: (1) The first package-mode LLM stage (`cleaner`) replaces `WorkingPackage` with a reader-built package that drops `Env`, so `.env` entries from the uploaded zip (e.g. `AWS_ENDPOINT_URL` needed by `examples/input/1-floci-s3`) are lost before `goTester` runs. (2) Even if `dp.Env` survived, per-test `"env"` overrides declared in a `TestFile` JSON are overwritten by the package-level value — the documented feature is dead code.
- Proposed change: Copy `original.Env` in both `MakeDeploymentFile` implementations; in `GetTestFiles`, merge `dp.Env` and the fixture's own `Env` (fixture entries last, so they win — Go ≥1.19 `exec.Cmd` keeps the last duplicate).
- Why: Env-dependent functions (any AWS/HTTP function needing endpoint config — half the paper set) currently fail or hit real endpoints in `goTester` regardless of translation quality.
- Architecture impact: Local | Effort: S | Priority: **P1**

### [x] [A6] `UndeterministicResults` JSON tag is semantically inverted
- Category: Bug
- Affected component(s): `internal/domain/types.go` (`UndeterministicResults bool \`json:"deterministic"\``)
- Problem / current state: A fixture author who writes `"deterministic": true` (natural reading: "this test is deterministic") actually sets `UndeterministicResults = true`, *relaxing* the similarity threshold. Note: the existing paper fixtures (`f10`, `f14`) set `"deterministic": true` *intending* the relaxed behavior (both are genuinely non-deterministic — live weather API, timestamps), so they depend on the current mapping.
- Proposed change: Accept a correctly-named `"undeterministic"` key (preferred) while keeping `"deterministic"` as a legacy alias with unchanged behavior, via a custom `UnmarshalJSON`; migrate fixtures over time.
- Why: Removes silent mis-classification of test strictness for future fixtures without breaking the existing evaluation set.
- Architecture impact: Local | Effort: S | Priority: **P1**

### [x] [A7] `compilePipeline` loops forever on unknown or cyclic task references
- Category: Bug / Fault Tolerance
- Affected component(s): `internal/pipeline/pipeline_io.go`
- Problem / current state: The resolution loop only terminates when every stub resolves. A task whose `next` or `recovery` names a nonexistent ID (typo) or participates in a cycle never resolves, so the loop spins forever. Because `/reconfigure` calls this while holding the service's global mutex, one bad config body **deadlocks the entire service**.
- Proposed change: Detect a pass that makes no progress and return an error naming the unresolved task IDs.
- Why: Config experimentation via `/reconfigure` is the primary workflow of this thesis; a typo currently costs a service restart and can silently kill an evaluation batch.
- Architecture impact: Local | Effort: S | Priority: **P1**

### [x] [A8] `/reconfigure` races against an in-flight conversion
- Category: Bug
- Affected component(s): `internal/service/service.go` (`Start` calls `Convert` without holding the mutex; `reconfigure` swaps the runner's pipeline/client and removes its working dir under the other lock)
- Problem / current state: The worker's `Convert` runs outside the lock while `reconfigure` mutates `cc.pipeline`, `cc.client`, and `os.RemoveAll`s the working directory — a data race, plus the running job's build dir can be deleted mid-build/test.
- Proposed change: Serialize `Convert` and `Reconfigure` on a dedicated runner mutex (never held together with the state mutex, so no deadlock); `/reconfigure` then waits for the in-flight job to finish before swapping.
- Why: Prevents sporadic corrupted runs during evaluation sessions; based on Go memory-model rules for unsynchronized access.
- Architecture impact: Local | Effort: M | Priority: **P1**

### [x] [A9] `pollHandler` mutates the results map under a read lock
- Category: Bug
- Affected component(s): `internal/service/service.go`
- Problem / current state: `delete(service.results, jobUUID)` runs inside `RLock()/RUnlock()`. Two concurrent GETs (or a GET concurrent with the worker's write) can trigger Go's concurrent-map-write fatal error, crashing the whole service.
- Proposed change: Use `Lock()/Unlock()` for the delete.
- Why: A service crash aborts all queued jobs.
- Architecture impact: Local | Effort: S | Priority: **P1**

### [x] [A10] `log.Fatal` in library code kills the whole service on config mistakes
- Category: Bug / Fault Tolerance
- Affected component(s): `internal/translator/translator.go` (`NewLLMConverter`), `internal/llmconnector/ollama.go` (`Prepare`)
- Problem / current state: `log.Fatal` calls `os.Exit(1)`. A `/reconfigure` body using the generic `llmTask` without a prompt, or an Ollama pipeline missing `model_name`, terminates the process instead of failing the one request.
- Proposed change: `NewLLMConverter` returns a converter whose `Apply` always returns the descriptive error (factories can't return errors); Ollama's `Prepare` returns an error like ChatAI's already does.
- Why: A misconfiguration should fail one request, not the evaluation server.
- Architecture impact: Local | Effort: S–M | Priority: **P1**

### [x] [A11] Tasks with omitted `maxRetryCount` silently never execute
- Category: Bug
- Affected component(s): `internal/pipeline/pipeline.go` (`for ; task.RetryCount < task.MaxRetryCount; ...`), `internal/pipeline/pipeline_io.go`
- Problem / current state: `MaxRetryCount` defaults to 0, so the execute loop body never runs; the task is silently skipped (then its `Validation` still runs against stale state). The debug log also reports executions off by one — the field's real meaning is "max executions".
- Proposed change: Default `MaxRetryCount` to 1 at pipeline compile time; fix the log message.
- Why: A hand-written pipeline task that silently no-ops produces confusing "successful" runs with untranslated output.
- Architecture impact: Local | Effort: S | Priority: **P1**

### [x] [A12] `LogResponse` panics when `model_name` is absent from task params
- Category: Bug
- Affected component(s): `internal/translator/translator.go` (`cc.taskParams["model_name"].(string)`)
- Problem / current state: Gemini pipelines legitimately configure the model via `GEMINI_MODEL`, not `model_name`; any LLM task then panics on every invocation (recovered into a job-level failure).
- Proposed change: Comma-ok assertion with an `"unknown-model"` fallback string.
- Why: Unblocks the Gemini backend for comparison experiments.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [A13] Ollama connector goroutine leak on mid-stream errors
- Category: Bug / Fault Tolerance
- Affected component(s): `internal/llmconnector/ollama.go`
- Problem / current state: The unbuffered `callback` channel is read exactly once; if `Generate` invokes the callback and *then* returns an error, the goroutine blocks forever on the second send. (The hardcoded 5-minute deadline should also become a task param — deferred, enhancement.)
- Proposed change: Buffer the channel (capacity 2) so the goroutine can always complete.
- Why: Leaked goroutines accumulate over long evaluation batches.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [A14] Gemini connector panics on safety-blocked/empty responses
- Category: Bug
- Affected component(s): `internal/llmconnector/gemini.go` (`resp.Candidates[0]` unchecked, `resp.UsageMetadata` possibly nil; `EvalTokenCount` uses `TotalTokenCount`)
- Problem / current state: An empty candidate list (safety filter, quota) or nil usage metadata panics; metrics double-count prompt tokens into eval tokens.
- Proposed change: Guard both accesses and return a descriptive error; use `CandidatesTokenCount`.
- Why: Converts a job-killing panic into a retryable stage failure.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [A15] `BasicLLMDeploymentReader` picks a nondeterministic "main" file and accepts empty content
- Category: Bug
- Affected component(s): `internal/translator/readers.go`
- Problem / current state: It selects the first map key with prefix `main` in randomized map order. With Gemini's fallback schema (which allows `main.go`, `go.mod`, *and* `main.py`, all nullable), the `cleaner` stage can pick an empty `main.go` over the populated `main.py`, silently replacing the Python source with an empty root file; there is no check that `RootFile` is non-empty. Leftover chatter keys become `BuildFiles` and ship in the output zip.
- Proposed change: Deterministic selection (sorted keys; prefer exact `main.<original suffix>`; only non-empty content); reject responses with no usable main file. (Dropping non-file-looking keys from `BuildFiles` deferred — behavior question.)
- Status: **Resolved** — deterministic, non-empty selection implemented (`selectMainFile` in `readers.go`). The remaining leftover-key pruning was deliberately folded into [C3] (maintainer decision, 2026-07-04): once the response schema is a single `main.go`, separate pruning is obsolete and the reader drops unexpected keys as part of that change. **Update:** [C3] is implemented — the pruning is now in place.
- Why: An empty/garbage working package poisons every later stage while the pipeline still reports stage success.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [A16] `Metrics.AddMetric` clobbers `StartTime` to the zero value
- Category: Bug
- Affected component(s): `internal/domain/types.go`
- Problem / current state: Connector-returned `Metrics` have zero `StartTime`; `m.StartTime.After(zero)` is always true, so the request's start time is reset to year 1 on the first LLM call. Masked in service mode (Start overwrites afterwards), but `Pipeline.Execute`'s own timing and the `ConvertFromFileBest` path produce garbage `TotalTime`.
- Proposed change: Skip zero-valued times in `AddMetric`.
- Why: Trustworthy timing is required for the thesis's energy/cost evaluation.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [A17] Zip ingestion: last-`.py`-wins, macOS junk entries, CRLF `.env` parsing
- Category: Bug
- Affected component(s): `internal/inputhandler/reader.go`
- Problem / current state: Every `.py`/`.go` entry overwrites `RootFile`, so a multi-file Python package silently keeps only the last file; `__MACOSX/._main.py` AppleDouble entries match the suffix check and can clobber the real source; `.env` is split on `"\n"` leaving `\r` on Windows-authored files, producing malformed env entries passed to `exec`.
- Proposed change: Skip `__MACOSX/` and `._*` AppleDouble entries; trim `\r`/blank/comment lines from `.env`. The multi-source-file policy (error vs. support) is deferred to [C9].
- Status: **Resolved** — junk-entry skipping and `.env` line hygiene implemented in `reader.go` (first pass). Multi-source policy decided by maintainer (2026-07-04): **reject at upload** — `ReadFromReader` now errors on more than one `.py`/`.go` root file, naming the conflicting entries, and `uploadHandler` surfaces it as a 400 with the reason instead of a generic 500. Regression tests in `internal/inputhandler/reader_test.go`. Actual multi-file *support* remains [C9].
- Why: These are silent input corruptions that no downstream stage can recover from.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [A18] Test harness shares stdout with the translated function; any output corrupts the envelope
- Category: Bug
- Affected component(s): `internal/builder/test_handler.txt`, `internal/builder/validator.go` (`doTest`/`validateHarnessOutput`), `internal/fixture/output.go` (`MatchOutput`'s substring fallback), `internal/translator/prompts/1-stage-translate-1.md` and `-2.md`
- Problem / current state: `test_handler.txt`'s `main` writes the `{"response": …}` / `{"error": …}` envelope with `fmt.Println` to stdout, and `doTest` judges `cmd.Stdout`. stdout is therefore a channel shared between the harness and the function under test, with nothing reserving it. A generated handler that prints anything — `fmt.Print*`, or a logger built as `log.New(os.Stdout, …)` — prepends its output to the envelope, `validateHarnessOutput`'s `json.Unmarshal` fails, and `MatchOutput` silently degrades to substring containment (`output %q does not contain expected %q`). That fallback then fails on formatting alone, because `json.dumps`'s `", "`/`": "` separators in the fixture never appear literally in `json.Marshal`'s compact output — even when the response is correct. (On the structured path this is a non-issue: `compare.normalize` decodes JSON-in-string, so key order and spacing already don't matter. The bug is entirely that the structured path is never reached.)
- Evidence (run `run-20260807-132133`, 3 of the 6 failed functions): pf13 generated `var logger = log.New(os.Stdout, "", log.LstdFlags)` — a faithful rendering of the Python original's `logging` — returned a byte-correct `200` / `"Welcome to the Low Complexity Lambda Function!"`, and scored 0/1. pf9 and pf10 lost 1 and 2 cases the same way to `fmt.Printf` progress lines. On pf13 and pf10 the entire realign budget was then spent on a failure no code change could fix, until [C5] aborted the job. Note the model is not misbehaving: Python's `logging` writes to stderr, so a faithful translation of a logging function diverges only in the sink, and no prompt says the sink matters.
- Proposed change: reserve stdout deterministically in the harness, in the same spirit as [C3]/[C4] — in `test_handler.txt`'s `main`, keep a copy of the real `os.Stdout`, assign `os.Stdout = os.Stderr` before calling `handle`, and write the envelope to the saved handle. That contains `fmt.Print*` and any logger that reads `os.Stdout` at construction time. As a second layer for a writer that captured the descriptor earlier, have `validateHarnessOutput` extract the last balanced JSON object from stdout instead of requiring the whole buffer to parse. Add a line to both translate prompts stating that stdout belongs to the harness and diagnostics must go to stderr — but not as the only guard.
- Why: it converts correct translations into failures, and the failures it invents are indistinguishable to the repair loop from real ones, so they consume the most expensive stage in the pipeline before aborting. It is also a correctness trap for the 95-function `evaluation_set`, where logging functions are common.
- Architecture impact: Local | Effort: S | Priority: **P0** (corrupts pass/fail in the direction that costs the most)
- Status: **Implemented 2026-08-07.** Two layers in the harness, because the obvious single fix does not work. `test_handler.txt` saves the real stdout, assigns `os.Stdout = os.Stderr` before calling `handle`, and writes its envelope to the saved handle — which catches `fmt.Print*` (those resolve `os.Stdout` at call time) but **not** pf13's actual pattern: a package-level `var logger = log.New(os.Stdout, …)` is initialized before `main` runs and holds the original handle, so no reassignment inside `main` can reach it. The harness therefore also prints `harnessOutputMarker` (`__REFAAS_HARNESS_OUTPUT__`) immediately before the envelope, and `harnessEnvelope` in `validator.go` takes the text after the last occurrence. Behind the marker sits a brace-balanced, string-aware scan for the **last** top-level JSON object, which covers stdout with no marker at all (a package built by an older builder, this package's own hand-written test fixtures) and output printed *after* the envelope; it prefers the last object because the harness always writes the envelope last. Both are only candidates — the caller still unmarshals and falls back to the raw-text comparison, so a wrong guess degrades to the previous behavior instead of inventing a pass. Both translate prompts ([D1]'s pair) gained a one-line execution-contract rule sending diagnostics to stderr, as defence in depth rather than the guard.
  - Verified against the three functions the bug broke, re-run on the same config as `run-20260807-132133`: **pf13 0/1 → 1/1** and **pf9 1/3 → 3/3**, both flipping from failed to `Completed`, each now finishing in 2 LLM calls instead of 4 (pf9's prompt tokens 7,140 → 2,256, since the phantom failure no longer summons realign). **pf10 1/3 → 2/3**: its t2 passes, and the remaining t3 failure now reaches the *structured* comparator (`output mismatch at body.temperature_celsius: expected … 5.32 …`) instead of the substring fallback — that one is the live-weather-API fixture, i.e. the `outputMode: tolerant` problem in open question 1, not this bug.
  - Tests (`internal/builder/validator_test.go`): `TestHarnessEnvelopeExtraction` (marker, no marker, echoed event before the envelope, output after it, braces inside a JSON string body, non-JSON passthrough), `TestValidateHarnessOutputIgnoresFunctionStdout` (replays pf13's exact stdout, and asserts a genuine mismatch behind the same noise still fails), `TestGoPackageTesterToleratesStdoutLoggingFunction` (end-to-end through the real embedded harness with pf13's package-level logger) and `TestHarnessKeepsStdoutClean` (the redirect layer: function output lands on stderr as evidence, never on stdout).

### [x] [A19] `Metrics.TestOutcomes` accumulates across retry rounds, inflating persisted test counts
- Category: Bug / Evaluation
- Affected component(s): `internal/domain/types.go` (`RecordTestOutcome`), `internal/builder/validator.go` (`GoPackageTester.Apply`), `internal/floci/stage.go`, `cmd/energy/energy.go`
- Problem / current state: `RecordTestOutcome` appends to `Metrics.TestOutcomes`, and nothing resets the slice between validation executions. A function validated over N test rounds therefore records N × (fixture count) outcomes, with the failed early rounds and the final state indistinguishable. The legacy `TestCases` map is unaffected (last-write-wins per name), so the two views of the same job disagree and only the deprecated one is right. `cmd/energy` counts passes straight off the slice (`energy.go:181`), so [H4]'s per-bucket and AWS/non-AWS "passed / failed" columns inherit the inflation.
- Evidence (run `run-20260807-132133`): pf7 is archived as 9 passed / 10 outcomes where `test_cases` — and the truth — is 5/5; pf14 records 15 outcomes for 3 fixtures; pf13 records 0/3 for a single fixture. The energy report printed 29 passed / 1 failed for the eight completed jobs where the truth is 30/30.
- Proposed change: reset `Metrics.TestOutcomes` at the start of each validation execution on both routes, so the slice always describes the last round. (The alternative — add a `Round` field and have every consumer select the max — spreads the fix across consumers and leaves the naive read wrong.) Assert it in `runlog_test.go` with a job whose first round fails and second passes.
- Why: [H1a] exists so that per-test outcomes are readable from the archived run alone, without the server that produced it. As written, every function that needed a retry is recorded wrong, and the error is invisible unless you cross-check the legacy map. Every grouped pass-rate figure in the thesis is computed off this array.
- Architecture impact: Local | Effort: S | Priority: **P0** (silently wrong numbers in the primary evaluation artifact)
- Status: **Implemented 2026-08-07.** `Metrics.BeginTestRound()` (`internal/domain/types.go`) discards the previous round, and both validation stages call it as they start work: `GoPackageTester.Apply` after its preconditions, `flociTester` after `loadCases` and *before* deploy — so a round that dies during deployment reports no validated cases, which is true, rather than the previous round's. This makes the outcomes match the semantics `TestError` already had (the stages assign it per round rather than accumulating), and no consumer needed changing: `cmd/energy` counts straight off the slice and is correct by construction now.
  - `TestCases` is cleared alongside. It is last-write-wins per name, so it survived repeated rounds intact and is the reason the bug stayed invisible — but leaving it would let entries for fixtures a later round no longer runs outlive them, and two views of one job that disagree is precisely what this item is about. It is re-made as an empty map rather than nil so `test_cases` keeps serializing as `{}` for existing `/metrics` consumers.
  - Verified live on the same config as `run-20260807-132133`: **pf14 ran 4 goTester rounds and recorded 3 outcomes for its 3 fixtures** (before: 5 rounds → 15 outcomes, 3 of them stale passes); pf12 recorded 3 for 3. Tests: `TestBeginTestRoundDiscardsPreviousRound`, `TestBeginTestRoundDropsFixturesTheNewRoundNoLongerRuns` and `TestBeginTestRoundKeepsSerializedShapeStable` (`internal/domain/outcome_test.go`), plus the end-to-end `TestGoPackageTesterOutcomesDescribeLastRound` (`internal/builder/validator_test.go`), which fails a package, rewrites and rebuilds it between `Apply` calls the way a recovery hop does, and asserts the outcomes describe only the repaired round.
  - **The already-archived batch is not retroactively repaired** and cannot be: `runs/run-20260807-132133.jsonl` still holds the inflated arrays. For that file read `test_cases` (always correct) rather than `test_outcomes`, and treat `cmd/energy`'s passed/failed columns over it as wrong — its per-function *energy* figures are unaffected, since those come from `per_task` tokens. Whether `cmd/energy` should detect and repair pre-fix records (the last `len(test_cases)` entries are the final round) is a separate call, deliberately not taken here: silently reinterpreting recorded data is the failure mode that tool already refuses elsewhere.

---

## B. Software quality issues

### [x] [B1] Two divergent output-comparison implementations; the better one is unused by the core path
- Category: Code Quality
- Affected component(s): `internal/builder/validator.go` vs. `internal/floci/output.go`
- Problem / current state: `internal/floci`'s `matchOutput`/`jsonSubset` is deterministic, recursion-safe, reports a dotted path to the first divergence, and has tests; `JsonAwareSimilarityValidation` is buggy (A1–A3), untested, and reports nothing. Two subtly different definitions of "equivalent output" make experimental results incomparable between `goTester` and `flociTester`.
- Proposed change: Extract `jsonSubset` into a shared package and make `GoPackageTester` use it (after unwrapping the harness's `"response"`/`"error"` envelope), keeping similarity as an explicit opt-in fallback. For fixtures flagged non-deterministic (e.g. `f9`/`f10`, live external APIs — maintainer-confirmed as in-scope now and on larger scraped test sets later), the comparison must degrade to **type-shape only**: same JSON structure and value *types*, scalar values ignored. The current `valueValidation=false` mode approximates this but is undocumented and only skips scalar leaves — make shape-only comparison a first-class, documented mode of the unified comparator.
- Why: One tested, deterministic equivalence definition eliminates a class of nondeterministic verdicts and gives repair stages a mismatch *path* to report ([C1]); the shape-only mode is what makes network-dependent fixtures validatable at all (maintainer decision, 2026-07-04).
- Architecture impact: Local | Effort: M | Priority: P1
- Status: **Implemented 2026-07-04.** New shared package `internal/compare` (`JSONSubset(want, got, mode)` with divergence-path reporting and JSON-encoded-string awareness — body key order/formatting never matters) with **three modes**, anticipating the future AWS evaluation set where flociTester becomes a primary route needing flexibility beyond output equivalence: `Strict` (exact scalar types+values; catches stringified-number bugs), `Tolerant` (floci's historical lenient scalars, `"3"`≈`3` — stays the floci default so existing behavior is preserved), `ShapeOnly` (structure + value types only; array lengths may differ). goTester's `strategy: "json"` now uses `NewJSONStructureValidation` (Strict normally, ShapeOnly for `undeterministic` fixtures, similarity strictly as non-JSON fallback) — the old `JsonAwareSimilarityValidation` is deleted; `ValidationStrategy` returns a mismatch *reason* that lands in the new `TestFailure.Detail` and renders as a "Mismatch:" line in `{{ .failures }}`. Floci `TestCase` gains declarative `outputMode` (`tolerant`/`strict`/`shape`, documented in docs/floci-integration.md), and black-box fixtures flagged non-deterministic derive `shape` cases automatically — both validation routes now agree on what "equivalent" means. Note: goTester leaf strings are now compared exactly under Strict (previously fuzzy 0.85 overlap) — an intentional tightening.

### [x] [B2] No tests for orchestration or validation semantics
- Category: Code Quality
- Affected component(s): `internal/pipeline` (no tests for `executeTask`), `internal/builder` (no validator tests)
- Problem / current state: Retry budgets, recovery-before-retry, snapshot restore, validation-failure recursion are untested; the inverted validator (A1) and the recovery bug (A4) would both have been caught by small table-driven tests.
- Proposed change: Add `pipeline_test.go` with fake converters asserting max-executions semantics, recovery invocation order, snapshot restore, validation-failure retry count; add `validator_test.go` with expected/actual JSON pairs including type mismatches.
- Why: Every future pipeline change needs a safety net to avoid regressing the retry machinery the success rate depends on.
- Architecture impact: None (test-only) | Effort: M | Priority: P1
- Status: **Implemented 2026-07-04** (grown across the whole fixing campaign, completed in this pass). `pipeline_test.go` now covers all the originally listed gaps: max-executions semantics, recovery invocation order (`main, recover, main, recover, main` — recovery never runs after the final attempt), snapshot restore on a corrupted working package, validation-failure retry semantics (shared budget, validation error returned), cancellation abort at task entry, LLMError recovery-skip, and per-task metrics. `pipeline_io_test.go` covers compile-time checks (cycles/unknown refs, retry-count defaulting, embedded default pipeline). Validator semantics are covered by `validator_test.go` (direction, sibling keys, type mismatches, shape-only, harness envelope, evidence/timeout integration via real `go run`) plus the new `compare_test.go` mode table.

### [x] [B3] Error taxonomy exists but is never consumed; raw error strings muddle diagnostics
- Category: Code Quality
- Affected component(s): `internal/domain/errors.go`, `internal/pipeline/pipeline.go`, `internal/builder/builder.go`
- Problem / current state: `CompilationError`/`TestingError`/`LLMError` are created but nothing type-switches on them. When a recovery task fails, its error *replaces* the original task error, hiding the root cause. Build errors are double-wrapped;
- Proposed change: Wrap-and-join recovery errors with the original; strip redundant `exit status` suffixes; keep typed errors for [C1]'s crash-vs-mismatch routing.
- Why: Cleaner errors directly improve what the fixer prompt sees (`{{ .issue }}` is `LastError().Error()`).
- Architecture impact: Local | Effort: S | Priority: P2
- Status: **Implemented 2026-07-08.** `internal/pipeline/pipeline.go`'s `executeTask` now joins (`errors.Join`) the original task error with a wrapped recovery failure when `OnFailure` itself fails, instead of letting the recovery error silently replace it - `LastError()`/`{{ .issue }}` (and `errors.As`) can still see the original defect. `internal/builder/builder.go`'s `GolangBuilder.Apply` no longer calls `request.AddError` itself (both the temp-dir and build-failure paths) - `executeTask` already records every returned task error into `req.errs` exactly once, so the same failure was previously appearing twice (once raw, once `CompilationError`-wrapped). `formatBuildError`'s fallback (no parseable diagnostics) now drops the trailing `%+v` of the error when it's a plain `*exec.ExitError` ("exit status N"), which is redundant once the command's combined stdout/stderr is already shown; non-exit-status errors (e.g. command not found) are still appended since they carry unique information. `internal/domain/errors.go`'s `CompilationError`/`TestingError`/`LLMError` gained `Unwrap() error` so they compose correctly inside `errors.Join`/`fmt.Errorf("%w", ...)` and remain traversable by `errors.As`/`errors.Is`. Tests: `TestExecuteTaskJoinsOriginalErrorWithFailedRecovery` (`internal/pipeline/pipeline_test.go`), `TestFormatBuildErrorStripsRedundantExitStatus` (`internal/builder/builder_test.go`).

### [x] [B4] Job status conflates "in progress" and "unknown"
- Category: Code Quality
- Affected component(s): `internal/service/service.go` (`results` written only on completion)
- Problem / current state: `HEAD /{uuid}` returns 404 until the job finishes; clients cannot distinguish a queued/running job from a nonexistent one.
- Proposed change: Record the request at enqueue time with a status field; `HEAD` returns 200+status, `GET` on an unfinished job returns 202/425 instead of 404.
- Why: Reliable polling avoids evaluation scripts abandoning or double-submitting long jobs.
- Architecture impact: Local | Effort: S | Priority: P2
- Status: **Implemented 2026-07-08.** New `status map[uuid.UUID]jobStatus` (`"queued"`/`"running"`) on `ConverterService`, set in `uploadHandler` at enqueue time and updated to `"running"` in `Start` when a job is dequeued, removed once `Convert` returns. `pollHandler` checks this map first: `HEAD` on a queued/running job returns 200 with an `X-Job-Status` header, `GET` returns 202 with a status message instead of falling through to the existing "not found"/completed-package logic. Unknown uuids still 404.

### [x] [B5] Observability gaps: chatlogs lack job/stage correlation; no per-stage metrics
- Category: Code Quality
- Affected component(s): `internal/llmconnector/client.go` (`LogResponse`), `internal/domain/types.go` (`Metrics`)
- Problem / current state: Chatlogs cannot be mapped to jobs/stages; `Metrics` aggregates all LLM calls into single counters — "tokens per stage" and "which stage's retries exhaust" are unanswerable. Metrics are wiped on `/reconfigure`.
- Proposed change: Thread request UUID + task ID into chatlog filenames; add `PerTask map[string]TaskMetrics` (attempts, tokens, duration, outcome) populated in `executeTask`/`LLMConverter.Apply`.
- Why: Instrumentation prerequisite for nearly every prioritization decision here and for the thesis's prediction goal.
- Implementation notes (verified 2026-07-04): the GWDG ChatAI proxy reliably reports token usage via the standard OpenAI-compatible `usage` object in both non-streaming and streaming modes, and the totals match across modes. If streaming is ever added, `stream_options: {include_usage: true}` must be set explicitly (usage then arrives only in the final SSE chunk before `[DONE]`). `prompt_tokens_details` is always `null` on this backend — no cached/uncached breakdown — so cost tracking must rely on `prompt_tokens`/`completion_tokens`/`total_tokens` only.
- Architecture impact: Local | Effort: M | Priority: **P0** (raised 2026-07-04 by maintainer — open question 1, the failure-mode distribution, stays unanswerable until this lands; also a prerequisite for the [G5] experiment)
- Status: **Implemented 2026-07-04.** `Metrics.PerTask` (`map[taskID]*TaskMetrics`: executions, failures, duration, LLM calls, prompt/eval tokens) is populated by `executeTask` (`RecordTaskAttempt` per execution attempt) and `LLMConverter.Apply` (`RecordLLMCall`, including failed calls — they cost tokens too); `ConversionRequest.CurrentTask` carries the running task id (restored after recovery recursion). Chatlog filenames now embed `<8-char request id>_<task id>_<model>`. Appears in `GET /metrics` as `per_task`. **This unblocks open question 1** — run f1–f14 and read the per-task failure/token distribution. Not addressed (unchanged scope): metrics still wiped on `/reconfigure` (use `store-metrics.sh`), `TestTime`/`TestCases` still not merged in `AddMetric`. Tests: `TestExecuteTaskRecordsPerTaskMetrics`.

### [x] [B6] Documentation drift on prompt wiring and Floci examples
- Category: Code Quality
- Affected component(s): `CLAUDE.md` (claims `1-stage-translate.md` is wired — `prompts.go` actually embeds `-1` as `coder` and `-2` as `coder2`; plain `translate.md` is the unwired one), `docs/floci-integration.md` (references `examples/floci/pipeline.json`, which does not exist — only `pipeline-bundled.json`), README env-var table (missing chatai/floci vars)
- Proposed change: Correct the three references (docs-only change).
- Why: Prevents a future engineer/agent from editing the wrong (unwired) prompt file.
- Architecture impact: None | Effort: S | Priority: P2
- Status: **Implemented 2026-07-10.** `CLAUDE.md` now correctly says `prompts.go` embeds six templates (`1-stage-translate-1.md` → `coder`, `1-stage-translate-2.md` → `coder2`) and that the plain `1-stage-translate.md` is the unwired draft. `docs/floci-integration.md` and `README.md` now point at `examples/floci/pipeline-bundled.json` (the file that actually exists) instead of the nonexistent `examples/floci/pipeline.json`; the same stale path in a `docker-compose.yml` comment was fixed too. The README's environment-variable table gained the missing `ACADEMIC_CLOUD_ENDPOINT`/`ACADEMIC_CLOUD_API_KEY` (chatai) and `FLOCI_ENABLED`/`FLOCI_ENDPOINT`/`FLOCI_REGION` (Floci) rows, plus `APP_PORT`/`LLM_CALL_INTERVAL` which were also genuinely read (`internal/pipeline/defaults.go`, `internal/pipeline/runner.go`) but undocumented.

### [x] [B7] `goTester` runs `go run .` in the service's own directory when `WorkingDir` is unset
- Category: Code Quality
- Affected component(s): `internal/builder/validator.go`, `internal/pipeline/runner.go`
- Problem / current state: If a pipeline places `goTester` without a preceding `goBuilder`, `cmd.Dir` is `""` and `go run .` executes in the refaas process's CWD.
- Proposed change: Error out in `Apply` when `runner.WorkingDir() == ""` naming the missing `goBuilder` prerequisite.
- Why: Turns a bizarre, hard-to-diagnose behavior into an immediate config error.
- Architecture impact: Local | Effort: S | Priority: P2
- Status: **Implemented 2026-07-10.** `GoPackageTester.Apply` (`internal/builder/validator.go`) now checks `runner.WorkingDir() == ""` right after the working-package nil check and returns a config error naming the current task id and the missing `goBuilder` prerequisite, instead of silently falling through to `cmd.Dir = ""` (which runs `go run .`/`./fn` in the refaas process's own CWD). No change needed in `internal/pipeline/runner.go` - `WorkingDir()` already existed as the thing to check. Test: `TestGoPackageTesterRequiresWorkingDir`.

### [x] [B8] `Metrics.Issues` records the same error once per recursion level
- Category: Code Quality / Evaluation
- Affected component(s): `internal/pipeline/pipeline.go` (`executeTask`'s several `req.AddError` call sites), `internal/service/service.go` (`Issues` assembled from `request.Errors()`)
- Problem / current state: `executeTask` calls `req.AddError` on the failed-attempt path *and* at each of its exits, and a task's error is then propagated up through the parent frames, which add it again. One abort therefore lands in the append-only error list several times over.
- Evidence (run `run-20260807-132133`): pf13's `Issues` holds `1/1 tests failed: t1 (output mismatch)` three times — those three are genuine, one per attempt — followed by four byte-identical copies of `repair loop for task (goTester) made no progress after 3 identical failures in a row, aborting: …`, which is one event. pf12 and pf10 show the same shape. Reconstructing a job's real attempt history means de-duplicating by hand.
- Proposed change: record an error once, where it is first observed, and let propagation carry it upward without re-adding; or de-duplicate consecutive identical entries inside `AddError`. Keep the distinct per-attempt failures — those *are* the history — and drop only the re-adds of the same wrapped error on the way up.
- Why: `Issues` is the free-text half of the archived record, and for a job that never completes it is the only place the failure story survives at all ([H2] persists completed jobs only). Padding it with duplicates makes attempt counts unreadable both by eye and by script.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [B9] `go test ./...` is not green: a live, billable ChatAI test never skips
- Category: Code Quality
- Affected component(s): `internal/llmconnector/chatai_test.go` (`TestChatAIInvocationClient_LiveInvokeLLM`), `internal/pipeline/defaults.go` (the `godotenv/autoload` blank import), `CLAUDE.md`
- Problem / current state: the test skips only when `ACADEMIC_CLOUD_API_KEY` is unset — but `godotenv/autoload` loads `.env` into the process before any test runs, so in a working checkout the key is always set and the test always makes a real, billable call to `meta-llama-3.1-8b-instruct`. It is flaky by construction too: it asserts the reply contains `"pong"`, and on 2026-08-07 the model answered `{}` (2 completion tokens), failing the package. Every other package passes, so the whole suite's exit status is decided by an 8B model's phrasing. Separately, `CLAUDE.md` still states the repo has "no test suite to run (`go test ./...` will currently report 'no test files')", which has been untrue since [B2].
- Proposed change: gate the live tests behind an explicit opt-in (`REFAAS_LIVE_LLM_TESTS=1`, or `testing.Short()` so a plain `go test ./...` skips them while `-run` still reaches them), keeping the existing key check as a second condition. Loosen the assertion to "non-empty and parses as JSON" so a small model's wording cannot fail the suite. Correct the `CLAUDE.md` sentence to describe the suite that now exists.
- Why: a default `go test ./...` that spends money and fails for reasons unrelated to the change under test trains everyone to ignore it — and the contributor guide currently tells a new engineer or agent there is nothing to run, so the failure is never even seen.
- Architecture impact: Local | Effort: S | Priority: P2

---

## C. Pipeline feature improvements (primary focus)

### [x] [C1] Feed structured per-test failure evidence into the repair/align loop
- Category: Feature
- Affected component(s): `internal/builder/validator.go` (`GoPackageTester.Apply`/`doTest`), `internal/translator/translator.go` (template vars), `3-stage-align.md`
- Problem / current state: `doTest` produces a rich error (actual output, expected output, stderr) but `Apply` discards it, returning only `"N tests failed"`. That string becomes `LastError()`, which is all any LLM stage can ever see — and the align prompt doesn't even reference `{{ .issue }}`. Recorded evidence (identical errors across 4+ attempts) shows blind repair does not converge.
- Proposed change: Collect per-test failures into the `TestingError` (`Failures []TestFailure{Name, Input, Expected, Actual, Stderr}`, capped), expose them as a rendered `{{ .failures }}` template var (deterministic order). Distinguish process error vs. output mismatch so pipelines can route crashes to `fixer` and mismatches to `realign`. Update `3-stage-align.md` to consume `{{ .failures }}` ([D2]).
- Why: Execution feedback (failing input, expected vs. actual) is the single strongest known signal for LLM self-repair — Chen et al., *Teaching Large Language Models to Self-Debug* (arXiv:2304.05128) show unit-test feedback substantially outperforms blind resampling — and the current pipeline structurally withholds it.
- Architecture impact: Local | Effort: M | Priority: **P0**
- Status: **Implemented 2026-07-04** (together with [D2]). `domain.TestFailure` carries name/kind/input/expected/actual/stderr (fields truncated at 2000 chars); kinds distinguish `output mismatch`, `execution error`, and `invalid test fixture`. `goTester` collects one entry per failing test (sorted by name for determinism) into `NewTestingErrorWithFailures`, and its summary now names the failing cases (`"2/3 tests failed: t1 (output mismatch), ..."`) instead of just a count. `LLMConverter.Apply` exposes the most recent TestingError's evidence as `{{ .failures }}` to every prompt. **Deferred:** kind-based *routing* (crashes → fixer vs. mismatches → realign) needs a per-error-type recovery mechanism in the pipeline config schema — revisit if evidence-driven realign alone proves insufficient. `flociTester` still uses the plain `NewTestingError`; wiring its per-case evidence in belongs to [C10]. Tests: real `go run` integration tests in `validator_test.go` (mismatch + crash evidence), render/selection tests in `readers_test.go`.

### [x] [C2] The embedded default (dev) pipeline wastes its test-failure retries — fix the dead-end without losing its brevity
- Category: Feature
- Affected component(s): `internal/pipeline/default.yaml`, `internal/pipeline/pipeline.go`
- Problem / current state: In `default.yaml`, `goTester` is the *validation* of the `builder` task. A validation failure re-executes the *same* task — i.e. rebuilds identical code and re-runs the same tests, up to 3 times, with no LLM stage ever invoked (recovery only fires on `Execute` failure). `realign` isn't registered in this pipeline.
- Context (maintainer, 2026-07-04): the three pipeline configs are *all* intentional and relevant. `default.json` is the canonical, extensive paper pipeline; `default.yaml` is a **deliberately short dev pipeline** to save resources during quick functional tests (it may be aligned with `default.json` later); `scripts/summary-pipeline.json` is an experimental variant (summary → `coder2`) to be evaluated against `default.json`. So do **not** simply replace `default.yaml` with the full chain.
- Proposed change: Keep `default.yaml` short and cheap, but remove the guaranteed-waste loop: either (a) set the builder task's retry budget so a test-validation failure fails fast instead of rebuilding identical code twice more, or (b) add one compact repair hop for validation failures (a single `fixer`/`realign` recovery) if quick dev runs should exercise the repair path at all. Full alignment with `default.json` remains a later option.
- Why: Retrying identical code cannot change the outcome; in a dev pipeline the dead-end wastes build/test cycles and makes quick functional tests report misleadingly slow/void failures.
- Architecture impact: Local | Effort: S | Priority: P1 (downgraded from P0 — the canonical evaluation pipeline `default.json` does not have this dead-end)
- Status: **Implemented 2026-07-04** (variant (b)). `goTester` moved from the builder's `validation` to its own `tester` task (maxRetryCount 2) with a compact repair path: `testRecovery` (one `realign` attempt) → `recoveryBuild` (goBuilder — required, otherwise the retest would run the old binary). Test failures now get exactly one evidence-driven repair attempt ([C1]/[D2]) instead of two pointless identical rebuilds; the pipeline stays 7 small tasks. The optional `executeTask` change (route validation failures through OnFailure) was not made — no task uses `validation` for testing anymore in either default config.

### [x] [C3] Always regenerate `go.mod` deterministically; never trust the LLM's
- Category: Feature
- Affected component(s): `internal/translator/readers.go` (`GoJsonOllamaReader`), `internal/builder/builder.go`, prompts showing `go.mod` examples
- Problem / current state: The LLM-authored `go.mod` is a persistent, observed failure class: `unknown directive` (metrics 2026-07-01, four identical failures); the wired prompt's example teaches `go 1.x` / `v1.x` placeholders plus `v1.24` — an invalid version (Go modules require full semver). `internal/floci/packager.go` already proves the deterministic alternative: discard `go.mod`/`go.sum`, run `go mod init` + `go mod tidy` unconditionally.
- Proposed change (maintainer-confirmed direction, 2026-07-04 — implement all three parts together): (1) **programmatic `go.mod` generation**: in `GoJsonOllamaReader.MakeDeploymentFile` drop any `go.mod`/`go.sum` keys from the LLM response and always set `BuildCmd = ["go mod init example.com", "go mod tidy", "go build -o fn ."]` — dependencies are handled by the pipeline, never by the model; (2) **response schema**: change the translate (and fix/realign) stages' `output_keys` so the LLM ideally returns exactly *one* Go file (`main.go`) — this also enables the fixed, closed schema shape that [E1] needs, and the reader should then drop/ignore any unexpected extra response keys (absorbs the leftover-key pruning deferred from [A15], maintainer decision 2026-07-04); (3) **prompts**: update the translate/repair prompts ([D1]/[D3]) to remove all `go.mod` examples and state explicitly that only the Go source is expected and dependency resolution is automatic. Supersedes [A4]'s fallback.
- Why: Eliminates an entire observed failure class with zero LLM involvement — `go mod tidy` derives requirements from imports authoritatively per the Go modules reference (go.dev/ref/mod) — and shrinks the model's job to the one artifact it is actually good at producing.
- Architecture impact: Local | Effort: S–M | Priority: **P0** (explicitly re-confirmed as highly desirable by maintainer, 2026-07-04)
- Status: **Implemented 2026-07-04.** (1) `GoJsonOllamaReader` now discards `go.mod`/`go.sum` and every non-`.go` response key (absorbing [A15]'s pruning) and always sets `BuildCmd = ["go mod init example.com", "go mod tidy", "go build -o fn ."]`; additional non-empty `.go` sources are kept. (2) All LLM stage factories default to closed single-file `output_keys` (`main.go` for coder/coder2/fixer/realign, `main.py` for cleaner; still overridable per task), and all three connectors now emit the JSON Schema `required` list for non-nullable fields (`OutputSchema.RequiredKeys`) — so an empty `{}` no longer satisfies the schema. (3) All four active prompts (`1-stage-translate-1/-2.md`, `2-stage-repair.md`, `3-stage-align.md`) no longer show `go.mod` examples and state that dependencies are resolved automatically from imports. [A4]'s `isGoModFailure` build fallback is retained as defense-in-depth. Regression tests: `readers_test.go` (reader pruning + BuildCmd + factory schema defaults), `outputschema_test.go` (required keys). Note: `builder.go`'s injected `test_handler.go` still overwrites any same-named LLM key, unchanged.

### [x] [C4] Deterministic Go post-processing between `coder` and `goBuilder`
- Category: Feature
- Affected component(s): new converter (registered via `RegisterConverterFactory`) or extension of `GoJsonOllamaReader.prepareGoRootFile`
- Problem / current state: Observed failures include `expected 'package', found 'import'` (LLM omitted the package clause) and missing/unused imports. Each such case costs a full build cycle plus an LLM fixer round-trip.
- Proposed change: After parsing the LLM response: (1) prepend `package main` if the file lacks a package clause; (2) run `go/parser.ParseFile` and, on syntax error, fail the *convert* task's validation (retry re-samples the translator); (3) run `golang.org/x/tools/imports.Process` (goimports as a library) before the first build.
- Why: Replaces LLM round-trips with exact, deterministic fixes for the two most mechanical failure classes; `imports.Process` requires no model capability — the key property for small-model deployments.
- Architecture impact: Local (one new converter + one dependency) | Effort: M | Priority: P1
- Status: **Implemented 2026-07-05**, with point (2) of the proposal deliberately **inverted** on maintainer instruction. `postProcessGoSource` (`internal/translator/goformat.go`) (a) inserts a missing `package main` — and renames a foreign package, which otherwise breaks the build as "found packages X and main" against the harness — and (b) runs goimports (`golang.org/x/tools/imports.Process`), which adds missing stdlib imports *and* removes unused ones (an unused import is a compile error in Go, so a single stray one was a guaranteed build failure). It gofmts too, so a later repair round sees normalized code. Dependency pinned to the `x/tools v0.30.0` already present as an indirect dep (plus `x/mod`): 6 lines of go.mod/go.sum, no transitive upgrades.
- **Syntax errors do not re-sample the translator** (maintainer decision): source that still doesn't parse is returned *unchanged* and the stage does **not** fail. Resampling a broken generation tends to produce a differently broken one; instead the code proceeds to `goBuilder`, whose compiler diagnostic is both more precise than `go/parser`'s and already routed to the fixer as `{{ .issue }}` via [D3]'s `extractDiagnostics` — so no new plumbing was needed for that. The package clause is still added even to unparseable source, so the compiler reports the *real* syntax error instead of "expected 'package'".
- Placement: in the reader (`GoJsonOllamaReader.prepareGoRootFile`, plus extra `.go` build files) rather than as a separate pipeline task, so every Go-producing stage — `coder`, `coder2`, `fixer`, `realign` — gets it automatically; the fixer's own output can carry an unused import just as easily as the translator's, and a task that must be wired into every config would eventually be missing from one. Tests: `internal/translator/goformat_test.go`.

### [x] [C5] Detect repair-loop stagnation (narrowed after [E3]; not superseded by it)
- Category: Feature
- Affected component(s): `internal/pipeline/pipeline.go` (retry loop) or `GolangBuilder`
- Problem / current state: `metrics-20260701122938.json` shows the same `go.mod` error verbatim four times: the fixer's output didn't change the failing artifact, and the pipeline paid for each identical attempt.
- Proposed change: Compare the current failure text with the previous attempt's; on an exact repeat, either abort early with a "no progress" error, or set `req.Metadata["stagnant"]` that the fixer prompt surfaces.
- Why: Reflexion-style loops only help when feedback changes behavior (Shinn et al., *Reflexion*, arXiv:2303.11366); detecting identical outcomes converts guaranteed-wasted attempts into an early exit or a differentiated retry.
- Architecture impact: Local | Effort: S–M | Priority: P2 (downgraded — see below)
- **Evaluated against [E3] (2026-07-05): keep, but narrowed.** [E3] varies sampling on *resample-style retries of the same task* (`CurrentAttempt > 1`), which does cover the `cleaner`/`coder` case this item also addressed — so the "bump sampling temperature" sub-item is now redundant and has been removed from the proposal above. It does **not** cover the loop the recorded waste actually came from: in a builder⇄fixer cycle the recovery task is invoked afresh per builder retry and, because `executeTask` breaks out of its loop *without* incrementing `RetryCount` on success, a recovery task that keeps succeeding always reports `CurrentAttempt == 1` — so E3's bump never fires for it. Two further caveats: `retry_temperature` is **opt-in and set in no shipped config** (`default.json`, `default.yaml` and `scripts/summary-pipeline.json` all lack it), so E3 is inert by default; and E3 makes attempts *differ*, it cannot stop a doomed loop early, which is the token-saving half of this item. Priority dropped to P2 because [C3] and [C4] eliminated the deterministic failure classes behind the observed instance — measure first ([B5] + [H2] now record per-stage failures durably) and implement only if a stagnant loop still shows up in the data.
- **Real-world confirmation (2026-08-07):** a manual run of `evaluation_set/f6.zip` against `scripts/chatai-devstral-summary.json` logged `running task (testRecovery) with (1 / 3) executions` repeatedly. Traced in full and confirmed **not an infinite loop** — bounded by `testRouter`'s own `maxRetryCount: 5` — with a regression test (`TestExecuteTaskSharedRecoveryGetsFreshBudgetPerInvocation` in `pipeline_test.go`) pinning the mechanism: `executeTask`'s success path exits via `break`, which (unlike `continue`) skips a `for` loop's post-statement, so a task with no `validation` step never advances its own `RetryCount` on success. **This is load-bearing, not a bug** — `gollmRecovery` is shared as the recovery target of both `builder` and `testRecoveryBuild` in the canonical pipeline, and a lifetime-accumulating counter would let one parent's failures exhaust the shared budget before the other gets a turn; a draft fix (incrementing `RetryCount` on success) was traced through and confirmed to break exactly that sharing before being discarded. This confirmed [C5] was the right lever — see below.
- Status: **Implemented 2026-08-07.** `domain.ConversionRequest.RecordFailure(taskID, errText)` tracks, per task id, how many consecutive times that task has failed with byte-identical error text — chosen over a fuzzy comparison because goTester's `TestingError` summary and the fixer's numbered compiler diagnostics ([D3]) are both stable summaries (test names/kinds, `file:line:col` lines) rather than raw values, so a genuinely stuck loop reproduces them exactly while real progress reliably changes them. In `executeTask`'s recovery branch (`internal/pipeline/pipeline.go`): on the **2nd** consecutive identical failure, sets `req.Metadata["stagnant"] = "true"` (surfaced to both `2-stage-repair.md` and `3-stage-align.md` as `{{ .stagnant }}` — a "your last fix didn't change anything, try something genuinely different" nudge) but still invokes recovery once more; on the **3rd**, aborts instead of invoking recovery again, joining a "no progress after N identical failures" error. Chose the abort-early proposal over the softer flag-only one, but combined both rather than picking one exclusively: the flag gives the model one real chance to course-correct before the budget is cut, which a bare abort-on-first-repeat wouldn't. Thresholds (2/3) are fixed constants, not `task_args`-configurable — this is a property of LLM repair loops in general, not something worth tuning per pipeline task. Scoped to the recovery-invocation path only (not plain retries or validation-failure retries), matching the item's original problem statement; broadening it is a possible future extension, not implemented. Tests: `internal/domain/stagnation_test.go` (counting/reset semantics), `TestExecuteTaskStagnationAbortsBeforeExhaustingRetryBudget` + `TestExecuteTaskStagnationResetsOnDifferentFailure` in `pipeline_test.go`, `TestRecoveryPromptsRenderStagnantNudge` in `translator/readers_test.go` (both prompts, both branches of align's failures/issue conditional).

### [x] [C6] Validate uploads and fixtures before spending any LLM tokens
- Category: Feature
- Affected component(s): `internal/service/service.go` (`uploadHandler`), `internal/inputhandler/reader.go`
- Problem / current state: An upload with no `.py` file, no `test/` fixtures, or unparseable fixture JSON is accepted; the failure surfaces stages later (or worse, with zero tests `goTester` passes vacuously and the job "succeeds" with no behavioral validation). Fixture-format problems are only discoverable at comparison time.
- Proposed change: At upload: require exactly one root source file and ≥1 test fixture; parse every fixture against the unified fixture schema from [C10] (which covers both the black-box and the Floci dialect); when strategy is `json`, verify `Output` parses as JSON and does **not** contain a top-level `"response"` wrapper. Canonical format confirmed by maintainer (2026-07-04): expected output = the Python handler's return object, as in the current `examples/paper/f1–f14` fixtures; the inconsistent files under `examples/output/2026-*` stem from an outdated test runner and can be ignored. Return 400 with a per-file error list. This is also where [C10]'s "floci required but disabled" rejection belongs, and where **a missing `meta.json` must be rejected when the benchmark/strict flag is set** ([H1]) — the dataset guarantees one per artifact, so its absence in a benchmark run means a mispackaged or wrong-source upload, and translating it would spend the full LLM budget on a result nobody can attribute afterwards. Outside strict mode `meta.json` stays optional so ad-hoc uploads, the bundled `examples/input/*.zip` and the upload tests keep working.
- Why: Fail-fast saves the full LLM/build budget of a doomed run and prevents vacuous "successes" from polluting success-rate numbers.
- Architecture impact: Local | Effort: M | Priority: P1 (the `meta.json` check is **P0 for the benchmark run** — see [H1])
- Status: **Implemented 2026-07-05.** `inputhandler.Validate(pkg, opts)` runs in `uploadHandler` before anything is queued: requires a root source file and ≥1 fixture, parses every fixture through `internal/fixture` (so a malformed one surfaces here, not at comparison time), and requires `meta.json` when `opts.RequireMeta` is set. All problems are collected into one `*ValidationError` and returned as a single `400`, so a bad artifact takes one upload to diagnose. Benchmark mode comes from `REQUIRE_META=true|1` via `BenchmarkValidateOptions()`. Two parts of the original spec were deliberately **not** implemented: the `{"response": …}` wrapper check (a handler may legitimately return that key, so a hard rejection would be wrong — the canonical format is enforced by the dataset instead), and [C10]'s "floci required but disabled" rejection, which needs C10's routing to exist first. Tests: `internal/inputhandler/validate_test.go`. Note `cmd/refaas/main_test.go`'s upload helper now sends a minimal *valid* package — an empty zip is correctly rejected now.

### [x] [C7] Deterministic and complete test context for prompts
- Category: Feature
- Affected component(s): `internal/translator/translator.go` (`getFirstTestFile`)
- Problem / current state: Prompts receive the input/output of *one* test file chosen by randomized map iteration — retries can see different examples (non-reproducible), and multi-case behavior (error branches) is invisible to the translator.
- Proposed change: Sort test file names and expose up to k (configurable) input/output pairs as `{{ .tests }}`; keep `{{ .input }}`/`{{ .output }}` as the first sorted pair.
- Why: Test cases in the prompt act as few-shot behavioral specs — showing the error-path fixture is the only way the model can learn the expected non-happy-path statusCode mapping (mechanism per Chen et al., arXiv:2304.05128); determinism makes experiments reproducible.
- Architecture impact: Local | Effort: S | Priority: P1
- Status: **Implemented 2026-07-04.** `getFirstTestFile` (random map order) replaced by `sortedTestFiles` (lexical order, unparseable fixtures skipped); `{{ .tests }}` renders up to `max_test_examples` (default 3, converter-level task param — stripped before connector `Prepare`) input/expected pairs, each field capped at 2000 chars; `{{ .input }}`/`{{ .output }}` remain as the first *sorted* pair for backward compatibility. Both translate prompts consume `{{ .tests }}` ([D1]). Tests: `TestSortedTestFilesAndRenderExamples`.

### [x] [C8] Python feature pre-scan: deterministic source analysis feeding the translate prompt
- Category: Feature
- Affected component(s): new converter (e.g. `pyScan`), output carried via `req.Metadata` (the metadata mode built for `summary`)
- Problem / current state: The paper set includes constructs a small model mishandles silently: decorators (`f13`), generators/`inspect` (`f14`), third-party libs (`requests` in `f9`/`f10`, `boto3`), recursion (`f6`). Nothing analyzes the source; infeasible translations are discovered only after the full budget is spent — the exact waste the thesis's "prediction" goal targets.
- Proposed change: A non-LLM converter that scans the Python source for third-party imports (mapped through a static table: `requests`→`net/http`, `boto3.client("s3")`→`aws-sdk-go-v2/service/s3`, …), decorators, `async`, `yield`, `**kwargs`, raised exception classes. Writes findings into `req.Metadata` (`{{ .py_features }}`, `{{ .lib_hints }}`) and emits a feasibility-warning metric.
- Why: Injecting explicit API-mapping hints removes the hardest reasoning step (library equivalence) from the model's job — the "more structure, less reliance on large-model reasoning" tradeoff this pipeline needs at 30B scale.
- Architecture impact: Local (fits the registry architecture by design) | Effort: M | Priority: **P0** (raised 2026-08-24)
- **Scope extended and rescheduled 2026-08-24 (maintainer decision):** this scanner is also the prediction module's feature extractor — see [I3], which specifies the accurate Python-AST variant and the fixed-width numeric vector emitted alongside the prompt hints built here. Build it **once**, for both consumers, and land it **before** the [I1] labelling run so that run records features and outcomes together. [I3]'s `meta.json` reproduction check (`cc`/`lloc` over all 95 artifacts) doubles as this item's acceptance test.

- **Status: implemented 2026-08-24** as `internal/pyscan` (analysis library, no pipeline dependency), the `pyScan` converter in `internal/pipeline/pyscan.go`, and `cmd/pyscan` (offline CLI). Registered as `root` in both `default.yaml` and `default.json` (the latter with `required: true`), before the cleaner.
  - **Acceptance test result** (`internal/pyscan/calibration_test.go`, runs against the real 95 artifacts): `cc` reproduces `meta.json` **exactly for 92/95**, within 2 for 94/95, Pearson **r = 0.9998**. The definition that achieves this is the **max block complexity** counting BoolOp chains and comprehensions — not a whole-file sum, which was 5× too high. `lloc` correlates at **r = 0.9936** with a systematic ≈ −4 offset and is *not* an exact reproduction: radon's raw counter works on a different basis, and no clean formula recovers the dataset's values (a regression on construct counts gave fractional coefficients and residual sd 7.7, so the difference is not a countable-construct one — most likely the values were computed on a pre-normalisation form of the source we do not have). Per this item's own rule, the extractor's value is what the model trains on; the correlation is what matters for a feature, and the thresholds in the test are set to catch a regression rather than chase the last few lines.
  - **`halstead_*` are deliberately named apart from `meta.json`'s `h_*`**: this scanner's operator/operand basis is broader (f0: 34.3 vs 1.5), so a same-named field would invite a false comparison in the write-up.
  - **Cross-checks against EVALUATION_DATASET.md, all exact**: `uses_aws` non-zero for **58/95** (§7 says boto3 in 58), `n_cases_with_setup` non-zero for **40/95** (§6.4 says 40 need Floci provisioning), `n_mode_shape` non-zero for **14/95** (§6.2 says 27 tests in 14 functions), max `cc` = 147 (§8's D+ ceiling). These were not fitted — they fall out of the scan and independently corroborate it.
  - **56 feature columns** in four families (size/complexity, library surface, dynamic-Python markers, fixture surface), fixed order, versioned by `FeatureSchemaVersion`. Recorded on `Metrics.Features` for every job, so the [I1] run log is directly `(features → outcome)` training data.
  - **Finding for [I5]/[I7] — 8 of the 56 columns are constant-zero on `evaluation_set`**: `has_infeasible_lib`, `lib_yaml`, `lib_pytz`, `lib_jwt`, `n_yield`, `n_async`, `n_mode_strict`, `n_cases_with_env`. Two consequences. (a) **Baseline B4's infeasibility blocklist cannot skip a single function on this corpus** and will be numerically identical to B0/always-translate — expect that result rather than discovering it. (b) Those columns are pure width at N=95; the training pipeline must drop zero-variance columns **inside each CV fold**, not over the whole table, or the choice of which to drop leaks the test fold. `cmd/pyscan` prints this list to stderr on every corpus scan so it cannot be forgotten.

### [x] [C8a] Exact AWS SDK for Go v2 module paths and idiom hints
- Category: Feature / Bug
- Affected component(s): `internal/pyscan/libmap.go` (`awsServiceModules`), `internal/pyscan/hints.go` (`AWSHints`, `awsServiceNames`), `internal/pyscan/extract.py` (`client_factory_literals`, schema 2 → 3), `internal/pipeline/pyscan.go` (`aws_hints`), `internal/translator/prompts/{1-stage-translate-1,1-stage-translate-2,2-stage-repair,3-stage-align}.md`, `cmd/pyscan`
- Problem / current state: [C8] left boto3 mapped to the placeholder `service/<service>` and rendered only *"AWS services used: ecs, sts"*. Run `20260831-190900` shows what the model does with a service name and no path — it invents one. Of the 20 functions that never built:
  - **f20** imported `service/stepfunctions` (really `sfn`), **f16** `service/iotdata` (really `iotdataplane`), **f26** `service/ecstypes` (really `ecs/types`). A wrong path fails `go mod tidy` for the module as a **unit**, so all the *valid* imports are reported unresolvable too — and `maxCompilerErrors = 5` then truncates the list before the actual culprit appears, so the fixer spent four attempts on a diagnostic naming only correct package paths.
  - The build diagnostics across all 95 jobs are dominated by v1-vs-v2 idioms, not by logic: `cannot use aws.Bool(true) … as bool value` ×33, `types redeclared in this block` ×26, `out.ResponseMetadata undefined` ×12, `undefined: dynamodbattribute` ×5, plus `time.Parse undefined (type string has no field or method Parse)` ×7 and `event is not a type` ×7 (a local shadowing a package). The **fixer prompt had no AWS guidance at all**, yet it is the stage that sees these errors and consumed 926k of the run's 2.8M tokens.
- Change (implemented 2026-09-04):
  1. **`awsServiceModules`**: boto3 client name → exact Go module. Covers all 23 services in the corpus (0 unresolved) plus the near-corpus surface. The entries that earn the table are where the names diverge — `stepfunctions`→`sfn`, `iot-data`→`iotdataplane`, `logs`→`cloudwatchlogs`, `events`→`eventbridge`, `cognito-idp`→`cognitoidentityprovider`, `elbv2`→`elasticloadbalancingv2`, and `config`→`configservice`, which additionally collides with `aws-sdk-go-v2/config` (the credential loader) and so carries an explicit aliasing warning. An unlisted service is *reported as unlisted* rather than omitted: silence is what leaves the model guessing.
  2. **`aws_hints`**, a second metadata key rendering the six v2 idioms above, each traceable to a diagnostic count from the run rather than to general SDK advice. Emitted only for AWS functions, so the 37 non-AWS functions pay nothing.
  3. **Wired into the repair and align prompts**, not just translate. The build fails on v1 shapes and the test round fails on the runtime half of the same mistake (f72: `json.Unmarshal` into `types.AttributeValue`), so all three stages need it. The fixer additionally gets `lib_hints`, since it cannot verify an import path it was never given, and a new rule that a multi-line `finding module for package` failure is usually *one* bad import, not a network problem.
  4. **`client_factory_literals`** (extract.py): string literals passed to any client/resource factory call, which catches a project's own boto3 wrapper. f26 and f90 reach boto3 only through `get_client(service, event)`, so the service name appears solely at the call site; both were in the failed set. Kept **separate from `boto3_services`** on purpose — that field feeds the `n_boto3_services` feature column, and widening it would change the values the shipped model was fitted on. Verified: `cmd/pyscan` over `evaluation_set` is byte-identical to `evaluation/prediction/features.csv` across all 95 rows and 56 columns.
  5. The pointer-vs-value bullet moved out of the generic pitfalls list (where it fired for every function) into `aws_hints`, and the generic slot now carries the package-shadowing rule the 14 shadowing diagnostics call for.
- Coverage on `evaluation_set`: 58/95 functions get the idiom block (exactly the 58 AWS functions), 54 get at least one exact module path, 23 distinct services resolved, **0 unresolved**.
- Tests: `internal/pyscan/pyscan_test.go` (`TestAWSServicesResolvesDivergentNames` pins the four traps and the unknown-service report; `TestLibHintsResolveServicesBehindAWrapper` covers f26's shape *and* asserts `boto3_services` stays untouched; `TestAWSHintsOnlyForAWSFunctions`), `internal/pipeline/pyscan_test.go` (exact path in `lib_hints`, `aws_hints` published), and `internal/translator/prompts_test.go` — new: every embedded prompt must parse and render both with and without the AWS vars, since a mismatched `{{ if }}`/`{{ end }}` otherwise surfaces only when a converter is constructed, potentially an hour into a run.
- The other half of this failure — `extractDiagnostics` discarding the causal line of a `go mod tidy` failure, so the fixer could not act on a bad path even once it occurred — is fixed separately as [C13].
  - Interpreter: auto-detected `python3`/`python`, override with `PYSCAN_PYTHON`; added to the Dockerfile. The stage degrades to a warning when it cannot run (it enriches a prompt), except under `required: true`.

### [~] [C9] Support multi-file Python inputs — DROPPED
- Category: Feature
- Affected component(s): `internal/inputhandler/reader.go`, prompts, `codeBlockGenerator`
- Problem / current state: Uploads with more than one `.py`/`.go` root file are now **rejected with a clear error** ([A17], maintainer decision 2026-07-04) — the silent last-wins mistranslation mode is gone. What remains is actual *support*: scraped AWS function sets will contain multi-module functions that currently cannot be translated at all.
- Proposed change: When needed (triggered by the scraped test sets), replace the rejection with minimal support: pick the root deterministically (the handler-containing file — `def lambda_handler`/`def handler` — else `main.py`, else lexically first), collect the remaining `.py` files into `BuildFiles` so they flow into `{{ .code }}`, and fix `codeBlockGenerator`'s fence language for non-Go build files (it currently hardcodes ```go).
- Why: Expands the input domain the pipeline can be *correct* on; the fail-fast rejection already protects correctness, so this item is purely about coverage.
- Architecture impact: Local | Effort: M | Priority: P2
- **DROPPED 2026-07-05 (maintainer decision): will not be implemented.** Single-file input is the contract — the dataset pipeline already inlines repo-local imports, so every artifact ships one self-contained `main.py` (EVALUATION_DATASET.md §2). The [A17] rejection stays as the guard: a multi-source upload fails fast with a clear error rather than being silently mistranslated from a fragment. Nothing further to do here; leaving the item for the record.

### [x] [C10] Unified test-fixture schema and per-job validation routing (goTester vs. flociTester)
- Category: Feature
- Affected component(s): `internal/domain/types.go` (`TestFile`, incl. the declared-but-unused `Services` field), `internal/floci/testcase.go` (`parsePackageTestCase` already shape-detects both dialects — reuse it), `internal/builder/validator.go`, `internal/service/service.go` (job admission), pipeline configs
- Problem / current state: Two validation paths exist (black-box `goTester` and the Floci integration stage) with two fixture dialects, but nothing routes a job to the right one. Today a side-effecting function can be "validated" by `goTester` alone (meaningless — side effects unchecked), and a Floci-dependent job can run with Floci disabled and silently skip its only real validation (`flociTester` is a no-op when disabled).
- Proposed change (maintainer-specified decision matrix, 2026-07-04): (1) Document **one** fixture schema covering both kinds: plain `input`/`output` for pure functions; `payload`/`expectedOutput`/`setup`/`sideEffects` for side-effecting ones (formalize the shape detection that `floci.parsePackageTestCase` already implements; decide whether `TestFile.Services` is consumed by it or removed). (2) At job start, classify the upload: *floci-required* iff any fixture declares `setup`/`sideEffects`. (3) Route per job: Floci enabled + required → `flociTester` validates; Floci enabled + not required → standard `goTester`; **Floci disabled + required → block the translation with a clear error before any LLM call**; Floci disabled + not required → standard `goTester`. `FLOCI_ENABLED` remains the single switch for whether the Floci service runs at all.
- Why: Ensures every translation is validated by the strongest harness its fixtures demand and turns the current silent no-op into an explicit contract — a prerequisite for the thesis goal of validating side-effecting workloads. Maintainer requirement; no external source needed.
- Dataset urgency (2026-07-05): **40 of the 95 `evaluation_set` functions declare `setup` resources** and 13 assert `sideEffects` (EVALUATION_DATASET.md §3, gotcha 4). Without this routing, running the benchmark with Floci down yields 40 functions failing for infrastructure reasons that are indistinguishable from translation defects — so the "block when Floci is required but disabled" branch is what protects the headline result, not a nicety.
- Status: **Implemented 2026-07-05.** Part (1), the unified schema, landed earlier as [C12]; this completes the routing.
  - **Classification**: `fixture.RequiresFloci(cases)` — true iff any case declares `setup`/`sideEffects`. It builds on the existing `TestCase.HasSideEffects()` (`len() > 0`), so the canonical schema's *empty* `setup: []`/`sideEffects: []` blocks correctly classify a pure function as black-box, and legacy `input`/`output` fixtures keep working through `fixture.Parse`'s lowering.
  - **Routing**: a new `testRouter` converter (`internal/pipeline/testrouter.go`) resolves both testers *by name through the converter registry*, so the pipeline package imports neither `internal/builder` nor `internal/floci` and the Floci integration stays an optional blank import. It runs exactly one tester per job. Notably `goTester` must **not** also run for a Floci job: it would drive the AWS SDK with no emulator behind it, producing infrastructure failures that look like translation defects — and risking real-AWS calls ([C11]).
  - **Never a silent pass**: if a job needs the Floci route while the backend is disabled, or `flociTester` isn't registered in the binary, the router returns a hard error instead of skipping. (`flociTester` on its own still no-ops when disabled, which is what made a vacuous success possible before.)
  - **Admission** (`inputhandler.Validate`): the same classification rejects such uploads with a `400` naming the offending fixtures, before any LLM call. Floci availability is read per request from the new `Runner.FlociEnabled()` rather than from the environment, so a `/reconfigure` that toggles `floci.enabled` is respected immediately.
  - **Config**: `default.json` and `default.yaml` now use `testRouter` (task ids unchanged); `goTester`/`flociTester` remain valid task names for existing configs, so nothing external breaks.
  - Tests: `internal/pipeline/testrouter_test.go` covers all four rows of the matrix plus the legacy/empty-blocks schema variants, the not-linked-in case, and registry resolution; `internal/inputhandler/validate_test.go` covers the admission block.
- Architecture impact: Local (a routing decision in job admission + existing converters; no pipeline redesign — both testers already exist as registered stages)
- Estimated effort: M
- Priority: P1

### [x] [C11] Prevent AWS leakage: generated code must always resolve to the Floci harness, never real AWS
- Category: Feature / Fault Tolerance
- Affected component(s): `internal/builder/validator.go` (goTester exec env), `internal/floci` (deploy env), `internal/translator/prompts/1-stage-translate-*.md`, optionally a small deterministic post-generation check
- Problem / current state: Input functions scraped from AWS examples call real AWS services. During `goTester` runs (`go run .` with full host network) or Floci-deployed runs, generated Go code that does not honor an endpoint override can silently contact production AWS endpoints — real side effects, cost, and non-reproducible test outcomes. Nothing currently guards against this.
- Proposed change (maintainer requirement, 2026-07-04): (1) Always inject the harness environment into every test execution (`AWS_ENDPOINT_URL` pointing at Floci, dummy `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/region) — for goTester *and* the Floci Lambda env — regardless of what the upload's `.env` contains; (2) instruct the translate prompt ([D1]) that AWS SDK clients must be constructed with the `AWS_ENDPOINT_URL` override (the pattern the existing `examples/output/2026-06-27` translation already shows); (3) add a cheap deterministic post-generation check that flags AWS SDK client construction without an endpoint override before the code is ever executed. Fixture-declared external HTTP APIs (e.g. `f9`/`f10`) remain allowed per maintainer — this item is specifically about AWS service calls.
- Why: Prevents irreversible external side effects and cost during batch evaluation and keeps AWS-backed validation hermetic and reproducible; a single leaked `PutObject` against real AWS would also invalidate the experiment's isolation assumptions. Maintainer requirement; no external source needed.
- Architecture impact: Local
- Estimated effort: M
- Priority: P1
- Status: **Implemented 2026-07-05**, all three parts.
  1. **Execution environment** (`internal/builder/awsenv.go`, `TestExecutionEnv`): every test run gets a purpose-built environment instead of the raw host one. Host `AWS_*` variables are **stripped wholesale** — a developer's or CI runner's ambient credentials (including `AWS_PROFILE`, which can name a role to assume) are the one thing that could turn a translated `PutObject` into a real write. Endpoint, dummy credentials and `AWS_EC2_METADATA_DISABLED` are then forced *last* so neither the package `.env` nor a fixture can override them, while region and S3 path-style are defaults a fixture may legitimately pin (the dataset has a function deriving its region from the invoked ARN). Endpoint/region come from `Runner.FlociEndpoint()`/`FlociRegion()`, new accessors that avoid a `builder`→`floci` dependency.
  2. **Prompts**: both translate prompts now state that AWS clients must honour `AWS_ENDPOINT_URL` (and path-style S3), mirroring what the Python originals do with `boto3.client(..., endpoint_url=...)`. This matters most for AWS SDK **v1**, which never consults that variable on its own.
  3. **Deterministic check** (`warnIfAWSEndpointIgnored`): flags a translation that imports an AWS SDK with no visible endpoint resolution. Deliberately a **warning, not a failure** — SDK v2 resolves `AWS_ENDPOINT_URL` from the environment by itself, so the absence of markers is suggestive, not conclusive, and a hard failure would produce false positives on correct code. Containment is part 1's job; this is the heads-up that a given function would likely fail against the emulator anyway.
- **Fail-closed detail found by the end-to-end test**: a `Runner` built through `NewRunner` carries an empty Floci config, which would have injected an *empty* `AWS_ENDPOINT_URL` — worse than none, since the SDK then resolves the real AWS endpoint. `TestExecutionEnv` therefore supplies its own endpoint/region fallbacks rather than trusting the caller to be configured.
- **Floci route** (original 2026-07-05 decision, **superseded 2026-09-04** — see [C11a] below): the deployed Lambda got dummy credentials, region and metadata-disable via `lambdaEnv`, but deliberately **not** `AWS_ENDPOINT_URL` — the reasoning being that the function runs inside the emulator's network, where the emulator injects the endpoint reachable from *there*, and that overriding it with this process's host-side address would break every side-effect assertion. The first half of that (a host-side address is wrong inside the container) is correct; the second (the emulator supplies one) was an untested assumption, and run 20260831-190900 falsified it.
- Residual risk, documented in code: a translation that ignores the endpoint override entirely can still *attempt* an outbound connection to real AWS. With dummy credentials it cannot authenticate, so no data is read or written; preventing the connection attempt itself would need network isolation (out of scope). Fixture-declared external HTTP APIs (f9/f10) are unaffected by design.
- Tests: `internal/builder/awsenv_test.go` (credential stripping, forced endpoint, overridable region, fail-closed fallback, no slice aliasing between cases) and `TestGoPackageTesterIsolatesAWSEnv`, which runs a real program that reports the env it observes and asserts the host's `AWS_PROFILE`/key are gone.

### [x] [C11a] Inject a container-reachable `AWS_ENDPOINT_URL` into the deployed Lambda
- Category: Bug / Fault Tolerance
- Affected component(s): `internal/floci/lambdaenv.go` (new), `internal/floci/probe.go` (new), `internal/floci/deployer.go`, `internal/floci/stage.go`, `internal/floci/config.go`, `internal/pipeline/runner.go` (`FlociConfig.LambdaEndpoint`)
- Problem / current state: [C11] left the Floci-deployed Lambda without `AWS_ENDPOINT_URL`, on the assumption the emulator injects one. Run `20260831-190900` (95 functions, `runs/run-20260831-170746.jsonl`) shows it does not. Evidence:
  - Once a function built, the pass rate splits almost entirely along the validation route: **goTester 37/47 = 79%**, **flociTester 5/28 = 18%**. By AWS usage: non-AWS 26/37 = 70%, AWS 16/58 = 28% — and AWS functions that happened to route to goTester (where `TestExecutionEnv` *does* force the endpoint) passed **11/13**.
  - 35 of the 52 Floci execution errors are AWS credential/endpoint errors, not behavioural ones: `UnrecognizedClientException` ×21, `InvalidAccessKeyId` ×8 (400/403 — the dummy credentials presented to *real* AWS), `PermanentRedirect` ×6 (301 — real S3 answering a wrongly-addressed bucket request), plus 5 × `Function.TimedOut`.
  - The translated code was not at fault. Every generated `main.go` inspected from that run (f15/f51/f53/f55/f74, extracted from `runs/packages-20260831-190900.zip`) guards its override with `if ep := os.Getenv("AWS_ENDPOINT_URL"); ep != ""` — the branch was simply never taken. Several more failures are the same cause laundered through the translation's own error handling (f55 returns `{"error":"dynamodb get failed"}` on all 5 cases; f83's side-effect assertions fail on writes that silently never landed).
  - 10 of the 23 Floci failures are *wholly* explained by these errors, and roughly 14 once the swallowed ones are counted — i.e. this single defect accounts for most of the gap between the 78.9% build rate and the 44.2% end-to-end rate.
- Change (implemented 2026-09-04):
  1. **The endpoint is injected again**, but the *Lambda-visible* one rather than this process's copy. `docs/floci.md` ("Lambda on native Linux Docker") documents the container → emulator path per deployment shape: the docker bridge gateway on native Linux, the Docker VM on Docker Desktop, container IPs when Floci itself runs in Docker.
  2. **Detected, not guessed** (`probe.go`): a stdlib-only probe Lambda is deployed into the emulator once per process and asked which address reaches it from the inside. It reports (a) whatever `AWS_ENDPOINT_URL` the emulator injected — so the [C11] assumption is now *measured* on every host rather than assumed, (b) its own default gateway read from `/proc/net/route`, so the real bridge is used instead of an assumed `172.17.0.1`, and (c) the caller's static candidates. Result cached per endpoint; warmed in the background by `checkReachable` so the first conversion does not pay for it.
  3. **Fallback is wrong-but-local, never absent.** If the probe cannot run, the first static candidate is used — the same reasoning `Runner.FlociEndpoint` documents for goTester: an unreachable endpoint fails fast against localhost, an unset one resolves to real AWS.
  4. **Escape hatch**: `floci.lambda_endpoint` / `FLOCI_LAMBDA_ENDPOINT` overrides the detection; `off` restores the pre-fix behaviour of injecting nothing.
  5. **The update path now pushes the environment too.** `deployLambda` previously called only `UpdateFunctionCode` for an existing function. Since the Lambda name is reused across conversions, a function created once with the wrong environment kept it for the emulator's whole lifetime — so without this, the fix would not reach any already-running Floci instance.
- Known limitation, *not* fixed here: `lambdaEnv` sets `AWS_S3_FORCE_PATH_STYLE`, which boto3 and the JS SDK read but **the Go SDK v2 does not** — it needs `s3.Options.UsePathStyle` in code, the way `NewClients` sets it for our own client. Path-style S3 in a translated function therefore depends on the translate prompt asking for it (both already do), not on this environment. The 6 `PermanentRedirect` errors are the visible cost when it is missed.
- Tests: `internal/floci/lambdaenv_test.go` — endpoint injected/omitted, credentials and metadata-disable retained, candidate ordering for loopback vs shared-hostname endpoints, port preservation, explicit override and `off`, cache hit avoids the probe, and `TestProbeSourceCompiles` (the probe lives in a string literal, so nothing else in the build would catch an error in it).

### [x] [C12] Unify the two test JSON shapes into one canonical fixture schema
- Category: Feature
- Affected component(s): `internal/domain/types.go` (`TestFile`), `internal/floci/testcase.go` (`parsePackageTestCase`), `internal/builder/validator.go` (`goTester`), `internal/floci` (`flociTester`) — overlaps with [C10]'s routing work but is narrower: this item is only about the *shape* of the fixture JSON, not which tester a job gets routed to
- Problem / current state: `goTester` and `flociTester` currently accept two structurally different fixture dialects: (1) simple black-box I/O — `{"input": "<json-string>", "output": "<json-string>"}` for side-effect-free functions; (2) the Floci side-effect dialect — `{"name", "description", "payload", "expectedOutput", "setup": [...], "sideEffects": [...]}`. Maintaining two shapes (plus `floci.parsePackageTestCase`'s ad hoc shape-detection between them) doubles the fixture-parsing code path and the mental model needed to author a test.
- Proposed change (not yet implemented — structural note only): treat the Floci side-effect shape as canonical and make the simple I/O shape its degenerate case, unified into one schema: `name`, `description`, `payload` (parsed JSON value passed as the event — for simple fixtures this is just the old `input`), `expectedOutput` (optional — omitting it means "skip output validation, assert side effects only"; for simple fixtures this is the old `output`), `setup`/`sideEffects` (empty for simple fixtures), plus an `outputMode` (`tolerant` default / `strict` / `shape` — `shape` replacing today's `undeterministic: true` flag) and a `provenance` field (`{"method": "heuristic|mined|llm", "output_source": "golden|authored"}`) recording how the fixture was produced. Legacy `input`/`output`/`deterministic` fixtures would be auto-lowered into this canonical shape by the parser rather than requiring authors to rewrite existing fixtures.
- Why: One schema instead of two removes duplicated parsing/detection logic in `floci.parsePackageTestCase` and `builder/validator.go`, gives every fixture a place to record how it was generated (useful once fixture mining/generation is explored), and folds `undeterministic`/`deterministic` into the same `outputMode` concept already introduced by [B1]'s shape-only comparison mode — rather than keeping it as a separate boolean alongside the new mode enum.
- Architecture impact: Local (schema/parsing consolidation; no new pipeline stages) | Effort: M | Priority: P2
- Status: **Implemented 2026-07-10.** New shared package `internal/fixture`: `TestCase` (the rich shape, now with an optional `env` field carrying the legacy per-test env overrides), `Assertion`, `MatchOutput` (moved from `floci/output.go`; the vectors in `floci/output_test.go` still pin its semantics through a thin wrapper, unchanged), `Parse`/`FromPackage`/`LoadFromDir` with the legacy lowering (input→payload incl. base64-encoded-input decoding, output→expectedOutput omitted-when-empty, `undeterministic`/legacy `deterministic`→`outputMode: "shape"`, name defaults to file stem). `floci` aliases these types (`TestCase = fixture.TestCase`), so the checker/setup registries and the external-contract vocabulary are untouched. `goTester` now parses fixtures per-file through `fixture.Parse` (sorted, one broken fixture no longer aborts the rest), feeds `payload` on stdin, unwraps the harness envelope, and judges `expectedOutput` via `fixture.MatchOutput` per the fixture's `outputMode`; cases with `setup`/`sideEffects` run output-only with a warning ([C10] owns the routing). The translator's prompt context (`{{ .tests }}`/`{{ .input }}`/`{{ .output }}`) parses the same schema, so rich fixtures render correctly. **Deleted:** `ValidationStrategy`/`SimilarityValidation`/`JSONStructureValidation` and the `task_args.strategy` switch (now ignored with a deprecation log; removed from all bundled configs), `DeploymentPackage.GetTestFiles`, and the `adrg/strutil` dependency. **Semantics change (intentional):** legacy fixtures without `outputMode` are now compared with the canonical *tolerant* default in both stages (previously Strict under `strategy: "json"`, fuzzy similarity otherwise); fixtures wanting strict scalar typing declare `outputMode: "strict"`. Docs updated (README, docs/floci-integration.md, CLAUDE.md). Tests: `internal/fixture/testcase_test.go` (lowering round-trip, base64, rich passthrough incl. ignored `provenance`, deterministic order), `validator_test.go` (goTester-reads-rich, rich mismatch evidence, env merge, harness-output modes).

---

## D. Per-stage prompt improvements

### [x] [D1] Convert prompt: fix the few-shot that teaches broken response bodies; state the harness contract
- Category: Prompt-Convert
- Affected component(s): `internal/translator/prompts/1-stage-translate-1.md` (the wired `coder` prompt) **and** `1-stage-translate-2.md` (same few-shot content; used by `coder2` in `scripts/summary-pipeline.json`, which will be evaluated against `default.json` — both prompts must get identical fixes or the comparison measures prompt drift instead of the summary stage)
- Problem / current state: (1) The "Input Handling" few-shot returns `Body: fmt.Sprintf("%v", map[string]interface{}{...})`, which prints Go map syntax (`map[result:3]`), not JSON — but the fixtures (e.g. `f2`: `"body": "{\"result\": 3}"`) expect a JSON string body, so the example *teaches the model to fail the tests*. (2) The prompt never explains how the code is executed (stdin event → `handle` → response wrapped as `{"response": ...}`). (3) The output example shows `go 1.x`/`v1.x` placeholders a literal-minded small model will copy into an invalid `go.mod`. (4) `{{ .output }}` is shown without the `{{ .input }}` that produces it (variable exists but is unused). (5) No Python→Go semantic gotchas.
- Proposed change: Replace the body few-shot with `json.Marshal` + `string(b)`; add an "Execution contract" section; pair input *and* expected output (use [C7]); drop the `go.mod` example per [C3]; add a compact gotcha list (`dict.get(k, default)` → explicit zero-value handling; `raise X` → non-2xx statusCode branch; f-strings → `fmt.Sprintf`; Python `True/None` casing vs JSON `true/null`; integer vs float division).
- Why: Few-shot examples dominate output form — a demonstrably wrong exemplar is actively harmful (in-context learning imitates demonstrations; Brown et al., arXiv:2005.14165); stating the I/O contract turns "semantic equivalence" into a checkable output format.
- Note: point (3) (`go.mod` placeholders) is already done by [C3] — both translate prompts now request a single `main.go` and state that dependencies are automatic. Remaining scope: the body few-shot fix, the execution contract, input/output pairing, and the gotcha list.
- Status: **Implemented 2026-07-04** (together with [C7], both prompts identically). The body few-shot now uses `json.Marshal` + `string(body)` with an explicit "never `fmt.Sprintf(\"%v\", ...)`" warning; a new "Execution Contract" section states how the harness invokes and compares the code (raw event in, `StatusCode`/JSON-string `Body` compared against the Python return, mirror error branches as statusCodes); a "Python → Go pitfalls" list covers `dict.get` defaults, `True/None` vs `true/null`, f-string formatting, `/` vs `//` division, and `json.Marshal` bodies; and the input section embeds `{{ .tests }}` (input→expected pairs) with a fallback to the old single `{{ .output }}` block. Template rendering covered by `TestTranslatePromptRendersTests`.
- Architecture impact: Local | Effort: S | Priority: **P0**

### [x] [D2] Align prompt: give it the failure evidence and a checkable definition of "equivalent"
- Category: Prompt-Align
- Affected component(s): `internal/translator/prompts/3-stage-align.md`
- Problem / current state: The prompt asks the model to verify alignment using only `{{ .original }}` and `{{ .code }}` — no test results, no `{{ .issue }}`, no input/output pairs — even though the stage only runs *after concrete tests failed*. Also: numbering gap (rules 5→7), untagged code fence, step-by-step/JSON-only contradiction.
- Proposed change: Restructure around evidence: "The Go version failed these test cases: [input, expected stdout, actual stdout] ({{ .failures }} from [C1]; minimally `{{ .issue }}` + `{{ .input }}`/`{{ .output }}` today). Modify the Go code so that for each input it produces exactly the expected output. Do not change behavior for passing cases. Return complete corrected files." Fix numbering/fence.
- Why: Converts an open-ended judgment task into a constrained transformation with a verifiable target — the setting where execution-feedback repair is proven effective (Chen et al., arXiv:2304.05128); the prompt-side half of [C1].
- Architecture impact: Local | Effort: S | Priority: **P0**
- Status: **Implemented 2026-07-04.** `3-stage-align.md` rewritten: it now states the harness execution contract, embeds `{{ .failures }}` via a template conditional ("fix the Go code so that each listed input produces exactly the expected output; do not change behavior for inputs not listed"), and falls back to `{{ .issue }}` when no evidence exists. Numbering gap (5→7) fixed, Python fence tagged, the step-by-step/JSON-only contradiction removed *for this prompt* ([D4] still covers translate/repair), and "return the full corrected file" added. Template rendering covered by `TestAlignPromptRendersFailureEvidence`.

### [x] [D3] Fix-errors prompt: structured compiler errors, no contradictions, minimal-change directive
- Category: Prompt-FixErrors
- Affected component(s): `internal/translator/prompts/2-stage-repair.md`, `internal/builder/builder.go` (error text construction)
- Problem / current state: `{{ .issue }}` is the raw combined stdout/stderr wrapped in `failed to build. … exit status 1`. The prompt says both "you only return the code for the handler function" and "return the complete code and other files" — a contradiction a small model resolves unpredictably. Its example output embeds a `go.mod` with literal `\r\n` escapes and the invalid `v1.24` version. Nothing tells the model to preserve working parts.
- Proposed change: Pre-parse Go compiler output (`file:line:col: message`) into a numbered list; delete the "only the handler function" sentence; add "change only what is necessary to fix the listed errors"; remove the `go.mod` example per [C3].
- Why: Precise, localized error context is the input format compiler-repair works best with — the Go toolchain already emits machine-parseable positions; removing the format contradiction eliminates a coin-flip in every fixer call.
- Note: the broken `go.mod` example and the "other files" wording are already fixed by [C3] (single-`main.go` output stated in the prompt). Remaining scope: structured compiler errors and the minimal-change directive.
- Status: **Implemented 2026-07-04** (together with [D4]/[E4]). `formatBuildError` in `builder.go` parses raw build output into a numbered, de-duplicated list of diagnostics (`file:line:col: message`, `go:` module errors, `go.mod:` parse errors), preserving the raw dump only when nothing is parseable — and keeping the marker lines verbatim so `isGoModFailure` still triggers. `2-stage-repair.md` rewritten: "change only what is necessary", "fix the first error first — later errors are often consequences", contradiction and step-by-step line removed, grammar fixed, full-file return required. Tests: `builder_test.go` (parsing, capping, marker preservation, raw fallback).
- **Extraction corrected 2026-09-04 — see [C13]**: the numbered list was right for compiler output but wrong for `go mod tidy`, where it reported only progress lines and dropped the one line that named the failure.
- Architecture impact: Local | Effort: S–M | Priority: P1

### [x] [C15] Replace the `cleaner` opening stage with `summary`, and make prompt-enrichment stages fault-safe
- Category: Feature / Fault Tolerance
- Affected component(s): `scripts/benchmark.json`, `internal/pipeline/pipeline.go` + `pipeline_io.go` (`Optional`), `internal/translator/prompts/1-stage-translate-2.md`, `internal/pipeline/shipped_configs_test.go`
- Problem / current state: the benchmark pipeline opened with `cleaner`, which asks the model to re-emit the entire Python file with comments added. Measured over run 20260831-190900:
  - **409,853 tokens — 14.6% of the run** — and **86 of its 436 wall-clock minutes**, at a mean 2,404 *output* tokens per job, to produce an artifact that is discarded after translation.
  - Worse than the cost: it puts an LLM rewrite of the source *between* the original function and its translation. Every later stage translates the model's Python, not the user's, and any drift introduced there is invisible — nothing in the pipeline compares the two.
  - It was also a single point of failure. **f62 died here**: two ChatAI timeouts (`context deadline exceeded`) exhausted the stage's retries and ended the conversion with zero build attempts, over a prompt embellishment.
- Change (2026-09-04):
  1. **`clean`/`cleaner` → `summarize`/`summary`.** `summary` runs in metadata mode: it returns one sentence into `{{ .intent }}` and leaves `WorkingPackage` untouched, so `convert` translates the original source. The prompt-input cost is the same, the output cost drops from ~2,400 tokens to a sentence.
  2. **`convert`: `coder` → `coder2`.** These are one change, not two: only `1-stage-translate-2.md` reads `{{ .intent }}`. Pairing `summary` with `coder` would pay for a sentence nobody reads — and the mistake is silent, since the run completes and looks normal. `shipped_configs_test.go` now pins the pairing.
  3. **New task field `optional`** (default false, so every existing pipeline is unchanged). When an optional task exhausts its retries, `executeTask` logs, records the error and continues rather than failing the job, and skips that task's own `Validation` — there is nothing it produced to validate. This is the generalisation of the failure policy `pyScan` already had, moved to the layer that owns retry/abort: a stage that only enriches a prompt must not be able to fail a conversion.
  4. `{{ .intent }}` is guarded with `{{ if .intent }}`, so a degraded summary leaves no dangling "Intent:" heading.
- **Deliberately rejected**: the alternative of keeping a code-rewriting stage that inserts the intent back as a comment. It reintroduces exactly the problem — the model regenerates the whole file to add one line, paying the output cost again and reopening the drift window — for a comment the translate prompt can receive directly as a template variable.
- Limits of `optional`, pinned by tests: cancellation and a prediction-gate decline return early from the retry loop, so marking a task optional never keeps a stopped or declined job running; the retry budget is still spent in full before degrading; and the failure is still recorded in `req.Errors()` and `Metrics.PerTask`, so a degraded job is distinguishable from a clean one in the run log.
- Expected effect on the next run: ~8% fewer tokens and ~80 fewer wall-clock minutes, the translation performed against the unmodified source, and f62's failure mode eliminated.
- Tests: `internal/pipeline/optional_test.go` (degrade after retries, default still fails the job, validation skipped, cancellation still aborts, the flag survives compilation), `shipped_configs_test.go` (summary present and optional, paired with coder2, `cleaner` rejected outright), `internal/translator/prompts_test.go` (no "Intent:" heading without an intent).
- Architecture impact: Local | Effort: S | Priority: P1

### [x] [C14] Retry budgets: measured, left alone, and the stagnation guard made able to see a repeat
- Category: Bug / Evaluation
- Affected component(s): `internal/domain/stagnation.go` (new, `normaliseFailure`), `internal/domain/types.go` (`RecordFailure`), `internal/pipeline/pipeline.go` (threshold commentary), `scripts/benchmark.json` (documented budgets)
- Problem as originally stated (2026-09-04 analysis of run 20260831-190900): the two repair stages consume 61% of all tokens, 30 jobs burned the full realign budget and aborted, and the proposal was to cut `builder` 4→3 and the stagnation abort 3→2.
- **That proposal was wrong, and re-deriving the marginal yield per attempt is what showed it.** Passes gained by each successive attempt:
  - `builder`: attempt 1 → 48, 2 → +19, 3 → +6, **4 → +2**. Cutting to 3 would have cost two functions.
  - `goTester`: 1 → 29, 2 → +6, 3 → +5, 4 → +2, 5 → +0 (2 jobs, both failed).
  - Stagnation guard: flagging at the 2nd identical failure produced **four rescues** — f41, f57, f60 on goTester and one on builder all failed identically twice, got the `{{ .stagnant }}` nudge, and then passed. Aborting at 2 would have cost all four. Aborting at 3 costs nothing: no task in 95 jobs ever recovered after three consecutive identical failures.
  Every budget is therefore already at the knee of its yield curve. The numbers stay; they are now documented with the measurement beside them so they are not re-tightened on intuition.
- **The real defect was that the guard could not recognise a repeat.** Of the 20 functions that never built, 8 exhausted their budget without the guard firing — not because they were progressing but because byte comparison could not see through cosmetic variation:
  - f0 reported `undefined: mail.AddressList` four times at 101:52, 102:52, 102:74, 101:74 — one defect, four coordinates.
  - f82 alternated the same type error between 69:19 and 73:25; f38 cycled `aws.Bool(undo)` → `&undo` → `aws.Bool(undo)`.
  - f16/f26 failed `go mod tidy`, whose lines come back in nondeterministic order, so **a module-resolution loop could never be detected at all**. ([C13] shrinking that output to its causal line helps; normalising order is what makes it reliable.)
- Change: `RecordFailure` compares normalised text. Exactly two axes are normalised — Go diagnostic **positions** (`file.go:101:52:` → `file.go:`) and the **order** of the numbered diagnostic list (sorted, de-duplicated). Identifiers, types, messages, test names and failure kinds are untouched, because those are what distinguish one defect from another.
- **Validated by replaying the full run before changing anything**: the normalised guard newly fires on 8 builder jobs and 1 goTester job, **all of which failed anyway**, and on **zero** jobs that succeeded. f10, f20, f26 and f44 abort a full attempt earlier; the rest are at least recorded as stagnation rather than budget exhaustion, which is the difference between a run log that explains itself and one that does not.
- **Caveat for the next run**: 23 of the 30 jobs that exhausted the goTester budget were Floci-route jobs failing on AWS credentials, so this whole yield curve was measured through the defect [C11a] fixes. Re-measure before drawing conclusions from it again.
- Tests: `internal/domain/stagnation_test.go` — f0's position drift and f16/f26's diagnostic reordering are now detected, while four real-progress transitions (failure kind changes, fewer cases failing, a different fix attempt at the same position, one diagnostic resolved of two) must still reset the counter.
- Architecture impact: Local | Effort: S | Priority: P1

### [x] [C13] Diagnostic extraction reports the cause of a `go mod tidy` failure, not its progress
- Category: Bug
- Affected component(s): `internal/builder/builder.go` (`extractDiagnostics`, new `collectDiagnostics`/`appendCapped`, `goProgressRe`, `maxModuleErrors`, `maxContinuationLines`)
- Problem / current state: [D3]'s extraction was designed against *compiler* output and is wrong for the module resolver. Verified against real `go mod tidy` output captured from the toolchain (`tidyFailureOutput` in `builder_test.go`), two independent defects compounded:
  1. **Progress consumed the whole budget.** `go mod tidy` prints `go: finding module for package X` once per import — resolvable or not — then `go: downloading …` and `go: found … in …` per module. All match the `"go: "` prefix test, so `maxCompilerErrors = 5` was spent on them before any error line was reached.
  2. **The causal line was unreachable at any cap.** The real failure is a two-line block whose second line is indented with a tab rather than prefixed `go: `, so the prefix test discarded it outright: `go: example.com imports` / `\t…/service/iotdata: module …@latest found (v1.45.1), but does not contain package …/service/iotdata`.
  The result for f16 in run 20260831-190900 was a fixer prompt listing five `finding module for package <valid path>` lines — one per *correct* import — plus "further errors omitted". Nothing in it was wrong, the bogus `service/iotdata` never appeared, and the stage burned all four builder attempts. The marker occurs 46 times across that run.
- Change (implemented 2026-09-04):
  1. `goProgressRe` filters resolution chatter (`finding module for package`, `downloading`, `found … in`, `upgraded`, `extracting`) before anything is capped.
  2. Indented lines are **folded into the diagnostic above them** instead of dropped. This also recovers the compiler's own explanations, which since Go 1.18 carry the reason for a type error on a continuation line (`*s3.Client does not implement Storer (missing method Put)`). Bounded at `maxContinuationLines = 4`.
  3. **Two caps, not one.** The cascade argument justifying a cap of 5 is about compiler errors; module errors do not cascade — each block is an independent unresolvable import, and the last is as likely to be the culprit as the first — so they get their own, higher cap and cannot be crowded out by a compiler cascade.
  4. The omission note is emitted only when real diagnostics were dropped, and says how many. The old unconditional note was itself misleading: what it had omitted was usually progress.
- **Second, silent consequence, also fixed**: `isGoModFailure` runs on the *formatted* error, so a marker (`missing go.sum entry`, `unknown revision`, …) pushed past the cap by progress lines took the deterministic `rebuildWithFreshGoMod` fallback with it — the build then failed for a reason the pipeline already knew how to repair. Pinned by `TestGoModMarkerSurvivesProgressChatter`.
- Result on the captured failure: the fixer now receives exactly one diagnostic — `go: example.com imports github.com/aws/aws-sdk-go-v2/service/iotdata: module github.com/aws/aws-sdk-go-v2@latest found (v1.45.1), but does not contain package …/service/iotdata` — instead of five correct import paths.
- Complements [C8a], which makes the bad import less likely; this makes it recoverable when it happens anyway.
- Tests: `builder_test.go` — `TestExtractDiagnosticsSurfacesTheModuleCause` (against the verbatim toolchain output: cause present, progress absent, no valid import reported), `TestExtractDiagnosticsFoldsCompilerExplanations`, `TestExtractDiagnosticsCapsClassesSeparately`, `TestGoModMarkerSurvivesProgressChatter`; the original [D3] tests are unchanged and still pass.
- Architecture impact: Local | Effort: S | Priority: P1

### [x] [D4] All prompts: remove the "step by step" vs. "output nothing but JSON" contradiction
- Category: Prompt-Convert / Prompt-FixErrors / Prompt-Align
- Affected component(s): `1-stage-translate-1.md` (rule 1 vs. 7), `2-stage-repair.md` (rule 1 vs. 5), `3-stage-align.md` (rule 1 vs. 7)
- Problem / current state: Each prompt instructs "Let's work this out in a step by step way…" while a later CRITICAL rule forbids any output except JSON. Under a JSON-constrained decoder the reasoning instruction can't be followed; on an unconstrained backend it invites prose that breaks `json.Unmarshal`.
- Proposed change: Delete the step-by-step sentence (or give reasoning a sanctioned `"notes"` key in the output schema that readers ignore).
- Why: Contradictory instructions measurably degrade instruction-following, and smaller models are the most sensitive; constrained decoding already nullifies the CoT benefit — based on structured-output decoding mechanics (Ollama structured outputs documentation).
- Architecture impact: Local | Effort: S | Priority: P1
- Status: **Done 2026-07-04.** The contradiction was removed prompt by prompt as each was rewritten: translate prompts by [D1], align by [D2], and repair (the last holdout) by [D3]'s rewrite. No active prompt contains the "step by step" instruction anymore (the unwired `1-stage-translate.md` draft still does — irrelevant, not embedded).

### [ ] [D5] Document stage: scope it to what translation actually needs (or merge it with summarize)
- Category: Prompt-Document / Efficiency
- Affected component(s): `internal/translator/prompts/0-stage-document.md`, `internal/translator/prompts.go`
- Problem / current state: The cleaner adds generic inline comments, doubling the source's token footprint through every later stage without targeting translation; costs a full-source LLM round-trip; grammar is off; it's the stage whose output most easily corrupts the package under the Basic reader (A15).
- Proposed change: Refocus on translation-relevant facts: input-event/response shape, env vars read, external services called, error branches and status codes. Alternatively: drop the separate cleaner call and extend `summary`'s `output_keys` to return both `intent` and documented source in one call.
- Why: Comments that spell out I/O shape and side effects are precisely the context that reduces translation ambiguity for a small model; generic comments are inert tokens.
- Architecture impact: Local | Effort: S | Priority: P2

---

## E. Small-model robustness

### [x] [E1] Enforce per-task output schemas for the code-producing stages
- Category: Small-Model Robustness
- Affected component(s): `internal/translator/prompts.go` (factories), `internal/llmconnector/schema.go`, `outputschema.go`
- Problem / current state: Only `summary` sets `output_keys`. `coder`/`fixer`/`realign` fall back to the generic any-object schema on Ollama (an empty `{}` satisfies it), a `main.go/go.mod/main.py` triple on Gemini (invites a stray `main.py`), and schema-less `json_object` on ChatAI.
- Proposed change: Default `output_keys` to `{"main.go": {nullable:false}}` in the code-task factories (mirroring `NewSummaryConverter`); emit `required: ["main.go"]` in the Ollama/ChatAI schema payloads. Prefer **fixed, closed schema shapes** (explicit keys, no unbounded `additionalProperties`) — which [C3]'s single-`main.go` output makes possible.
- API findings (verified 2026-07-04 via experiments, see `scripts/chatai-check-json-schema.sh`): the GWDG proxy passes `response_format: json_schema` straight through to the model's backend (vLLM guided decoding). Enforcement is real when it works but is a **per-model capability**: weaker models (Llama-3.1-8B) fail to compile a grammar for unbounded `additionalProperties` and **silently fall back to unconstrained text** — the proxy never rejects an unsupported schema with an error. Fixed-shape schemas worked on all models tested. Consequences: (a) use closed schemas, (b) run the check script against each evaluation model before an experiment, (c) never trust enforcement alone — Go-layer validation ([E2]) is mandatory.
- Why: Constrained decoding moves format compliance from the model's competence to the sampler — the highest-value trade for a ~30B model; both Ollama's and OpenAI's structured-output docs report large reductions in format errors vs. prompt-only instructions, and the 2026-07-04 experiments confirm closed shapes are the reliable subset on this backend.
- Architecture impact: Local | Effort: S | Priority: P1
- Status: **Implemented as part of [C3]** (2026-07-04): closed single-file `output_keys` defaults on every LLM stage factory; `required` emitted by Ollama/ChatAI/Gemini for non-nullable fields; Gemini's `main.go/go.mod/main.py` fallback triple is now only reachable via the raw `llmTask` factory. What remains is **operational**: run `scripts/chatai-check-json-schema.sh` against each evaluation model before an experiment, since enforcement stays per-model ([E2] is the code-side safety net).

### [x] [E2] Deterministic JSON extraction fallback before failing a parse
- Category: Small-Model Robustness
- Affected component(s): `internal/translator/readers.go` (`JsonCodeBlockReader` — despite the name, it does *not* strip code fences)
- Problem / current state: Any leading prose, a ```` ```json ```` fence, or trailing commentary makes `json.Unmarshal` fail; the reader logs and returns nil, surfacing as the misleading "could not find main". ChatAI's `json_object` mode makes this a live path.
- Proposed change: On unmarshal failure, deterministically retry: strip markdown fences, extract the first balanced `{…}` region, re-parse; only then fail — with an error saying "response was not a JSON object".
- Why: Recovers, at zero token cost, the most common small-model formatting slip instead of burning a full LLM retry.
- Architecture impact: Local | Effort: S | Priority: **P0** (raised 2026-07-04: API experiments confirmed the ChatAI backend **silently** drops schema enforcement on models that can't compile the grammar — see [E1] — so the Go layer is the only reliable line of defense against unconstrained text responses)
- Status: **Implemented 2026-07-04.** `JsonCodeBlockReader` now returns `(map[string]string, error)` and tries three extractions in order: the trimmed raw response, the contents of the first markdown code fence (language tag stripped), and the first balanced top-level `{...}` region (string/escape-aware, so braces inside embedded code don't break matching). Non-string fields in an otherwise valid object are skipped instead of failing the response. On total failure the error now says "LLM response is not a JSON object …" (truncated echo) instead of the old nil map that surfaced downstream as the misleading "could not find main". All three call sites (both package readers + metadata mode) propagate the new error. Regression tests in `readers_test.go` cover fenced/prose-wrapped/escaped-brace responses, non-string skipping, truncated JSON, and plain garbage. Note: a *truncated* response still fails (no balanced object) — detecting that specifically via finish/done reason remains [F3].

### [x] [E3] Vary sampling on resample-style retries
- Category: Small-Model Robustness
- Affected component(s): `internal/pipeline/pipeline.go` + `internal/translator/translator.go` (`Prepare` runs fresh per attempt — the hook exists)
- Problem / current state: Retries of `cleaner`/`coder` (no recovery task) re-send the identical prompt at `temperature: 0.1`; a near-greedy model reproduces essentially the same wrong output, so `maxRetryCount` on those tasks buys almost nothing.
- Proposed change: Track the attempt number (e.g. via `req.Metadata`) and add an opt-in temperature bump (`task_args.retry_temperature`) on attempts >1.
- Why: Sampling diversity is what makes repeated attempts explore different solutions — the core observation behind self-consistency (Wang et al., arXiv:2203.11171).
- Architecture impact: Local | Effort: S–M | Priority: P1
- Status: **Implemented 2026-07-10.** `domain.ConversionRequest` gained a transient `CurrentAttempt int` field (mirroring `CurrentTask`, deliberately *not* `req.Metadata` — that map is promoted into prompt template vars, so a bookkeeping key there would leak into `{{ .* }}`). `pipeline.go`'s `executeTask` sets it to `task.RetryCount + 1` fresh on every retry-loop iteration (both the Execute-failure and validation-failure retry paths funnel through it, so both count as "attempts"). `translator.go`'s `LLMConverter` gained an opt-in `task_args.retry_temperature`: parsed once in `NewLLMConverter` (and stripped from `taskParams` like `prompt`/`reader`/`mode`), then `paramsForAttempt` hands `Client.Prepare` a one-off copy with `"temperature"` overridden only when `retry_temperature` is configured *and* `CurrentAttempt > 1` — the converter's own long-lived `taskParams` map is never mutated, so a bump can't leak into a later, unrelated first attempt. Fully opt-in: unconfigured tasks see byte-identical `taskParams` on every attempt, exactly as before. Also fixed a related pre-existing gap while here: `internal/llmconnector/gemini.go` previously hardcoded `temperature = 0.1` in every `InvokeLLM` call regardless of `taskParams`, so neither a pipeline's base `temperature` nor this new `retry_temperature` had any effect on the Gemini backend; `Prepare` now reads `"temperature"` (same key `ollama`/`chatai` already honor) into a new `Temperature` field that `InvokeLLM` uses, defaulting to the old hardcoded `0.1` when a task doesn't set one. Tests: `TestExecuteTaskTracksCurrentAttempt` (`internal/pipeline/pipeline_test.go`), `TestLLMConverterRetryTemperature`/`TestLLMConverterNoRetryTemperatureConfigured` (`internal/translator/translator_test.go`, the package's first test file), `TestGeminiInvocationClientPrepareSetsTemperature` (`internal/llmconnector/gemini_test.go`).

### [x] [E4] Truncate feedback to the first compiler errors
- Category: Small-Model Robustness / Efficiency
- Affected component(s): `internal/builder/builder.go`, `2-stage-repair.md` input
- Problem / current state: The fixer receives the full build output; Go compilers cascade — one missing brace produces dozens of downstream errors that mislead a small model into "fixing" symptoms.
- Proposed change: After [D3]'s parsing, pass only the first N (e.g. 5) distinct `file:line` errors, noting "further errors omitted; fix these first".
- Why: Focusing a limited-capacity model on the root error mirrors how cascading diagnostics are meant to be consumed and shrinks the prompt.
- Architecture impact: Local | Effort: S | Priority: P2
- Status: **Implemented 2026-07-04** as part of [D3]: `extractDiagnostics` de-duplicates and caps at `maxCompilerErrors` (5) distinct diagnostics, appending "... further errors omitted; fix the ones above first"; the repair prompt tells the model later errors are often consequences of the first.

### [x] [E5] Stop sending junk/foreign parameters to Ollama; set an explicit output budget
- Category: Small-Model Robustness / Code Quality
- Affected component(s): `internal/llmconnector/ollama.go` (`Prepare`)
- Problem / current state: `Prepare` injects OpenAI-style `max_tokens` and `response_format` defaults into Ollama's `Options`, and leaves the pipeline-level `strategy` key in (ChatAI deletes it; Ollama doesn't) — none are valid Ollama options, and crucially **no `num_predict` limit is actually set**, leaving truncation behavior to model defaults. Confirmed 2026-07-04: Ollama only *warns* about unknown options and ignores them, so the junk keys are harmless noise — the actionable part of this item is solely the missing output budget.
- Proposed change: Mirror ChatAI's deletions (`strategy`, `output_keys`); map a `max_tokens` task param to Ollama's `num_predict`; drop the `response_format` default (the `Format` field already handles structure).
- Why: An explicit, sufficient `num_predict` prevents silent mid-JSON truncation — per Ollama's API documentation, `num_predict` is the generation-length control, not `max_tokens`.
- Status: **Implemented 2026-07-04.** Ollama's `Prepare` now uses an allowlist (`ollamaOptionKeys`) of options `/api/generate` actually understands; everything else is dropped with a debug log for genuinely unknown keys. `max_tokens` is mapped onto `num_predict`, an explicit `num_predict` wins, and a shared `defaultMaxOutputTokens` (also used by ChatAI) applies otherwise — so an output budget is always in effect and overruns become detectable ([F3]). Note: the filtering stays in `Prepare` (not `Configure`) because `Configure` only receives connector `Args`, while these params are per-task and legitimately differ between stages (per-stage model/temperature overrides). Tests in `connector_local_test.go`.
- Architecture impact: Local | Effort: S | Priority: P2

---

## F. Fault tolerance

### [x] [F1] Per-test and per-build-command timeouts
- Category: Fault Tolerance
- Affected component(s): `internal/builder/validator.go` (`doTest`), `internal/builder/builder.go`
- Problem / current state: Test/build subprocesses inherit only the job's cancellation context. A translated function with an infinite loop (or `f9`-style code blocking on a dead URL without an HTTP timeout) hangs the **single** worker goroutine indefinitely — every queued job stalls until someone manually calls `/stop`.
- Proposed change: Wrap each `doTest` run in `context.WithTimeout` (default 30s, override via `task_args.test_timeout`) and each build command similarly (e.g. 120s). Report a timeout as a distinct failure kind so [C1] can feed it to repair.
- Why: Converts a pipeline-wide outage into a single failed test with actionable feedback and protects batch throughput.
- Architecture impact: Local | Effort: S | Priority: **P0**
- Status: **Implemented 2026-07-04.** Per-test timeout (`task_args.test_timeout`, default 30s) and per-build-command timeout (`task_args.build_timeout`, default 2m), parsed via a shared `parseTimeout` (duration string or bare seconds). A timeout surfaces as the new `domain.TestFailureTimeout` kind with an explanatory Stderr, flowing into `{{ .failures }}` ([C1]). Crucial detail found during testing: killing `go run` doesn't close the pipes its compiled *child* holds, so `cmd.Run()` would keep blocking — `cmd.WaitDelay = 2s` forces Run to return anyway (both in `doTest` and `runBuildCommands`). Caveat: the orphaned child may keep running until it exits on its own (process-group kill would be Unix-specific; revisit if orphan CPU burn becomes a real problem). Tests: hanging-program timeout and hanging-build-command tests in `validator_test.go`.

### [x] [F2] Retry transient LLM API failures at the connector, not the task level
- Category: Fault Tolerance
- Affected component(s): `internal/llmconnector/chatai.go`, `ollama.go`, `gemini.go`
- Problem / current state: A 429/5xx/network blip is an ordinary task failure: it consumes a task retry, triggers the recovery LLM task (spending tokens on a "fix" for code with no new defect), and pollutes `LastError()` — the next prompt's `{{ .issue }}` becomes an HTTP error message.
- Proposed change: Retry idempotent transient failures 2–3 times with exponential backoff inside `InvokeLLM`; classify the final error as `domain.LLMError` and have `executeTask` skip `OnFailure` for `LLMError`s.
- Why: Separates infrastructure noise from code defects so retry/recovery budgets are only spent on actual translation problems.
- Architecture impact: Local | Effort: M | Priority: P1
- Status: **Implemented 2026-07-04.** All three connectors retry transient failures (connection errors, 429, 5xx — `transientHTTPStatus`) up to 3 attempts with exponential backoff (`sleepBackoff` in `resilience.go`, context-aware so `/stop` interrupts it); cancellations and deadline hits are never retried; 4xx/decode/truncation errors fail immediately. `LLMConverter.Apply` wraps any remaining `InvokeLLM` error as `domain.LLMError`, and `executeTask` now skips `OnFailure` for `LLMError`s — a recovery prompt cannot fix an outage. Side effect: Ollama's `InvokeLLM` now calls `Generate` synchronously (it honors the context), removing the goroutine/channel from [A13] entirely. Tests: retry/no-retry paths for ChatAI and Ollama via fake HTTP backends (`connector_local_test.go`), recovery-skip vs. recovery-run in `pipeline_test.go`.

### [x] [F3] Detect truncated LLM responses via finish/done reason
- Category: Fault Tolerance
- Affected component(s): `internal/llmconnector/ollama.go` (`DoneReason` ignored on success), `chatai.go` (`finish_reason` not parsed)
- Problem / current state: A response cut off at the token limit reaches the reader as malformed JSON, producing the misleading "could not find main" and an undirected retry.
- Proposed change: Parse `DoneReason`/`finish_reason`; when it indicates length, return a specific error ("response truncated at N tokens"), optionally auto-retry once with a doubled limit.
- Why: Makes an invisible failure mode self-describing and mechanically fixable — especially relevant for small local models with tight `num_ctx`.
- Status: **Implemented 2026-07-04** for all three connectors: Ollama checks `done_reason == "length"` (reporting `eval_count`), ChatAI parses `finish_reason` and reports completion tokens (usage metrics still recorded on truncation), Gemini checks `FinishReason == MAX_TOKENS`. The error text names the knob to raise (`max_tokens`/`num_predict`). The optional auto-retry-with-doubled-limit was skipped — task-level retries plus the actionable error cover it. Tests with fake HTTP backends in `connector_local_test.go` (Ollama + ChatAI truncation and happy paths).
- Architecture impact: Local | Effort: S | Priority: P1

### [x] [F4] Upload handler must not block forever on a full queue
- Category: Fault Tolerance
- Affected component(s): `internal/service/service.go` (`uploadHandler`)
- Problem / current state: `service.requestQueue <- …` blocks the HTTP handler indefinitely when 100 jobs are queued, holding the connection and the parsed upload in memory.
- Proposed change: Non-blocking send (`select` with `default`) returning `503` with a Retry-After hint; also remove the job's `cancels` entry in that path.
- Why: Keeps the service responsive under batch load so evaluation scripts fail visibly instead of hanging.
- Architecture impact: Local | Effort: S | Priority: P2
- Status: **Implemented 2026-07-10.** `uploadHandler` now sends on `requestQueue` via `select`/`default` instead of a blocking send; when the queue is full it cancels the job's context, removes the `cancels`/`status` entries registered just before the send (so no dangling bookkeeping survives a rejected job), and responds `503 Service Unavailable` with a `Retry-After: 30` header instead of blocking the handler goroutine. Added `internal/service/service_test.go` (the package's first test file): `TestUploadHandlerRejectsWhenQueueFull` pre-fills a capacity-1 queue and asserts the 503/Retry-After response and that no `cancels`/`status` entry is left behind.

### [x] [F5] Configurable minimum delay between LLM calls (global rate-limit throttle)
- Category: Fault Tolerance / Efficiency
- Affected component(s): `internal/llmconnector` (a shared throttle around every `InvokeLLM`), configured via `ConverterOptions.Args` / an env default (e.g. `LLM_CALL_INTERVAL`)
- Problem / current state: Experiment pipelines multiply retries × recovery hops × complex input functions into rapid back-to-back LLM calls; provider rate limits (429s) then surface as ordinary task failures that burn retry budget and skew evaluation results. There is no pacing mechanism anywhere. ([F2] handles *reacting* to such failures; this item *prevents* most of them.)
- Proposed change (maintainer requirement, 2026-07-04): a single shared limiter that all connectors wait on before each call, **across all running jobs** — since the service has exactly one worker goroutine, a "sleep until `lastCall + interval`" guard (guarded by a mutex for future-proofing) is sufficient; no scheduler needed. Interval configurable, default 0 (disabled) so local Ollama runs are unaffected.
- Why: Proactively staying under provider rate limits is cheaper and cleaner than reactive retry/backoff alone, and keeps batch-experiment timing comparable across runs. Maintainer requirement; no external source needed.
- Architecture impact: Local
- Estimated effort: S
- Priority: P1
- Status: **Implemented 2026-07-04.** `llmconnector.ConfigureThrottle` reads `LLM_CALL_INTERVAL` (duration string like "2s"/"500ms", or a bare JSON number = seconds; "0s"/empty disables) from `ConverterOptions.Args`; the env default is registered in `defaults.go` and re-read on every Runner build / `/reconfigure` like all other Args. `waitForCallSlot` reserves slots under a mutex (correct even with future concurrent callers) and is called before **every** attempt in all three connectors — retries count as calls too — with context-aware waiting so `/stop` isn't blocked by the throttle. Tests in `connector_local_test.go` (interval spacing, cancellation, disable, numeric form).

### [ ] [F6] Stop logging API keys in plaintext at startup
- Category: Fault Tolerance / Efficiency
- Affected component(s): `internal/service/service.go`'s MakeConverterService has a line: log.Infof("Starting converter service with options: %+v", options) where options is a pipeline.ConverterOptions whose Args map holds connector credentials (ACADEMIC_CLOUD_API_KEY, GEMINI_API_KEY, etc., merged in via envDefaults()). This means every service startup and every /reconfigure logs live API keys in plaintext to whatever captures stdout (files, log aggregators, CI output).
- Fix: redact secret-shaped keys before logging, e.g. add a small helper that copies the ConverterOptions and masks any Args key matching *API_KEY / *KEY / *_TOKEN (or maintain an explicit denylist: ACADEMIC_CLOUD_API_KEY, GEMINI_API_KEY) before passing to log.Infof. Apply the same treatment anywhere else ConverterOptions or Args get logged (grep for %+v.*options and Args in internal/service and internal/pipeline). Add a test asserting the log output never contains a real key value.
- Confirmed live 2026-08-07: the batch run's startup line logged both `ACADEMIC_CLOUD_API_KEY` and `GEMINI_API_KEY` in plaintext, and the `/reconfigure` that set up the run repeated it — see `runs/service-20260807-132133.log`, which is now an artifact of the evaluation and carries both keys.
- Architecture impact: Local
- Estimated effort: S
- Priority: P2

---

## G. Efficiency & token economy

### [x] [G1] Build once, run the binary per test — stop recompiling in `go run .`
- Category: Efficiency
- Affected component(s): `internal/builder/validator.go` (`doTest`)
- Problem / current state: `goBuilder` already produces `fn` via `go build -o fn .`, but `doTest` invokes `go run .` for every test file — a full compile per test case, multiplied by every validation retry.
- Proposed change: Execute `./fn` in `doTest`, falling back to `go run .` only if the binary is missing.
- Why: Cuts test-stage latency by compile cost × test count without changing semantics, shortening every repair iteration.
- Architecture impact: Local | Effort: S | Priority: P1
- Status: **Implemented 2026-07-04.** `doTest` executes `./fn` when the build produced it, falling back to `go run .` otherwise. Side benefit for [F1]: a timeout then kills the translated program directly (no `go run` parent/child pipe indirection, no orphan). The timeout test pre-builds `fn` to exercise this path; the mismatch/crash tests cover the fallback.

### [ ] [G2] Right-size the repair/align prompt payloads
- Category: Efficiency
- Affected component(s): `2-stage-repair.md`, `internal/translator/translator.go`
- Problem / current state: Every fixer attempt resends the complete Python original + complete Go code + full raw build log; in observed 6-attempt failure runs that is ~6× the base source tokens on identical context. `{{ .intent }}` is available (when `summary` ran) but unused by repair.
- Proposed change: Replace full `{{ .original }}` in the repair prompt with `{{ .intent }}` when present (keep the original in align, whose job is semantic comparison); combine with [D3]/[E4] error truncation; make inclusion configurable via `task_args`.
- Why: Compile-error repair is a local-syntax task that rarely needs the source-language text; shrinking irrelevant context cuts cost-per-attempt and reduces distraction for small models.
- Architecture impact: Local | Effort: S | Priority: P2

### [ ] [G3] Make the cleaner stage skippable and measure its contribution
- Category: Efficiency
- Affected component(s): `internal/pipeline/default.yaml`, `internal/translator/prompts.go`
- Problem / current state: The document stage is a full-source LLM call whose output inflates `{{ .code }}` for every later stage and retry; its benefit to translation accuracy has never been measured (no per-stage metrics — [B5]).
- Proposed change: Add a documented "lean" pipeline config without `cleaner` (or with `summary` only, per [D5]) and A/B the f1–f14 set against the default chain once [B5] instrumentation exists.
- Why: If documentation doesn't lift success rate, removing it is a pure ~30–50% token saving per run; either outcome improves tokens-per-successful-translation.
- Architecture impact: None (config + experiment) | Effort: S–M | Priority: P2
- Note: the measurement is now a subtraction over `per_task` ([B5]) — the `root`/cleaner task's token column *is* its cost, and [H4]'s script converts it to energy directly. Worth folding into the energy study's per-stage breakdown rather than running as a separate experiment.

### [ ] [G4] Reuse the Go module cache across builds and jobs
- Category: Efficiency
- Affected component(s): `docker-compose.yml`/Dockerfile
- Problem / current state: Every `go mod tidy` in a fresh container re-downloads `aws-lambda-go` and friends; the module cache lives in the container layer and is lost on rebuild. Network flakiness during download masquerades as a build failure the fixer can't fix.
- Proposed change: Mount a named volume at `GOMODCACHE` in the compose file; optionally pre-warm the cache with `aws-lambda-go` in the image.
- Why: Removes both latency and a spurious network-dependent failure mode from every build/retry cycle; based on the Go modules cache design (go.dev/ref/mod#module-cache).
- Architecture impact: Local (infra config) | Effort: S | Priority: P2

### [ ] [G5] Experiment: continued LLM conversation across pipeline stages (translate → fix → align)
- Category: Efficiency / Small-Model Robustness (evaluation experiment — do not commit as default behavior before measuring)
- Affected component(s): `internal/llmconnector` (per-conversion message-history cache on the `Client`), `internal/translator` (opt-in via task param)
- Problem / idea (maintainer, 2026-07-04): today every stage and every retry sends a fresh single-turn prompt, resending the full code context each time. Chat APIs support multi-turn conversations by resending prior prompts/responses; keeping translate → fix → align (and their retries) in *one* continued conversation could improve repair quality — the model sees its own previous attempt plus the resulting error instead of a reconstructed snapshot — at the cost of a context that grows with every turn.
- Proposed change: Prototype an **opt-in** conversation mode (e.g. `task_args.conversation: true`): the connector caches the message history keyed by conversion-request id and appends new turns instead of replacing them; cap the number of retained turns so retries can't blow the context window. Then evaluate against `default.json` on f1–f14 for the two target metrics: translation success rate and tokens-per-successful-translation. [B5] (per-stage token/attempt metrics) is a hard prerequisite for the measurement; ChatAI's token accounting is confirmed reliable (see [B5] notes), so cost tracking is feasible.
- Why: Visible-history self-repair is the setting of Chen et al.'s self-debugging (arXiv:2304.05128) and Reflexion (Shinn et al., arXiv:2303.11366) — but whether it beats fresh-context repair *for a ~30B model under this token budget* is an open empirical question, since history growth directly fights the tokens-per-success metric; hence an experiment, not a committed change.
- Architecture impact: Local (state on the LLM client, keyed per request; the `Client` interface and four-stage pipeline design stay unchanged)
- Estimated effort: M
- Priority: P2 (blocked on [B5])
- Note: the token-economy half of this experiment is measurable once [H2] persists run metrics — compare tokens-per-*successful*-translation between the conversation mode and `default.json`, not raw token totals.

---

## H. Evaluation (energy study)

> Derived 2026-07-05 from [evaluation/EVALUATION.md](evaluation/EVALUATION.md), after checking that
> document's assumptions against the implementation. Three of its original
> instrumentation TODOs were already satisfied by [B5] and are closed there;
> the proposed per-call `CallRecord` struct was dropped as unnecessary (the
> energy model is linear in token counts, so the existing per-stage
> aggregates give an identical pipeline total and per-stage breakdown). The
> items below are the gaps that remain. Analysis and thesis-writing tasks
> stay in EVALUATION.md — only code-side work is tracked here.
>
> **Self-contained run-log records** (maintainer note, 2026-07-05): the `meta.json` information should
> already sit on the metrics at upload time, so the final JSONL run log carries everything needed
> for analysis without joining against the artifacts. **This is already how [H1] works** —
> `pipeline.MakeConversionRequest` resolves `FunctionID` and attaches the parsed `Meta` (including
> the verbatim `Raw` bytes) to `Metrics` the moment the upload is read, and [H2]'s job record embeds
> that whole `Metrics` object. A run-log line therefore already contains function id, bucket, cc,
> lloc, aws, imports, description and provenance alongside the per-stage tokens; `runlog_test.go`
> asserts the grouping metadata survives into the record. Two caveats worth remembering when
> analysing: (a) `Metrics.Meta` is nil for uploads without a `meta.json` (only benchmark mode
> requires one — [C6]), and (b) ~~**only completed jobs are persisted**, so functions that never
> built successfully are absent from the run log entirely and their pass-rate denominator has to
> come from `/metrics` or the logs. If that denominator matters for the thesis numbers, revisit the
> completed-only rule in [H2] before the batch run rather than after.~~ **Settled 2026-08-07:** the
> denominator did matter — the first batch put 68% of its energy in jobs the archive excluded — so
> [H2] now persists every finished job tagged `completed`, and `cmd/energy` filters instead. Note
> the one archive predating this (`run-20260807-132133.jsonl`) still holds completed jobs only.
>
> **GWDG infrastructure reply, 2026-08-22** — integrated in [H9]. Three constants are now
> provider-stated (2× H200 rather than 4× H100 PCIe, FP8 rather than assumed BF16,
> carbon-neutral operation) and the central energy estimate fell by roughly a factor of two
> as a result. Two questions were **not** answered (node power, current PUE) and one was
> **declined** — throughput and concurrency, which GWDG measures but may not release. That
> refusal is the load-bearing one: `B` does not become knowable by asking again, so the
> sensitivity sweep over it is the reported result rather than a placeholder. Anything below
> that speaks of "pending GWDG values" is superseded.
>
> **Dataset** ([evaluation/EVALUATION_DATASET.md](evaluation/EVALUATION_DATASET.md), from the
> `ise-dataset-pipeline` repo): `evaluation_set` = 95 functions / 392 tests, expectations
> *recorded from the real Python function* and validated over 10 deterministic runs;
> `function_set` = the legacy 14 paper functions / 41 tests, expectations never executed
> (report the two separately). Artifacts are flat ZIPs — `main.py`, `meta.json`, `test/*.json` —
> and the test schema is our own `internal/fixture` shape, so no adapter is needed and the
> external `provenance` block is already ignored as an unknown field. Two consequences beyond
> [H1]/[H2]: **40 of the 95 functions provision AWS resources via `setup`**, which makes
> [C10]'s "block when Floci is required but disabled" the difference between a real result
> and 40 infrastructure failures that look like translation defects; and **no test expects an
> error**, so any unhandled error is unambiguously a failure.

### [x] [H1] Ingest `meta.json` and record function identity + grouping metadata on every job
- Category: Evaluation
- Affected component(s): `internal/inputhandler/reader.go` (currently ignores `meta.json`), `internal/service/service.go` (`uploadHandler` validates `fileHeader.Filename` and then discards it), `internal/pipeline/runner.go` (`MakeConversionRequest`), `internal/domain/types.go`
- Problem / current state: `GET /metrics` returns `{job-uuid: Metrics}` and nothing anywhere links a job to *which* function it translated. Both identity signals are currently thrown away: the uploaded filename is dropped after the `.zip` suffix check, and the dataset's `meta.json` matches none of `ReadFromReader`'s three branches (`.py`/`.go` → root, `test/` → fixtures, `.env` → env), so it is silently ignored. A batch over the 95-function `evaluation_set` therefore produces 95 anonymous metric blocks.
- Proposed change: parse `meta.json` from the archive root into a typed struct on `DeploymentPackage`/`ConversionRequest`, and carry it into the persisted metrics record ([H2]). Capture (a) the **function id** — from `meta.json` if it names one, else the ZIP filename stem (`f42`), else the job UUID; the stem is sufficient and is what the dataset uses — and (b) the **grouping fields** `bucket`, `cc`, `lloc`, `aws`, `type`, `imports`, `description` plus the provenance block. Keep the raw `meta.json` bytes verbatim alongside the parsed fields so a later dataset-schema addition needs no re-plumbing. Note the maintainer's plan to backfill `meta.json` into `function_set` too, so both sets take the same path. Beware `meta.json`'s `type` field over-reports network usage (it counts `urllib.parse`/`http.HTTPStatus`); prefer the `aws` flag when grouping.
- **`meta.json` is required for benchmark runs** (maintainer decision, 2026-07-05): an artifact without one produces an unattributable result, which is worth nothing after hours of LLM spend — so it must be rejected at upload rather than discovered in the analysis. Gate it behind a strict/benchmark switch (an env-configured flag in the `FLOCI_ENABLED`/`LLM_CALL_INTERVAL` idiom, e.g. `REQUIRE_META=true`, resolved in `defaults.go`) rather than making it unconditional: the existing `examples/input/*.zip`, the README's curl example, and the service/`cmd` upload tests all ship without a `meta.json`, and ad-hoc dev uploads must keep working. Enabled for the benchmark run, every artifact carries one by construction. The rejection itself belongs in [C6]'s validation gate; parsing and plumbing belong here.
- Why this improves the evaluation: `N* = E_translation / (E_py − E_go)` is defined *per function* and the headline result is its distribution across the set, so identity is load-bearing; and the dataset's intended reporting axes — pass rate per complexity bucket A/B/C/D+, and AWS vs. non-AWS (EVALUATION_DATASET.md §8–§9) — are computable only if `bucket`/`cc`/`aws` travel with the metrics. None of it is reconstructable after the fact once the server has moved on. Based on reasoning; no external source needed.
- Architecture impact: Local | Effort: S–M | Priority: **P0** (blocks the primary result and every grouped result)
- Status: **Implemented 2026-07-05.** `domain.FunctionMeta` + `ParseFunctionMeta` (new `internal/domain/meta.go`) model the documented fields and keep the verbatim bytes in `Raw`; `inputhandler.ReadFromReader` parses an archive-root `meta.json` (checked *after* the `test/` branch, so `test/meta.json` stays a fixture) and errors on a present-but-corrupt one. `domain.ResolveFunctionID` picks identity in the agreed order — `meta.Name`/`meta.ID` → artifact filename stem → `job-<uuid8>` — and `pipeline.MakeConversionRequest(pkg, sourceName)` (signature extended; both call sites updated) records `FunctionID` and `Meta` on `Metrics`, so `/metrics` and the run log are both attributable. Robustness rule worth keeping: invalid JSON fails the upload, but a field whose *type* we guessed wrong does not — typed fields degrade and `Raw` still carries everything, since the dataset owns that schema. Tests: `internal/domain/meta_test.go`, four new cases in `internal/inputhandler/reader_test.go`.

### [x] [H1a] Emit a per-function result summary alongside the energy metrics
- Category: Evaluation
- Affected component(s): the [H2] run log; `internal/domain` (`Metrics.TestCases`, `BuildError`, `TestError`, `Issues` already exist)
- Problem / current state: the dataset's reading guide (EVALUATION_DATASET.md §5) distinguishes outcomes that the current metrics blur together: a build/packaging failure ("report separately"), an output mismatch, an unhandled error, and a failed side-effect assertion are all just counters plus free-text `Issues` today. `TestCases map[string]bool` gives per-test pass/fail but not *why*, even though the [C1] failure evidence (`domain.TestFailure.Kind`: mismatch / execution error / timeout / invalid fixture) already classifies it.
- Proposed change: persist the per-test outcome *with its failure kind* in the run log, and mark the 27 `shape`-mode tests (14 functions) distinctly so value-level equivalence claims can exclude them as the dataset advises. No new measurement — just don't discard the classification that [C1] already produces.
- Why this improves the evaluation: it separates "translation defect" from "infrastructure/packaging failure", which the dataset explicitly asks to report apart; without it a Floci outage and a genuine semantic divergence are indistinguishable in the results table. Based on reasoning; no external source needed.
- Architecture impact: Local | Effort: S | Priority: P1
- Status: **Implemented 2026-07-05.** `domain.TestOutcome` (name, passed, kind, output mode, route, truncated detail) is appended per case via `Metrics.RecordTestOutcome`, which also keeps the legacy `TestCases map[string]bool` in sync so existing `/metrics` consumers and the archived `examples/metrics/*.json` shape keep working. Two failure kinds were added for the distinctions the dataset's reading guide draws: `TestFailureSetup` (infrastructure — the case never ran) and `TestFailureSideEffect` (the function responded plausibly but did not leave the AWS state the original produced).
  - `goTester` records pass and fail alike, reusing the classification [C1] already produces; `flociTester` previously recorded **nothing at all**, and now classifies per phase — its `runCase` returns the failure kind alongside the error (setup / execution error / output mismatch / side-effect mismatch).
  - Each outcome carries `Route` (`goTester` vs `flociTester`), which matters now that [C10] routes per job, and `OutputMode` normalized via the new `fixture.TestCase.OutputModeName()` — so the 27 `shape`-mode tests can be excluded when claiming value-level equivalence, as the dataset advises. The `outputMode` vocabulary also got named constants (`OutputModeTolerant`/`Strict`/`Shape`) instead of literals scattered across three call sites, since those strings are a contract with the dataset pipeline.
  - Everything flows into [H2]'s run log unchanged, because the job record embeds `Metrics`; `runlog_test.go` now asserts the outcomes, their classification and the per-stage model all survive into the archived line.
- ~~Not addressed (needs a decision, see the Section H preamble): a function that never builds is not `Completed`, so it is absent from the run log entirely — the "whole function fails to build → report separately" row of the dataset guide has to be sourced from `/metrics` or the logs unless the completed-only rule changes.~~ **Resolved 2026-08-07:** the completed-only rule was dropped ([H2]); failed jobs are archived with `completed: false` and their per-test outcomes, so the dataset guide's "report separately" row now comes straight out of the run log.

### [x] [H2] Persist run metrics to disk as jobs complete (never lose a batch to an error)
- Category: Evaluation
- Affected component(s): `internal/service/service.go` (in-memory `results`/`metrics` maps; `reconfigure` wipes both by design), `scripts/store-metrics.sh`
- Problem / current state: metrics exist **only in memory**, and there are at least four ways to lose them — a process crash or panic, a restart, a `/reconfigure` (which clears both maps deliberately), and simply never getting around to running `store-metrics.sh`, which is a manual `curl` of the whole map *after the fact*. A 95-function batch is hours of LLM time and real energy spend; losing it to any of these means paying for it twice. There is also no record of *which* configuration produced a given dump, so two runs cannot be told apart afterwards.
- Proposed change (maintainer request, 2026-07-05 — **metrics must be durable against any error**): append one JSON object per finished job to a run log (e.g. `runs/<run-id>.jsonl`) at the moment the worker finishes that job, containing job id, function id and dataset metadata ([H1]), completion status, per-test outcomes ([H1a]) and the full `Metrics` including `per_task`. Write it *append-only and immediately* — a job's record must survive whatever happens to the next job. Add a run header (or sibling file) capturing the active pipeline config, LLM client, model, `LLM_CALL_INTERVAL` and the git commit. Keep `/metrics` unchanged for live inspection, and keep writing the in-memory map so nothing downstream breaks. Worth considering: flush partial metrics for a job that dies mid-pipeline, since a crashed job's tokens were still spent.
- Why this improves the evaluation: replaces EVALUATION.md's JSONL/`CallRecord` item with the minimum that actually makes the artifact durable and self-describing — a thesis artifact must stay interpretable without the server that produced it, and an energy measurement that can be erased by a stray `/reconfigure` is not a measurement. Based on reasoning; no external source needed.
- Architecture impact: Local | Effort: M | Priority: **P0** (every un-persisted batch is a re-run at full energy cost)
- Status: **Implemented 2026-07-05.** `internal/service/runlog.go` appends one JSON object per finished job to `runs/run-<ts>.jsonl` (`RUN_LOG_DIR`; `off`/empty disables), opening/writing/closing per record so a job's line survives whatever happens to the next one. Records carry job id, function id, LLM client and the full `Metrics` including `per_task` and the dataset `meta`; the file is created lazily and opens with a `run_start` header, and `/reconfigure` appends a boundary marker so records from different configurations are distinguishable. **Only completed jobs are persisted** (maintainer decision) — the guard lives in `recordJob` itself, not only at the call site, so no future caller can bypass it; failures stay visible via `/metrics` and the logs. Write failures are logged, never fatal: losing the archive is bad, failing conversions because a disk filled would be worse. `runs/` is gitignored. Tests: `internal/service/runlog_test.go` (completed vs. incomplete, single header, append across jobs, reconfigure boundary, disabled mode); race-clean.
- Deferred: the run header does not yet capture the pipeline config/git commit ([H3] adds the model per stage, which is the more load-bearing half); partial metrics for a job that dies mid-pipeline are not flushed, which follows from the completed-only rule.
- **Completed-only rule revisited and dropped, 2026-08-07** (the open question the section H preamble asked to settle "before the batch run rather than after" — the batch settled it). `recordJob` now archives **every** finished job, tagged `"completed": true|false`, and `cmd/energy` does the filtering instead.
  - Why the original reasoning did not survive contact with a real batch: it is right that a failed job has no translation to evaluate and must not sit in a per-function energy or pass-rate table, but that argues for *labelling*, not discarding. In `run-20260807-132133` the six failed jobs held **86.9 kJ of the run's 127.3 kJ — 68% of the inference energy** — and none of it was archived, because a job that fails is usually one that exhausted its repair budget first. An `E_translation` that silently omits two thirds of its own spend is not a measurement, and `N*` inherits the error directly.
  - The stated fallback also did not hold: failures were said to stay "visible via `/metrics`", but that map is in-memory and wiped by `/reconfigure` — the exact fragility this item exists to remove. The only data with no durable home was the data the archive excluded. Discarding is irreversible; labelling is not.
  - Reader contract: an **absent** `completed` field means `true`. Pre-change logs contain completed jobs only, so decoding the missing field into Go's zero value would reclassify every historical translation as a failure and empty every table. `JobRecord.IsCompleted` (`cmd/energy/runlog.go`) encodes this, and the field is a `*bool` on both sides so `false` is written explicitly while `run_start`/`reconfigure` records omit it entirely.
  - `cmd/energy` keeps the original intent as its **default**: `Build` splits the records, every existing table (totals, mean/median, per-stage, per-bucket, AWS, break-even, the `-sweep` denominator) describes completed translations only, and a new "Failed attempts" section reports the count, the function ids, the tokens, the energy, its share of the run's total spend, and **the cost per successful translation with failures amortized in** — the figure the thesis actually needs and could not previously be computed. `-json` carries the costed failures too, so nothing recorded is invisible. A run with no failures prints "none - every recorded job produced a translation".
  - Verified: the pre-flag archive `runs/run-20260807-132133.jsonl` reports exactly the same numbers as before (8 translations, 40.4 kJ); a fresh mixed run (`runs/run-20260808-102226.jsonl`, pf1 + pf14) archives `completed=True`/`completed=False` and reports 3.1 kJ for the one translation against 26.5 kJ wasted — 89.6% of that run's spend — and 29.6 kJ per success. Tests: `TestRunLogRecordsIncompleteJobsAsFailed`, `TestRunLogMarksCompletedJobs`, `TestRunLogCompletedFlagOmittedOnNonJobRecords` (`internal/service/runlog_test.go`); `TestJobRecordTreatsAbsentCompletedAsSuccess`, `TestBuildSeparatesFailedAttempts`, `TestBuildWithoutFailuresReportsNone`, `TestBuildAllFailedStillAccountsEnergy` (`cmd/energy/energy_test.go`). The replaced `TestRunLogSkipsIncompleteJobs` asserted the old contract and is gone.
  - Still deferred: partial metrics for a job that dies *mid-pipeline* (a panic or a hard cancel before the worker records anything) are still not flushed — that is a different gap from the completed-only rule and is unchanged by this.

### [x] [H3] Record the model per stage for per-model energy coefficients
- Category: Evaluation
- Affected component(s): `internal/domain/types.go` (`TaskMetrics`), `internal/translator/translator.go` (already resolves the model name for the chatlog label), `internal/llmconnector`
- Problem / current state: `TaskMetrics` records tokens but not which model produced them. Every stage may override `model_name` via `task_args`, and `e_in`/`e_out` are derived from a specific model's parameter count and weight bytes — so a mixed-model run cannot be costed, and a run-level average would be quietly wrong.
- Proposed change: add the resolved model name to `TaskMetrics` (populated in `RecordLLMCall`, where the translator already has the value); make sure Gemini's `GEMINI_MODEL` key resolves too, given the existing `model_name`/`GEMINI_MODEL` inconsistency.
- Why this improves the evaluation: lets the energy script pick the right coefficients per stage instead of assuming a single model, and documents post hoc which model produced which result. Based on the coefficient derivation in EVALUATION.md §3 (both coefficients are functions of `n_params` and weight bytes); no external source needed.
- Architecture impact: Local | Effort: S | Priority: P1
- Status: **Implemented 2026-07-05.** The model is reported by the **connector**, not inferred from task params: each `InvokeLLM` sets `Model` on the `domain.Metrics` it returns (before any error path, so a truncated response — which did consume tokens — is still costable), and `RecordLLMCall` carries it into `TaskMetrics.Model`. This is what makes a Gemini stage attributable at all: Gemini resolves `GEMINI_MODEL`, so the translator's `taskParams["model_name"]` never names it. Bonus fix from the same change: Gemini chatlogs were being written as `..._unknown-model_...` and now carry the real model. `AddMetric` deliberately does **not** aggregate `Model` — the request-level metrics span every stage, so a single name there would be misleading. A stage that somehow saw two models joins them with `,` rather than dropping one, since its tokens then can't be costed with one coefficient pair. Tests: `internal/domain/metrics_test.go`, `TestConnectorsReportModel` in `internal/llmconnector/connector_local_test.go`, and end-to-end attribution through a stub client in `internal/translator/attribution_test.go`.
- Deliberately **not** done: unifying Gemini onto `model_name`. `default.yaml` and `default.json` set `options.model_name` pipeline-wide for Ollama/ChatAI, so making Gemini honour that key would silently send it `qwen2.5-coder:3b` the moment someone switched `LLMClient` to `gemini` while keeping the same options block. `GEMINI_MODEL` stays Gemini's override key; a regression test pins this.

### [x] [H4] Energy-model script over the run logs
- Category: Evaluation
- Affected component(s): new tooling under `evaluation/` (never in the service path); consumes [H2]'s run logs
- Problem / current state: EVALUATION.md §3–§4 define the formulas and constants, but nothing computes them; the numbers would otherwise be assembled by hand per run.
- Proposed change: a standalone script that reads the run logs and emits energy per run, per stage and per function, plus `N*` once runtime data ([H6]) exists — with every constant from §4 in **one config file**, so both the pending GWDG reply and the §8 sensitivity sweep (`B` ∈ [8,128], BF16→FP8, MFU, PUE) are config edits rather than code edits.
- Why this improves the evaluation: the model is pure post-processing over recorded token counts, so keeping it outside the pipeline prevents experimental assumptions from leaking into production code and lets the sensitivity table be regenerated on demand. Based on reasoning; no external source needed.
- Architecture impact: None (separate tool) | Effort: M | Priority: P1 (after [H1]/[H2])
- Status: **Implemented 2026-07-05** as `cmd/energy` (Go, so it reuses `domain.Metrics` for decoding; nothing in it runs during a conversion). Usage:
  ```sh
  go run ./cmd/energy runs/run-*.jsonl              # report
  go run ./cmd/energy -sweep runs/run-*.jsonl       # + section 8 sensitivity table
  go run ./cmd/energy -json runs/run-*.jsonl        # machine-readable, for plotting
  go run ./cmd/energy -runtime evaluation/runtime.json runs/run-*.jsonl   # + N*
  ```
  - **Constants live only in `evaluation/energy.config.json`** — there is deliberately no compiled-in fallback, so the thesis constants table has one source of truth and the GWDG reply is a config edit. The config is validated on load, and a missing `models._default` entry is a hard error because it is what stops a run using an unlisted model from being costed as *zero*.
  - **Verified against the document, not just itself**: the tests assert the derived coefficients reproduce EVALUATION.md §3's published figures (e_in ≈ 0.41 J/token, e_out ≈ 2.6 J/token at B=32, prefill ≈ 4,900 tok/s, decode step ≈ 41 ms), the whole e_out-vs-B table, and the §3 worked example (5 calls × 6,000/1,500 tokens ⇒ ≈ 33 kJ ≈ 9.3 Wh). A separate test loads the shipped config so a typo in it fails the build rather than silently changing every number in the thesis.
  - Reports: total/mean/median energy and CO₂e, **per-stage breakdown with the repair share** (repair stages named in the config rather than hardcoded), and the dataset's two reporting axes — per complexity bucket and AWS vs non-AWS — plus passed/failed/shape-only test counts per group from [H1a]'s outcomes.
  - `-sweep` re-costs the *same measured token counts* under varied assumptions (B, BF16→FP8, MFU, PUE), which is the honest form of the §8 table: only the coefficients are assumptions, the tokens are facts.
  - `N*` is computed when `-runtime` supplies measured per-invocation energies ([H6]); functions where Go is not faster are reported as "never pays back" rather than silently dropped, and functions without a measurement are listed as missing. Until [H6] exists the tool says so instead of inventing numbers.
  - Models seen in a run log but absent from the config are costed on the default **and named in the report** as an assumption; stages from pre-[H3] records with no model are surfaced as `(unrecorded)`.
  - A malformed run-log line is a hard error with `file:line`, not a skip: quietly costing fewer translations than actually ran would understate the total.

### [x] [H5] Account for or bound the pipeline's local compute energy
- Category: Evaluation
- Affected component(s): `internal/hostenergy` (new), `internal/service/service.go`, `internal/pipeline/pipeline.go`, `internal/domain/types.go`, `cmd/energy` (`config.go`, `energy.go`, `report.go`), `evaluation/energy.config.json`
- Problem / current state: `E_translation` counted LLM inference only, but each build attempt runs `go mod init`/`go mod tidy` and `go build`, each test round runs one `./fn` process per fixture, and the Floci route additionally starts emulator containers — all multiplied by every repair iteration. The visible symptom: `cmd/energy`'s per-stage table printed **0.0 J** against `goBuilder`, `goTester` and `pyScan`, the three stages that do all the local work.
- **Resolved 2026-09-04 as *measure*, and directly rather than by scaling a representative round.**
  1. `internal/hostenergy` reads the RAPL package counters under `/sys/class/powercap`. The service samples them either side of every job (the authoritative per-job figure, including the gaps between stages) and `executeTask` either side of every task attempt (the breakdown). It is a counter difference — **no assumed wattage enters a measured figure at all**. Wraparound of the fixed-width microjoule register is handled per domain; only top-level packages are summed, since the core/uncore/dram sub-domains are subsets of their package.
  2. `E_translation = E_inference × PUE + E_host`. PUE is deliberately **not** applied to the host term: that machine sits on a desk, not in GWDG's hall.
  3. **Gross and marginal are both reported**, because they differ by ~4× here. Measured over run 20260831-190900, **92% of pipeline wall clock is spent waiting on the remote LLM API** (24,101 s of 26,134 s) with the host near idle, against 2,033 s of actual local compute. The service measures its own idle baseline once at startup — before it takes the first job, since by the time a job exists the machine is no longer idle — and records it on every job, so the analysis can subtract it. Marginal matches the marginal-cost framing the document already adopts for inference; gross is the honest answer to "what did this machine burn while producing that translation".
  4. **Absent stays absent.** An unmetered host (WSL2, macOS, containers) or a run log written before this records no host energy, and `cmd/energy` then prints `host energy NOT COUNTED` rather than a zero — a zero is indistinguishable from "the build stages were free", which is the exact claim this item exists to stop making. `host.fallback_power_watts` re-costs such a log from a stated wattage, tagged `ESTIMATED` everywhere it appears; it defaults to 0. A set that mixes measured and estimated jobs reports `mixed` rather than rounding to either.
- **Arithmetic this quietly broke, and the fix**: `RepairShare` and the per-stage `Share` recovered inference joules by dividing the facility total by PUE. That stops being correct the moment anything else joins that total, so both now divide by an explicit `TotalComputeJoules`. Pinned by `TestSharesAreOfInferenceNotOfTheTotal` with a deliberately oversized host term.
- Scale, re-costing run 20260831-190900 with a nominal 25 W / 11 W idle: mean `E_translation` **11.5 kJ → 16.1 kJ** per completed translation (+40%), of which ~2.6 kJ is marginal. Neither negligible nor dominant — which is why it needed measuring rather than arguing about.
- Tests: `internal/hostenergy/hostenergy_test.go` (multi-domain summing, counter wraparound, unusable readings reported rather than zeroed, clean degradation where no counter exists) and `cmd/energy/hostenergy_test.go` (measured beats fallback, absent stays absent, fallback tagged estimated, shares unaffected by the host term, mixed provenance surfaced).
- Architecture impact: Local | Effort: M | Priority: P1

### [x] [H6] Go vs. Python runtime measurement harness reusing the fixture payloads
- Category: Evaluation
- Affected component(s): new tooling under `evaluation/`; reuses `internal/fixture` payloads and the envelope of `internal/builder/test_handler.txt`
- Problem / current state: EVALUATION.md §6 requires methodologically symmetric measurement of both versions; nothing exists yet. The Go side already has its harness (JSON event on stdin → `handle` → `{"response": …}` on stdout); the Python side has no equivalent, and cold start is not separated from steady state anywhere.
- Proposed change: a Python harness mirroring the Go one exactly, plus a driver that runs both over the *same* fixture payloads on the same machine under `perf stat -e power/energy-pkg/,power/energy-ram/`, measuring cold start (one process per invocation) separately from steady state (N invocations in one process), applying the same PUE to both sides.
- Why this improves the evaluation: reusing the canonical fixtures guarantees both sides see identical inputs through identical envelopes by construction, which is precisely the symmetry the comparison depends on — and it means the harness needs no new test data. Based on reasoning; no external source needed.
- Architecture impact: None (separate tool) | Effort: M | Priority: **P0** (raised 2026-08-24)
- **Rescheduled 2026-08-24 (maintainer decision): implement this before the [I1] `evaluation_set` run.** The measured per-invocation ΔE is what [I9] composes the prediction module's secondary objective from, and running it first means the single pass yields labels, per-function `E_translation` *and* ΔE at once instead of needing a repeat for the missing term.

- **Status: implemented 2026-08-24** as `cmd/runtime` (driver, meters, report) plus `evaluation/harness` (the two harnesses, embedded from one package so they cannot drift apart). Full method write-up is in [EVALUATION.md §6 "Implementation"](evaluation/EVALUATION.md); the points worth having here:
  - **Symmetry is structural.** Both harnesses read the same fixture payloads as JSON Lines, invoke once per line, and emit the same marker+envelope with the same [A18] stdout discipline; both sides run under one meter, on one machine, with the same AWS isolation (`builder.TestExecutionEnv`, so an AWS call cannot resolve differently on the two sides and be recorded as a runtime difference). `harness_test.go` pins the marker across all four files that must agree on it.
  - **Two-point cold/steady split**, `T(1)` and `T(N)` differenced, with no clock inside either harness — an in-harness clock would compare Go's runtime clock against Python's `time` module and bias the very quantity under test, and it puts import-time work on the startup side where Lambda puts it too. Minimum of repetitions, not mean.
  - **N escalates until the signal clears the measured noise.** This was not anticipated in the original item and turned out to matter most: at a fixed N, most functions in this corpus do microseconds of work against a millisecond of startup, the difference is buried in scatter, and the naive answer is a per-invocation cost of **zero** — which would enter `runtime.json` as "free to run" and send `N*` to infinity. The first run of the tool produced exactly that for 5 of 14 functions before the escalation was added. A function that never resolves is reported `UNRESOLVED` and omitted from `runtime.json`, because `cmd/energy` names a missing function but would cost a zero as free.
  - **Three meters, no fabricated joules**: `rapl` (powercap sysfs, no root/perf needed — the primary), `perf` (as §6 specifies), and `time`. The `time` meter reports **no energy at all** unless `-watts` states a package power, and then tags every figure `energy_derived`. **Neither RAPL nor perf works under WSL2**, so measured energy needs a bare-metal Linux host — that run is still outstanding and is a prerequisite for quoting any absolute joule figure or `N*`.
  - **First result (paper set, 14/14 measured, derived energy at 15 W — the timings are measurements, the joules are not): Go speedup median 1.9× steady state vs. 15.0× cold start** (min 3.9×, max 21.0×). The ~8× gap between the two is the finding: §6 predicted cold start would be where Go's advantage is largest, and for short serverless invocations that column is what governs payback. `runtime.json` still carries the steady-state figure — break-even asks about a *deployed* function, which at `N*` invocations is overwhelmingly warm, and charging every invocation a cold start would understate `N*` on both sides and flatter the conclusion. Cold figures are in `-report` for the write-up.
  - Verified end to end against `runs/packages-20260807-132133.zip`: measurement → `runtime.json` → `go run ./cmd/energy -runtime` → break-even N* computed for all 8 completed translations. The paper set's `N*` values are enormous (median ~2×10⁷) because those functions are trivial by design; `evaluation_set` is what to report, and it needs [I1]'s translated packages first.
  - Two bugs the end-to-end run caught and that are now fixed: the correctness gate matched `"error"` anywhere in the output, so a function whose *successful response* described an error (pf14) was wrongly dropped — it now parses the envelope and checks its top-level key; and the sub-resolution zero described above.

- **Measured on real RAPL 2026-08-31 — supersedes the derived figures above.** The bullet above reports the paper set at 15 W *derived* energy under WSL, which this item itself flagged as not quotable. That bare-metal run has now happened (Intel i7-7500U, `intel-rapl:0`/`package-0`, `performance` governor, on AC), and the derived numbers were wrong in both directions:

  | paper set (14/14 measured) | derived @15 W (WSL) | measured (RAPL) |
  |---|---|---|
  | steady state | 1.9x | **1.3x** |
  | cold start | 15.0x | **24.4x** |

  The derived method **overstated** Go's steady-state advantage and **understated** its cold-start advantage. This matters because `runtime.json` deliberately carries the steady-state figure into `N*`, so the derived constants were flattering the payback case: on the measured numbers 6 of 10 completed paper-set translations report **"never pays back" (Go not faster)**, against a median steady-state speedup of 1.3x with some functions at 0.6x. Consistent with this item's own note that the paper set is trivial by design — but the direction is worth stating plainly, because it makes `N*` worse, not better, and the write-up must not quote the 1.9x/15.0x pair.
  - `evaluation_set` measured the same day: 57 of 95 measurable, median **1.7x** steady / **55.4x** cold; `N*` median 7.76e6 over 8 functions, 3 never paying back. Files: `evaluation/runtime.json` (57 entries) and `evaluation/runtime-functionset.json` (14) — kept apart deliberately, since one `-out` overwrites the other.
  - **A hang in the measurement path was found during the `evaluation_set` pass and filed as [H10]** — f52's translated binary ran 25 min at ~45% CPU and stalled the whole pass. Both passes *above* were completed with a manual kill plus an external watchdog, not with tooling that could be trusted to run unattended. **[H10] was implemented 2026-09-02**, so the second-pass measurement below needed neither.

- **Second-pass measurement 2026-09-02 (`runtime-20260831-190900.json`, promoted to `evaluation/runtime.json`) — this is the measurement to quote.** Run against [I1]'s second-pass packages (`packages-20260831-190900.zip`), same host, `performance` governor, on AC, `rapl`/`package-0`. **66 of 95 measured** (against 57 on the first pass), 23 provisioned through fixture setup, 1 killed by the new [H10] timeout and reported `TIMEOUT`.

  | | first pass (11 successes) | **second pass (42 successes)** |
  |---|---|---|
  | measured | 57 | **66** |
  | steady state, median | 1.7x | **2.0x** |
  | cold start, median | 55.4x | **47.2x** |
  | `N*` computed for | 8 | **25** |
  | `N*` median | 7.76e6 | **1.17e7** |
  | never pays back | 3 | **17** |

  - **Every one of the 42 completed translations has a runtime measurement**, so the `N*` figures cover the whole success set rather than a subset of it.
  - **`N*` got *worse* as the pipeline got *better*, and this needs saying explicitly rather than smoothing over.** The second pass completes harder functions - bucket D+ costs **10.03 Wh** per completed translation against **1.17 Wh** for bucket A - while the steady-state speedup only moved 1.7x -> 2.0x. Translation success and translation worthwhileness are not the same property and do not move together.
  - **Payback distribution over the 42 successes**: within 100k invocations 7 (17%), within 1M 11 (26%), within 10M 12 (29%), within 100M 21 (50%); **17 (40%) never pay back at all** because Go is not faster at steady state. Cheapest: f76 (14.9k), f77 (26.9k), f30 (35.0k), f45 (55.3k), f40 (86.3k).
  - **Go's advantage is concentrated where the SDK is**: by AWS usage, median speedup **2.9x (AWS, n=35)** vs **1.0x (non-AWS, n=31)**. By bucket: A 1.7x, B 3.0x, C 1.4x, D+ 2.8x.
  - Cold start fell 55.4x -> 47.2x between passes. Both are measurements of *different translated code*, not a regression: the first pass largely measured translations that failed their tests, so its sample was Go that ran fast while computing the wrong answer. Prefer the second-pass figure and do not present the two as a time series.

### [ ] [H7] Verify token accounting across connector-internal retries
- Category: Evaluation
- Affected component(s): `internal/llmconnector/chatai.go` (the [F2] retry loop assigns `metrics = m` per attempt), `ollama.go`, `gemini.go`
- Problem / current state: transient failures are retried *inside* `InvokeLLM`, and the loop overwrites the metrics of the previous attempt rather than accumulating. In practice a rate-limited or 5xx attempt generates nothing and reports no usage, so nothing is lost today — but the energy figures rest on that assumption and it is untested. (The adjacent case is already correct: a truncated `finish_reason=length` response *does* consume tokens, and `RecordLLMCall` runs before the error check, so it is counted.)
- Proposed change: add a fake-backend test asserting that a retried-then-successful call reports only the successful attempt's usage, and that a truncated response's tokens are still counted; switch overwrite → accumulate if any backend is found to report usage on a failed attempt.
- Why this improves the evaluation: a cheap guard on the one place where the energy accounting could silently under-count, in code that already has fake-backend test infrastructure. Based on reasoning; no external source needed.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [H8] `cmd/energy` reports a coefficient assumption for stages that consumed no tokens
- Category: Evaluation
- Affected component(s): `cmd/energy/energy.go` (`ModelAssumed`), `cmd/energy/report.go` (the `assumed` set and the `WARNING:` line)
- Problem / current state: `ModelAssumed` is set whenever a stage's model is missing from `evaluation/energy.config.json`, which includes the non-LLM stages (`builder`, `goTester`, `testRecoveryBuild`) that record no model and no tokens by design. Their zero tokens cost zero under any coefficients, but the report still prints `WARNING: costed with default coefficients (no config entry): (unrecorded)`.
- Evidence (run `run-20260807-132133`): every stage that made LLM calls used `devstral-2-123b-instruct-2512`, which *is* in the config, so the run was fully costed with the intended coefficients — and the report carried the warning anyway, sourced entirely from the three token-free build/test stages.
- Proposed change: set `ModelAssumed` only when the stage actually consumed something (`LLMCalls > 0`, or non-zero tokens). Keep the `(unrecorded)` label for the case it was written for — a stage that did make calls but reported no model, i.e. a pre-[H3] record.
- Why this improves the evaluation: the warning is the tool's one signal that a number in the thesis rests on a substituted coefficient. Firing it on a run where nothing was substituted devalues it, and a reader has no way to tell a genuinely assumed coefficient from this noise.
- Architecture impact: None (separate tool) | Effort: S | Priority: P2
- **Status: implemented 2026-08-31.** `report.go` now flags a stage only when it consumed tokens (`PromptTokens > 0 || EvalTokens > 0`), the token-based variant of the two options above. Keying on `LLMCalls > 0` was tried first and rejected: `TestEvaluateFlagsUnknownModel`'s record has 100 prompt tokens and no call count, and a stage that consumed tokens *was* costed on a substituted coefficient whether or not the call counter survived — tokens are what the joules derive from, so they are the honest trigger. The existing test passes unchanged. Confirmed on the `evaluation_set` run `run-20260830-210122`: the warning is gone and every LLM stage was costed on the configured `devstral-2-123b-instruct-2512` coefficients.

### [x] [H9] Integrate the GWDG infrastructure reply into the energy constants
- Category: Evaluation
- Affected component(s): `evaluation/energy.config.json`, `evaluation/EVALUATION.md` (§2–§4, §7–§8, §10–§11, Open Questions), `cmd/energy/config.go`, `cmd/energy/report.go`, `cmd/energy/main.go`, `cmd/energy/energy_test.go`
- Problem / current state: every coefficient rested on inferred hardware — 4× H100 PCIe from the KISSKI platform page, BF16 from a guess at the model card, a 2 kW node, and a German-grid CO₂ intensity. EVALUATION.md flagged precision and concurrency as the two dominant assumptions and marked the whole constants table provisional pending a support request.
- What the reply said (2026-08-22): **answered** — Devstral 2 123B runs on **2× H200** at **FP8** (FP16 is the house default, Devstral an exception), and GWDG operates **carbon-neutral**; **declined** — aggregate throughput and concurrent-request counts, which they hold but may not release; **not answered** — node power under load and the current PUE.
- Change made: constants file rewritten with per-value `_source` tags separating `gwdg` from `assumed`, plus a `_gwdg_reply_2026_08_22` block recording confirmed/declined/unanswered so the provenance travels with the numbers. `n_gpu` 4→2, peak 756→1979 TFLOP/s (H200 FP8 dense), HBM 2.0→4.8 TB/s, bytes/param 2→1, node power 2000→1700 W (2×700 W GPU + the same 150 W/GPU host share the old figure used). `cmd/energy` gained a market-based CO₂ intensity alongside the location-based one, a hardware provenance line in the report header, and two new sweep axes (`node_power_watts`, `peak_flops_per_gpu`).
- Reading the hardware answer (worth keeping, because the reply looks self-contradictory): the generic "4× H100 PCIe on KISSKI?" question was confirmed, but the precision answer names our model as running on 2× H200. The model-specific statement governs — the platform average is not what we cost. The two also corroborate each other: 123B at FP8 is 123 GB of weights, fitting 2×141 GB with ~159 GB left for KV cache; at BF16 it would be 246 GB, leaving 36 GB and nothing like the advertised 256K context. The stated precision and the stated GPU count only fit together.
- Why the CO₂ handling is two numbers and not one: "CO₂-neutral" describes procurement, not physics — the electricity was still drawn. The report now emits a location-based figure (German grid average) *and* a market-based figure (GWDG's contractual zero), which is the GHG Protocol Scope 2 dual-reporting rule. Quoting only the market figure would present the pipeline as free; quoting only the location figure would misstate what the provider reports. Neither covers embodied manufacturing emissions, which stays a §9 lower-bound caveat.
- Effect on the result: the central estimate dropped ~2.25× — the `run-20260807-132133` archive goes from 40.4 kJ to 18.0 kJ over its 8 translations, and §3's worked example from 33 kJ (9.3 Wh) to 15.5 kJ (4.3 Wh), with the range over `B` narrowing from 4–20 Wh to 3–10 Wh. `N*` scales linearly with this, so every break-even figure roughly halves. Because it moves in the direction that makes the thesis conclusion *easier*, the pessimistic end of the sweep is what the write-up should quote.
- Verification: `TestSupersededCoefficientsStillDerive` pins the pre-reply figures (e_in 0.41, e_out 2.6, prefill 4900 tok/s, decode step 41 ms) against the current formula, so the claim "only the inputs changed, not the method" is testable rather than asserted. `TestShippedConfigIsValid` now also fails if the config silently reverts to BF16, loses either new sweep axis, or drops the market intensity. Two new tests cover the dual-CO₂ reporting and the case where no market intensity is configured (no fabricated zero).
- Architecture impact: None (constants + separate tool) | Effort: M | Priority: **P0** (every energy figure in the thesis is linear in these)
- Follow-up left open: node power and current PUE are still unanswered and worth one short follow-up mail — neither is likely sensitive, and node power is now the second-largest uncertainty. Concurrency is **not** worth pursuing: the refusal was explicit.

### [x] [H10] `cmd/runtime` has no timeout on the measured invocation; one hanging translation stalls the whole pass
- Category: Fault Tolerance / Evaluation
- Affected component(s): `cmd/runtime` (the measurement exec path), alongside the existing timeouts in `cmd/runtime/provision.go`
- Problem / current state: the only timeouts in the tool guard the Floci provisioner — a 10 s endpoint probe and a 60 s `prepare` per function. The measured invocation itself is unbounded, so a translated binary that does not terminate blocks the pass forever. It cannot even be distinguished from slow-but-progressing work, because N escalation ([H6]) legitimately makes some runs long.
- Evidence (2026-08-31, `evaluation_set` pass over `packages-20260830-230152.zip`): f52's `fn` ran **25 minutes at ~45% CPU** with the log frozen at f51, having consumed its payloads and then not exited. The pass had to be unstuck by killing the child by hand; it then recorded `f52 SKIP go: 200-invocation run: signal: terminated` and continued normally through the remaining ~40 functions. Left alone overnight it would have produced a partial `runtime.json` and no summary — and this pass is the input to every `N*` in the thesis.
- Why this is not covered by [F1]: that item put per-test and per-build-command timeouts into `internal/builder`, on the *conversion* path. `cmd/runtime` is separate tooling with its own exec calls and inherited none of them. A non-terminating translation is not a rare pathology to design around either — it is an ordinary LLM translation defect, and the corpus contains at least one.
- Proposed change: run both the Python and the Go measured invocations under `exec.CommandContext` with a per-run deadline, scaled with N so escalation cannot trip it (e.g. a base budget plus a per-invocation allowance, or a multiple of the observed `T(1)`). On expiry, kill the process **group** — a bare `Cmd.Process.Kill` leaves grandchildren — and record the function as skipped with a distinct reason (`TIMEOUT`, not the generic "not runnable"), so a hang is visible in the report rather than silently absent. Apply the same deadline to both sides so it cannot bias the comparison by cutting one side off sooner.
- Why this improves the evaluation: the measurement pass is long, unattended and run rarely on a machine with real RAPL counters; a single hang currently costs a whole run. A distinct `TIMEOUT` reason also carries real information — a translation that compiles, deploys and then hangs is a different failure class from one that does not build, and [I1]'s labels should be able to tell them apart.
- Architecture impact: None (separate tool) | Effort: S | Priority: P1 (before the next full measurement pass)
- **Status: implemented 2026-09-02** in `cmd/runtime/timeout.go` (+ `timeout_unix.go` / `timeout_other.go`), wired through `runOnce`/`bestOf` in `measure.go` and into `perf.go`. It was filed after f52 stalled the first pass for 25 minutes; before it was implemented **f51 stalled the second pass and cost the maintainer a run**, which is what finally forced it.
  - **Budget scales with N** as the item required: `90s + 50ms x invocations`, capped at 15 min. [H6]'s escalation legitimately makes late rounds far longer than early ones, so a fixed deadline would be either uselessly loose at N=1 or would cut off honest work at N=100000.
  - **Identical budget for both language sides.** It is a pure function of N, tested as such: a timeout that cut one side off sooner than the other would bias the very comparison the tool exists to measure.
  - **Kills the process group, not the process.** A translated function that shells out would otherwise leave a grandchild holding the stdout/stderr pipes and `Wait` would block on them even after the direct child died. `timeout_unix.go` sets `Setpgid` and signals `-pgid`; the non-unix fallback kills the single process, which is adequate because measurement needs RAPL or perf and both are Linux.
  - **Covers every meter, not just the primary one.** `perfMeter` builds its own wrapped `perf stat` command rather than running the one it is handed, so the budget had to be threaded to it explicitly (`SetBudget`); leaving it uncovered would have been a known hole in a rarely exercised path.
  - **`TIMEOUT` is a distinct skip reason** (`skipReason` in `main.go`), not folded into the generic "not runnable" - a translation that builds, starts and then hangs is a different failure class from one that never compiled, and [I1]'s labels should be able to tell them apart.
  - **Five tests**, including one that spawns an actual grandchild (`sh -c "sleep 300 & wait"`) - the case the group kill exists for; it returns in 0.30 s instead of hanging. Also covers budget monotonicity/cap, an ordinary non-zero exit not being mislabelled as a timeout, and the `TIMEOUT` labelling itself.
  - **Effect on the 2026-09-02 pass**: 95 attempted, 66 measured, **1 killed and reported `TIMEOUT`**, no manual intervention and no watchdog. Notably f51 - the function that hung the aborted run - measured cleanly this time (1.6x), so the hang is sampling-dependent, which is exactly why a timeout is the right mechanism rather than a blocklist.

---

## I. Prediction & candidate selection (new — 2026-08-24)

> Opened 2026-08-24; **maintainer decisions folded in the same day** (see "Decisions" at the end
> of the section — label, single run, extractor, method set and [H6] ordering are all settled).
> **Nothing in this section exists in code yet** — there is no predictor, no feature extractor,
> no training data, and no ML dependency anywhere in the repo (`go.mod` is clean, `internal/`
> has no `predictor` package, `scripts/` has no Python).
>
> **The decision problem.** Given an uploaded artifact *before any LLM call is made*, emit a
> decision in {translate, skip} plus a score. Primary criterion: will the translation succeed?
> Secondary: is the translation worth its energy? The two are different questions and are
> deliberately handled differently — see [I9]: only the first is learned, the second is
> *composed* from the first plus [H6]'s runtime measurements and `cmd/energy`.
>
> **The constraint that shapes every choice here.** The predictor's own energy must be
> negligible against what it saves. From the pilot run, one wasted translation costs ~4–10 kJ;
> a decision-tree or logistic-regression inference over ~25 numeric features costs on the order
> of microjoules. That is a ~10⁹ margin, which means *any* classical model is affordable and the
> real cost driver is **feature extraction**, not inference. So the rule is: features must be
> deterministic, single-pass, and LLM-free. One LLM call for feature extraction would cost more
> than everything else in this section combined ([I8] makes that quantitative rather than
> asserted).
>
> **Execution order** (changed by the 2026-08-24 decisions — the extractor and [H6] now come
> *before* the labelling run, so the run produces everything at once):
>
> ```
> [C8]/[I3] accurate Python scanner  ─┐
> [H6] Go-vs-Python runtime harness  ─┼─► [I1] one full evaluation_set run ─► [I2] signal check
>                                     │        (labels + per-function E_translation + ΔE)
> [I11] leakage audit ───────────────┘                    │
>                                                          ▼
>                              [I4] dataset table ─► [I5] baselines ─► [I6] LR + RF ─► [I7] evaluation
>                                                                              │
>                                                          [I8] predictor energy ─┴─► [I9] worthwhileness
>                                                                                        │
>                                                                                    [I10] integration
> ```

### [x] [I1] Produce the labelled corpus: one full `evaluation_set` pass with per-function outcomes
- Category: Prediction (data)
- Affected component(s): `evaluation/evaluation_set` (95 artifacts), `internal/service/runlog.go` (already records everything needed), `runs/`, `cmd/energy`
- Problem / current state: supervised prediction needs (features → outcome) pairs and **none exist**. `run-20260807-132133` covers 14 functions on the paper set, predates the [A18]/[A19]/[H2] fixes, and its own Open-Question entry says "re-run the set before citing anything from it". The `evaluation_set` — the only corpus large enough to train on at all — has no recorded run.
- Proposed change: run the full 95-function set through `default.json` on the canonical model, with the Floci route enabled (40 of 95 functions need `setup` provisioning per EVALUATION_DATASET.md §6.4, and they will otherwise fail for infrastructure reasons and pollute the labels as if they were translation defects). Archive to `runs/` as usual; [H1]/[H2] already put `meta`, `per_task`, `test_outcomes` and `completed` into every record, so **no new instrumentation is needed** — the run log is already a usable label source.
- **Label — SETTLED 2026-08-24: `all_tests_passed` on the final validation round.** Derived from `Metrics.TestOutcomes`, which post-[A19] describes the last round only. Rationale: it is the property the thesis actually claims, whereas `completed` conflates "every test passed" with "the pipeline gave up gracefully".
  - Also record `pass_fraction` and `completed` as **secondary columns** in [I4]'s table. They cost nothing to carry, they let a regression variant be tried later without a re-run, and `completed` is the natural label for a "will this even build?" ablation.
  - **Shape-mode handling:** 27 tests across 14 named functions (`f1 f6 f22 f34 f37 f43 f48 f49 f54 f63 f67 f80 f86 f91`) compare types only and cannot catch a value regression (EVALUATION_DATASET.md §6.2). They still count toward `all_tests_passed`, but [I4] carries a `shape_only_fraction` column per function so [I7] can report the comparison with those 14 excluded as a robustness check. Do not silently drop them — a positive resting entirely on shape-mode evidence is a weak positive, and the write-up should be able to say how many there are.
  - **`f88/t1` has no `expectedOutput`** (§6.9) and only asserts the function does not error. Treat it as a pass condition as-is; note it where the label is defined.
- **Repetitions — SETTLED 2026-08-24: one pass for now.** The consequence has to be stated rather than glossed: LLM translation is stochastic, so a single pass gives labels whose own reproducibility is **unmeasured**, and no classifier can exceed the reproducibility of its labels. Therefore:
  - Every accuracy figure in [I7] is reported **without a known noise ceiling**, and the write-up must say so in exactly those terms — not "our model achieves X%" but "X% against single-run labels of unmeasured stability".
  - A model that lands close to the base rate is then **ambiguous**: it may be a weak model or it may be at the noise floor. Single-run labels cannot distinguish those two, and that ambiguity is the price of the decision.
  - **Cheapest partial mitigation, if a few hours free up later:** re-run only a **stratified 20-function subset** (5 per bucket) and report the flip rate on it as an *estimate* of the ceiling. That is ~30–50 min, not a second full pass, and it converts the limitation from unbounded to bounded. Recorded here as the obvious follow-up rather than a requirement.
  - Keep the run id in the dataset filename ([I4]) so a second pass can be joined later without ambiguity.
- Cost to plan for (measured, from `runs/batch-20260807-132133.csv`): 14 functions took 16.2 min wall-clock at 70 s/function mean, sequential (one worker, by design). `evaluation_set` is harder (higher cc, real repo code, Floci provisioning), so budget **2.5–4 h** for the pass. Energy, post-[H9] constants: ~4 kJ/function ⇒ ~385 kJ (~107 Wh). This is an overnight job, not an interactive one.
- **Run it only once the prerequisites are in**, per the 2026-08-24 ordering: [C8]/[I3]'s scanner and [H6]'s runtime harness both land first, so this single pass yields labels, per-function `E_translation` *and* the ΔE measurements [I9] needs — instead of being repeated later for want of one of them.
- Why: it is the single blocking dependency. Feature engineering, model choice and evaluation protocol can all be *designed* now (and are, below), but none can be *executed* without this.
- Architecture impact: None (uses existing instrumentation) | Effort: S (to launch) / L (wall-clock) | Priority: **P0 — blocks [I2], [I4]–[I9]**
- **Status: run 2026-08-30/31, `run-20260830-210122.jsonl` (run id `20260830-230152`).** 95 attempted, 0 skipped, 0 errors; **11 completed (11.6%)**. Bare-metal Ubuntu host, real RAPL, `scripts/benchmark.json` (sha256 `6ac5439c`) on `devstral-2-123b-instruct-2512`, Floci route enabled and exercised (23 functions provisioned from fixture `setup`). Wall clock ~8.2 h — well over the 2.5–4 h budgeted here, on a 4-thread i7-7500U at ~100 s per LLM call.
  - **The two candidate labels agree exactly on this run**: `all_tests_passed` (the settled label) and `completed` both select the same 11 functions — `f1 f8 f28 f31 f39 f41 f67 f81 f87 f92 f94`. So nothing in [I2]'s findings below depends on the label choice, which is worth stating since the two were expected to diverge.
  - **Effective positives are 10, not 11**: `f92`/`f94` are one [I11] near-duplicate group (similarity 0.978). No other success falls inside a group.
  - Run log, batch CSV, manifest and packages are now tracked in git (see the `runs/*` negations in `.gitignore`) so runs are comparable across machines; service logs stay ignored because pre-[F6] ones carry API keys in plaintext.

- **Status: SECOND pass 2026-08-31, `run-20260831-170746.jsonl` (run id `20260831-190900`) — this is the corpus to use.** 95 attempted, 0 skipped, 0 errors; **42 completed (44.2%)**, against 11 (11.6%) on the first pass. Same host, same model, same corpus; what changed was the pipeline, not the data — see the three fixes below. The first pass is kept above as provenance (and because [I2]'s superseded conclusion rests on it), but every downstream item ([I4]–[I9]) should consume this one.
  - **The two labels agree exactly again**: `all_tests_passed` and `completed` both select the same 42 functions, so [I2]'s revised findings are again independent of the label definition.
  - **Effective positives are 38, not 42** after [I11] grouping: 8 successes fall inside near-duplicate groups (`f2 f9 f12 f33 f59 f60 f75 f93`). Effective base rate 38/86 = 44.2% — coincidentally the same as the raw rate.
  - **Cost per successful translation fell 4.4x: 44.70 Wh -> 10.17 Wh**, and the run produced ~4x the successes for *less* total spend (427.2 Wh vs 491.6 Wh). Wasted share 95.7% -> 68.5%.
  - **What changed (three pipeline fixes, all landed 2026-08-31 before this run).** The delta cannot be attributed to any one of them individually — they shipped together:
    1. **The translate/repair/align prompts mandated `(events.APIGatewayProxyResponse, error)` as the handler signature.** That type always serialises as `{"statusCode",...,"multiValueHeaders",...}`, but only **28 of 95** functions expect an API Gateway envelope — 43 expect a plain object (Alexa/Lex and custom dicts), 20 expect `null` (Python returning `None`), 11 something else. The model was being *instructed* to produce a shape that cannot match two thirds of the fixtures, and was obeying. This accounted for **137 of 201 output mismatches (68%)**, and for **28 functions whose every failing case was this artifact**. Signature is now `(any, error)` with explicit per-shape rules; the harness never required the type (`response, err := handle(...)`; it marshals whatever comes back), so this was purely a prompt defect. **Envelope mismatches: 137 -> 0**; output mismatches overall 201 -> 45.
    2. **Deterministic unused-variable repair** (`internal/builder/unusedvars.go`): 10 functions had failed to build on `declared and not used` alone. Fired 22 times this run and **fixed 11 builds with no LLM call at all**; the "would break the declaration" guard reverted once, catching the `:=`-with-no-new-names case. Build failures from unused variables: 10 -> 1.
    3. **Repair-stage sampling** (`temperature: 0.5`, `top_p: 0.95` on `gollmRecovery`/`testRecovery` in `scripts/benchmark.json`, config sha `6ac5439c` -> `e82fbaf9`). [E3]'s `retry_temperature` is structurally inert on those stages — they never *fail* (122 executions, 0 failures), so their `RetryCount` never advances and `CurrentAttempt` is permanently 1 — while making 56% of all LLM calls. Stagnation flags 75 -> 47, aborts 124 -> 68: improved, not solved. **[E3] itself should be fixed to key off invocations rather than `RetryCount`; filed as a follow-up.**
  - **Remaining 53 failures are overwhelmingly genuine**: value mismatch 17, execution error 14, undefined SDK symbol 8, other build 7, type mismatch 4, side-effect 2, unused variable 1. The mechanical classes are gone; what is left is semantic difficulty concentrated in AWS SDK translation.
  - **Energy and payback for this pass** (full report: `go run ./cmd/energy -runtime evaluation/runtime.json runs/run-20260831-170746.jsonl`):
    - 42 translations cost **134.62 Wh** (mean 3.21, median 1.64); 53 failures cost **292.55 Wh**, i.e. **68.5%** of the run's spend, down from 95.7%. **Cost per success 10.17 Wh**, down from 44.70.
    - **Repair is now the largest cost centre**: `testRecovery` 27.2% + `gollmRecovery` 17.3% = **44.5%** of inference energy, against `convert` 31.6% and `clean` 24.0%. [G2] (right-sizing repair payloads) is therefore worth more now than when it was filed.
    - **Complexity predicts cost even though it does not predict success.** Mean energy per completed translation: A 1.17 Wh, B 2.14, C 2.31, **D+ 10.03** - about 8.6x across the range - while success is flat across the same buckets ([I2], A vs D+ p = 0.37). `cc` is a *cost* feature, not a *feasibility* feature; that is the more defensible use for it and it feeds [I9] directly rather than [I6].
    - **Break-even ([H6]'s second-pass measurement): median N* = 1.17e7 invocations; 17 of the 42 successes never pay back.** Only ~26% repay within 1M invocations. So on this corpus most successful translations are not worth performing on energy grounds - which is the [I9] argument, now resting on measured data for every success rather than on 8 functions.

### [x] [I1a] Bare-metal run checklist (prerequisites for [I1] and the [H6] measurement)

Verified 2026-08-26 against the real corpus; these are measurements, not guesses. Work through
them **on the measurement machine before starting the run** — the translation pass is 2–4 h and
sequential, so anything discovered mid-run costs the whole run.

**1. Python third-party packages — the biggest single gap.** Running the [H6] harness's loader over
all 95 `evaluation_set` sources on a stdlib-only interpreter: **only 23 of 95 import successfully.**

| missing module | functions |
|---|---|
| `boto3` | 54 |
| `dateutil` | 15 |
| `bs4` | 2 |
| `botocore` | 1 |
| **loads and runs** | **23** |

Without them `cmd/runtime` skips 76% of the corpus, which means almost no ΔE, which means no `N*` —
the headline energy result. Create a venv with `boto3`, `python-dateutil`, `beautifulsoup4`,
`urllib3`, `requests`, **pin the versions and record them in the manifest** (they affect Python-side
energy, and the Go-side-needs-none asymmetry is already a §6 threat). Point `PYSCAN_PYTHON` at that
interpreter: `pyscan` needs only stdlib, so one variable serves both, and having the analysis stage
and the measurement harness disagree about which Python is in use would be a nasty source of
unexplained differences. Handler-name resolution is **not** a problem: 0 of 95 lack a name the
harness recognises (`lambda_handler`/`handler`/`main`).

**2. RAPL counter permissions.** `/sys/class/powercap/intel-rapl:*/energy_uj` is root-only by default
on most distributions (post-CVE-2020-8694). Verify `cmd/runtime -meter rapl` succeeds *before* the
run — it fails loudly with the fix in the message. This is the difference between measured joules
and the derived `-watts` fallback, and therefore between quoting `N*` and not.

**3. Thermal and frequency stability.** A multi-hour measurement on a laptop will throttle, and
functions measured late would look slower than identical work measured early. AC power, performance
governor, consider pinning off turbo for stability rather than speed. The design already helps —
Python and Go run back to back per function, so drift hits both sides of each ratio similarly — but
re-measure a handful of early functions at the end and compare; a systematic shift invalidates
cross-function comparisons even where each individual ratio survives.

**4. Floci emulator up, for both passes.** The translation run needs it for the 40 setup-declaring
functions ([C10] routes them to `flociTester`), and `cmd/runtime` now provisions the same fixtures
before measuring. `cmd/runtime` probes it at startup and refuses to begin without it.

**5. Warm the Go module cache.** `cmd/runtime` does `go mod init`/`go mod tidy` per function, so 95
builds are network-bound from cold. Prime it once, or the measurement pass is dominated by downloads
(and fails entirely without network).

**6. Service env for the translation pass:** `REQUIRE_META=true` (benchmark mode — no unattributable
result), `RUN_LOG_DIR` set, `FLOCI_ENABLED=true`, `LLM_CALL_INTERVAL` matched to the backend's rate
limit. `scripts/run-benchmark.sh` records all of these in its manifest and warns when `REQUIRE_META`
is unset.

**7. Do not run `go test ./...` while the benchmark service is up.** `cmd/refaas`'s tests bind port
8080 and reconfigure the service on it; with a benchmark service already there they talk to the
wrong process and fail confusingly (observed 2026-08-26 — `TestStopEndpoint` 500s because the test's
converter factory is registered in the test process, not the running service). Also note the ChatAI
connector test only skips when `ACADEMIC_CLOUD_API_KEY` is *absent*, so on the measurement machine —
where the key will be set for the translation run — `go test ./...` makes a **live billable call**
([B9]).

**8. Use `scripts/benchmark.json` — the run config is not a free choice.** Added 2026-08-26, because
until then no committed config combined the canonical task graph with the evaluation backend:
`default.json` had the right graph but ollama/`qwen2.5-coder:3b` dev settings, and
`scripts/chatai.json` had chatai settings but a **stale graph with no `pyScan` and a bare
`goTester`** — a run on it would have recorded no feature vectors ([I4] would have had nothing to
join) and sent all 40 setup-declaring functions to the black-box tester, which ignores their
assertions with a warning and passes. Both are fixed; `internal/pipeline/shipped_configs_test.go`
now compiles every shipped config and pins the benchmark one's `pyScan required` root, its
`testRouter` route, and its model. The model is pinned because
`evaluation/energy.config.json`'s coefficients were derived for `devstral-2-123b-instruct-2512`
specifically (123B, FP8, 2×H200 per the GWDG reply) — running a different model and costing it with
these constants would put every energy figure in the thesis on coefficients belonging to something
else. `run-benchmark.sh` applies the config itself and records its sha256 in the manifest, so a run
cannot silently use the dev pipeline.

**9. Smoke-test the chain before committing the hours.** ~5 artifacts spanning the buckets plus one
AWS/setup case, end to end: `run-benchmark.sh` → `cmd/energy` → `cmd/runtime` → `cmd/energy
-runtime`. This exercises every join in the pipeline (upload, poll, package archive, feature vector,
provisioning, measurement, `N*`) at a cost of minutes.

**10. Setup UFW** Run this on host beforehand `sudo ufw allow in on docker0`

**Order of operations is fixed**: translation pass first — it produces both the labels ([I1]) and the
translated packages `cmd/runtime` needs — then the measurement pass against `runs/packages-<id>.zip`.

### [x] [I2] Signal check: confirm there is anything to predict before spending the day
- Category: Prediction (data)
- Affected component(s): the `evaluation_set` pass from [I1]
- Problem / current state: the entire premise assumes the success rate sits somewhere in the middle. It might not. The paper-set pilot came out 8/14 (~57%), which is ideal — but that set was hand-built and half its failures were bad fixtures, not bad translations. If `evaluation_set` comes out at 90%+ success, "always translate" is near-optimal, there is almost nothing to gain, and any classifier will look good on accuracy while saving no energy. If it comes out at 10%, the same in reverse.
- Proposed change: as soon as [I1] finishes (or on the first ~20 functions while it runs), check three numbers: overall `all_tests_passed` rate, per-bucket rate (A/B/C/D+), and the AWS vs non-AWS split. Abort criterion: if the base rate is outside roughly **[20%, 85%]**, or if per-bucket rates are flat, stop and reframe — see the fallback below.
- Fallback if there is no signal, which is a **publishable result, not a failure**: report "at this pipeline's maturity, ex-ante selection cannot beat always-translate on this corpus, because the pipeline succeeds on X% of inputs regardless of their static profile" — and pivot the secondary objective ([I9], energy worthwhileness) to the primary one, since a function can be perfectly translatable and still not worth translating. With [H6] landing first, that pivot is fully supported by measured data rather than being a consolation prize.
- Why: this is a one-day modelling budget under thesis time pressure. Half a day spent training models on a corpus with no separable structure is half a day lost; the check costs an hour of analysis on data [I1] produces anyway.
- Architecture impact: None (analysis) | Effort: S | Priority: **P0 (gate on [I6])**

- **[SUPERSEDED - see the revised result at the end of this item. Kept because the envelope defect was found by chasing exactly this number, and because a pre-registered criterion that fires must be recorded as having fired, not quietly deleted.]** **RESULT 2026-08-31 (from [I1]'s first pass `run-20260830-210122`): the pre-registered abort criterion is met on both of its two clauses.** Base rate **11.6% (11/95)**, below the [20%, 85%] band; and per-bucket rates are **flat**. Both labels agree (see [I1]), so this does not turn on the label definition.

  | split | rate | Fisher exact (two-sided) |
  |---|---|---|
  | bucket A | 3/25 = 12.0% | |
  | bucket B | 3/25 = 12.0% | |
  | bucket C | 3/25 = 12.0% | A vs D+: **p = 1.00** |
  | bucket D+ | 2/20 = 10.0% | |
  | **uses AWS** | **3/58 = 5.2%** | |
  | **no AWS** | **8/37 = 21.6%** | **p = 0.021** |

- **The complexity result is the important negative one.** `meta.json`'s radon-derived bucket — the axis the corpus is *stratified on*, and the quantity [I3]'s `pyScan` reproduces to 92/95 exactly — has **no relationship to translation success here**. A predictor built on complexity features has nothing to learn on this corpus. This was not the expected outcome and should be reported rather than quietly dropped: it is a direct, measured answer to "is `cc` the right ex-ante signal", and the answer is no.

- **What does separate the classes is the library/API surface, not the size or shape of the code.** AWS users succeed at a quarter the rate of non-users. The observed failures are consistent with that being a real mechanism rather than a proxy: the recurring build errors are boto3→`aws-sdk-go-v2` translation defects (`undefined: smithy.As`, `aws.Bool(...)` used as a `bool`, `undefined: lambdacontext.RemainingTime`), i.e. the model mis-renders a large, version-sensitive SDK surface.

- **Cross-corpus corroboration (`function_set`, 14 functions, same service/config/model, run back-to-back 2026-08-31, `run-20260831-084331`): 10/14 = 71.4% vs the first pass's 11.6%, Fisher p = 5.4e-06.** (This comparison was what first suggested the gap was ours rather than the corpus's; against the second pass's 44.2% the contrast is much smaller and the `function_set` run was made on the *pre-fix* pipeline, so do not restate it as a current figure.) `function_set` is **100% non-AWS** and 12/14 bucket A. So the corpus gap is explained by the same axis: absence of AWS, not simplicity of control flow. This also rules out a setup/regression explanation for the low `evaluation_set` rate — the pipeline reaches 71% on the same machine, minutes apart. Energy contrast: cost per success **1.45 Wh** (`function_set`) vs **44.70 Wh** (`evaluation_set`); wasted share 57.2% vs 95.7%.

- **What the trivial AWS rule would actually buy** (relevant to [I5]'s baselines, computed on the **first pass** - the second-pass figures below supersede these): "skip anything whose `meta.aws` is set" cuts 95 attempts to 37, keeps 8 of 11 successes, and drops inference energy **468.3 Wh → 207.6 Wh (−56%)**, improving cost per success **42.6 → 26.0 Wh**. That is a one-line rule with no model, no features and no training, and any learned predictor has to beat *it*, not "always translate". Note this is **not** [I5]'s B4 blocklist — `pyScan` reports `has_infeasible_lib` constant-zero on this corpus, so B4 skips nothing; this is a different, `meta`-derived rule and it should be added to the baseline set explicitly.

- **Caveats that must travel with these numbers.**
  - Only **10 independent positives** after [I11] grouping (`f92`/`f94` collapse). The AWS split rests on 3 and 8 positives; Wilson 95% CIs are [1.8%, 14.1%] and [11.4%, 37.2%] — they barely fail to overlap, and one reclassified function moves the conclusion.
  - `p = 0.021` is **uncorrected** across the handful of splits inspected here. Treat it as "worth pursuing", not as an established effect.
  - **Bucket is stratified by design (25/25/25/20); AWS is not.** AWS functions may fail because SDK translation is hard, *or* because they are longer and more side-effecting. This data cannot separate those, and [I7] must say so.
  - Labels are single-run and their reproducibility is unmeasured ([I1]); at an 11.6% base rate, a handful of flips is a large relative change.

- **[WITHDRAWN 2026-08-31 by the revised result below - do not act on this paragraph.]** ~~Recommendation: take this item's own fallback.~~ With the base rate at 11.6% and buckets flat, "always translate" is not the thing to beat — *"never translate"* is 88.4% accurate, and the honest framing is that ex-ante selection on this corpus reduces to a library-surface rule. Pivot the primary objective to [I9]'s energy worthwhileness, which the [H6] measurements now support with real data: a function can be perfectly translatable and still never pay back — 3 of 11 `evaluation_set` and 6 of 10 `function_set` completed translations report "never pays back" outright. Before committing modelling time, the cheap next step is [I4]'s table plus [I5]'s baselines, so the AWS rule above is measured properly rather than argued from a 2x2.

---

- **REVISED RESULT 2026-08-31, second pass (`run-20260831-170746`, 42/95 = 44.2%): the abort criterion no longer fires, and the modelling day is worth spending.** The first pass tripped both clauses; chasing *why* found that the low base rate was largely a prompt defect of ours, not a property of the corpus (see [I1]'s second status block). With that fixed the picture changes qualitatively, not just numerically.

  | split | first pass | second pass |
  |---|---|---|
  | overall | 11/95 = 11.6% | **42/95 = 44.2%** |
  | bucket A | 3/25 = 12.0% | 13/25 = 52.0% |
  | bucket B | 3/25 = 12.0% | 10/25 = 40.0% |
  | bucket C | 3/25 = 12.0% | 12/25 = 48.0% |
  | bucket D+ | 2/20 = 10.0% | 7/20 = 35.0% |
  | A vs D+ | p = 1.00 | **p = 0.37** |
  | **uses AWS** | 3/58 = 5.2% | **16/58 = 27.6%** |
  | **no AWS** | 8/37 = 21.6% | **26/37 = 70.3%** |
  | AWS split | p = 0.021 | **p = 0.0001** |
  | `goTester` route | 27.0% | 78.7% |
  | `flociTester` route | 3.3% | 17.9% |

  - **Base rate 44.2% is inside the pre-registered [20%, 85%] band**, on 42 positives (38 independent after [I11] grouping) rather than 11. Both labels agree exactly, as on the first pass.
  - **The complexity negative result survives, and is now credible rather than degenerate.** Buckets spread a little (52/40/48/35) but A vs D+ is p = 0.37. At 11.6% "no bucket signal" was arguably just a floor effect; at 44.2% it is a real measurement, and it says `cc`-derived complexity — the axis this corpus is *stratified on*, and the one [I3]'s `pyScan` reproduces to 92/95 — does not predict translation success. Report it as a finding.
  - **The library-surface signal strengthened sharply**: 27.6% vs 70.3%, p = 0.0001, with non-overlapping Wilson intervals ([17.8, 40.2] vs [54.2, 82.5]). This is no longer "worth pursuing" — it is the structure the predictor exists to exploit.
  - **The bar for a learned model rose at the same time, and this is the number [I7] must quote.** "Never translate" fell from 88.4% to **55.8%** accurate, so accuracy is finally a meaningful axis. But the trivial skip-AWS rule ([I5]) is now a *worse* trade than it looked: it forgoes **16 of 42 real successes** to save 54% of spend (188.7 Wh / 26 successes = 7.3 Wh per success, against 9.7 Wh for always-translate). A learned predictor has to beat that trade, not the old one.
  - **Caveats unchanged and still binding**: single-run labels of unmeasured stability ([I1]); the second pass bundles three pipeline fixes so its delta attributes to none of them individually; AWS is not stratified by design the way bucket is, so "hard SDK" and "longer, more side-effecting" remain unseparated; the p-values are uncorrected across the handful of splits inspected.
  - **What this says about method, beyond the number:** the first pass would have produced a defensible-looking negative result — "ex-ante selection cannot beat always-translate on this corpus" — that was substantially an artifact of our own prompt. The lesson worth carrying into the write-up is that a base rate near a floor should be interrogated as a possible pipeline defect *before* it is reported as a property of the corpus.

### [x] [I3] Deterministic ex-ante feature extractor — built as [C8]'s `pyScan`, accurate variant
- **Status: implemented 2026-08-24 together with [C8]** — one scanner, both consumers, as this item specified. Full result, the calibration numbers, the constant-column finding and its consequences for [I5]/[I7] are recorded under [C8] rather than duplicated here.
- Category: Prediction (features)
- Affected component(s): **the same scanner [C8] specifies** — one implementation, two consumers; plus `internal/fixture` for the fixture-side features
- **SETTLED 2026-08-24: build the accurate Python-AST scanner, and complete [C8] and [I3] together, before the [I1] run.** This closes the "Python subprocess vs. Go heuristic tokenizer" question in favour of real parsing: `cc`/Halstead stay comparable to the dataset's own radon-derived values, train/serve parity is guaranteed because training and the service call the *same* extractor, and [C8]'s prompt hints get accurate library/construct detection rather than regex approximations. The cost accepted with it is a Python interpreter in the service image.
- Problem / current state: [C8] specifies a non-LLM Python source scanner that writes `{{ .py_features }}`/`{{ .lib_hints }}` into `req.Metadata` for the translate prompt. That is *exactly* the feature vector a predictor needs, computed at exactly the right moment (upload time, before any LLM call). Building two scanners would be duplicated work and would let the prompt hints and the model features drift apart.
- Proposed change: one extractor emitting **both** shapes from one AST pass — the human-readable hint text [C8] injects into the prompt, and a fixed-width numeric vector (stable, versioned key order) for the model. Four feature families, all free:
  1. **Size/complexity** — `lloc`, `cc`, `cc/lloc`, Halstead difficulty/vocabulary/length, max nesting depth, number of `def`s, branch and loop counts.
  2. **Library surface** — number of imports, third-party import count, `aws` flag, distinct boto3 service count, one-hot over a *fixed, closed* vocabulary (`boto3`, `requests`, `dateutil`, `bs4`, `urllib3`, …) plus an `other` bucket, `stdlib_only` flag, and a **hard-infeasibility marker** for libraries with no realistic Go equivalent (`numpy`, `pandas`, `scipy`, ML stacks) — a function importing those is a near-deterministic skip and belongs in the rule baseline of [I5] as much as in the model.
  3. **Dynamic-Python markers** — the constructs that actually break translation: `eval`/`exec`, `getattr`/`setattr`, decorators, `*args`/`**kwargs`, `yield`, `async`, comprehension count, class definitions, `try`/`except` and `raise` counts, regex use, pickle.
  4. **Fixture-side features (free, and easy to overlook)** — available at upload from `internal/fixture` without touching the Python at all: number of test cases, mean payload size and nesting depth, count of cases declaring `setup`/`sideEffects`, `outputMode` distribution, expected-output arity. A side-effecting fixture set is plausibly a strong difficulty signal and costs nothing to read.
- **Leakage rules, non-negotiable**: no feature may depend on anything knowable only *after* translation starts — no compiler diagnostics, no token counts, no stage durations, no test outcomes. And no feature may require an LLM call: in particular **do not use `meta.json`'s free-text `description`**, which reads as LLM-generated and would silently make every prediction cost an inference.
- **Acceptance test — reproduce `meta.json`**: run the extractor over all 95 artifacts and compare its `cc`/`lloc` against the `meta.json` values produced by the external dataset pipeline. Report agreement and a stated tolerance. This is the extractor's test suite *and* the evidence that the predictor works on an arbitrary upload with no `meta.json` — which is what makes the contribution a mechanism rather than a dataset artefact. Where the two disagree, **the extractor's own value is what the model trains on**, so that training and serving see identical numbers.
- Why: it is the only component on the critical path that is genuine engineering rather than analysis, it is already half-specified as [C8], and doing it before [I1] means the run records the feature vector alongside the outcome instead of it being reconstructed afterwards.
- Architecture impact: Local (new converter via `RegisterConverterFactory`, per repo convention) | Effort: M | Priority: **P0 — do first, with [C8]**

### [x] [I4] One feature/label table as the single artifact every method consumes
- **Status: done 2026-09-02.** `evaluation/prediction/build_dataset.py` joins `cmd/pyscan`'s
  feature CSV (features + `group_id`), the run log's labels, `cmd/energy -json`'s per-function
  facility joules and [H6]'s `runtime-20260831-190900.json` into
  **`evaluation/prediction/dataset-20260831-190900.csv` — 95 rows × 80 columns**, committed as
  this item asked. Cost columns cover **all 95 functions, failures included** (`cmd/energy`'s
  `failed_attempts.translations` carries them), which is what makes the counterfactual replay in
  [I7] possible at all: a skip decision has to be able to subtract the cost of an attempt that
  produced nothing.
  - Labels reproduce [I1] exactly: 42/95 positive, `all_tests_passed` and `completed` agreeing
    on the same 42 functions; 86 independent `group_id`s.
  - ΔE is present for **all 42 successes** (plus 24 failures whose Go side was measured anyway),
    so the benefit term of the energy headline is measured, never imputed.
  - Added beyond the specified columns: `meta_type` (the dataset's workload-character axis, which
    [I9] step 2 groups ΔE on) and `reached_validation` (separates "failed a test" from "never got
    as far as a test" — 20 of the 53 failures never reached validation).
- Category: Prediction (data)
- Affected component(s): new `evaluation/prediction/` (Python, scikit-learn, its own `requirements.txt`, **never** part of `go build` — the same separation `cmd/energy` keeps for the energy model)
- Problem / current state: with several methods and a baseline set, the fastest way to produce an unreproducible comparison is to let each method assemble its own view of the data.
- Proposed change: one script joining [I3]'s features to [I1]'s run-log labels into a single versioned CSV (`evaluation/prediction/dataset-<run-id>.csv`), one row per function. Columns:
  - **Features** — [I3]'s vector, in its versioned key order.
  - **Labels** — `all_tests_passed` (primary, settled), plus `pass_fraction`, `completed` and `shape_only_fraction` as secondaries.
  - **Grouping** — `bucket`, `aws`, and **`group_id` from [I11]** (the near-duplicate/fork-aware group key, *not* `repo_uri`, which misses forks).
  - **Costs** — measured `E_translation` per function from `cmd/energy -json`, and per-invocation ΔE from [H6]. These two are what let [I7] report energy rather than accuracy, and [I9] compose worthwhileness without a second model.
  - Commit the CSV: it is small, and it makes every number in the thesis re-derivable without re-running the pipeline or holding an LLM budget.
- Why: every method, baseline and plot then reads one file, and a reviewer can reproduce the whole comparison from the repo.
- Architecture impact: None (separate tooling) | Effort: S | Priority: **P0**

### [x] [I5] Baselines the models must beat — including the trivial ones
- **Status: done 2026-09-02**, in `evaluation/prediction/evaluate.py` under [I7]'s split protocol.
  B3's `cc` threshold is fitted inside the training fold; **B4 skips nothing** on this corpus, as
  [C8] predicted (`has_infeasible_lib` is constant-zero), so it is numerically identical to B0 and
  the write-up should say so rather than presenting it as a distinct policy. **B5 skip-AWS** was
  added per [I2]'s note, since it — not B4 — is the trivial rule the models actually have to beat.
  Full table in `evaluation/prediction/results-20260831-190900.txt`; the row that matters:
  B5 translates 37/95, keeps 26/42 successes for **198.1 Wh (7.62 Wh per success, against 427.2 Wh
  / 10.17 for B0)**. It buys a 54% spend cut at the price of 16 real successes.
- Category: Prediction (evaluation)
- Affected component(s): `evaluation/prediction/`
- Problem / current state: "random forest achieves 78% accuracy" is not a result. Without trivial baselines it is not even interpretable — at a 75% base rate, "always translate" achieves 75%.
- Proposed change: implement and report all of these alongside the learned models, under the identical split protocol of [I7]:
  - **B0 always-translate** — the current pipeline, the baseline the thesis is actually arguing against. Zero prediction energy, zero missed opportunities, maximum waste.
  - **B1 never-translate** — the degenerate lower bound; makes the energy axis honest.
  - **B2 majority class** — the accuracy floor that exposes an unbalanced corpus.
  - **B3 single-threshold on `cc`** — one number, no training, fully interpretable. If the random forest cannot beat this, that *is* the finding, and it is a good one: it says complexity alone explains translation feasibility. Fit the threshold **inside each training fold**, never on the whole set, or it is not a baseline but a peek.
  - **B4 hard-infeasibility rule list** — the `numpy`/`pandas`/`scipy` blocklist from [I3] plus perhaps an lloc cap. Costs nothing, is trivially explainable to an examiner, and may capture most of the achievable saving.
- Why: the comparison against B0 is the thesis claim; the comparison against B3/B4 is what separates "machine learning helped" from "machine learning was ceremony around a threshold". Both belong in the write-up regardless of which way they come out.
- Architecture impact: None | Effort: S | Priority: P1 (cheap, do it first — it is also the fastest way to sanity-check [I4]'s table)

### [x] [I6] The candidate methods
- **Status: M1 and M2 done 2026-09-02; M3 (LLM-judge) not run** — [I8] closed its argument with a
  measurement instead, so the expensive upper bound is no longer needed to make the point.
  Hyperparameters fixed a priori as this item required, so [I7]'s non-nested outer protocol stays
  licensed (the *threshold* is still selected by an inner CV — it is a fitted quantity, not a
  hyperparameter).
- **There is real ex-ante signal, and this is the headline of section I.** M1 logistic regression
  reaches **ROC-AUC 0.763 ± 0.030** under grouped 5×10 CV; a group-level label permutation test
  puts the null at **0.511 ± 0.088, p = 0.010** over 200 permutations. M2 random forest is
  **0.728 ± 0.015** — the linear arm wins, which at N = 95 with 56 features is the expected
  ordering and is the arm [I9] needs anyway for its probabilities.
- **The coefficients say something [I2] could not.** The strongest signals are *fixture and
  effect* features, not complexity ones: `n_cases_with_setup` (−1.19), `n_raise` (−1.16),
  `n_loops` (−0.95), `n_cases_with_side_effects` (−0.71), `uses_reflection` (−0.67),
  `lib_boto3` (−0.54). `cc` is not in the top twelve. This is consistent with [I2]'s negative
  complexity result rather than in tension with it: what predicts failure here is **how much
  externally-observable behaviour the function has to reproduce**, not how intricate its control
  flow is. That is a reportable finding and it is the one an examiner will remember.
- Category: Prediction (modelling)
- Affected component(s): `evaluation/prediction/`
- **SETTLED 2026-08-24: two trained models — logistic regression and random forest — with the LLM-judge as an optional third. The tiny MLP is dropped from the comparison and recorded as a future idea.** Rationale: at **N = 95 with ~25 features** an MLP has no capacity advantage it can actually exploit, and the two retained models answer the same question with better interpretability and far less variance.
- Proposed change — each ~30–60 LOC on top of [I4]'s table:
  - **M1 Logistic regression** (L2, standardized features). The interpretable arm: its coefficients go into the thesis directly as "what makes a function hard for this pipeline to translate", and it produces **calibrated probabilities**, which [I9]'s expected-value composition needs and a forest's vote fraction does not give for free. If only one model survives the schedule, it should be this one.
  - **M2 Random forest** (depth-limited, ~200 trees, `min_samples_leaf` ≥ 3). The non-linear arm: handles the one-hot/high-dimensional library features without scaling and captures interactions M1 cannot (e.g. "high cc *and* boto3"). Its permutation importances are a second, independent read on the same question M1 answers — where the two agree, the finding is robust; where they disagree, that is worth a paragraph. If probabilities are needed from it, calibrate inside the fold.
  - **M3 (optional, only if the day allows) LLM-as-judge** — one small-model call asking whether the function is translatable to Go. Its value is not accuracy, it is being the *expensive upper bound*: it makes [I8]'s "the predictor must not defeat its own purpose" argument quantitative by putting a method with real inference cost on the same axis as two methods with effectively none. Skip without regret if time is short — the B0–B4 baselines already anchor the comparison from below.
  - **Future idea, not scheduled: tiny MLP.** Worth revisiting only if the corpus grows well past 95 (which [I10]'s score-recording makes happen passively over time). Recorded here so the option is not lost; explicitly *not* part of this week's comparison.
- **Hyperparameters: fix them a priori, do not tune.** With 95 samples, a hyperparameter search either overfits the corpus or consumes the budget in nested-CV bookkeeping. Defensible defaults, stated in the write-up as chosen without tuning, are the stronger scientific position — and they are what make the plain repeated-CV estimate in [I7] unbiased.
- Why: two well-matched models plus five baselines is a complete comparison at this sample size, and dropping the MLP buys back the hours that [I7]'s protocol and [I11]'s audit actually need.
- Architecture impact: None (offline) | Effort: M | Priority: **P0 (gated on [I2])**

### [x] [I7] Evaluation protocol: how the data is split, and why accuracy is the wrong headline
- **Status: done 2026-09-02** in `evaluation/prediction/evaluate.py`. Repeated
  `StratifiedGroupKFold` on `group_id`, **10 folds × 5 repeats** (the item said 5×10; 10 folds
  keeps 9/10 of an 86-group corpus in training, and the repeat count is what supplies the spread).
  Everything fitted lives inside the fold: zero-variance filtering, standardization, class
  weighting, B3's threshold, and the operating point — the last via an **inner 5-fold CV on the
  training fold only**, so no test row ever influences where the gate is placed.
- **The whole comparison is an offline counterfactual replay, and needs no further translation
  run.** Every function was translated once, so `y_i`, the measured `E_i` it actually cost, and
  [H6]'s measured `ΔE_i` are all known; a gate is just a decision vector over those rows, with
  `net(d, N) = Σ d_i·y_i·N·ΔE_i − Σ d_i·E_i`. The only quantity a replay cannot produce is the
  predictor's own cost, which [I8] measures separately. This is worth stating explicitly in the
  thesis: the energy claim rests on measurement, not on a simulated pipeline.
- **Two operating points are reported, because they answer different questions** and only one of
  them is the thing people mean by "does the predictor work":
  - *balanced point* (maximise balanced accuracy in-fold) — M1 translates **46.4 ± 4.0** of 95,
    keeps **28.8 ± 3.1** of the 42 successes, spends **174.4 ± 26.2 Wh** against B0's 427.2:
    a **59% cut in inference energy for 69% of the successes retained**, and **6.03 ± 0.37 Wh per
    success against B0's 10.17 (−41%)**. Accuracy 0.676 ± 0.029.
  - *energy point* (maximise net joules at N = 10⁶ in-fold) — far more conservative: 9.8 translated,
    6 successes kept, because at that horizon most translations do not repay at all.
- **The headline energy curve, and the uncomfortable part of it.** Net energy saved versus B0,
  swept over N (Wh, positive = the gate helps):

  | policy | N=10³ | N=10⁵ | N=10⁶ | N=10⁷ | N=10⁹ |
  |:---|---:|---:|---:|---:|---:|
  | ORACLE (knows `y` and ΔE) | +426.9 | +415.5 | +448.7 | +812.6 | +42036.9 |
  | B1 never-translate | +426.9 | +398.0 | +135.2 | −2492.0 | −291487.2 |
  | B3 `cc` threshold | +343.2 | +328.1 | **+191.0** | −1180.0 | −151988.4 |
  | B5 skip-AWS | +228.7 | +200.0 | −61.5 | −2676.1 | −290283.0 |
  | M1 LR [energy pt] | +384.0 | +360.5 | +146.4 | −1994.4 | −237482.6 |
  | M1 LR [balanced pt] | +252.6 | +234.1 | +65.6 | −1618.9 | −186919.6 |
  | M2 RF [balanced pt] | +294.6 | +268.4 | +30.7 | −2347.2 | −263907.5 |

  - **No gate beats B0 at high N, and none beats B1 at low N.** That is not a modelling failure;
    it is what the corpus is. The dominant term is not feasibility but payback: **17 of the 42
    successful translations have ΔE ≤ 0 — the Go version is *slower* per invocation** — so no
    success-predictor, however good, can make them worth doing. Only the oracle, which also knows
    ΔE, dominates everywhere, and it translates just 11 of 95 at N = 10⁶ for 18.9 Wh.
  - **At the horizon where selection is live (N ≈ 10⁶), B3's one-number `cc` threshold (+191.0 Wh)
    beats both learned models.** Report that plainly: it is exactly the "machine learning was
    ceremony around a threshold" outcome this item pre-registered as a legitimate finding. The
    learned models' advantage is not in this metric — it is the AUC and the calibrated probability
    [I9] composes with, which a threshold does not give.
- **The honest summary sentence**: on this corpus a prediction gate is worth ~**59% of inference
  energy for ~31% of the successes**, but *no* gate built on feasibility alone changes the sign of
  the energy result, because the translations that succeed are mostly not the translations that
  repay. That splits section I's two objectives cleanly — and it says the *secondary* one ([I9])
  is the load-bearing half, which is the reverse of the assumption the section opened with.
- **Per-group generalisation (`--breakdown`), done 2026-09-02 — the model is not just "high `cc` → fail".**
  M1's discrimination survives *inside* every complexity bucket, which is the question this item
  asked:

  | slice | n | base rate | M1 AUC | M2 AUC | M1 recall | M1 Wh/success |
  |:---|---:|---:|---:|---:|---:|---:|
  | all | 95 | 0.44 | 0.772 | 0.732 | 0.690 | 5.74 |
  | bucket A | 25 | 0.52 | 0.846 | 0.929 | 0.846 | 1.76 |
  | bucket B | 25 | 0.40 | 0.760 | 0.813 | 0.600 | 6.46 |
  | bucket C | 25 | 0.48 | 0.705 | 0.756 | 0.667 | 7.32 |
  | bucket D+ | 20 | 0.35 | 0.747 | **0.242** | 0.571 | 12.46 |
  | aws=true | 58 | 0.28 | 0.740 | 0.735 | 0.500 | 5.38 |
  | aws=false | 37 | 0.70 | 0.622 | 0.608 | 0.808 | 5.88 |

  - **Predictability decays with complexity for M1 (0.85 → 0.75) but M2 inverts on D+ (0.242 —
    materially worse than chance, n = 20).** The forest is anti-predictive exactly where the
    functions are most expensive: D+ costs 12.5 Wh per success against bucket A's 1.8. That is a
    concrete reason to prefer M1 as the shipped model beyond its calibration, and it should be
    reported rather than averaged away — a single corpus-wide AUC hides it completely.
  - **Discrimination lives mostly on the AWS subset** (0.740 at a 0.28 base rate) and is weak on
    non-AWS (0.622 at 0.70). Consistent with the coefficients: the signal is effect-surface, and
    non-AWS functions have little of it to vary over.
- **External corroboration on `function_set` (`--external`), done 2026-09-02.** Trained on all 95
  `evaluation_set` rows, tested once on the 14-function `function_set` (run `functionset-20260831`,
  `runs/run-20260831-084331.jsonl`, RAPL, 10/14 = 71.4% base rate).
  - **Leakage checked first**: `cmd/pyscan` over both corpora together produces **zero groups
    spanning them**, so `function_set` is genuinely external and not a near-duplicate slice.
  - **M1 transfers; M2 does not.** M1 reaches **AUC 0.850** and at the balanced point translates
    13 of 14 while keeping **all 10** successes, for 10.9 Wh against B0's 14.5 (**−25% spend, zero
    successes lost**). M2 reaches **AUC 0.525 — chance** — and its threshold degenerates to
    translating everything. Combined with M2's bucket-D+ collapse above, **M1 is the model [I10]
    should ship**, and this is the evidence for it.
  - **The energy operating point does not transfer.** M1's energy-point threshold, fitted on
    `evaluation_set` at N = 10⁶, is 1.000 — it translates *nothing* on `function_set`. The
    balanced point transfers; the energy point is corpus-specific, because it encodes that
    corpus's ΔE distribution as much as its labels. Say this explicitly rather than reporting only
    the flattering row.
  - **Three caveats, all binding, none of which the number can be quoted without.** (1) n = 14 with
    10 positives — the AUC's confidence interval spans roughly ±0.2 and it must never be ranked
    against the in-corpus 0.763. (2) `function_set` expectations were never executed against the
    Python originals (EVALUATION_DATASET.md §4), so its labels are noisier than `evaluation_set`'s.
    (3) **The two runs did not use an identical pipeline**: `scripts/benchmark.json` differed
    (`temperature: 0.5`, `top_p: 0.95` were added to the `gollmRecovery` and `testRecovery` repair
    stages for the `evaluation_set` run) and the `function_set` run was made from a dirty working
    tree (`git_dirty: yes`, commit `34497186`). So this is a cross-corpus *and* cross-configuration
    test, which makes a positive result more impressive and a negative one uninterpretable —
    fortunately M1's is positive.
- Category: Prediction (evaluation)
- Affected component(s): `evaluation/prediction/`, reuses `cmd/energy`'s per-function costs and `N*` machinery
- **Answering "don't we need a proper train/test split?" — yes to the principle, no to a single static holdout.** The requirement is that no information from the evaluation data reaches model fitting or model selection. A one-off 80/20 split is the textbook way to get that, and at N = 95 it is the *wrong* implementation of it: a 20% test set is **19 functions**, so a single accuracy estimate carries a standard error of roughly ±11 points — wide enough to reorder the methods by luck — and it throws away 19 of the 95 training examples the models can least afford to lose. The protocol below gets the same guarantee without either cost.
  - **Primary estimate: repeated stratified k-fold cross-validation** over all 95 — 5 folds × 10 repeats, stratified jointly on `all_tests_passed` and `bucket`. Every function serves as test data in exactly one fold per repeat, so all 95 contribute to the estimate; the 10 repeats give a mean **and a spread**, which is the number that actually supports a claim of contribution.
  - **Why this is unbiased here:** CV is optimistically biased when the same data drives hyperparameter or feature selection. [I6] fixes hyperparameters a priori and [I3] fixes the feature set a priori, so there is no selection loop to leak through — that a-priori rule is not stylistic, it is what licenses the plain (non-nested) protocol. **If any tuning is added later, the protocol must become nested CV**, or the estimate stops being honest.
  - **Everything fitted goes inside the fold** — standardization, class weighting, B3's `cc` threshold, any calibration, and the [I8] operating point. The single most common way a study like this quietly invalidates itself is choosing the decision threshold on the test fold.
  - **Grouping is mandatory, not optional:** split with `StratifiedGroupKFold` on the `group_id` column `cmd/pyscan` now emits. [I11] measured it: **16 functions in 7 groups, effective N = 86**, and four of those groups cross repository boundaries, so grouping on `repo_uri` would not have caught them. Splitting on `function_id` puts near-duplicates on both sides of the boundary and inflates every method equally, which is exactly the kind of error that does not look anomalous in the results.
  - **Independent confirmation set:** the 14-function `function_set` is a genuinely separate corpus and can serve as a one-shot external check of the model trained on all 95 — a *different-corpus* generalization test, which is more informative than a random slice of the same one. Caveat that must travel with it: `function_set` expectations were never executed against the Python originals (EVALUATION_DATASET.md §4), so its labels are noisier than `evaluation_set`'s. Report it as corroboration, never as the headline number.
- **Then: accuracy is the wrong headline metric.** The two error types have wildly asymmetric costs and accuracy weights them equally. A **false positive** (translating a function that fails) wastes one translation — measurable, bounded, ~4–10 kJ. A **false negative** (skipping a function that would have succeeded) forgoes the entire lifetime saving of that function — unbounded in `N`, the invocation count.
  - **Threshold-free metrics**: ROC-AUC and average precision, so the model comparison does not depend on where the operating point is placed.
  - **The headline metric**: **net energy saved versus B0 (always-translate), swept over the decision threshold**, using real per-function `E_translation` and [H6]'s ΔE from [I4]'s table, parameterised by invocation count `N`. Report it as a curve over `N`, which connects directly to `cmd/energy`'s existing break-even `N*` — the predictor and the energy model then tell one coherent story instead of two.
  - **Operating point**: chosen on the training folds by maximising expected net energy at a stated `N`; report the confusion matrix there.
  - **Label-noise caveat, per the single-run decision in [I1]**: report every accuracy figure as "against single-run labels of unmeasured stability". There is no measured ceiling to compare against, so a result near the base rate cannot be attributed to the model rather than to label noise.
  - **Per-group generalisation**: report per-bucket and AWS/non-AWS performance (the dataset's own two reporting axes, EVALUATION_DATASET.md §9), to show whether the model learned anything beyond "high cc → fail".
- Why: this is what turns a classifier benchmark into an energy result, which is what the thesis is about, and the split protocol is what makes the comparison defensible at a sample size this small.
- Architecture impact: None | Effort: M | Priority: **P0**

### [x] [I8] Measure the predictor's own energy in the same units as the pipeline
- **Status: done 2026-09-02**, `evaluation/prediction/predictor_energy.py`, using
  `energy.config.json`'s own `node_power_watts` and `pue` so the figure is consistent with every
  other energy number in the thesis.
- **Measured, standalone framing**: `cmd/pyscan` costs **27 ms per function** (2.60 s for all 95;
  34 ms cold for a single artifact, process start included), M1 inference **0.9 µs**. Charged at
  1700 W × 1.05 PUE — *the GPU inference node's power*, which no CPU-side parse comes near, so
  this is a deliberate upper bound — that is **48.8 J per function**, i.e.
  **E_predictor / E_translation = 3.0 × 10⁻³** (mean) / 5.5 × 10⁻³ (median).
  **Break-even: the predictor pays for itself if it avoids one wasted translation per 331
  functions screened.** It avoids roughly one in three.
- **Marginal framing, which is the honest one here**: [C8] already runs `pyScan` on every job, so
  the *additional* energy of prediction is the inference alone — **1.6 mJ, ~10⁻⁷ of a translation**.
  Report both, as this item required; the standalone number is the conservative one and it is
  still three orders of magnitude clear.
- The item expected ~10⁻⁶ for M1/M2. The standalone figure is worse than that only because the
  Python-AST extractor ([I3]'s accepted cost) dominates and is charged at GPU-node power; the
  conclusion is unchanged and now rests on a stopwatch rather than an assertion. **M3 was not run**
  — with the margin measured at 10⁻³ even under a pessimal power assumption, an LLM-judge upper
  bound would only restate arithmetic already in hand.
- Category: Prediction (evaluation)
- Affected component(s): `evaluation/prediction/`, `evaluation/energy.config.json`, `cmd/energy`
- Problem / current state: "the prediction must not defeat the purpose of the saving" is currently an assertion. It is almost certainly true by a huge margin for M1/M2 and *not obviously* true for M3 — but no number exists either way, and an examiner will ask for one.
- Proposed change: measure wall-clock for (a) [I3]'s feature extraction per function — now the dominant term, since it parses Python rather than pattern-matching — and (b) each model's inference; convert with the **same** node-power and PUE constants already in `energy.config.json`; report `E_predictor / E_translation` per method. Expected outcome, to be confirmed not assumed: ~10⁻⁶ or better for M1/M2, versus order-unity for M3. Then state the break-even explicitly: the predictor pays for itself if it avoids one wasted translation per *k* screened functions, for the measured *k*.
- Note the extractor is doing double duty: with [C8] shipped, its cost is incurred by the translation pipeline anyway, so the *marginal* energy of prediction is the inference alone. Report both framings — marginal and standalone — rather than picking the flattering one.
- Why: it is the item that makes the section's central premise checkable, it is cheap, and it reuses the constants table so the number is consistent with every other energy figure in the thesis.
- Architecture impact: None | Effort: S | Priority: P1

### [~] [I9] The secondary objective (energy-saving potential): compose it, do not train a second model
- **Status: step 1 done, step 2 measured and it contradicts this item's stated hypothesis
  (2026-09-02).** The composition `net(d, N) = Σ d·y·N·ΔE − Σ d·E` is implemented and is what
  produces [I7]'s energy table; the ORACLE row there is that composition under perfect knowledge,
  and it is the only policy that dominates B0 at every N — which is the direct evidence that
  worthwhileness, not feasibility, is the binding constraint on this corpus.
- **Step 2's group-level ΔE expectation, measured over the 42 successes — the sign is the
  opposite of what this item assumed:**

  | group | n | median ΔE / invocation | save energy |
  |:---|---:|---:|---:|
  | `type: network` | 18 | **+17.8 mJ** | 16/18 |
  | `type: other` | 18 | −0.010 mJ | 8/18 |
  | `type: pure` | 6 | −0.028 mJ | **1/6** |
  | `aws: true` | 16 | **+30.7 mJ** | 13/16 |
  | `aws: false` | 26 | −0.004 mJ | 12/26 |

  This item predicted "network- and I/O-bound functions ... save little from a language switch,
  compute-bound ones save most". **The measurement says the reverse**: network/AWS functions are
  where essentially all of the saving is, and `pure` compute functions almost never repay —
  because on this corpus they do microseconds of work against a startup cost Go does not win.
  - **The caveat that must travel with it**: these invocations run against the Floci **emulator**,
    so the "network" delta is `boto3` marshalling and local HTTP — where Go's stack genuinely beats
    Python's — and not wire latency. Against real AWS, wire time would dominate both sides and the
    delta would compress. State this as a limitation; do not quietly generalise it to production.
  - Consequence for [I5]'s B5: **skip-AWS is exactly backwards as an energy rule.** AWS functions
    are harder to translate *and* the only ones whose translations repay. That tension is the
    interesting sentence, and it is why B5 goes sharply negative at high N in [I7]'s table.
- **2026-09-03: the objective itself is now energy-aware, and it changes the result.** Training on
  `all_tests_passed` teaches a model to find *translatable* functions; the research question asks
  for *worthwhile* ones. Two cost-sensitive variants were added to `evaluate.py`, both trainable on
  measured data with **no imputation anywhere** — the key observation being that the value a
  translation returned,

      v_i = y_i · N · ΔE_i − E_i

  is known for every one of the 95 rows: `y` and `E` for all of them (`cmd/energy` costs the
  failures too), `ΔE` for all 42 successes — and a false negative is *by definition* a success, so
  the one quantity that looks missing is never actually needed.
  - **A1 `[cost-weighted]`** — keep the feasibility label, weight each example by `|v_i|` (the
    regret of getting it wrong), normalized to mean 1 so the L2 penalty keeps its meaning.
  - **A2 `[energy-target]`** — *relabel* to `z_i = 1{v_i > 0}` (the decision that would have been
    right) **and** weight by `|v_i|`.
- **Result at N = 10⁶ (net Wh saved versus B0 always-translate; grouped 5×10 CV as in [I7]):**

  | policy | N=10⁵ | **N=10⁶** | N=10⁷ | N=10⁹ | AUC(target) |
  |:---|---:|---:|---:|---:|---:|
  | ORACLE | 415.5 | **448.7** | 812.6 | 42037 | — |
  | B3 `cc` threshold | 328.1 | 191.0 | −1180 | −151988 | — |
  | M1 plain (feasibility) | 360.5 | 146.4 | −1994 | −237483 | 0.763 |
  | M1 A1 cost-weighted | 258.3 | 163.1 | −789 | −105487 | 0.695 |
  | **M1 A2 energy-target** | 300.7 | **279.7** | **+68.8** | −23127 | **0.760** |
  | **M2 A2 energy-target** | 337.1 | **326.6** | **+221.2** | −11367 | **0.809** |

  - **Worthwhileness is as predictable as feasibility.** The energy-target models reach AUC
    **0.760** (M1) and **0.809** (M2) *against their own label* — matching the 0.763 the
    feasibility model gets against its own. This is the finding: there is ex-ante signal for
    "worth translating", not merely for "translatable".
  - **In absolute terms the gap to the oracle largely closes.** At N = 10⁶ the oracle achieves
    313.4 Wh of net energy; plain M1 captured **11.2 Wh (3.6%)**, M2 A2 captures
    **191.3 ± 17 Wh (61%)**.
  - **The relabelling is what matters, not the weighting.** A1 (+163.1) barely beats B3's +191.0;
    A2 nearly doubles it. Weighting cannot rescue a target that says "translate" about the 31
    successes that should not have been translated.
  - **These are the first policies to beat B0 at N = 10⁷**, and they lose an order of magnitude
    less at 10⁹ than anything else.
  - **Report AUC against the trained target, never against `y`.** The energy-target models score
    0.476/0.450 against success — they were told to ignore it. `evaluate.py` now prints both
    columns with that warning inline, because a single "AUC" column here invites exactly the wrong
    reading.
- **A gate has a bounded useful range, and this is the more defensible framing of the whole
  section.** Measured at N = 10⁸ as a second point: B0 reaches 28 764 Wh against the oracle's
  33 279 — only 16% headroom — and essentially every gate *loses* to B0 there (best: M2 A1 at
  +159 Wh). The target also gets *harder* to learn as N rises (M1 AUC 0.760 → 0.628, M2
  0.809 → 0.475), for an interpretable reason: at 10⁶ "worth it" selects the large-ΔE
  network/AWS functions, a structurally distinctive group; at 10⁸ it admits anything marginally
  above zero, which has no common signature. So:
  - below ~10⁵ invocations nothing repays and "translate nothing" is optimal;
  - above ~10⁷–10⁸ nearly everything repays and "translate everything" is near-optimal;
  - **between ~10⁶ and 10⁷ selection earns its keep**, and that is exactly where A2 dominates.
- **Not shipped, deliberately.** `internal/predictor` still carries the feasibility M1 ([I10]).
  Three reasons, all of which would have to be answered first: the energy target requires
  committing to an `N`; its positive class is 11 of 95 with spreads of ±17–53 Wh; and M2 — which
  wins on this target — is the arm that failed to transfer in [I7].
- **The external check cannot settle it on the corpora we have, and that is itself a finding.**
  `report_external` now carries the cost-sensitive variants through, but at N = 10⁶ only
  **1 of 14** `function_set` functions is worth translating, so its AUC(target) (0.308 / 0.692) is
  computed on a single positive and means nothing. `function_set` has no AWS functions, and AWS is
  where the ΔE lives — it is structurally the wrong corpus for the energy question. **Validating
  the energy target needs a second AWS-bearing corpus**, which is a data-collection task, not an
  analysis one. Record it as the named limitation.
- **Two limitations specific to this target**, both worth stating before a reviewer finds them:
  1. **`z` is noisier than `y`.** It depends on `E_i`, the measured cost of one particular attempt,
     so a function that happened to burn three repair rounds gets a worse label than the same
     function on a luckier run. The feasibility label has no such run-to-run component.
  2. **`z` is defined by hindsight, which is legitimate for a label but makes `N` a reported
     parameter rather than a learned one.** Results must travel as a curve over `N`; a single
     number silently hides the range finding above.
- **Remaining**: fold the group-level ΔE expectation into a scoring function that ranks *candidates*
  (it is currently reported, not applied), and decide whether the emulator caveat is severe enough
  to restrict the claim to AWS-emulated workloads. Neither needs a new translation run.
- Category: Prediction (modelling)
- Affected component(s): `cmd/energy` (`N*`), [H6], [I6]'s calibrated probabilities
- **SETTLED 2026-08-24: [H6] is implemented before the [I1] run, so option 1 below is the plan** and the coarse proxy is no longer needed. Sequencing consequence: the single `evaluation_set` pass yields ΔE measurements alongside the labels, which is exactly why [H6] belongs before it rather than after.
- Problem / current state: "estimated energy saving potential" looks like a second regression target, but its label — the per-invocation energy delta between the Python original and the Go translation — **only exists after the translation has been done and measured**. Training a regressor on it would mean an even smaller label set than [I1]'s (only the successful translations have a ΔE at all) and would present an unvalidated model's output as an energy estimate.
- Proposed change:
  1. **Compose rather than learn.** Expected net saving = `P(success)` (from [I6]'s calibrated M1) × (`N` × ΔE_invocation − `E_translation`). `P(success)` is the learned part; ΔE comes from [H6]; `E_translation` is already measured per function. No second model, and the two objectives stay cleanly separated — the primary criterion gates, the secondary ranks what passes the gate.
  2. ΔE is measured only for functions that translated successfully, so ranking a *candidate* still needs an ex-ante estimate of it. Use the measured ΔE distribution **grouped by workload character** (`type` pure/compute vs. network/io, `aws` flag): network- and I/O-bound functions are latency-dominated and save little from a language switch, compute-bound ones save most. Report it as a group-level expectation with its spread, not a per-function point estimate — that is what the data supports.
  3. **Full ex-ante ΔE regression** — out of scope. Record it as a limitation, not as an unfinished task.
- Why: it delivers the secondary objective at a fraction of the cost and avoids presenting an unvalidated regressor as a measurement. The framing — "we predict feasibility and *derive* worthwhileness" — is also the more defensible one.
- Architecture impact: None | Effort: S | Priority: P1 (unblocked, now that [H6] precedes [I1])

### [x] [I10] Service integration: `internal/predictor` + a `predictGate` converter, off by default
- **Status: done 2026-09-02**, deliberately thin, exactly as this item scoped it.
  - **`internal/predictor`** reads an exported JSON model (coefficients, standardizer, threshold,
    provenance) and scores a vector. Pure stdlib — `go.mod` gains no ML dependency — and it
    imports neither `internal/pipeline` nor `internal/domain`, so it stays usable offline.
    **M1 is what ships**, on [I7]'s evidence: the forest does not transfer (AUC 0.525 on
    `function_set`, 0.242 on bucket D+) and does not produce the calibrated probability [I9]
    composes with.
  - **`predictGate`** (`internal/pipeline/predictgate.go`, registered from an `init()` per the
    repo convention) reads the vector `pyScan` already recorded rather than scanning again — that
    is what makes the gate's marginal cost the inference alone (~1.6 mJ) rather than [I8]'s
    standalone 48.8 J. It **fails closed**: a missing model or missing vector is an error, not a
    pass-through, because failing open would report "translated everything" while claiming a gate
    was active. `scripts/predict.json` is the shipped demonstration config, and
    `TestPredictConfigGatesAfterPyScan` pins both that the gate descends from `pyScan` and that it
    ships with enforcement off.
  - **Score recorded on every job** (`Metrics.Prediction`), including when the gate only scores
    and when it declines. This is the cheap fix for N = 95 over time, and it is what keeps
    "would this have succeeded?" answerable once a gate is deployed.
  - **`Enabled` and `Enforce` are separate flags**, which was not in the original sketch and
    turned out to be the important design point. Enforcement destroys exactly the evidence the
    score-recording exists to collect, so the useful default is *score everything, change
    nothing*: it stays comparable to every section-H figure while growing the corpus.
  - **A skip is not a failure.** `domain.PredictionSkip` is a distinct error type; `executeTask`
    returns immediately on it rather than retrying (the vector is deterministic — a retry
    re-scores the same numbers) or invoking recovery (which would spend the budget the gate just
    declined to spend); the run log tags the record `skipped`; and `cmd/energy` reports declined
    candidates in their own section instead of folding them into "Failed attempts", where a free
    failure would flatter every cost-per-success figure.
  - **`POST /predict`** scores an artifact without translating it (`501` when disabled). It shares
    `Runner.ScorePackage` with the stage, so the endpoint's answer is the one the pipeline would
    act on. Verified end to end against real artifacts.
- **Train/serve parity is tested, and testing it found a real defect.**
  `TestParityWithScikitLearn` scores all 95 functions against scikit-learn's own probabilities and
  requires agreement to 1e-9; it now agrees to **2.2e-16**. Getting there required fixing
  `cmd/pyscan`'s CSV writer, which emitted six fixed decimals: the model was being *trained* on
  rounded features and *scored* against exact ones, a ~1e-7 skew (via `cc_per_lloc` and
  `halstead_difficulty`) that is invisible in a log and quite capable of flipping a candidate
  sitting on the threshold. `formatValue` now uses shortest-round-trip `'g'`. **Every figure in
  [I5]–[I7] was regenerated on the exact features and none of them moved** (M2's AUC went
  0.728 → 0.729; everything else is identical), so the fix changes no reported conclusion — but it
  had to be made before a model could honestly be called deployed.
- Category: Prediction (integration)
- Affected component(s): new `internal/predictor`, `internal/pipeline/registry.go` (registration only), `internal/domain` (`Metrics`), `internal/service`
- Problem / current state: the thesis needs a working demonstration that the gate fits the running system, but the modelling work is offline and the two must not become entangled.
- Proposed change, deliberately thin:
  - **Export the trained model as JSON** (LR weights, or RF trees) and write a pure-Go inference reader. This keeps `go.mod` free of any ML dependency, matching how the repo already keeps `cmd/energy`'s constants in a config file rather than in code. M1's export is a vector of coefficients — trivially small, trivially auditable, and one more reason to prefer it as the shipped model.
  - **A `predictGate` converter** registered via `RegisterConverterFactory` in an `init()` (per the repo's stated convention — never hard-coded into `pipeline.go`), usable either as `root`'s `canApply` precondition or as a task that aborts the run with a typed error.
  - **Record the score on every job regardless of the decision** (`Metrics.PredictionScore` + [I3]'s feature vector), so the run log becomes a growing labelled corpus and the "would this have succeeded?" question stays answerable post hoc. This is the cheapest possible fix for the N = 95 problem *over time* — and the thing that eventually makes the deferred MLP in [I6] worth revisiting.
  - **Off by default**, gated like Floci (`PREDICT_ENABLED` / a `ConverterOptions` field). A pipeline with an active gate produces a different denominator, and every existing baseline in section H must remain reproducible.
  - Optionally a `POST /predict` endpoint that scores an upload without translating it — the natural way to demo the feature and to score a corpus cheaply.
- Why: it demonstrates the mechanism end to end without letting an experimental model become load-bearing in the evaluation pipeline. The default-off rule is what protects every number already recorded.
- Architecture impact: Local (fits the registry architecture by design) | Effort: M | Priority: P2 (after the offline comparison is decided — integrating before knowing which model wins is wasted work)

### [x] [I11] Leakage audit: near-duplicate functions in `evaluation_set` and the grouping key
- Category: Prediction (data)
- Affected component(s): `evaluation/evaluation_set`, [I4]'s table (`group_id` column), [I7]'s CV splitter
- Problem / current state — **measured 2026-08-24, this is not hypothetical**: the corpus is scraped from The Stack and contains related functions.
  - **Five pairs share a `repo_uri`**: `f2`/`f75`, `f13`/`f47`, `f29`/`f60`, `f35`/`f44`, and — under *different* URIs — `f92`/`f94`.
  - Of those, **two pairs are near-duplicates by source**: `f92`/`f94` (565 vs 583 lines, only 28 differing lines after normalisation) and `f35`/`f44` (171 vs 176 lines, 34 differing). The other three pairs share a repo but differ substantially.
  - **`f92`/`f94` come from `tgockel/blockify` and `slparker/blockify` — a fork.** Their `repo_uri` values differ, so **grouping by `repo_uri` would not catch the worst case in the set.** That is precisely the failure mode this item exists to prevent.
  - Impact if unhandled: a near-duplicate pair split across the train/test boundary lets a model "predict" a function it has effectively already seen. With a 19-function test fold, one such pair is ~5% of it — enough to move a reported number, and it inflates every method equally, so it would not show up as anomalous.
- Proposed change: a one-off script that (a) computes pairwise source similarity across all 95 artifacts (normalised token or line similarity is sufficient — the two pairs above are obvious at any reasonable threshold), (b) assigns a `group_id` merging both same-repo and above-threshold-similar functions into one group, and (c) writes that column into [I4]'s table. [I7]'s splitter then uses `StratifiedGroupKFold` on it. Report the number of merged groups in the write-up — it is a one-line methods statement that pre-empts an obvious reviewer question.
- Also worth recording from the same pass: whether any near-duplicate pair **disagrees on its label**. Two ~97%-identical functions with opposite outcomes is direct evidence of the stochasticity that the single-run decision in [I1] leaves unmeasured — a free, partial substitute for the repeat runs that were deferred.
- Why: it is a half-hour script that protects the headline comparison from a defect already confirmed to exist in the data, and its by-product is the only label-noise evidence available under the one-run decision.
- Architecture impact: None (analysis) | Effort: S | Priority: **P0 (blocks [I7]'s splitter; do it while [I1] runs)**

- **Status: implemented 2026-08-26.** Fingerprinting in `internal/pyscan/extract.py` (`code_line_hashes`, schema v2), similarity and union-find grouping in `internal/pyscan/similarity.go`, wired into `cmd/pyscan` as a `group_id` **column of the same table** — [I4]'s builder does not have to join it in from a second file and [I7]'s splitter does not have to be trusted to remember. `go run ./cmd/pyscan -groups evaluation/evaluation_set/*.zip` prints the full audit.
- **Result on `evaluation_set`: 16 functions fall into 7 groups. Effective corpus size is 86 independent units, not 95 rows.** That is the number a grouped cross-validation splits over, and the number the write-up should state.

  | group | why | similarity |
  |:---|:---|---:|
  | `f50 f59` | same Alexa skill template, different repos | **1.000** |
  | `f92 f94` | same tool, a repo and its fork | 0.978 |
  | `f35 f44` | same CDK handler, same repo | 0.950 |
  | `f9 f12 f33 f93` | four copies of the AWS Lex booking sample, four different repos | 0.75–0.89 |
  | `f2 f75` | same repo, different code | 0.493 |
  | `f13 f47` | same repo, different code | 0.275 |
  | `f29 f60` | same repo, different code | 0.038 |

- **The headline finding: grouping by `repo_uri` is not a sufficient defence.** Four of the seven groups — ten of the sixteen functions, including the *most* similar pairs — were found by structural similarity alone and come from **different repositories**. `f50`/`f59` are structurally identical (Jaccard 1.000) and share nothing in their metadata. A repo-only rule would have caught three groups covering six functions and missed the worst offenders entirely.
- **Why AST canonicalisation and not text.** The fingerprint is `ast.unparse` of the tree with docstrings stripped, hashed per line. A line-based comparison scored `f50`/`f59` at 0.759 and the `f70`/`f71` bootcamp pair at 0.650 — the latter differs *only* in docstrings — which put real duplicates in the same band as ordinary Lambda boilerplate (every function here imports boto3, defines `lambda_handler` and returns a `statusCode`, which alone buys 0.4–0.6 between unrelated functions). Canonicalising first separates the two populations cleanly. Hashes rather than lines because the fingerprint travels in the feature table and must not carry a copy of the source.
- **Threshold 0.70**, chosen from the measured distribution: scores fall away sharply after the genuine duplicates, with a gap between 0.750 and 0.678, and every threshold in 0.65–0.75 yields the *same* connected components on this corpus — so the choice is not delicately balanced. `-similarity` overrides it.
- Same-repo pairs are merged regardless of similarity: same author, same house style, not independent observations. It costs three extra merges and is the conservative choice.
- Merging is transitive (union-find), which is what the four-copy Lex cluster needs; group ids are the lexically smallest member so the committed table is stable across runs and input orderings.
- Tests: 13 unit tests covering the Jaccard maths, threshold behaviour, transitivity, determinism, same-repo merging, and — via the real scanner — that the fingerprint is invariant to comments, docstrings and formatting while still separating genuinely different functions. `TestLeakageAuditOnEvaluationSet` pins the four similarity-only groups against the real corpus, so a fingerprint regression that stopped finding them fails the build rather than quietly restoring the leakage.
- **~~Still open, deferred to [I4] by necessity:~~ CLOSED 2026-09-02 against [I1]'s second-pass labels.** Two of the seven groups disagree internally:
  - **`f50`/`f59` — Jaccard 1.000, structurally identical source, opposite labels** (`f50` failed, `f59` passed). This is the label-noise evidence the item was looking for, and it is the strongest form available: identical canonicalised ASTs, one success and one failure in the same run, on the same model and pipeline. **No deterministic ex-ante predictor can be right on both**, so the achievable ceiling on this corpus is provably below 100% — worth stating in [I7]'s write-up next to the AUC.
  - **The caveat is real and must be stated with it**: the two artifacts ship *different fixture sets* (5 cases vs 4), and `f50` passed 4 of its 5. So the disagreement is partly attributable to a harder fixture, not purely to sampling stochasticity — the evidence is suggestive, not clean. They also cost very differently (29.4 kJ vs 11.1 kJ), i.e. `f50` burned repair budget `f59` did not.
  - `f29`/`f60` also disagree, but at similarity 0.038 (merged only by the same-repo rule), so they carry no information about stochasticity.
  - The other five groups — including `f92`/`f94` (0.978) and `f35`/`f44` (0.950) — agree internally. So **1 of the 2 genuinely near-identical pairs flipped**: too small a sample to quote a noise rate from, and exactly the reason the 20-function re-run named in [I1] is still the right follow-up.

### Threats to validity (write these into the thesis, not just here)

1. **N = 95, one corpus.** Any accuracy figure carries a confidence interval of roughly ±10 points. Report intervals, not point estimates, and never rank two methods whose intervals overlap.
2. **Single-run, stochastic labels.** Per the [I1] decision, labels come from one pass and their reproducibility is unmeasured, so no noise ceiling is available to contextualise any accuracy figure. State it as a limitation with the named mitigation (a 20-function re-run) rather than leaving it implicit. [I11]'s near-duplicate label agreement is the only partial evidence available in the meantime.
3. **The predictor learns *this* pipeline, not *translatability*.** Labels come from one pipeline configuration (`default.json`), one model (`devstral-2-123b-instruct-2512`), one prompt set. Improve the prompts and the labels shift. The honest claim is "predicts what this pipeline fails at", and it should be stated in exactly those words.
4. **Corpus bias.** `evaluation_set` is scraped AWS Lambda code from The Stack, 58/95 using AWS; the class balance and feature distribution of production traffic will differ. It also contains **copied sample code**: 16 of the 95 are near-duplicates or same-repo siblings, so the corpus provides **86 independent units, not 95** ([I11]) — report that as the effective N.
5. **Fixture quality bounds the label.** EVALUATION_DATASET.md §4 warns that functions are not certified correct and that expectations record behaviour including bugs; the paper-set pilot found 5 of 9 failures were fixture artefacts rather than translation defects. A label that says "failed" may mean "the fixture was unsatisfiable" — inspect the failures from [I1] before trusting the negative class.
6. **The extractor is now on the translation path too.** With [C8]/[I3] shipped before the [I1] run, the labels are produced by a pipeline whose prompts already receive the scanner's hints. That is the intended configuration, but it means these labels are not comparable to `run-20260807-132133`'s — do not mix the two corpora when reporting success rates.

### Decisions (settled 2026-08-24)

| Question | Decision | Where it lands |
|:---|:---|:---|
| Label | **`all_tests_passed`** on the final round; `pass_fraction`/`completed` carried as secondary columns | [I1], [I4] |
| Repeat runs | **One pass for now**; noise ceiling explicitly unmeasured, 20-function re-run named as the cheap follow-up | [I1], [I7], threat 2 |
| Feature extractor | **Accurate Python-AST scanner**, [C8] and [I3] completed together **before** the run | [I3] |
| Method set | **Logistic regression + random forest**; LLM-judge optional third; **MLP dropped** to a future idea | [I6] |
| [H6] ordering | **[H6] implemented before the full run** — so [I9] composes from measured ΔE, no coarse proxy | [I9], [H6] |
| Train/test split | **Repeated stratified group k-fold** (5×10) over all 95 with a-priori hyperparameters, not a single 80/20 holdout; `function_set` as external corroboration | [I7], [I11] |

---

## Open questions

1. **Failure-mode distribution is unmeasured.** [B5] has landed, so the instrumentation now exists (`per_task` gives executions/failures/tokens per stage) — what is missing is the *run*. Do a full f1–f14 pass and read the per-task failure and token distribution before re-prioritizing the remaining C-items. For that run to be attributable and durable, do [H1]+[H2] first; the same run then doubles as the pilot for the energy study.
   - **Answered 2026-08-07** by the first full `function_set` pass — run id `run-20260807-132133`, `default.json`'s task graph on chatai / `devstral-2-123b-instruct-2512`. Artifacts: `runs/run-20260807-132133.jsonl` (completed jobs), `examples/metrics/metrics-batch-20260807-132133.json` (all 14, failures included), `runs/service-20260807-132133.log`, `runs/batch-20260807-132133.csv`, `runs/packages-20260807-132133.zip`.
   - Distribution: **14/14 built, zero build failures, `gollmRecovery` never executed** — [C3]/[C4] eliminated the build-repair failure class, and this run consequently exercised none of that half of the pipeline. All remaining failure is in validation: 8/14 jobs `Completed`, 32/41 tests passing on the final round. `testRecovery` (realign) is **48.3% of inference energy** over 15 executions that flipped 2 test outcomes; `root`/cleaner is a further ~18% with its contribution still unmeasured ([G3]).
   - Of the 9 final-round test failures: **5 are fixture expectations no correct Go translation can satisfy** (CPython exception text in pf12/pf7, `datetime.now()` timestamps and live third-party API bodies under `outputMode: tolerant` in pf14/pf10/pf9 — consistent with EVALUATION.md's warning that `function_set` expectations were never executed; these want `shape` mode or exclusion), **3 are [A18]**, and 1 (pf8 t2, a recursive graph algorithm) is a genuine semantic divergence.
   - The Floci route was **not** exercised: no `function_set` fixture declares `setup`/`sideEffects` and no function uses AWS, so `testRouter` sent all 14 to `goTester`. [C10]/[C11] stay unvalidated until the 95-function `evaluation_set` runs.
   - Follow-ups this run raised: [A18], [A19], [B8], [B9], [H8], and the [H2] completed-only rule — the 6 failed jobs account for **86.9 kJ of the run's 127.3 kJ**, all of it absent from the run log. Section H's own preamble asked for that decision "before the batch run rather than after"; the batch says revisit it. **[A18], [A19] and the [H2] rule are now closed** (2026-08-07); [B8], [B9] and [H8] remain. Since all three closures change what a run records or how it is judged, the numbers above are superseded — re-run the set before citing anything from it.
2. **Does a continued conversation across stages beat fresh-context repair?** (new, maintainer 2026-07-04) — keeping translate → fix → align in one multi-turn conversation might improve repair quality but grows the context every turn. To be evaluated empirically for translation success rate *and* tokens-per-success; see [G5]. Blocked on [B5] for measurement.

## Resolved questions (answered by maintainer, 2026-07-04)

- **Canonical pipeline config** → all three are intentional and stay. `default.json` is the canonical, extensive paper pipeline (main evaluation target); `default.yaml` is a deliberately short dev pipeline for cheap functional tests (may later be aligned with `default.json`); `scripts/summary-pipeline.json` is the summary→`coder2` experiment to be evaluated *against* `default.json`. Folded into the reframed [C2] and [D1] (both translate prompt variants must receive identical fixes).
- **Fixture format inconsistency** → the odd `examples/output/2026-*` files stem from an outdated test runner and can be ignored; the current f1–f14 fixtures already use the corrected format (bare Python handler return object). [C6] proceeds on that basis.
- **ChatAI `json_schema` enforcement** → verified: the proxy passes it through to vLLM guided decoding; enforcement is per-model, weak models silently fall back to unconstrained text, the proxy never errors on unsupported schemas; fixed-shape (closed) schemas worked on every model tested; `scripts/chatai-check-json-schema.sh` verifies a given model. Folded into [E1] (closed schemas + per-model check) and [E2] (raised to P0 — Go-layer validation is mandatory).
- **ChatAI token accounting** → verified reliable in both modes via the OpenAI-compatible `usage` object; streaming needs `stream_options: {include_usage: true}`; `prompt_tokens_details` is always null (no cache breakdown). Folded into [B5].
- **Ollama unknown options** → confirmed warn-and-ignore; [E5]'s scope reduces to setting an explicit `num_predict`.
- **Branch intent** → `validator` is a stale old implementation; `validator-2` is the only relevant branch and is ahead of `main`. No reconciliation needed for the A1–A3/[B1] fixes (A-fixes are committed on `validator-2`).
- **Network-dependent fixtures** → external network access is accepted and will matter on larger scraped test sets; for non-deterministic return values the app must compare **type shape only** (structure + value types, not values). Folded into [B1]. Note this is distinct from AWS service calls, which must never leave the Floci harness — see [C11].
