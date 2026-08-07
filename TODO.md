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

**B. Software quality**
- [x] [B1] Unify the two output-comparison implementations (+ type-shape-only mode)
- [x] [B2] Tests for orchestration and validation semantics
- [X] [B3] Error taxonomy unused; raw error strings muddle diagnostics
- [X] [B4] Job status conflates "in progress" and "unknown"
- [x] [B5] Observability: chatlog correlation + per-stage metrics **(P0)**
- [X] [B6] Documentation drift on prompt wiring and Floci examples
- [X] [B7] `goTester` runs `go run .` in the service CWD when `WorkingDir` unset

**C. Pipeline features**
- [x] [C1] Structured per-test failure evidence into the repair/align loop **(P0)**
- [x] [C2] Fix the dev pipeline's test-failure dead-end (keep it short)
- [x] [C3] Deterministic `go.mod`; LLM returns only `main.go`
- [x] [C4] Deterministic Go post-processing (package clause, goimports)
- [x] [C5] Detect repair-loop stagnation (narrowed — see item)
- [x] [C6] Validate uploads and fixtures before spending LLM tokens
- [x] [C7] Deterministic and complete test context for prompts
- [ ] [C8] Python feature pre-scan feeding the translate prompt
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
- [ ] [H5] Account for or bound local compute energy (build/test/Floci)
- [ ] [H6] Go vs. Python runtime measurement harness reusing the fixture payloads
- [ ] [H7] Verify token accounting across connector-internal retries

---

> Produced by a full read of the orchestration (`internal/pipeline`), all prompt templates and
> their embed wiring, the build/test harness (`internal/builder`), the LLM connectors, the
> service layer, the Floci stage, the paper fixture set (`examples/paper/f1–f14`), and the
> recorded failure evidence in `examples/metrics/`. Items are grouped by category (A–G) and
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

### [ ] [C8] Python feature pre-scan: deterministic source analysis feeding the translate prompt
- Category: Feature
- Affected component(s): new converter (e.g. `pyScan`), output carried via `req.Metadata` (the metadata mode built for `summary`)
- Problem / current state: The paper set includes constructs a small model mishandles silently: decorators (`f13`), generators/`inspect` (`f14`), third-party libs (`requests` in `f9`/`f10`, `boto3`), recursion (`f6`). Nothing analyzes the source; infeasible translations are discovered only after the full budget is spent — the exact waste the thesis's "prediction" goal targets.
- Proposed change: A non-LLM converter that scans the Python source for third-party imports (mapped through a static table: `requests`→`net/http`, `boto3.client("s3")`→`aws-sdk-go-v2/service/s3`, …), decorators, `async`, `yield`, `**kwargs`, raised exception classes. Writes findings into `req.Metadata` (`{{ .py_features }}`, `{{ .lib_hints }}`) and emits a feasibility-warning metric.
- Why: Injecting explicit API-mapping hints removes the hardest reasoning step (library equivalence) from the model's job — the "more structure, less reliance on large-model reasoning" tradeoff this pipeline needs at 30B scale.
- Architecture impact: Local (fits the registry architecture by design) | Effort: M | Priority: P1

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
- **Floci route**: the deployed Lambda now gets dummy credentials, region and metadata-disable via `lambdaEnv`, but deliberately **not** `AWS_ENDPOINT_URL` — the function runs inside the emulator's network, where the emulator injects the endpoint reachable from *there*; overriding it with this process's host-side address would break every side-effect assertion.
- Residual risk, documented in code: a translation that ignores the endpoint override entirely can still *attempt* an outbound connection to real AWS. With dummy credentials it cannot authenticate, so no data is read or written; preventing the connection attempt itself would need network isolation (out of scope). Fixture-declared external HTTP APIs (f9/f10) are unaffected by design.
- Tests: `internal/builder/awsenv_test.go` (credential stripping, forced endpoint, overridable region, fail-closed fallback, no slice aliasing between cases) and `TestGoPackageTesterIsolatesAWSEnv`, which runs a real program that reports the env it observes and asserts the host's `AWS_PROFILE`/key are gone.

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
- Architecture impact: Local | Effort: S–M | Priority: P1

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
> requires one — [C6]), and (b) **only completed jobs are persisted**, so functions that never
> built successfully are absent from the run log entirely and their pass-rate denominator has to
> come from `/metrics` or the logs. If that denominator matters for the thesis numbers, revisit the
> completed-only rule in [H2] before the batch run rather than after.
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
- Not addressed (needs a decision, see the Section H preamble): a function that never builds is not `Completed`, so it is absent from the run log entirely — the "whole function fails to build → report separately" row of the dataset guide has to be sourced from `/metrics` or the logs unless the completed-only rule changes.

### [x] [H2] Persist run metrics to disk as jobs complete (never lose a batch to an error)
- Category: Evaluation
- Affected component(s): `internal/service/service.go` (in-memory `results`/`metrics` maps; `reconfigure` wipes both by design), `scripts/store-metrics.sh`
- Problem / current state: metrics exist **only in memory**, and there are at least four ways to lose them — a process crash or panic, a restart, a `/reconfigure` (which clears both maps deliberately), and simply never getting around to running `store-metrics.sh`, which is a manual `curl` of the whole map *after the fact*. A 95-function batch is hours of LLM time and real energy spend; losing it to any of these means paying for it twice. There is also no record of *which* configuration produced a given dump, so two runs cannot be told apart afterwards.
- Proposed change (maintainer request, 2026-07-05 — **metrics must be durable against any error**): append one JSON object per finished job to a run log (e.g. `runs/<run-id>.jsonl`) at the moment the worker finishes that job, containing job id, function id and dataset metadata ([H1]), completion status, per-test outcomes ([H1a]) and the full `Metrics` including `per_task`. Write it *append-only and immediately* — a job's record must survive whatever happens to the next job. Add a run header (or sibling file) capturing the active pipeline config, LLM client, model, `LLM_CALL_INTERVAL` and the git commit. Keep `/metrics` unchanged for live inspection, and keep writing the in-memory map so nothing downstream breaks. Worth considering: flush partial metrics for a job that dies mid-pipeline, since a crashed job's tokens were still spent.
- Why this improves the evaluation: replaces EVALUATION.md's JSONL/`CallRecord` item with the minimum that actually makes the artifact durable and self-describing — a thesis artifact must stay interpretable without the server that produced it, and an energy measurement that can be erased by a stray `/reconfigure` is not a measurement. Based on reasoning; no external source needed.
- Architecture impact: Local | Effort: M | Priority: **P0** (every un-persisted batch is a re-run at full energy cost)
- Status: **Implemented 2026-07-05.** `internal/service/runlog.go` appends one JSON object per finished job to `runs/run-<ts>.jsonl` (`RUN_LOG_DIR`; `off`/empty disables), opening/writing/closing per record so a job's line survives whatever happens to the next one. Records carry job id, function id, LLM client and the full `Metrics` including `per_task` and the dataset `meta`; the file is created lazily and opens with a `run_start` header, and `/reconfigure` appends a boundary marker so records from different configurations are distinguishable. **Only completed jobs are persisted** (maintainer decision) — the guard lives in `recordJob` itself, not only at the call site, so no future caller can bypass it; failures stay visible via `/metrics` and the logs. Write failures are logged, never fatal: losing the archive is bad, failing conversions because a disk filled would be worse. `runs/` is gitignored. Tests: `internal/service/runlog_test.go` (completed vs. incomplete, single header, append across jobs, reconfigure boundary, disabled mode); race-clean.
- Deferred: the run header does not yet capture the pipeline config/git commit ([H3] adds the model per stage, which is the more load-bearing half); partial metrics for a job that dies mid-pipeline are not flushed, which follows from the completed-only rule.

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

### [ ] [H5] Account for or bound the pipeline's local compute energy
- Category: Evaluation
- Affected component(s): `internal/builder` (durations already recorded per task via `TaskMetrics.Duration`), `internal/floci`, evaluation tooling
- Problem / current state: `E_translation` counts LLM inference only, but each build attempt runs `go mod init`/`go mod tidy` and `go build`, each test round runs one `./fn` process per fixture, and the Floci route additionally starts emulator containers — all multiplied by every repair iteration. This energy is currently invisible to the model.
- Proposed change: decide and document one of: (a) measure a representative build/test round with `perf stat` and scale it by the already-recorded per-task durations, or (b) declare it an excluded term with an order-of-magnitude bound in threats to validity. Do not leave it silently unmodelled.
- Why this improves the evaluation: an energy claim that omits a component of its own pipeline invites the obvious examiner question; the durations needed to bound it are already being recorded, so closing it is cheap. Based on reasoning; no external source needed.
- Architecture impact: None (analysis) | Effort: S (bound) / M (measure) | Priority: P1

### [ ] [H6] Go vs. Python runtime measurement harness reusing the fixture payloads
- Category: Evaluation
- Affected component(s): new tooling under `evaluation/`; reuses `internal/fixture` payloads and the envelope of `internal/builder/test_handler.txt`
- Problem / current state: EVALUATION.md §6 requires methodologically symmetric measurement of both versions; nothing exists yet. The Go side already has its harness (JSON event on stdin → `handle` → `{"response": …}` on stdout); the Python side has no equivalent, and cold start is not separated from steady state anywhere.
- Proposed change: a Python harness mirroring the Go one exactly, plus a driver that runs both over the *same* fixture payloads on the same machine under `perf stat -e power/energy-pkg/,power/energy-ram/`, measuring cold start (one process per invocation) separately from steady state (N invocations in one process), applying the same PUE to both sides.
- Why this improves the evaluation: reusing the canonical fixtures guarantees both sides see identical inputs through identical envelopes by construction, which is precisely the symmetry the comparison depends on — and it means the harness needs no new test data. Based on reasoning; no external source needed.
- Architecture impact: None (separate tool) | Effort: M | Priority: P1

### [ ] [H7] Verify token accounting across connector-internal retries
- Category: Evaluation
- Affected component(s): `internal/llmconnector/chatai.go` (the [F2] retry loop assigns `metrics = m` per attempt), `ollama.go`, `gemini.go`
- Problem / current state: transient failures are retried *inside* `InvokeLLM`, and the loop overwrites the metrics of the previous attempt rather than accumulating. In practice a rate-limited or 5xx attempt generates nothing and reports no usage, so nothing is lost today — but the energy figures rest on that assumption and it is untested. (The adjacent case is already correct: a truncated `finish_reason=length` response *does* consume tokens, and `RecordLLMCall` runs before the error check, so it is counted.)
- Proposed change: add a fake-backend test asserting that a retried-then-successful call reports only the successful attempt's usage, and that a truncated response's tokens are still counted; switch overwrite → accumulate if any backend is found to report usage on a failed attempt.
- Why this improves the evaluation: a cheap guard on the one place where the energy accounting could silently under-count, in code that already has fake-backend test infrastructure. Based on reasoning; no external source needed.
- Architecture impact: Local | Effort: S | Priority: P2

---

## Open questions

1. **Failure-mode distribution is unmeasured.** [B5] has landed, so the instrumentation now exists (`per_task` gives executions/failures/tokens per stage) — what is missing is the *run*. Do a full f1–f14 pass and read the per-task failure and token distribution before re-prioritizing the remaining C-items. For that run to be attributable and durable, do [H1]+[H2] first; the same run then doubles as the pilot for the energy study.
2. **Does a continued conversation across stages beat fresh-context repair?** (new, maintainer 2026-07-04) — keeping translate → fix → align in one multi-turn conversation might improve repair quality but grows the context every turn. To be evaluated empirically for translation success rate *and* tokens-per-success; see [G5]. Blocked on [B5] for measurement.

## Resolved questions (answered by maintainer, 2026-07-04)

- **Canonical pipeline config** → all three are intentional and stay. `default.json` is the canonical, extensive paper pipeline (main evaluation target); `default.yaml` is a deliberately short dev pipeline for cheap functional tests (may later be aligned with `default.json`); `scripts/summary-pipeline.json` is the summary→`coder2` experiment to be evaluated *against* `default.json`. Folded into the reframed [C2] and [D1] (both translate prompt variants must receive identical fixes).
- **Fixture format inconsistency** → the odd `examples/output/2026-*` files stem from an outdated test runner and can be ignored; the current f1–f14 fixtures already use the corrected format (bare Python handler return object). [C6] proceeds on that basis.
- **ChatAI `json_schema` enforcement** → verified: the proxy passes it through to vLLM guided decoding; enforcement is per-model, weak models silently fall back to unconstrained text, the proxy never errors on unsupported schemas; fixed-shape (closed) schemas worked on every model tested; `scripts/chatai-check-json-schema.sh` verifies a given model. Folded into [E1] (closed schemas + per-model check) and [E2] (raised to P0 — Go-layer validation is mandatory).
- **ChatAI token accounting** → verified reliable in both modes via the OpenAI-compatible `usage` object; streaming needs `stream_options: {include_usage: true}`; `prompt_tokens_details` is always null (no cache breakdown). Folded into [B5].
- **Ollama unknown options** → confirmed warn-and-ignore; [E5]'s scope reduces to setting an explicit `num_predict`.
- **Branch intent** → `validator` is a stale old implementation; `validator-2` is the only relevant branch and is ahead of `main`. No reconciliation needed for the A1–A3/[B1] fixes (A-fixes are committed on `validator-2`).
- **Network-dependent fixtures** → external network access is accepted and will matter on larger scraped test sets; for non-deterministic return values the app must compare **type shape only** (structure + value types, not values). Folded into [B1]. Note this is distinct from AWS service calls, which must never leave the Floci harness — see [C11].
