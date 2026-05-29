# Floci Integration Tests

This repository can optionally validate translated Lambda functions against a local Floci emulator. The Floci stage deploys the translated Go Lambda, invokes it with test payloads, validates the response, and checks AWS side effects (S3 + DynamoDB).

## Start Floci (Docker Compose)

Enable the Floci profile so the emulator starts alongside the service:

```bash
docker compose --profile floci up -d
```

Notes for Floci:
- The container mounts the Docker socket so Lambda and other container-backed services can start real containers.
- The data directory is mounted and run as root to avoid extraction permission errors.
- The Lambda Docker network is pinned to the compose network name so runtime containers can reach Floci's runtime API.

## Enable the Floci Stage

The embedded pipeline includes a `flociTester` stage, but it is disabled by default. Turn it on by setting `floci_enabled` to `true` in your pipeline options:

```yaml
options:
  floci_enabled: true
  floci_endpoint: "http://localhost:4566"
  floci_region: "us-east-1"
  floci_function_name: "refaas-translated"
```

When enabled, the stage scans test files under `test/floci/` or files ending in `.floci.yaml`, `.floci.yml`, or `.floci.json`.

## Test Case Format (YAML or JSON)

A file may contain a single test case, a list of cases, or a suite with a `cases` array.

```yaml
name: "orders-suite"
description: "writes to S3 + DynamoDB"
setup:
  - type: "s3/create-bucket"
    params:
      bucket: "orders"
  - type: "dynamodb/create-table"
    params:
      table: "Orders"
      hash_key: "id"
      hash_key_type: "S"

cases:
  - name: "store-order"
    payload:
      id: "o-123"
      bucket: "orders"
      key: "o-123.json"
      table: "Orders"
      message: "order placed"
    expected:
      statusCode: 200
      body:
        ok: true
    side_effects:
      - type: "s3/object-exists"
        params:
          bucket: "orders"
          key: "o-123.json"
      - type: "s3/object-contains"
        params:
          bucket: "orders"
          key: "o-123.json"
          contains: "order placed"
      - type: "dynamodb/item-exists"
        params:
          table: "Orders"
          key:
            id: "o-123"
          attributes:
            status: "stored"
```

The output validator uses JSON subset matching, so the actual Lambda response can include additional fields beyond the expected object.

## Supported Setup Actions

- `s3/create-bucket`
- `dynamodb/create-table`

## Supported Side Effect Checks

- `s3/object-exists`
- `s3/object-contains`
- `dynamodb/item-exists`

## Example Source

See `examples/floci/` for a small Go Lambda example and test suite that exercise S3 and DynamoDB side effects. Copy the test file into your package under `test/floci/` when preparing a zip for conversion.

## Lambda Packaging Notes

The Floci stage builds a Go custom runtime:
- `GOOS=linux`, `GOARCH=amd64`
- `go build -tags lambda.norpc -o bootstrap .`

The resulting `bootstrap` binary is zipped at the root of the archive for compatibility with Floci's Lambda extractor.
