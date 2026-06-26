# Main Agent Guide

## Context

This repository implements a configurable Go-based pipeline that translates serverless functions into more energy-efficient implementations with LLM assistance. The thesis focus is practical automated green code translation: improving end-to-end validation so translated code is correct for more complex, side-effecting workloads, and adding prediction mechanisms that can avoid infeasible or energy-ineffective translation attempts.

The research framing is that automated translation can reduce energy debt from legacy implementations, but only when the translated workload and pipeline overhead justify the effort. The current codebase supports that evaluation by assembling prompt-driven translation stages, build and test validation, and optional Floci-backed integration checks.

## Project Structure

- `cmd/refaas`: Thin executable entrypoint that wires packages and starts the HTTP service.
- `internal/service`: HTTP API, job queue, background worker, cancellation, metrics, and runtime reconfiguration.
- `internal/pipeline`: Core orchestration layer. Defines converter registration, pipeline compilation from YAML, task execution, retries, validation, and recovery links.
- `internal/translator`: Prompt templates and LLM-backed translation converters.
- `internal/builder`: Build and test validation stages for translated Go packages.
- `internal/floci`: Optional Floci integration stages for deployed Lambda-style validation.
- `internal/inputhandler`: Reads and normalizes uploaded zip input packages.
- `internal/outputhandler`: Writes converted output packages and handles output-side errors.
- `internal/llmconnector`: Provider-specific LLM clients and common client interfaces.
- `internal/domain`: Shared request, package, test, and metrics types.
- `examples`: Sample inputs, outputs, and test fixtures.
- `data`: Local data artifacts and generated Lambda bootstrap content.
- `docs`: Architecture notes, Floci integration docs, and images.
- `scripts`: Helper scripts for reconfiguration and metrics storage.

## Key Components and Locations

- Pipeline compilation and execution:
  - `internal/pipeline/pipeline_io.go`: Reads `PipelineFile` YAML, resolves converter factories, and links tasks.
  - `internal/pipeline/pipeline.go`: Executes tasks, handles retries, validation, recovery, cancellation, and metrics timing.
  - `internal/pipeline/registry.go`: Converter factory registration and the `canCompile` precondition checker.
  - `internal/pipeline/default.yaml`: Embedded default pipeline configuration used at startup.

- Translation and prompt stages:
  - `internal/translator/prompts.go`: Registers translation-related converters and embeds the stage prompts.
  - `internal/translator/translator.go`: Builds LLM prompts, invokes the selected client, parses the response into a deployment package, and records LLM metrics.
  - `internal/translator/readers.go`: Response readers for different output formats.

- Validation and build stages:
  - `internal/builder/builder.go`: Writes the working package to a temp directory and runs `go build`-style checks.
  - `internal/builder/validator.go`: Runs package tests, checks outputs, and records build/test failures.
  - Validation strategies include similarity-based checks and JSON-aware matching for structured output.

- Optional deployment validation:
  - `internal/floci/pipeline.go`: Optional Floci tester stage.
  - `internal/floci/build.go`, `internal/floci/runner.go`, `internal/floci/checkers_*.go`: Local AWS-emulator integration and deployment-side checks.

- Service and request handling:
  - `internal/service/service.go`: HTTP endpoints, request queue, worker loop, stop handling, metrics collection, and reconfigure support.
  - `internal/inputhandler/reader.go`: Upload parsing and normalization.
  - `internal/outputhandler/writer.go`: Package output writing and error reporting.

- Shared domain model:
  - `internal/domain/types.go`: `ConversionRequest`, `DeploymentPackage`, `TestFile`, and `Metrics`.

## Coding Conventions

- Follow idiomatic Go naming: exported symbols use `PascalCase`, unexported helpers use `camelCase`, and package names stay short and lowercase.
- Prefer explicit error returns and wrapping with context; use `fmt.Errorf("...: %w", err)` or descriptive error messages rather than silent failure.
- Use `logrus` for runtime logging and keep logs informative but local to the failing stage.
- Propagate `context.Context` through long-running work so cancellation reaches build, test, and LLM calls.
- Keep pipeline stages small and composable; most behavior is wired through converter factories rather than hard-coded in one place.
- Preserve package copies when mutating request state; the pipeline uses snapshots to recover from failed translation attempts.
- Run `gofmt` on any Go files you change and prefer minimal, targeted edits over broad refactors.
- Verify behavior in code before claiming it exists.
- Do not hallucinate prediction models, validation stages, or API endpoints that are not present in the repository.
- Don't invent anything, focus on the given task and ensuring correct working code.

## Domain Knowledge

- The project is pipeline-centric: a translation run is a sequence of configurable stages, not a single prompt.
- The default flow is typically `cleaner` → `coder` → `goBuilder` → `goTester`, with optional recovery and Floci stages.
- Prompt-based translation is split across multiple stages so the system can clean the source, translate it, repair build failures, and realign outputs after testing.
- Validation is central to the thesis because translation quality is only useful if the result compiles and matches expected behavior.
- The important practical challenge is that LLM translation can succeed syntactically but still fail semantically, especially for side-effecting or multi-file serverless functions.
- The second thesis challenge, prediction, aims to prevent wasted translation work when a candidate is unlikely to produce an energy-saving or correct result; in this repository that goal is not yet realized as a standalone predictor.
- When extending the system, prioritize checks that measure correctness, reproducibility, and failure recovery over adding more prompt complexity without validation.