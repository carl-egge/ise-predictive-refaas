# Audit & TODO List — Python→Go Serverless Translation Pipeline (ReFaaS)

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
waste. Fourth, the recorded metrics (`examples/metrics/`) show that the dominant real failure
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

> **Status 2026-07-04 (branch `validator-2`):** all A items are fixed except the parts that
> depend on other categories — [A15] (dropping non-file chatter keys, behavior question) and
> [A17] (multi-source-file policy, deferred to [C9]). Regression tests were added in
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

### [~] [A15] `BasicLLMDeploymentReader` picks a nondeterministic "main" file and accepts empty content
- Category: Bug
- Affected component(s): `internal/translator/readers.go`
- Problem / current state: It selects the first map key with prefix `main` in randomized map order. With Gemini's fallback schema (which allows `main.go`, `go.mod`, *and* `main.py`, all nullable), the `cleaner` stage can pick an empty `main.go` over the populated `main.py`, silently replacing the Python source with an empty root file; there is no check that `RootFile` is non-empty. Leftover chatter keys become `BuildFiles` and ship in the output zip.
- Proposed change: Deterministic selection (sorted keys; prefer exact `main.<original suffix>`; only non-empty content); reject responses with no usable main file. (Dropping non-file-looking keys from `BuildFiles` deferred — behavior question.)
- Status: **Partially fixed** — deterministic, non-empty selection implemented (`selectMainFile` in `readers.go`); pruning of non-file chatter keys from `BuildFiles` still open.
- Why: An empty/garbage working package poisons every later stage while the pipeline still reports stage success.
- Architecture impact: Local | Effort: S | Priority: P2

### [x] [A16] `Metrics.AddMetric` clobbers `StartTime` to the zero value
- Category: Bug
- Affected component(s): `internal/domain/types.go`
- Problem / current state: Connector-returned `Metrics` have zero `StartTime`; `m.StartTime.After(zero)` is always true, so the request's start time is reset to year 1 on the first LLM call. Masked in service mode (Start overwrites afterwards), but `Pipeline.Execute`'s own timing and the `ConvertFromFileBest` path produce garbage `TotalTime`.
- Proposed change: Skip zero-valued times in `AddMetric`.
- Why: Trustworthy timing is required for the thesis's energy/cost evaluation.
- Architecture impact: Local | Effort: S | Priority: P2

### [~] [A17] Zip ingestion: last-`.py`-wins, macOS junk entries, CRLF `.env` parsing
- Category: Bug
- Affected component(s): `internal/inputhandler/reader.go`
- Problem / current state: Every `.py`/`.go` entry overwrites `RootFile`, so a multi-file Python package silently keeps only the last file; `__MACOSX/._main.py` AppleDouble entries match the suffix check and can clobber the real source; `.env` is split on `"\n"` leaving `\r` on Windows-authored files, producing malformed env entries passed to `exec`.
- Proposed change: Skip `__MACOSX/` and `._*` AppleDouble entries; trim `\r`/blank/comment lines from `.env`. The multi-source-file policy (error vs. support) is deferred to [C9].
- Status: **Partially fixed** — junk-entry skipping and `.env` line hygiene implemented in `reader.go`; last-`.py`-wins behavior intentionally unchanged pending [C9].
- Why: These are silent input corruptions that no downstream stage can recover from.
- Architecture impact: Local | Effort: S | Priority: P2

---

## B. Software quality issues

### [ ] [B1] Two divergent output-comparison implementations; the better one is unused by the core path
- Category: Code Quality
- Affected component(s): `internal/builder/validator.go` vs. `internal/floci/output.go`
- Problem / current state: `internal/floci`'s `matchOutput`/`jsonSubset` is deterministic, recursion-safe, reports a dotted path to the first divergence, and has tests; `JsonAwareSimilarityValidation` is buggy (A1–A3), untested, and reports nothing. Two subtly different definitions of "equivalent output" make experimental results incomparable between `goTester` and `flociTester`.
- Proposed change: Extract `jsonSubset` into a shared package and make `GoPackageTester` use it (after unwrapping the harness's `"response"`/`"error"` envelope), keeping similarity as an explicit opt-in fallback.
- Why: One tested, deterministic equivalence definition eliminates a class of nondeterministic verdicts and gives repair stages a mismatch *path* to report ([C1]).
- Architecture impact: Local | Effort: M | Priority: P1

### [ ] [B2] No tests for orchestration or validation semantics
- Category: Code Quality
- Affected component(s): `internal/pipeline` (no tests for `executeTask`), `internal/builder` (no validator tests)
- Problem / current state: Retry budgets, recovery-before-retry, snapshot restore, validation-failure recursion are untested; the inverted validator (A1) and the recovery bug (A4) would both have been caught by small table-driven tests.
- Proposed change: Add `pipeline_test.go` with fake converters asserting max-executions semantics, recovery invocation order, snapshot restore, validation-failure retry count; add `validator_test.go` with expected/actual JSON pairs including type mismatches.
- Why: Every future pipeline change needs a safety net to avoid regressing the retry machinery the success rate depends on.
- Architecture impact: None (test-only) | Effort: M | Priority: P1

### [ ] [B3] Error taxonomy exists but is never consumed; raw error strings muddle diagnostics
- Category: Code Quality
- Affected component(s): `internal/domain/errors.go`, `internal/pipeline/pipeline.go`, `internal/builder/builder.go`
- Problem / current state: `CompilationError`/`TestingError`/`LLMError` are created but nothing type-switches on them. When a recovery task fails, its error *replaces* the original task error, hiding the root cause. Build errors are double-wrapped; typos ("no soruce root file") ship in user-facing messages.
- Proposed change: Wrap-and-join recovery errors with the original; strip redundant `exit status` suffixes; keep typed errors for [C1]'s crash-vs-mismatch routing.
- Why: Cleaner errors directly improve what the fixer prompt sees (`{{ .issue }}` is `LastError().Error()`).
- Architecture impact: Local | Effort: S | Priority: P2

### [ ] [B4] Job status conflates "in progress" and "unknown"
- Category: Code Quality
- Affected component(s): `internal/service/service.go` (`results` written only on completion)
- Problem / current state: `HEAD /{uuid}` returns 404 until the job finishes; clients cannot distinguish a queued/running job from a nonexistent one.
- Proposed change: Record the request at enqueue time with a status field; `HEAD` returns 200+status, `GET` on an unfinished job returns 202/425 instead of 404.
- Why: Reliable polling avoids evaluation scripts abandoning or double-submitting long jobs.
- Architecture impact: Local | Effort: S | Priority: P2

### [ ] [B5] Observability gaps: chatlogs lack job/stage correlation; no per-stage metrics
- Category: Code Quality
- Affected component(s): `internal/llmconnector/client.go` (`LogResponse`), `internal/domain/types.go` (`Metrics`)
- Problem / current state: Chatlogs cannot be mapped to jobs/stages; `Metrics` aggregates all LLM calls into single counters — "tokens per stage" and "which stage's retries exhaust" are unanswerable. Metrics are wiped on `/reconfigure`.
- Proposed change: Thread request UUID + task ID into chatlog filenames; add `PerTask map[string]TaskMetrics` (attempts, tokens, duration, outcome) populated in `executeTask`/`LLMConverter.Apply`.
- Why: Instrumentation prerequisite for nearly every prioritization decision here and for the thesis's prediction goal.
- Architecture impact: Local | Effort: M | Priority: P1

### [ ] [B6] Documentation drift on prompt wiring and Floci examples
- Category: Code Quality
- Affected component(s): `CLAUDE.md` (claims `1-stage-translate.md` is wired — `prompts.go` actually embeds `-1` as `coder` and `-2` as `coder2`; plain `translate.md` is the unwired one), `docs/floci-integration.md` (references `examples/floci/pipeline.json`, which does not exist — only `pipeline-bundled.json`), README env-var table (missing chatai/floci vars)
- Proposed change: Correct the three references (docs-only change).
- Why: Prevents a future engineer/agent from editing the wrong (unwired) prompt file.
- Architecture impact: None | Effort: S | Priority: P2

### [ ] [B7] `goTester` runs `go run .` in the service's own directory when `WorkingDir` is unset
- Category: Code Quality
- Affected component(s): `internal/builder/validator.go`, `internal/pipeline/runner.go`
- Problem / current state: If a pipeline places `goTester` without a preceding `goBuilder`, `cmd.Dir` is `""` and `go run .` executes in the refaas process's CWD.
- Proposed change: Error out in `Apply` when `runner.WorkingDir() == ""` naming the missing `goBuilder` prerequisite.
- Why: Turns a bizarre, hard-to-diagnose behavior into an immediate config error.
- Architecture impact: Local | Effort: S | Priority: P2

---

## C. Pipeline feature improvements (primary focus)

### [ ] [C1] Feed structured per-test failure evidence into the repair/align loop
- Category: Feature
- Affected component(s): `internal/builder/validator.go` (`GoPackageTester.Apply`/`doTest`), `internal/translator/translator.go` (template vars), `3-stage-align.md`
- Problem / current state: `doTest` produces a rich error (actual output, expected output, stderr) but `Apply` discards it, returning only `"N tests failed"`. That string becomes `LastError()`, which is all any LLM stage can ever see — and the align prompt doesn't even reference `{{ .issue }}`. Recorded evidence (identical errors across 4+ attempts) shows blind repair does not converge.
- Proposed change: Collect per-test failures into the `TestingError` (`Failures []TestFailure{Name, Input, Expected, Actual, Stderr}`, capped), expose them as a rendered `{{ .failures }}` template var (deterministic order). Distinguish process error vs. output mismatch so pipelines can route crashes to `fixer` and mismatches to `realign`. Update `3-stage-align.md` to consume `{{ .failures }}` ([D2]).
- Why: Execution feedback (failing input, expected vs. actual) is the single strongest known signal for LLM self-repair — Chen et al., *Teaching Large Language Models to Self-Debug* (arXiv:2304.05128) show unit-test feedback substantially outperforms blind resampling — and the current pipeline structurally withholds it.
- Architecture impact: Local | Effort: M | Priority: **P0**

### [ ] [C2] The embedded default pipeline cannot recover from test failures — align it with the known-good chain
- Category: Feature
- Affected component(s): `internal/pipeline/default.yaml`, `internal/pipeline/pipeline.go`
- Problem / current state: In `default.yaml`, `goTester` is the *validation* of the `builder` task. A validation failure re-executes the *same* task — i.e. rebuilds identical code and re-runs the same tests, up to 3 times, with no LLM stage ever invoked (recovery only fires on `Execute` failure). `realign` isn't registered in this pipeline. The fuller chain in `default.json`/README does not have this problem.
- Proposed change: Replace `default.yaml`'s content with the `default.json` task graph. Optionally: on validation failure, route through `OnFailure` before the retry (flag as slightly behavior-changing).
- Why: Test-failure→repair is the pipeline's core success-rate mechanism and it is unreachable in the shipped default; retrying identical code cannot change the outcome.
- Architecture impact: Local | Effort: S | Priority: **P0**

### [ ] [C3] Always regenerate `go.mod` deterministically; never trust the LLM's
- Category: Feature
- Affected component(s): `internal/translator/readers.go` (`GoJsonOllamaReader`), `internal/builder/builder.go`, prompts showing `go.mod` examples
- Problem / current state: The LLM-authored `go.mod` is a persistent, observed failure class: `unknown directive` (metrics 2026-07-01, four identical failures); the wired prompt's example teaches `go 1.x` / `v1.x` placeholders plus `v1.24` — an invalid version (Go modules require full semver). `internal/floci/packager.go` already proves the deterministic alternative: discard `go.mod`/`go.sum`, run `go mod init` + `go mod tidy` unconditionally.
- Proposed change: In `GoJsonOllamaReader.MakeDeploymentFile`, drop `go.mod`/`go.sum` keys and always set `BuildCmd = ["go mod init example.com", "go mod tidy", "go build -o fn ."]`; remove `go.mod` from prompt output examples. Supersedes [A4]'s fallback.
- Why: Eliminates an entire observed failure class with zero LLM involvement — `go mod tidy` derives requirements from imports authoritatively per the Go modules reference (go.dev/ref/mod).
- Architecture impact: Local | Effort: S | Priority: **P0**

### [ ] [C4] Deterministic Go post-processing gate between `coder` and `goBuilder`
- Category: Feature
- Affected component(s): new converter (registered via `RegisterConverterFactory`) or extension of `GoJsonOllamaReader.prepareGoRootFile`
- Problem / current state: Observed failures include `expected 'package', found 'import'` (LLM omitted the package clause) and missing/unused imports. Each such case costs a full build cycle plus an LLM fixer round-trip.
- Proposed change: After parsing the LLM response: (1) prepend `package main` if the file lacks a package clause; (2) run `go/parser.ParseFile` and, on syntax error, fail the *convert* task's validation (retry re-samples the translator); (3) run `golang.org/x/tools/imports.Process` (goimports as a library) before the first build.
- Why: Replaces LLM round-trips with exact, deterministic fixes for the two most mechanical failure classes; `imports.Process` requires no model capability — the key property for small-model deployments.
- Architecture impact: Local (one new converter + one dependency) | Effort: M | Priority: P1

### [ ] [C5] Detect repair-loop stagnation and change strategy instead of re-spending tokens
- Category: Feature
- Affected component(s): `internal/pipeline/pipeline.go` (retry loop) or `GolangBuilder`
- Problem / current state: `metrics-20260701122938.json` shows the same `go.mod` error verbatim four times: the fixer's output didn't change the failing artifact, and the pipeline paid for each identical attempt.
- Proposed change: Compare the current failure text with the previous attempt's; on an exact repeat, either abort early with a "no progress" error, or set `req.Metadata["stagnant"]` that the fixer prompt surfaces, and/or bump sampling temperature ([E3]).
- Why: Reflexion-style loops only help when feedback changes behavior (Shinn et al., *Reflexion*, arXiv:2303.11366); detecting identical outcomes converts guaranteed-wasted attempts into an early exit or a differentiated retry.
- Architecture impact: Local | Effort: S–M | Priority: P1

### [ ] [C6] Validate uploads and fixtures before spending any LLM tokens
- Category: Feature
- Affected component(s): `internal/service/service.go` (`uploadHandler`), `internal/inputhandler/reader.go`
- Problem / current state: An upload with no `.py` file, no `test/` fixtures, or unparseable fixture JSON is accepted; the failure surfaces stages later (or worse, with zero tests `goTester` passes vacuously and the job "succeeds" with no behavioral validation). Fixture-format problems are only discoverable at comparison time.
- Proposed change: At upload: require exactly one root source file and ≥1 test fixture; parse every fixture as `domain.TestFile`; when strategy is `json`, verify `Output` parses as JSON and does **not** contain a top-level `"response"` wrapper (document the canonical format: expected = the Python handler's return object, as in `examples/paper/*`). Return 400 with a per-file error list. *(Open question: confirm the canonical fixture format first.)*
- Why: Fail-fast saves the full LLM/build budget of a doomed run and prevents vacuous "successes" from polluting success-rate numbers.
- Architecture impact: Local | Effort: M | Priority: P1

### [ ] [C7] Deterministic and complete test context for prompts
- Category: Feature
- Affected component(s): `internal/translator/translator.go` (`getFirstTestFile`)
- Problem / current state: Prompts receive the input/output of *one* test file chosen by randomized map iteration — retries can see different examples (non-reproducible), and multi-case behavior (error branches) is invisible to the translator.
- Proposed change: Sort test file names and expose up to k (configurable) input/output pairs as `{{ .tests }}`; keep `{{ .input }}`/`{{ .output }}` as the first sorted pair.
- Why: Test cases in the prompt act as few-shot behavioral specs — showing the error-path fixture is the only way the model can learn the expected non-happy-path statusCode mapping (mechanism per Chen et al., arXiv:2304.05128); determinism makes experiments reproducible.
- Architecture impact: Local | Effort: S | Priority: P1

### [ ] [C8] Python feature pre-scan: deterministic source analysis feeding the translate prompt
- Category: Feature
- Affected component(s): new converter (e.g. `pyScan`), output carried via `req.Metadata` (the metadata mode built for `summary`)
- Problem / current state: The paper set includes constructs a small model mishandles silently: decorators (`f13`), generators/`inspect` (`f14`), third-party libs (`requests` in `f9`/`f10`, `boto3`), recursion (`f6`). Nothing analyzes the source; infeasible translations are discovered only after the full budget is spent — the exact waste the thesis's "prediction" goal targets.
- Proposed change: A non-LLM converter that scans the Python source for third-party imports (mapped through a static table: `requests`→`net/http`, `boto3.client("s3")`→`aws-sdk-go-v2/service/s3`, …), decorators, `async`, `yield`, `**kwargs`, raised exception classes. Writes findings into `req.Metadata` (`{{ .py_features }}`, `{{ .lib_hints }}`) and emits a feasibility-warning metric.
- Why: Injecting explicit API-mapping hints removes the hardest reasoning step (library equivalence) from the model's job — the "more structure, less reliance on large-model reasoning" tradeoff this pipeline needs at 30B scale.
- Architecture impact: Local (fits the registry architecture by design) | Effort: M | Priority: P1

### [ ] [C9] Handle multi-file Python inputs explicitly
- Category: Feature
- Affected component(s): `internal/inputhandler/reader.go`, prompts, `codeBlockGenerator`
- Problem / current state: Only one source file survives ingestion (last wins, silently); a Python function with a helper module is mistranslated from a fragment without any error.
- Proposed change: Minimal: collect additional `.py` files into `BuildFiles` (they already flow into `{{ .code }}`), detect the handler-containing file as root; or at least reject multi-source zips with a clear 400 ([C6]).
- Why: Expands the input domain the pipeline can be *correct* on and eliminates a silent-wrong-answer mode.
- Architecture impact: Local | Effort: M | Priority: P2

---

## D. Per-stage prompt improvements

### [ ] [D1] Convert prompt: fix the few-shot that teaches broken response bodies; state the harness contract
- Category: Prompt-Convert
- Affected component(s): `internal/translator/prompts/1-stage-translate-1.md` (the wired `coder` prompt)
- Problem / current state: (1) The "Input Handling" few-shot returns `Body: fmt.Sprintf("%v", map[string]interface{}{...})`, which prints Go map syntax (`map[result:3]`), not JSON — but the fixtures (e.g. `f2`: `"body": "{\"result\": 3}"`) expect a JSON string body, so the example *teaches the model to fail the tests*. (2) The prompt never explains how the code is executed (stdin event → `handle` → response wrapped as `{"response": ...}`). (3) The output example shows `go 1.x`/`v1.x` placeholders a literal-minded small model will copy into an invalid `go.mod`. (4) `{{ .output }}` is shown without the `{{ .input }}` that produces it (variable exists but is unused). (5) No Python→Go semantic gotchas.
- Proposed change: Replace the body few-shot with `json.Marshal` + `string(b)`; add an "Execution contract" section; pair input *and* expected output (use [C7]); drop the `go.mod` example per [C3]; add a compact gotcha list (`dict.get(k, default)` → explicit zero-value handling; `raise X` → non-2xx statusCode branch; f-strings → `fmt.Sprintf`; Python `True/None` casing vs JSON `true/null`; integer vs float division).
- Why: Few-shot examples dominate output form — a demonstrably wrong exemplar is actively harmful (in-context learning imitates demonstrations; Brown et al., arXiv:2005.14165); stating the I/O contract turns "semantic equivalence" into a checkable output format.
- Architecture impact: Local | Effort: S | Priority: **P0**

### [ ] [D2] Align prompt: give it the failure evidence and a checkable definition of "equivalent"
- Category: Prompt-Align
- Affected component(s): `internal/translator/prompts/3-stage-align.md`
- Problem / current state: The prompt asks the model to verify alignment using only `{{ .original }}` and `{{ .code }}` — no test results, no `{{ .issue }}`, no input/output pairs — even though the stage only runs *after concrete tests failed*. Also: numbering gap (rules 5→7), untagged code fence, step-by-step/JSON-only contradiction.
- Proposed change: Restructure around evidence: "The Go version failed these test cases: [input, expected stdout, actual stdout] ({{ .failures }} from [C1]; minimally `{{ .issue }}` + `{{ .input }}`/`{{ .output }}` today). Modify the Go code so that for each input it produces exactly the expected output. Do not change behavior for passing cases. Return complete corrected files." Fix numbering/fence.
- Why: Converts an open-ended judgment task into a constrained transformation with a verifiable target — the setting where execution-feedback repair is proven effective (Chen et al., arXiv:2304.05128); the prompt-side half of [C1].
- Architecture impact: Local | Effort: S | Priority: **P0**

### [ ] [D3] Fix-errors prompt: structured compiler errors, no contradictions, minimal-change directive
- Category: Prompt-FixErrors
- Affected component(s): `internal/translator/prompts/2-stage-repair.md`, `internal/builder/builder.go` (error text construction)
- Problem / current state: `{{ .issue }}` is the raw combined stdout/stderr wrapped in `failed to build. … exit status 1`. The prompt says both "you only return the code for the handler function" and "return the complete code and other files" — a contradiction a small model resolves unpredictably. Its example output embeds a `go.mod` with literal `\r\n` escapes and the invalid `v1.24` version. Nothing tells the model to preserve working parts.
- Proposed change: Pre-parse Go compiler output (`file:line:col: message`) into a numbered list; delete the "only the handler function" sentence; add "change only what is necessary to fix the listed errors"; remove the `go.mod` example per [C3].
- Why: Precise, localized error context is the input format compiler-repair works best with — the Go toolchain already emits machine-parseable positions; removing the format contradiction eliminates a coin-flip in every fixer call.
- Architecture impact: Local | Effort: S–M | Priority: P1

### [ ] [D4] All prompts: remove the "step by step" vs. "output nothing but JSON" contradiction
- Category: Prompt-Convert / Prompt-FixErrors / Prompt-Align
- Affected component(s): `1-stage-translate-1.md` (rule 1 vs. 7), `2-stage-repair.md` (rule 1 vs. 5), `3-stage-align.md` (rule 1 vs. 7)
- Problem / current state: Each prompt instructs "Let's work this out in a step by step way…" while a later CRITICAL rule forbids any output except JSON. Under a JSON-constrained decoder the reasoning instruction can't be followed; on an unconstrained backend it invites prose that breaks `json.Unmarshal`.
- Proposed change: Delete the step-by-step sentence (or give reasoning a sanctioned `"notes"` key in the output schema that readers ignore).
- Why: Contradictory instructions measurably degrade instruction-following, and smaller models are the most sensitive; constrained decoding already nullifies the CoT benefit — based on structured-output decoding mechanics (Ollama structured outputs documentation).
- Architecture impact: Local | Effort: S | Priority: P1

### [ ] [D5] Document stage: scope it to what translation actually needs (or merge it with summarize)
- Category: Prompt-Document / Efficiency
- Affected component(s): `internal/translator/prompts/0-stage-document.md`, `internal/translator/prompts.go`
- Problem / current state: The cleaner adds generic inline comments, doubling the source's token footprint through every later stage without targeting translation; costs a full-source LLM round-trip; grammar is off; it's the stage whose output most easily corrupts the package under the Basic reader (A15).
- Proposed change: Refocus on translation-relevant facts: input-event/response shape, env vars read, external services called, error branches and status codes. Alternatively: drop the separate cleaner call and extend `summary`'s `output_keys` to return both `intent` and documented source in one call.
- Why: Comments that spell out I/O shape and side effects are precisely the context that reduces translation ambiguity for a small model; generic comments are inert tokens.
- Architecture impact: Local | Effort: S | Priority: P2

---

## E. Small-model robustness

### [ ] [E1] Enforce per-task output schemas for the code-producing stages
- Category: Small-Model Robustness
- Affected component(s): `internal/translator/prompts.go` (factories), `internal/llmconnector/schema.go`, `outputschema.go`
- Problem / current state: Only `summary` sets `output_keys`. `coder`/`fixer`/`realign` fall back to the generic any-object schema on Ollama (an empty `{}` satisfies it), a `main.go/go.mod/main.py` triple on Gemini (invites a stray `main.py`), and schema-less `json_object` on ChatAI.
- Proposed change: Default `output_keys` to `{"main.go": {nullable:false}}` in the code-task factories (mirroring `NewSummaryConverter`); emit `required: ["main.go"]` in the Ollama/ChatAI schema payloads.
- Why: Constrained decoding moves format compliance from the model's competence to the sampler — the highest-value trade for a ~30B model; both Ollama's and OpenAI's structured-output docs report large reductions in format errors vs. prompt-only instructions.
- Architecture impact: Local | Effort: S | Priority: P1

### [ ] [E2] Deterministic JSON extraction fallback before failing a parse
- Category: Small-Model Robustness
- Affected component(s): `internal/translator/readers.go` (`JsonCodeBlockReader` — despite the name, it does *not* strip code fences)
- Problem / current state: Any leading prose, a ```` ```json ```` fence, or trailing commentary makes `json.Unmarshal` fail; the reader logs and returns nil, surfacing as the misleading "could not find main". ChatAI's `json_object` mode makes this a live path.
- Proposed change: On unmarshal failure, deterministically retry: strip markdown fences, extract the first balanced `{…}` region, re-parse; only then fail — with an error saying "response was not a JSON object".
- Why: Recovers, at zero token cost, the most common small-model formatting slip instead of burning a full LLM retry.
- Architecture impact: Local | Effort: S | Priority: P1

### [ ] [E3] Vary sampling on resample-style retries
- Category: Small-Model Robustness
- Affected component(s): `internal/pipeline/pipeline.go` + `internal/translator/translator.go` (`Prepare` runs fresh per attempt — the hook exists)
- Problem / current state: Retries of `cleaner`/`coder` (no recovery task) re-send the identical prompt at `temperature: 0.1`; a near-greedy model reproduces essentially the same wrong output, so `maxRetryCount` on those tasks buys almost nothing.
- Proposed change: Track the attempt number (e.g. via `req.Metadata`) and add an opt-in temperature bump (`task_args.retry_temperature`) on attempts >1.
- Why: Sampling diversity is what makes repeated attempts explore different solutions — the core observation behind self-consistency (Wang et al., arXiv:2203.11171).
- Architecture impact: Local | Effort: S–M | Priority: P1

### [ ] [E4] Truncate feedback to the first compiler errors
- Category: Small-Model Robustness / Efficiency
- Affected component(s): `internal/builder/builder.go`, `2-stage-repair.md` input
- Problem / current state: The fixer receives the full build output; Go compilers cascade — one missing brace produces dozens of downstream errors that mislead a small model into "fixing" symptoms.
- Proposed change: After [D3]'s parsing, pass only the first N (e.g. 5) distinct `file:line` errors, noting "further errors omitted; fix these first".
- Why: Focusing a limited-capacity model on the root error mirrors how cascading diagnostics are meant to be consumed and shrinks the prompt.
- Architecture impact: Local | Effort: S | Priority: P2

### [ ] [E5] Stop sending junk/foreign parameters to Ollama; set an explicit output budget
- Category: Small-Model Robustness / Code Quality
- Affected component(s): `internal/llmconnector/ollama.go` (`Prepare`)
- Problem / current state: `Prepare` injects OpenAI-style `max_tokens` and `response_format` defaults into Ollama's `Options`, and leaves the pipeline-level `strategy` key in (ChatAI deletes it; Ollama doesn't) — none are valid Ollama options, and crucially **no `num_predict` limit is actually set**, leaving truncation behavior to model defaults.
- Proposed change: Mirror ChatAI's deletions (`strategy`, `output_keys`); map a `max_tokens` task param to Ollama's `num_predict`; drop the `response_format` default (the `Format` field already handles structure).
- Why: An explicit, sufficient `num_predict` prevents silent mid-JSON truncation — per Ollama's API documentation, `num_predict` is the generation-length control, not `max_tokens`.
- Architecture impact: Local | Effort: S | Priority: P2

---

## F. Fault tolerance

### [ ] [F1] Per-test and per-build-command timeouts
- Category: Fault Tolerance
- Affected component(s): `internal/builder/validator.go` (`doTest`), `internal/builder/builder.go`
- Problem / current state: Test/build subprocesses inherit only the job's cancellation context. A translated function with an infinite loop (or `f9`-style code blocking on a dead URL without an HTTP timeout) hangs the **single** worker goroutine indefinitely — every queued job stalls until someone manually calls `/stop`.
- Proposed change: Wrap each `doTest` run in `context.WithTimeout` (default 30s, override via `task_args.test_timeout`) and each build command similarly (e.g. 120s). Report a timeout as a distinct failure kind so [C1] can feed it to repair.
- Why: Converts a pipeline-wide outage into a single failed test with actionable feedback and protects batch throughput.
- Architecture impact: Local | Effort: S | Priority: **P0**

### [ ] [F2] Retry transient LLM API failures at the connector, not the task level
- Category: Fault Tolerance
- Affected component(s): `internal/llmconnector/chatai.go`, `ollama.go`, `gemini.go`
- Problem / current state: A 429/5xx/network blip is an ordinary task failure: it consumes a task retry, triggers the recovery LLM task (spending tokens on a "fix" for code with no new defect), and pollutes `LastError()` — the next prompt's `{{ .issue }}` becomes an HTTP error message.
- Proposed change: Retry idempotent transient failures 2–3 times with exponential backoff inside `InvokeLLM`; classify the final error as `domain.LLMError` and have `executeTask` skip `OnFailure` for `LLMError`s.
- Why: Separates infrastructure noise from code defects so retry/recovery budgets are only spent on actual translation problems.
- Architecture impact: Local | Effort: M | Priority: P1

### [ ] [F3] Detect truncated LLM responses via finish/done reason
- Category: Fault Tolerance
- Affected component(s): `internal/llmconnector/ollama.go` (`DoneReason` ignored on success), `chatai.go` (`finish_reason` not parsed)
- Problem / current state: A response cut off at the token limit reaches the reader as malformed JSON, producing the misleading "could not find main" and an undirected retry.
- Proposed change: Parse `DoneReason`/`finish_reason`; when it indicates length, return a specific error ("response truncated at N tokens"), optionally auto-retry once with a doubled limit.
- Why: Makes an invisible failure mode self-describing and mechanically fixable — especially relevant for small local models with tight `num_ctx`.
- Architecture impact: Local | Effort: S | Priority: P1

### [ ] [F4] Upload handler must not block forever on a full queue
- Category: Fault Tolerance
- Affected component(s): `internal/service/service.go` (`uploadHandler`)
- Problem / current state: `service.requestQueue <- …` blocks the HTTP handler indefinitely when 100 jobs are queued, holding the connection and the parsed upload in memory.
- Proposed change: Non-blocking send (`select` with `default`) returning `503` with a Retry-After hint; also remove the job's `cancels` entry in that path.
- Why: Keeps the service responsive under batch load so evaluation scripts fail visibly instead of hanging.
- Architecture impact: Local | Effort: S | Priority: P2

---

## G. Efficiency & token economy

### [ ] [G1] Build once, run the binary per test — stop recompiling in `go run .`
- Category: Efficiency
- Affected component(s): `internal/builder/validator.go` (`doTest`)
- Problem / current state: `goBuilder` already produces `fn` via `go build -o fn .`, but `doTest` invokes `go run .` for every test file — a full compile per test case, multiplied by every validation retry.
- Proposed change: Execute `./fn` in `doTest`, falling back to `go run .` only if the binary is missing.
- Why: Cuts test-stage latency by compile cost × test count without changing semantics, shortening every repair iteration.
- Architecture impact: Local | Effort: S | Priority: P1

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

### [ ] [G4] Reuse the Go module cache across builds and jobs
- Category: Efficiency
- Affected component(s): `docker-compose.yml`/Dockerfile
- Problem / current state: Every `go mod tidy` in a fresh container re-downloads `aws-lambda-go` and friends; the module cache lives in the container layer and is lost on rebuild. Network flakiness during download masquerades as a build failure the fixer can't fix.
- Proposed change: Mount a named volume at `GOMODCACHE` in the compose file; optionally pre-warm the cache with `aws-lambda-go` in the image.
- Why: Removes both latency and a spurious network-dependent failure mode from every build/retry cycle; based on the Go modules cache design (go.dev/ref/mod#module-cache).
- Architecture impact: Local (infra config) | Effort: S | Priority: P2

---

## Open questions

1. **Failure-mode distribution is unmeasured.** `Metrics` records only aggregate `build_error`/`test_error` counts; no per-stage attempt/outcome breakdown exists, so it is unverified whether build failures actually outnumber semantic test failures across f1–f14. Implement [B5] and run the paper set once before re-prioritizing C-items against each other.
2. **Which pipeline config is canonical for the thesis evaluation?** Three materially different chains exist (`default.yaml`, `default.json`, `scripts/summary-pipeline.json` with `summary`+`coder2`). If `summary-pipeline.json` is the target, D-items should prioritize `1-stage-translate-2.md` (same defects as `-1`) and [G2] rises in priority.
3. **Expected-output fixture format is undocumented and inconsistent.** The paper fixtures put the bare Python return object in `output`; `examples/output/2026-07-01_12-56-12/test/f1.json` includes the harness's `{"response": ...}` wrapper *and* is invalidly escaped. Which format is authoritative? [C6] assumes the paper format.
4. **Does the GWDG ChatAI proxy honor `json_schema` response_format?** `chatai.go`'s own comment says enforcement depends on the proxy. If not enforced, [E2] becomes near-P0 for that backend.
5. **Ollama's behavior on unknown `Options` keys across versions** (warn vs. reject) — affects urgency of [E5]; recorded successful runs suggest warn-only for the deployed version.
6. **Branch intent:** current branch is `validator-2` with sibling `validator` — if a validator rework is underway, A1–A3/[B1] should be reconciled with it rather than patched independently.
7. **Network-dependent fixtures** (`f9` t2 fetches jsonplaceholder.typicode.com live): is external network access an accepted part of the test contract, or should those be mocked/marked non-deterministic? Determines whether [A6] alone suffices or those fixtures need rewriting.
