# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

### Running and Building
- **Run the service**: `go run ./cmd/refaas`
- **Run with Docker Compose**: `docker compose up --build`
- **Run tests**: `go test ./...`
- **Run a specific test**: `go test -v path/to/package/file_test.go`
- **Format code**: `gofmt -w .`

### Pipeline Interaction
- **Upload function**: `curl -F 'file=@path/to/your_function.zip' http://localhost:8080/`
- **Check job status**: `curl -I http://localhost:8080/<job-uuid>`
- **Download result**: `curl -O http://localhost:8080/<job-uuid>`
- **Stop job**: `curl -X POST http://localhost:8080/stop/<job-uuid>`
- **Retrieve metrics**: `curl http://localhost:8080/metrics`
- **Reconfigure pipeline**: `curl -X POST -H "Content-Type: application/json" -d '@config.json' http://localhost:8080/reconfigure`

## Architecture Overview

The project is a predictive service for rewriting FaaS functions into more energy-efficient versions using an AI-assisted pipeline.

### Core Pipeline Logic
The system is pipeline-centric, where a translation run is a sequence of configurable stages:
- **Orchestration**: `internal/pipeline` handles pipeline compilation from YAML, task execution, retries, and recovery links.
- **Translation**: `internal/translator` manages prompt templates, LLM interaction, and response parsing.
- **Validation**: `internal/builder` and `internal/floci` perform build checks, unit tests, and deployed Lambda-style validation.
- **Flow**: Typical flow is `cleaner` $\rightarrow$ `coder` $\rightarrow$ `goBuilder` $\rightarrow$ `goTester`.

### Project Structure
- `cmd/refaas`: Entrypoint.
- `internal/service`: HTTP API, job queue, and background worker.
- `internal/pipeline`: Orchestration and task registry.
- `internal/translator`: Prompt rendering and LLM translation.
- `internal/builder`: Build and test validation.
- `internal/llmconnector`: LLM provider abstractions.
- `internal/domain`: Shared types (`ConversionRequest`, `DeploymentPackage`, `Metrics`).

## Coding Conventions

- **Go Idioms**: Use `PascalCase` for exported symbols and `camelCase` for unexported helpers. Keep package names short and lowercase.
- **Error Handling**: Prefer explicit error returns and wrapping with context: `fmt.Errorf("...: %w", err)`.
- **Logging**: Use `logrus` for informative, stage-local logging.
- **Concurrency & Cancellation**: Propagate `context.Context` through all long-running operations to ensure cancellation reaches LLM calls and build processes.
- **State Management**: Preserve package copies when mutating request state to allow the pipeline to recover from failed translation attempts.
- **Tooling**: Always run `gofmt` before committing.
