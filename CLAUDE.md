# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Context

ReFaaS converts serverless functions from one language to another (currently Python → Go) using a configurable, LLM-assisted pipeline. A client uploads a `.zip` containing the source function and test fixtures; a background worker runs it through a pipeline of LLM translation and build/test validation stages and the converted package can be downloaded once the job completes.

This is a research codebase (thesis project). The two open problems it's built around: (1) end-to-end validation that translated code is correct for non-trivial, side-effecting workloads, and (2) prediction mechanisms to avoid infeasible or energy-ineffective translation attempts before spending LLM/build time on them. Prediction is not yet implemented anywhere in this repo — don't assume a predictor, scoring model, or related API exists unless you find it in code.

**Verify before claiming.** Several things referenced in docs/config are not implemented in code: `internal/floci` (Floci/AWS-emulator integration) does not exist as a package — `docs/floci.md` is just third-party reference documentation, not a description of code in this repo. Similarly the `chatai`/AcademicCloud LLM backend referenced in `.env.example` and `scripts/chatai.json` has no corresponding factory in `internal/llmconnector` — only `ollama` and `gemini` are registered (`internal/llmconnector/ollama.go`, `internal/llmconnector/gemini.go`). Always grep for a symbol/converter/endpoint before describing it as existing.

There are currently no `*_test.go` files in the repository — no automated Go test suite exists yet.

## Commands

```sh
go build ./...                 # build everything
go run ./cmd/refaas             # run the service locally (listens on :8080)
gofmt -l .                      # check formatting; run `gofmt -w <file>` on changed files
go vet ./...
```

There is no Makefile, lint config, or CI workflow in this repo, and no test suite to run (`go test ./...` will currently report "no test files"). If you add tests, place them as standard `_test.go` files next to the package they cover and run with `go test ./...` or `go test ./internal/<pkg>/...`.

Docker:
```sh
cp .env.example .env             # then set GEMINI_API_KEY / OLLAMA_API_URL etc.
docker compose up --build        # builds Dockerfile, starts refaas + ollama on :8080 / :11434
```

Helper scripts (`scripts/`):
- `reconfigure.sh <config.json>` — POSTs a JSON `ConverterOptions` body to `/reconfigure` (see `scripts/chatai.json` for a config shape example — note its `LLMClient: "chatai"` is not actually a registered backend, treat it as a template only).
- `store-metrics.sh` — GETs `/metrics` and saves the JSON into `examples/metrics/`.

## Architecture

The system is pipeline-centric: a conversion run is a sequence of configurable, retryable stages wired together at startup (or at runtime via `/reconfigure`), not a single LLM call.

```
cmd/refaas/main.go
  -> internal/service       HTTP API, job queue, background worker, metrics, reconfigure
       -> internal/pipeline Runner: holds compiled Pipeline + LLM Client, executes ConversionRequests
            -> internal/translator   LLM-backed Converters (cleaner/coder/fixer/realign)
            -> internal/builder      build + test Converters/validators
            -> internal/llmconnector LLM Client implementations (ollama, gemini)
       -> internal/inputhandler   .zip -> domain.DeploymentPackage
       -> internal/outputhandler  domain.DeploymentPackage -> .zip / HTTP errors
  -> internal/domain         shared types: ConversionRequest, DeploymentPackage, TestFile, Metrics
```

### Pipeline compilation and execution (`internal/pipeline`)

- `registry.go` defines the `Converter` interface (`Apply(*Runner, *domain.ConversionRequest) error`) and a global `converterFactories` map keyed by string name, populated via `RegisterConverterFactory` from `init()` functions in `internal/translator` and `internal/builder`. `MakeConverter(key, args)` looks a factory up by name — this is how YAML task names (`"cleaner"`, `"goBuilder"`, `"canCompile"`, etc.) resolve to concrete `Converter` implementations.
- `pipeline_io.go` parses a `PipelineFile` (YAML: top-level `options` + `tasks` list) into `ConversionTaskStub`s, then `compilePipeline` resolves each stub's `task`/`canApply`/`validation`/`recovery`/`next` references into a single linked graph of `ConversionTask` nodes anchored at the task with `id: "root"`. Task-level `task_args` are merged over the pipeline-level `options` before constructing each converter, so `model_name`/`strategy`/`temperature`/etc. cascade down unless overridden per task.
- `pipeline.go` (`Pipeline.Execute` / `executeTask`) walks the task graph: applies `CanApply` as a precondition, runs `Execute` with up to `MaxRetryCount` attempts (snapshotting/restoring `req.WorkingPackage` around each attempt), routes to `OnFailure` (the `recovery` task) on failure before retrying, then runs `Validation` and — on validation failure — retries the *same* task rather than advancing. On success it recurses into every task in `Next`. A `recover()` wraps the whole execution so a panic in any converter becomes a returned error instead of crashing the worker.
- `runner.go`'s `Runner` bundles the compiled `Pipeline`, an `llmconnector.Client`, and a scratch `workingDir` used by the build/test stages. `MakeCodeConverter`/`Reconfigure` resolve the LLM client by `ConverterOptions.LLMClient` via `llmconnector.Factories`, and resolve the pipeline from (in priority order) `CompiledPipeline`, `Pipeline` YAML struct, or the embedded `default.yaml`.
- `default.yaml` is embedded via `go:embed` and is the pipeline used at service startup; the README's `/reconfigure` example shows the fuller, more typical chain (`cleaner` → `coder` → `goBuilder`/`gollmRecovery` → `goTester` → `realign`/`testRecoveryBuild`).
- `"canCompile"` (`CanCompileConverter`) is the standard `canApply` precondition gating build/test stages — it just checks that source/working packages and root files exist and test-file counts match; it is not a build check itself.

### Translation stages (`internal/translator`)

- `prompts.go` embeds four markdown prompt templates (`prompts/stage-zero.md` cleanup, `stage-one.md` translate, `stage-two.md` build-fix, `stage-three.md` post-test realignment) and registers them as the `cleaner`/`coder`/`fixer`/`realign` converter factories, all backed by the same `LLMConverter`.
- `translator.go`'s `LLMConverter.Apply` renders the prompt template (with the current working code, the last recorded error, the original source, and the first test's input/output as template vars), calls `runner.LLMClient().Prepare(args)` then `InvokeLLM`, accumulates returned `domain.Metrics` onto the request, logs the raw prompt/response via `llmconnector.LogResponse` into `chatlogs/`, and replaces `req.WorkingPackage` with whatever `PackageReader` parses out of the LLM response.
- `readers.go` / `ReaderFactory` selects how the raw LLM text is turned back into a `DeploymentPackage` (`"go"` → `GoJsonOllamaReader`, `"deepseek"` → `GoDeepSeekOllamaReader`, default → `BasicLLMDeploymentReader`); chosen per-task via `task_args.reader` in the pipeline YAML.

### Build/test stages (`internal/builder`)

- `builder.go`'s `GolangBuilder` (`goBuilder`) writes `req.WorkingPackage` plus an embedded `test_handler.txt` harness into a fresh temp dir (`runner.SetWorkingDir`), runs the package's `BuildCmd` list, and on a known Go-modules failure mode (`"unknown revision"`) falls back to regenerating `go.mod` via `go mod init`/`go mod tidy` before retrying the build once.
- `validator.go`'s `GoPackageTester` (`goTester`) runs `go run .` once per `TestFile` against the built working directory, feeding `TestFile.Input` on stdin and comparing stdout against `TestFile.Output` through a pluggable `ValidationStrategy` — plain string-similarity (`SimilarityValidation`, overlap-coefficient threshold) by default, or a JSON-structure-aware comparison (`JsonAwareSimilarityValidation`, selected via `task_args.strategy: "json"`) that recursively diffs JSON objects/arrays and falls back to string similarity for non-JSON leaves. `TestFile.UndeterministicResults` relaxes the similarity threshold for non-deterministic outputs.

### Service layer (`internal/service`)

- Single `ConverterService` holds one `pipeline.Runner`, a buffered job channel (`requestQueue`, capacity 100), and in-memory `results`/`metrics` maps keyed by job UUID, guarded by one `sync.RWMutex`. There is exactly one background worker goroutine (`Start`) draining the queue sequentially — conversions are not processed concurrently.
- Routes (`mux.Router`): `POST /` upload (multipart `file`, ≤50MB, must end in `.zip`) → enqueues and 201-redirects to `/{uuid}`; `HEAD|GET /{uuid}` → job existence check or download-and-delete-on-GET of the resulting `.zip`; `GET /metrics` → dump of the in-memory metrics map; `POST /reconfigure` → decodes a `pipeline.ConverterOptions` body, swaps the runner's LLM client/pipeline, and **wipes all existing `results`/`metrics`**.
- Env vars consumed directly in `service.go`: `OLLAMA_API_URL` (default `http://localhost:11434`), `GEMINI_API_KEY` (default sentinel `"NOT+SET"`).

### Domain types (`internal/domain/types.go`)

- `ConversionRequest`: carries `SourcePackage` (immutable original) and `WorkingPackage` (mutated in place by each pipeline stage), accumulated `errs` (append-only via `AddError`, read via `Errors()`/`LastError()`), and `Metrics`.
- `DeploymentPackage`: `RootFile` (main source as a string) + `TestFiles`/`BuildFiles` (filename → content maps) + `BuildCmd` + `Env`. `Copy()` produces a snapshot used by the pipeline's retry/recovery logic to roll back a corrupted working package.
- `TestFile` (JSON-decoded from a `DeploymentPackage.TestFiles` entry): `Input`/`Output` fixtures, `Env` overrides, `Services` (declared but not consumed anywhere yet — no service mocking/deployment is implemented), `UndeterministicResults` flag.
- `Metrics.AddMetric` aggregates timing/token-count fields across stages; `StartTime`/`EndTime`/`TotalTime` track wall-clock per request.

## Conventions

- Idiomatic Go naming (`PascalCase` exported, `camelCase` unexported); short lowercase package names.
- Errors are returned and wrapped (`fmt.Errorf("...: %w", err)`) rather than panicking or silently swallowed; domain-specific error types (`domain.CompilationError`, `domain.TestingError`, `domain.LLMError`) carry stage-specific context.
- `logrus` (`log "github.com/sirupsen/logrus"`) for logging; keep new logs scoped to the failing stage, matching the existing `Debugf`/`Errorf` usage.
- `context.Context` is propagated through `Runner` (which embeds `context.Context` directly) so cancellation reaches LLM calls and subprocess execution (`exec.CommandContext`).
- New pipeline behavior should be added as a new `Converter` registered via `RegisterConverterFactory` in an `init()`, not hard-coded into `pipeline.go` — that's how `cleaner`/`coder`/`goBuilder`/`goTester`/etc. are all wired in today.
- Run `gofmt` on any changed Go files; prefer small, targeted diffs over broad refactors.
- Do not invent prediction models, validation stages, converters, or API endpoints that aren't present in the code — this repo's own contributor guide (`AGENTS.md`) calls this out explicitly, and it matters because the thesis evaluation depends on the README/docs accurately reflecting what's implemented.
