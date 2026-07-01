# Floci integration testing stage

The `internal/floci` package adds an **optional** pipeline stage that validates a
translated function as a real Lambda running inside a local
[Floci](https://floci.io) AWS emulator. Unlike the built-in `goTester` (which
runs `go run .` and compares stdout), the Floci stage exercises the function
through the actual Lambda runtime and can assert on **AWS side effects** — S3
objects, DynamoDB items, and anything else you add a checker for.

The feature is fully opt-in. With Floci disabled the stage is a no-op and the
existing translation/build/test behavior is unchanged.

## How it works

```
flociTester stage
  1. package   translated WorkingPackage -> provided.al2 "bootstrap" ZIP
  2. deploy    CreateFunction / UpdateFunctionCode in Floci, wait until Active
  3. for each test case:
       setup        run declarative setup actions (create bucket/table, seed data)
       invoke       call the Lambda with the case payload
       output       validate the response (tolerant JSON-subset match)
       side effects run declarative checkers against Floci (S3, DynamoDB, ...)
```

Packaging uses a Go custom runtime: the translated code only needs to expose a
`handle` function (the same one `goTester` calls); the packager injects
`lambda.Start(handle)` and builds a static Linux `bootstrap` binary with the
`lambda.norpc` build tag, zipped at the archive root.

## Enabling it

Two things must be true for the stage to do work:

1. **Backend enabled** — `floci.enabled = true` in the `ConverterOptions`
   (via `/reconfigure`) or `FLOCI_ENABLED=true` in the environment. This starts
   the backend "runner": it records the endpoint/region and pings Floci.
2. **Stage present** — a task with `task: "flociTester"` in the pipeline.

If the stage is present but the backend is disabled, it logs and returns
success without doing anything — so you can leave it in a pipeline and toggle it
with a single flag.

### Start Floci with Docker Compose

Floci runs as an extra, profile-gated service so it never starts unless asked:

```bash
docker compose --profile floci up --build
```

The compose service is configured per Floci's requirements:

- mounts the Docker socket (`/var/run/docker.sock`) — Lambda uses real
  containers;
- mounts a writable data dir (`./data/floci:/app/data`) — Floci extracts
  deployment packages there (a non-writable dir causes
  *"Failed to extract deployment package"*);
- runs as `root` to avoid permission failures on that mount;
- joins the `refaas-net` compose network and sets the Lambda Docker network to
  it, so Lambda runtime containers can reach Floci's runtime API (otherwise
  invocations time out with `Function.TimedOut`).

> On a native Linux host with UFW, also allow the docker bridge so Lambda
> containers can reach the host: `sudo ufw allow in on docker0`. Docker Desktop
> and Floci-in-Docker do not need this.

### Run the optional validation stage

Point the service at a pipeline that includes the stage and enables the backend.
The bundled example ([`examples/floci/pipeline.json`](../examples/floci/pipeline.json))
appends `flociIntegration` after `goTester` and sets `floci.enabled`:

```bash
docker compose --profile floci up --build           # start refaas + ollama + floci
./scripts/reconfigure.sh examples/floci/pipeline.json
# then upload a .zip as usual:
curl -F file=@examples/input/addition.zip http://localhost:8080/
```

## Defining test cases

Test cases are plain JSON files. Point a stage at a directory with
`task_args.test_cases_dir`; every `*.json` file in it is loaded (lexically). If
no directory is configured, the stage derives basic cases from the package's own
black-box fixtures (payload/expected output only, no side effects).

```json
{
  "name": "create-user",
  "description": "Stores a user in DynamoDB and writes an audit object to S3.",
  "payload": { "id": "u1", "name": "Ada Lovelace", "email": "ada@example.com" },
  "expectedOutput": { "status": "ok", "id": "u1" },
  "setup": [
    { "type": "s3.bucket", "bucket": "audit" },
    { "type": "dynamodb.table", "table": "Users", "hashKey": "id" }
  ],
  "sideEffects": [
    { "type": "s3.objectExists", "bucket": "audit", "key": "u1.json" },
    { "type": "s3.objectContains", "bucket": "audit", "key": "u1.json", "substring": "Ada Lovelace" },
    { "type": "dynamodb.itemExists", "table": "Users", "key": { "id": "u1" }, "attributes": { "name": "Ada Lovelace" } }
  ]
}
```

| Field            | Meaning                                                                 |
|------------------|-------------------------------------------------------------------------|
| `name`           | Case name (defaults to the file stem).                                  |
| `description`    | Free text.                                                              |
| `payload`        | Raw event passed to the Lambda.                                         |
| `expectedOutput` | Optional. Matched against the response as a tolerant **JSON subset** — extra fields are ignored, formatting is irrelevant. Omit to assert only on side effects. |
| `setup`          | Declarative actions run **before** invocation.                          |
| `sideEffects`    | Declarative assertions checked **after** invocation.                    |

### Built-in setup actions

| `type`            | Parameters                          | Effect                              |
|-------------------|-------------------------------------|-------------------------------------|
| `s3.bucket`       | `bucket`                            | Create bucket (idempotent).         |
| `s3.object`       | `bucket`, `key`, `body`             | Seed an object.                     |
| `dynamodb.table`  | `table`, `hashKey` (default `id`)   | Create a PAY_PER_REQUEST table.     |
| `dynamodb.item`   | `table`, `item`                     | Seed an item.                       |

### Built-in side-effect checkers

| `type`                 | Parameters                          | Asserts                                  |
|------------------------|-------------------------------------|------------------------------------------|
| `s3.objectExists`      | `bucket`, `key`                     | Object exists.                           |
| `s3.objectContains`    | `bucket`, `key`, `substring`        | Object body contains the substring.      |
| `dynamodb.itemExists`  | `table`, `key`, `attributes` (opt.) | Item exists; given attributes match.     |

## Adding a new assertion checker

Checkers and setup actions live in their own registries, so adding one does not
touch the runner. Register a `CheckerFunc` (or `SetupFunc`) in an `init()`:

```go
// internal/floci/checkers_sqs.go
func init() {
    RegisterChecker("sqs.queueHasMessage", CheckerFunc(checkSQSMessage))
}

func checkSQSMessage(ctx context.Context, c *Clients, spec json.RawMessage) error {
    var s struct {
        QueueURL  string `json:"queueUrl"`
        Substring string `json:"substring"`
    }
    if err := json.Unmarshal(spec, &s); err != nil {
        return err
    }
    // ... use an SQS client (add one to Clients) to receive and assert ...
    return nil
}
```

Then reference it from a test case: `{ "type": "sqs.queueHasMessage", ... }`.
Unregistered types fail loudly with the list of registered checkers, so typos
are easy to spot.

## Error reporting

The stage emits a clear error for each failure mode:

- **Floci unavailable** — a reachability ping fails before any deploy.
- **Lambda deploy failed** — create/update or the Active wait returns an error
  (including a `Failed` state reason).
- **Runtime timeout** — surfaced from the Lambda invoke / `Function.TimedOut`.
- **Unregistered checker/setup** — names the missing type and lists known ones.

Per-case failures are logged and accumulated onto the `ConversionRequest`
(`req.AddError`), and the stage returns a `domain.TestingError` summarizing how
many cases failed.
