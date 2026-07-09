# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Context

ReFaaS converts serverless functions from one language to another (currently Python → Go) using a configurable, LLM-assisted pipeline. A client uploads a `.zip` containing the source function and test fixtures; a background worker runs it through a pipeline of LLM translation and build/test validation stages and the converted package can be downloaded once the job completes.

This is a research codebase (thesis project). The two open problems it's built around: (1) end-to-end validation that translated code is correct for non-trivial, side-effecting workloads, and (2) prediction mechanisms to avoid infeasible or energy-ineffective translation attempts before spending LLM/build time on them. Prediction is not yet implemented anywhere in this repo — don't assume a predictor, scoring model, or related API exists unless you find it in code.

**Verify before claiming.** Always grep for a symbol/converter/endpoint before describing it as existing — `docs/floci.md` in particular is third-party Floci reference documentation, not a description of this repo's own code. Note that `internal/floci` *does* now exist: it is the optional, opt-in Floci-backed Lambda integration testing stage (`flociTester` converter), gated by `ConverterOptions.Floci.Enabled` / `FLOCI_ENABLED`; see `docs/floci-integration.md`. It is a no-op unless explicitly enabled, so it never affects default translation/build/test runs.

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
cp .env.example .env             # then set GEMINI_API_KEY / OLLAMA_API_URL / ACADEMIC_CLOUD_* etc.
docker compose up --build        # builds Dockerfile, starts refaas + ollama on :8080 / :11434
```

A `.env` file in the working directory is also picked up directly by the binary (not just Docker) via `github.com/joho/godotenv/autoload`, imported in `internal/pipeline/defaults.go`.

Helper scripts (`scripts/`):
- `reconfigure.sh <config.json>` — POSTs a JSON `ConverterOptions` body to `/reconfigure` (see `scripts/chatai.json` for an example that switches the runner to the `chatai` LLM client).
- `store-metrics.sh` — GETs `/metrics` and saves the JSON into `examples/metrics/`.

## Architecture

The system is pipeline-centric: a conversion run is a sequence of configurable, retryable stages wired together at startup (or at runtime via `/reconfigure`), not a single LLM call.

```
cmd/refaas/main.go
  -> internal/service       HTTP API, job queue, background worker, metrics, reconfigure
       -> internal/pipeline Runner: holds compiled Pipeline + LLM Client, executes ConversionRequests
            -> internal/translator   LLM-backed Converters (cleaner/coder/fixer/realign)
            -> internal/builder      build + test Converters/validators
            -> internal/llmconnector LLM Client implementations (ollama, gemini, chatai)
       -> internal/inputhandler   .zip -> domain.DeploymentPackage
       -> internal/outputhandler  domain.DeploymentPackage -> .zip / HTTP errors
  -> internal/domain         shared types: ConversionRequest, DeploymentPackage, TestFile, Metrics
```

### Pipeline compilation and execution (`internal/pipeline`)

- `registry.go` defines the `Converter` interface (`Apply(*Runner, *domain.ConversionRequest) error`) and a global `converterFactories` map keyed by string name, populated via `RegisterConverterFactory` from `init()` functions in `internal/translator` and `internal/builder`. `MakeConverter(key, args)` looks a factory up by name — this is how YAML task names (`"cleaner"`, `"goBuilder"`, `"canCompile"`, etc.) resolve to concrete `Converter` implementations.
- `pipeline_io.go` parses a `PipelineFile` (YAML: top-level `options` + `tasks` list) into `ConversionTaskStub`s, then `compilePipeline` resolves each stub's `task`/`canApply`/`validation`/`recovery`/`next` references into a single linked graph of `ConversionTask` nodes anchored at the task with `id: "root"`. Task-level `task_args` are merged over the pipeline-level `options` before constructing each converter, so `model_name`/`strategy`/`temperature`/etc. cascade down unless overridden per task.
- `pipeline.go` (`Pipeline.Execute` / `executeTask`) walks the task graph: applies `CanApply` as a precondition, runs `Execute` with up to `MaxRetryCount` attempts (snapshotting/restoring `req.WorkingPackage` around each attempt), routes to `OnFailure` (the `recovery` task) on failure before retrying, then runs `Validation` and — on validation failure — retries the *same* task rather than advancing. On success it recurses into every task in `Next`. A `recover()` wraps the whole execution so a panic in any converter becomes a returned error instead of crashing the worker.
- `runner.go`'s `Runner` bundles the compiled `Pipeline`, an `llmconnector.Client`, and a scratch `workingDir` used by the build/test stages. `ConverterOptions` embeds `PipelineFile` directly (anonymous field, `Options`/`Tasks` promoted to the top level of its JSON/YAML shape) rather than nesting it under a `pipeline:` key — so a `/reconfigure` body looks like `{"LLMClient", "args", "options", "tasks"}`, not `{"LLMClient", "args", "pipeline": {"options", "tasks"}}`. `MakeCodeConverter`/`Reconfigure` resolve the LLM client by `ConverterOptions.LLMClient` via `llmconnector.Factories`, and resolve the pipeline from (in priority order) `CompiledPipeline`, the embedded `PipelineFile` (when `len(Tasks) > 0`), or the embedded `default.yaml`.
- `default.yaml` is embedded via `go:embed` and is the pipeline used at service startup; the README's `/reconfigure` example shows the fuller, more typical chain (`cleaner` → `coder` → `goBuilder`/`gollmRecovery` → `goTester` → `realign`/`testRecoveryBuild`).
- `"canCompile"` (`CanCompileConverter`) is the standard `canApply` precondition gating build/test stages — it just checks that source/working packages and root files exist and test-file counts match; it is not a build check itself.

### Translation stages (`internal/translator`)

- `prompts.go` embeds five markdown prompt templates (`prompts/0-stage-document.md` cleanup, `0-stage-summarize.md` intent summary, `1-stage-translate.md` translate, `2-stage-repair.md` build-fix, `3-stage-align.md` post-test realignment) and registers them as the `cleaner`/`summary`/`coder`/`fixer`/`realign` converter factories, all backed by the same `LLMConverter`. `prompts/1-stage-translate-1.md` and `1-stage-translate-2.md` are unwired draft variants — see [[prompt-stage-variants]] memory.
- `translator.go`'s `LLMConverter` has two output **modes**, set per task via `task_args.mode` (default `"package"`): `"package"` replaces `req.WorkingPackage` with whatever `PackageReader` parses out of the LLM response (the original behavior, used by `cleaner`/`coder`/`fixer`/`realign`); `"metadata"` instead parses the response as a flat JSON object and merges it into `req.Metadata` (a `map[string]string` on `domain.ConversionRequest`), leaving `WorkingPackage` untouched — used by `summary` so a one-sentence intent doesn't clobber the code being translated.
- `LLMConverter.Apply` renders the prompt template with the current working code, the last recorded error, the original source, and the first test's input/output as template vars — **plus every key currently in `req.Metadata` promoted to a top-level var** (so a later stage's prompt can reference `{{ .intent }}` directly; the fixed vars always take precedence over a same-named metadata key). It then calls `runner.LLMClient().Prepare(cc.taskParams)`, `InvokeLLM`, accumulates returned `domain.Metrics`, and logs the raw prompt/response via `llmconnector.LogResponse` into `chatlogs/`. `LLMConverter.taskParams` is this task's merged `options`+`task_args` (minus `prompt`/`reader`/`mode`) — distinct from `ConverterOptions.Args`; see the vocabulary table in [README.md](README.md#converteroptions) if `args` vs `options` vs `task_args` is unclear.
- `readers.go` / `ReaderFactory` selects how the raw LLM text is turned back into a `DeploymentPackage` in package mode (`"go"` → `GoJsonOllamaReader`, default → `BasicLLMDeploymentReader`); chosen per-task via `task_args.reader`. Metadata mode reuses `readers.go`'s `JsonCodeBlockReader` directly rather than going through a `PackageReader` (that interface only knows how to produce a `*DeploymentPackage`, not a side-channel value).
- `prompts.go`'s `NewSummaryConverter` defaults `task_args.mode` to `"metadata"` and `task_args.output_keys` to a single `intent` field if the pipeline config doesn't already set them — so `task: "summary"` works with zero extra config, but both remain overridable.

### LLM connectors (`internal/llmconnector`)

- `client.go` defines the `Client` interface (`ClientName`, `Configure(connectorArgs)`, `Prepare(taskParams)`, `InvokeLLM(ctx, buf)`) and a `Factories` registry populated via `RegisterFactory` from each connector's `init()`. `ConverterOptions.LLMClient` (`"ollama"`, `"gemini"`, or `"chatai"`) selects which factory `pipeline.MakeCodeConverter`/`Reconfigure` invoke. The two method params are intentionally named differently because they're different things: `connectorArgs` is `ConverterOptions.Args` (connector wiring, set once); `taskParams` is the merged `options`+`task_args` for one task (re-evaluated on every execution, including retries).
- All three connectors (`ollama.go`, `gemini.go`, `chatai.go`) follow the same shape: `Configure(connectorArgs)` builds and caches the expensive client/transport object on the struct guarded by a `client == nil` check (so it's built once, not per-call), `Prepare(taskParams)` applies per-task overrides (model name, temperature, etc. — note Ollama/chatai read `model_name`, but Gemini reads its own `GEMINI_MODEL` key, an existing inconsistency) into a `RequestParams` field, and `InvokeLLM` reuses the cached client rather than constructing a new one per request.
- `outputschema.go`'s `OutputSchema` (`map[string]OutputField`, each field defaulting to a nullable string) is the shared, connector-neutral representation of `task_args.output_keys`, parsed by `ParseOutputSchema` and set on every connector by its own `Prepare`. Each `InvokeLLM` builds its own SDK-specific structured-output request from it, falling back to that connector's original fixed schema when no task-specific `output_keys` was given (Ollama → the generic `llmOutputSchema` in `schema.go`; Gemini → the hardcoded `main.go`/`go.mod`/`main.py` properties; ChatAI → plain `response_format: json_object` with no schema). This is what lets a task like `summary` request a differently-shaped response (`{"intent": "..."}`) without changing what `cleaner`/`coder`/`fixer`/`realign` get by default.
- `chatai.go`'s `ChatAIInvocationClient` talks to the GWDG/AcademicCloud "Chat AI" service, an OpenAI-compatible `/chat/completions` API, over plain `net/http` (no SDK dependency). It defaults `response_format` to `{"type":"json_object"}` and `max_tokens` to `2<<14` unless the task already set them, since the readers in `internal/translator` expect the entire response body to be a parseable JSON object.
- `schema.go`'s `llmOutputSchema` is an Ollama-specific structured-output schema passed via `Format` in `ollama.go`'s `Generate` call; it has no equivalent for Gemini/ChatAI today.

### Build/test stages (`internal/builder`)

- `builder.go`'s `GolangBuilder` (`goBuilder`) writes `req.WorkingPackage` plus an embedded `test_handler.txt` harness into a fresh temp dir (`runner.SetWorkingDir`), runs the package's `BuildCmd` list, and on a known Go-modules failure mode (`"unknown revision"`) falls back to regenerating `go.mod` via `go mod init`/`go mod tidy` before retrying the build once.
- `validator.go`'s `GoPackageTester` (`goTester`) runs `go run .` once per `TestFile` against the built working directory, feeding `TestFile.Input` on stdin and comparing stdout against `TestFile.Output` through a pluggable `ValidationStrategy` — plain string-similarity (`SimilarityValidation`, overlap-coefficient threshold) by default, or a JSON-structure-aware comparison (`JsonAwareSimilarityValidation`, selected via `task_args.strategy: "json"`) that recursively diffs JSON objects/arrays and falls back to string similarity for non-JSON leaves. `TestFile.UndeterministicResults` relaxes the similarity threshold for non-deterministic outputs.

### Service layer (`internal/service`)

- Single `ConverterService` holds one `pipeline.Runner`, a buffered job channel of `*queuedConversion` (`requestQueue`, capacity 100), in-memory `results`/`metrics` maps, and a `cancels map[uuid.UUID]context.CancelFunc` — all keyed by job UUID, guarded by one `sync.RWMutex`. There is exactly one background worker goroutine (`Start`) draining the queue sequentially — conversions are not processed concurrently.
- Routes (`mux.Router`): `POST /` upload (multipart `file`, ≤50MB, must end in `.zip`) → enqueues a `queuedConversion{ctx, request}` (the `ctx` is a fresh `context.WithCancel` whose `cancel` is stored in `cancels`) and returns the job UUID in the body; `HEAD|GET /{uuid}` → job existence check or download-and-delete-on-GET of the resulting `.zip`; `POST /stop/{uuid}` → looks up and calls the job's `cancel`, returning `202`/`404`; `GET /metrics` → dump of the in-memory metrics map; `POST /reconfigure` → decodes a `pipeline.ConverterOptions` body, swaps the runner's LLM client/pipeline, and **wipes all existing `results`/`metrics`** (but not `cancels`).
- Cancellation flow: `Start` passes the job's `ctx` into `Runner.Convert(ctx, req)`, which assigns it to `Runner`'s embedded `context.Context` for the duration of that call. Since `*Runner` itself satisfies `context.Context` (via embedding) and is threaded through every `Converter.Apply(runner, req)` call, anywhere downstream code already used `runner` as a context (`llmconnector` `InvokeLLM` calls, `validator.go`'s `exec.CommandContext`, `builder.go`'s build subprocess) observes the cancellation. `pipeline.go`'s `executeTask` also checks `runner.Err()` at task entry and after every failed `Execute`/`Validation` attempt, so a cancelled job aborts immediately instead of exhausting retries or invoking a recovery task (which could otherwise spend more LLM tokens after the job was told to stop). `Start` calls/clears the job's `cancel` once `Convert` returns, regardless of outcome.
- `service.go` itself no longer reads environment variables directly — it just calls `pipeline.MakeCodeConverter(&pipeline.ConverterOptions{})`. Environment defaults are resolved in `internal/pipeline/defaults.go`'s `envDefaults()` (`OLLAMA_API_URL`, `GEMINI_API_KEY`, `ACADEMIC_CLOUD_ENDPOINT`, `ACADEMIC_CLOUD_API_KEY`, `APP_PORT`, each with a hardcoded fallback) and merged into `ConverterOptions.Args` by `setDefaults()` in `runner.go`, which both `MakeCodeConverter` and `Reconfigure` call — so env vars are re-read fresh on every startup and every `/reconfigure`, not just once. A `.env` file is loaded automatically via the `godotenv/autoload` blank import.

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
