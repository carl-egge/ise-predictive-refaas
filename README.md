<div id="top">

<!-- HEADER STYLE: CLASSIC -->
<div align="center">

<img src="docs/img/logo.png" width="30%" style="position: relative; top: 0; right: 0;" alt="Project Logo"/>


<em>A predictive service for rewriting FaaS Functions into more energy efficient versions.</em>

<!-- BADGES -->
<em>Built with the tools and technologies:</em>

<img src="https://img.shields.io/badge/JSON-000000.svg?style=default&logo=JSON&logoColor=white" alt="JSON">
<img src="https://img.shields.io/badge/Ollama-000000.svg?style=default&logo=Ollama&logoColor=white" alt="Ollama">
<img src="https://img.shields.io/badge/Go-00ADD8.svg?style=default&logo=Go&logoColor=white" alt="Go">

</div>
<br>

---

> Developed in partial fulfillment of the requirements for the degree of Master of Computer Science at the Technical University Berlin.
> Based on the ReFaaS pipeline from ISE at TU Berlin.
>
Original Repository: [ReFaaS](https://github.com/ISE-TU-Berlin/ReFaaS)

---

## Table of Contents

- [Table of Contents](#table-of-contents)
- [Overview](#overview)
  - [Pipeline Overview](#pipeline-overview)
  - [Architecture](#architecture)
  - [API Endpoints](#api-endpoints)
  - [Data Structures](#data-structures)
      - [ConversionRequest](#conversionrequest)
      - [ConverterOptions](#converteroptions)
    - [PipelineFile](#pipelinefile)
  - [Additional Notes](#additional-notes)
- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Usage](#usage)
- [Docker](#docker)
  - [Floci Integration Tests (Optional)](#floci-integration-tests-optional)
- [| `ACADEMIC_CLOUD_ENDPOINT` | `https://chat-ai.academiccloud.de/v1` | Base URL for the Chat AI backend. |](#-academic_cloud_endpoint--httpschat-aiacademicclouddev1--base-url-for-the-chat-ai-backend-)
- [Example Usage](#example-usage)
  - [1. Upload a File](#1-upload-a-file)
  - [2. Check if a Job Exists](#2-check-if-a-job-exists)
  - [3. Download the Converted Package](#3-download-the-converted-package)
  - [4. Stop a Running Job](#4-stop-a-running-job)
  - [5. Retrieve All Metrics](#5-retrieve-all-metrics)
  - [6. Reconfigure the Pipeline](#6-reconfigure-the-pipeline)
- [Acknowledgement](#acknowledgement)
- [License](#license)

---

## Overview

The ReFaaS transforms serverless functions from one language to another (e.g., Python → Go) using a configurable AI-assisted pipeline. Upload a `.zip` file, and retrieve the converted package once processing is complete.

---

### Pipeline Overview
The conversion pipeline consists of several tasks, each with its own retry logic and validation steps. The tasks are executed in a sequence or conditionally based on the results of previous tasks.

An example pipeline configuration is provided in the [Example Usage](#6-reconfigure-the-pipeline) section and in the following figure.

<center>

![Pipeline Overview](./docs/img/pipeline.svg)
</center>

For more details check out [internal/pipeline/pipeline.go](internal/pipeline/pipeline.go) and [internal/pipeline/pipeline_io.go](internal/pipeline/pipeline_io.go). Default task registrations live in [internal/translator/prompts.go](internal/translator/prompts.go) and [internal/builder/builder.go](internal/builder/builder.go).

### Architecture

The codebase follows Standard Go Project Layout with a thin entrypoint and internal packages:

- **cmd/refaas**: service entrypoint.
- **internal/pipeline**: pipeline orchestration, task registry, and config parsing.
- **internal/llmconnector**: LLM client abstractions and provider implementations.
- **internal/translator**: prompt rendering, LLM translation, and response parsing.
- **internal/builder**: build/compile/test stages and validation strategies.
- **internal/inputhandler**: zip input parsing and normalization.
- **internal/outputhandler**: zip output writing and HTTP error reporting.
- **internal/service**: HTTP API and background processing.

### API Endpoints

| Endpoint | Method | Request | Response | Description |
|:---|:---|:---|:---|:---|
| `/` | POST | Multipart form with field `file` (`.zip`, max 50MB) | `201 Created` + Redirect to `/{uuid}`<br/>Errors: `400`, `415`, `500` | Upload a serverless function `.zip` for conversion. |
| `/{uuid}` | HEAD | - | `200 OK` if job exists<br/>`404 Not Found` if job unknown | Check if a submitted conversion job exists. |
| `/{uuid}` | GET | - | `200 OK` + Converted `.zip` file if completed<br/>`406 Not Acceptable` if not completed<br/>`404 Not Found` if unknown<br/>`500 Internal Server Error` on error | Download the converted serverless function package by UUID. |
| `/stop/{uuid}` | POST | - | `202 Accepted` if the job was found and cancellation was requested<br/>`404 Not Found` if the job is unknown<br/>`409 Conflict` if the job already finished | Gracefully stop a running or queued conversion job. |
| `/metrics` | GET | - | `200 OK` + JSON with metrics | Retrieve conversion processing metrics for all jobs. |
| `/reconfigure` | POST | JSON body with `ConverterOptions` | `201 Created` on success<br/>`500 Internal Server Error` on failure | Reconfigure the conversion pipeline at runtime. |

---

### Data Structures

##### ConversionRequest
```json
{
  "id": "string (UUID)",
  "sourcePackage": "DeploymentPackage (optional)",
  "workingPackage": "DeploymentPackage (optional)",
  "metrics": "Metrics (optional)",
  "completed": "boolean"
}
```

##### ConverterOptions
```json
{
  "pipeline": "PipelineFile (optional)",
  "LLMClient": "string",
  "args": { "key": "value" }
}
```


#### PipelineFile
Defines a sequence of tasks for the conversion pipeline.

```json
{
  "options": {
    "model_name": "string",
    "strategy": "string",
    "temperature": "float",
    "top_p": "float",
    "num_ctx": "integer"
  },
  "tasks": [
    {
      "id": "string",
      "task": "string",
      "task_args": { "key": "value" },
      "maxRetryCount": "integer",
      "validation": "string",
      "canApply": "string",
      "recovery": "string",
      "next": ["string"]
    }
  ]
}
```

**Key Elements:**
- `options`: Settings for the LLM model and inference behavior.
- `tasks`: A list of tasks executed sequentially or conditionally, each with retry logic, validation, and recovery tasks.

The embedded default pipeline lives in [internal/pipeline/default.yaml](internal/pipeline/default.yaml). A JSON example is available in default.json at the repository root.

---

### Additional Notes

- **Upload size limit**: Maximum 50MB file size.
- **Accepted format**: Only `.zip` files.
- **Job expiration**: Jobs are deleted **after download** or **server restart**.
- **Job cancellation**: `POST /stop/{uuid}` requests a graceful stop for a running job. Stopped jobs still write final metrics, including partial timing and any collected issues.
- **Concurrency**: A background worker sequentially processes uploaded jobs.
- **Pipeline Config**: The service supports **dynamic reconfiguration** without restarting.

---

## Getting Started

### Prerequisites

This project requires the following dependencies:

- **Programming Language:** Go
- **Package Manager:** Go modules

### Usage

Run the project with:

**Using [go modules](https://golang.org/):**
```sh
go run ./cmd/refaas
```

## Docker

Start the service using Docker Compose. Copy the example env file and update secrets before running:

```sh
cp .env.example .env
# Edit .env and set your keys (e.g. GEMINI_API_KEY)
docker compose up --build
```

The compose file exposes port `8080` on the host. Replace `OLLAMA_API_URL` in `.env` to point to a running Ollama instance if you use the `ollama` backend.

If you need a local Ollama server, enable the optional `ollama` service in `docker-compose.yml` and adjust `OLLAMA_API_URL` accordingly.

### Floci Integration Tests (Optional)

An optional Floci-backed stage can deploy translated Lambdas into a local AWS emulator and validate output plus side effects. See `docs/floci-integration.md` for setup, pipeline configuration, and test case format.


This will start the service running on port 8080. However, for isolation, it is recommended to run the service in a Docker container, see [Docker](#docker) for more details.

**🛠️ Environment Variables**

| Variable | Default | Description |
|:---|:---|:---|
| `OLLAMA_API_URL` | Internal default (`OLLAMA_API_URL`) | URL for connecting to Ollama-compatible LLM APIs. |
| `GEMINI_API_KEY` | `"NOT+SET"` | API key for Gemini LLM. |
| `ACADEMIC_CLOUD_API_KEY` | unset | API key for the AcademicCloud Chat AI backend. |
| `ACADEMIC_CLOUD_ENDPOINT` | `https://chat-ai.academiccloud.de/v1` | Base URL for the Chat AI backend. |
---


## Example Usage

### 1. Upload a File
```bash
curl -F 'file=@path/to/your_function.zip' http://localhost:8080/
```
- On success, will redirect (`201 Created`) to `/UUID` for the submitted job.

---

### 2. Check if a Job Exists
```bash
curl -I http://localhost:8080/<job-uuid>
```
- HTTP `200 OK`: Job exists
- HTTP `404 Not Found`: No such job

---

### 3. Download the Converted Package
```bash
curl -O http://localhost:8080/<job-uuid>
```
- Downloads a `.zip` file if the job is completed.

---

### 4. Stop a Running Job
```bash
curl -X POST http://localhost:8080/stop/<job-uuid>
```
- Requests a graceful cancellation of the submitted job.
- If the job is already running, the current stage is interrupted as soon as it reaches a cancellation point.
- Final metrics are still written for stopped jobs.

---

### 5. Retrieve All Metrics
```bash
curl http://localhost:8080/metrics
```
- Returns a JSON object with timing and issue metrics for each job.

---

### 6. Reconfigure the Pipeline

```bash
curl -X POST -H "Content-Type: application/json" -d '{
  "LLMClient": "ollama",
  "args": {
    "OLLAMA_API_URL": "http://your-ollama-instance:11434"
  },
  "pipeline": {
    "options": {
      "model_name": "qwen2.5-coder:32b",
      "strategy": "json",
      "temperature": 0.1,
      "top_p": 0.8,
      "num_ctx": 32768
    },
    "tasks": [
      {
        "id": "root",
        "task": "cleaner",
        "maxRetryCount": 2,
        "next": ["convert"],
        "validation": "canCompile"
      },
      {
        "id": "convert",
        "task": "coder",
        "task_args": {"reader": "go"},
        "maxRetryCount": 2,
        "validation": "canCompile",
        "next": ["builder"]
      },
      {
        "id": "builder",
        "task": "goBuilder",
        "canApply": "canCompile",
        "recovery": "gollmRecovery",
        "maxRetryCount": 4,
        "next": ["goTester"]
      },
      {
        "id": "gollmRecovery",
        "task": "fixer",
        "canApply": "canCompile",
        "task_args": {"reader": "go"},
        "maxRetryCount": 6
      },
      {
        "id": "goTester",
        "task": "goTester",
        "canApply": "canCompile",
        "maxRetryCount": 5,
        "recovery": "testRecovery"
      },
      {
        "id": "testRecovery",
        "task": "realign",
        "canApply": "canCompile",
        "task_args": {"reader": "go"},
        "maxRetryCount": 3,
        "next": ["testRecoveryBuild"]
      },
      {
        "id": "testRecoveryBuild",
        "canApply": "canCompile",
        "task": "goBuilder",
        "recovery": "gollmRecovery",
        "maxRetryCount": 3
      }
    ]
  }
}' http://localhost:8080/reconfigure
```
- Updates the pipeline configuration at runtime.
- Clears all previously submitted jobs and metrics!

## Acknowledgement

A big thank you goes to Sebastian Werner, the original developer of the ReFaaS tool and a great supervisor of my master thesis.

## License

A license will be picked at a later point of time.

<div align="right">

[![][back-to-top]](#top)

</div>

[back-to-top]: https://img.shields.io/badge/-BACK_TO_TOP-151515?style=flat-square