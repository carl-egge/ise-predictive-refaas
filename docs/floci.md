# Floci.io

Floci is a fast, free, open-source AWS emulator built with Quarkus Native. Starts in 24ms, uses 13 MiB at idle. Drop-in replacement for LocalStack — no auth token, no restrictions, ever.

A free, open-source local AWS emulator. No account. No feature gates. Just docker compose up.

## Why Floci?

| | Floci | LocalStack Community |
|---|---|---|
| Auth token required | No | Yes (since March 2026) |
| Security updates | Yes | Frozen |
| Startup time | **~24 ms** | ~3.3 s |
| Idle memory | **~13 MiB** | ~143 MiB |
| Docker image size | **~90 MB** | ~1.0 GB |
| License | **MIT** | Restricted |
| API Gateway v2 / HTTP API | ✅ | ❌ |
| Cognito | ✅ | ❌ |
| ElastiCache (Redis + IAM auth) | ✅ | ❌ |
| RDS (PostgreSQL + MySQL + IAM auth) | ✅ | ❌ |
| MSK (Kafka + Redpanda) | ✅ | ❌ |
| Athena (query state machine, mock mode) | ✅ | ❌ |
| Glue Data Catalog | ✅ | ❌ |
| Data Firehose (NDJSON delivery) | ✅ | ❌ |
| S3 Object Lock (COMPLIANCE / GOVERNANCE) | ✅ | ⚠️ Partial |
| DynamoDB Streams | ✅ | ⚠️ Partial |
| IAM (users, roles, policies, groups) | ✅ | ⚠️ Partial |
| STS (all 7 operations) | ✅ | ⚠️ Partial |
| Kinesis (streams, shards, fan-out) | ✅ | ⚠️ Partial |
| KMS (sign, verify, re-encrypt) | ✅ | ⚠️ Partial |
| ECS (clusters, services, tasks) | ✅ | ❌ |
| EKS (clusters, mock + real k3s) | ✅ | ❌ |
| EC2 (VPCs, instances, security groups) | ✅ | ⚠️ Partial |
| Native binary | ✅ ~40 MB | ❌ |

**Broad AWS coverage. Free forever.**

## Real Docker Integration

Unlike mock-only emulators, Floci runs **real Docker containers** for services where in-process emulation would compromise fidelity — stateful databases, connection-heavy protocols, and runtimes that require native execution. The result is wire-compatible behavior against the actual engine, not a simplified approximation.

| Service | Default Docker image | What's real |
|---|---|---|
| **Lambda** | `public.ecr.aws/lambda/<runtime>` | AWS runtime environment, execution model, warm container pool |
| **ElastiCache** | `valkey/valkey:8` | Full Redis/Valkey protocol, ACL-based IAM auth, SigV4 validation |
| **RDS (PostgreSQL)** | `postgres:16-alpine` | Real PostgreSQL engine, IAM auth via token, JDBC-compatible |
| **RDS (MySQL / Aurora)** | `mysql:8.0` | Real MySQL engine, IAM auth, JDBC-compatible |
| **RDS (MariaDB)** | `mariadb:11` | Real MariaDB engine, IAM auth, JDBC-compatible |
| **MSK** | `redpandadata/redpanda:latest` | Real Kafka-compatible broker via Redpanda |
| **ECS** | User-specified in task definition | Actual container lifecycle — start, stop, health checks |
| **EKS** | `rancher/k3s:latest` | Live Kubernetes API server (k3s), full kubeconfig |
| **OpenSearch** | `opensearchproject/opensearch:2` | Full OpenSearch engine with REST API |
| **ECR** | `registry:2` | Real OCI-compatible registry — `docker push` / `docker pull` work natively |

### Requirements

Docker-backed services require the Docker socket to be accessible:

```bash
docker run -d --name floci \
  -p 4566:4566 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -u root \
  hectorvent/floci:latest
```

In Docker Compose, add the socket volume alongside any other mounts.

### Overriding default images

All default images are configurable via environment variables, useful for pinning versions or using a local mirror:

| Variable | Default |
|---|---|
| `FLOCI_SERVICES_ELASTICACHE_DEFAULT_IMAGE` | `valkey/valkey:8` |
| `FLOCI_SERVICES_RDS_DEFAULT_POSTGRES_IMAGE` | `postgres:16-alpine` |
| `FLOCI_SERVICES_RDS_DEFAULT_MYSQL_IMAGE` | `mysql:8.0` |
| `FLOCI_SERVICES_RDS_DEFAULT_MARIADB_IMAGE` | `mariadb:11` |
| `FLOCI_SERVICES_MSK_DEFAULT_IMAGE` | `redpandadata/redpanda:latest` |
| `FLOCI_SERVICES_OPENSEARCH_DEFAULT_IMAGE` | `opensearchproject/opensearch:2` |
| `FLOCI_SERVICES_EKS_DEFAULT_IMAGE` | `rancher/k3s:latest` |
| `FLOCI_SERVICES_ECR_REGISTRY_IMAGE` | `registry:2` |
| `FLOCI_ECR_BASE_URI` | `public.ecr.aws` (Lambda runtime base) |

## Supported Services

| Service | How it works | Notable features |
|---|---|---|
| **SSM Parameter Store** | In-process | Version history, labels, SecureString, tagging |
| **SQS** | In-process | Standard & FIFO, DLQ, visibility timeout, batch, tagging |
| **SNS** | In-process | Topics, subscriptions, SQS / Lambda / HTTP delivery, tagging |
| **S3** | In-process | Versioning, multipart upload, pre-signed URLs, Object Lock, event notifications |
| **DynamoDB** | In-process | GSI / LSI, Query, Scan, TTL, transactions, batch operations |
| **DynamoDB Streams** | In-process | Shard iterators, records, Lambda ESM trigger |
| **Lambda** | **Real Docker containers** | Warm pool, aliases, Function URLs, SQS / Kinesis / DDB Streams ESM |
| **API Gateway REST** | In-process | Resources, methods, stages, Lambda proxy, MOCK integrations, AWS integrations |
| **API Gateway v2 (HTTP)** | In-process | Routes, integrations, JWT authorizers, stages |
| **IAM** | In-process | Users, roles, groups, policies, instance profiles, access keys |
| **STS** | In-process | AssumeRole, WebIdentity, SAML, GetFederationToken, GetSessionToken |
| **Cognito** | In-process | User pools, app clients, auth flows, JWKS / OpenID well-known endpoints |
| **KMS** | In-process | Encrypt / decrypt, sign / verify, data keys, aliases |
| **Kinesis** | In-process | Streams, shards, enhanced fan-out, split / merge |
| **Secrets Manager** | In-process | Versioning, resource policies, tagging |
| **Step Functions** | In-process | ASL execution, task tokens, execution history |
| **CloudFormation** | In-process | Stacks, change sets, resource provisioning |
| **EventBridge** | In-process | Custom buses, rules, targets (SQS / SNS / Lambda) |
| **EventBridge Scheduler** | In-process | Schedule groups, schedules, flexible time windows, retry policies, dead-letter queues |
| **CloudWatch Logs** | In-process | Log groups, streams, ingestion, filtering |
| **CloudWatch Metrics** | In-process | Custom metrics, statistics, alarms |
| **ElastiCache** | **Real Docker containers** | Redis / Valkey, IAM auth, SigV4 validation |
| **RDS** | **Real Docker containers** | PostgreSQL & MySQL, IAM auth, JDBC-compatible |
| **MSK** | **Real Docker containers** | Kafka compatible via Redpanda orchestration |
| **Athena** | In-process | Query state machine (mock mode — queries accepted, results empty) |
| **Glue** | In-process | Data Catalog for metadata management |
| **Data Firehose** | In-process | Streaming data delivery; records flushed as NDJSON to S3 |
| **ECS** | **Real Docker containers** | Clusters, task definitions, tasks, services, capacity providers, task sets |
| **EC2** | In-process | VPCs, subnets, security groups, instances, AMIs, key pairs, internet gateways, route tables, Elastic IPs, tags |
| **ACM** | In-process | Certificate issuance, validation lifecycle |
| **ECR** | In-process + **real OCI registry** | Repositories, image push / pull via stock `docker`, image-backed Lambda functions |
| **SES** | In-process | Send email / raw email, identity verification, DKIM attributes |
| **SES v2 (HTTP)** | In-process | REST JSON API, identities, DKIM, feedback attributes, account sending |
| **OpenSearch** | **Real Docker containers** | Domain CRUD, tags, versions, instance types, upgrade stubs |
| **AppConfig** | In-process | Applications, environments, profiles, hosted configuration versions, deployments |
| **AppConfigData** | In-process | Configuration sessions, dynamic configuration retrieval |
| **Bedrock Runtime** | In-process (stub) | Dummy Converse and InvokeModel responses for local development; streaming returns 501 |
| **EKS** | **Real Docker containers** (mock mode available) | Clusters, tagging; real mode starts k3s per cluster with a live Kubernetes API server |

> **Lambda, ElastiCache, RDS, MSK, ECS, EKS, and OpenSearch** spin up real Docker containers and support IAM authentication and SigV4 request signing — the same auth flow as production AWS. **ECR** runs a shared `registry:2` container so the stock `docker` client can push and pull image bytes against repositories returned by the AWS-shaped control plane.
>
> For per-service operation counts and endpoint protocols, see the [Services Overview](https://floci.io/floci/services/) in the documentation site.

# Quick Start

This guide gets Floci running and verifies that AWS CLI commands work against it in under five minutes.

## Step 1 — Start Floci

=== "Native (recommended)"

    `latest` is the native image — sub-second startup, minimal memory:

    ```yaml
    services:
      floci:
        image: hectorvent/floci:latest
        ports:
          - "4566:4566"
        volumes:
          # Local directory bind mount (default)
          - ./data:/app/data
    
          # OR named volume (optional):
          # - floci-data:/app/data
    
    # volumes:
    #   floci-data:
    ```

    ```bash
    docker compose up -d
    ```

=== "JVM"

    Use `latest-jvm` if you need broader platform compatibility:

    ```yaml
    services:
      floci:
        image: hectorvent/floci:latest-jvm
        ports:
          - "4566:4566"
        volumes:
          # Local directory bind mount (default)
          - ./data:/app/data
    
          # OR named volume (optional):
          # - floci-data:/app/data
    
    # volumes:
    #   floci-data:
    ```

    ```bash
    docker compose up -d
    ```

=== "Build from source"

    ```bash
    git clone https://github.com/floci-io/floci.git
    cd floci
    mvn quarkus:dev   # hot reload, port 4566
    ```

## Step 2 — Configure AWS CLI

Floci accepts any dummy credentials — no real AWS account needed.

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_DEFAULT_REGION=us-east-1
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
```

Add these to your shell profile (`.bashrc` / `.zshrc`) so they persist across sessions.

## Step 3 — Verify the Setup

Run a few quick smoke tests:

```bash
# S3 — create a bucket and upload a file
aws s3 mb s3://my-bucket --endpoint-url $AWS_ENDPOINT_URL
echo "hello floci" | aws s3 cp - s3://my-bucket/hello.txt --endpoint-url $AWS_ENDPOINT_URL
aws s3 ls s3://my-bucket --endpoint-url $AWS_ENDPOINT_URL

# SQS — create a queue and send a message
aws sqs create-queue --queue-name orders --endpoint-url $AWS_ENDPOINT_URL
aws sqs send-message \
  --queue-url $AWS_ENDPOINT_URL/000000000000/orders \
  --message-body '{"event":"order.placed"}' \
  --endpoint-url $AWS_ENDPOINT_URL

# DynamoDB — create a table
aws dynamodb create-table \
  --table-name Users \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --endpoint-url $AWS_ENDPOINT_URL
```

You should see successful responses for all three commands.

## Step 4 — Use in Your Application

Point your AWS SDK to Floci the same way:

=== "Python (boto3)"

    ```python
    import boto3

    s3 = boto3.client(
        "s3",
        endpoint_url="http://localhost:4566",
        region_name="us-east-1",
        aws_access_key_id="test",
        aws_secret_access_key="test",
    )
    ```

=== "Node.js"

    ```javascript
    import { S3Client } from "@aws-sdk/client-s3";

    const s3 = new S3Client({
      endpoint: "http://localhost:4566",
      region: "us-east-1",
      credentials: { accessKeyId: "test", secretAccessKey: "test" },
      forcePathStyle: true,
    });
    ```

=== "Go"

    ```go
    cfg, _ := config.LoadDefaultConfig(context.TODO(),
        config.WithRegion("us-east-1"),
        config.WithEndpointResolverWithOptions(
            aws.EndpointResolverWithOptionsFunc(func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
                return aws.Endpoint{URL: "http://localhost:4566"}, nil
            }),
        ),
    )
    client := s3.NewFromConfig(cfg)
    ```

## Step 5 — (Optional) Push and pull a container image to emulated ECR

Floci emulates ECR with a real OCI registry behind it, so the stock `docker` client works against repositories you create through the AWS CLI. No daemon configuration needed — Floci returns repository URIs that resolve to loopback, which `docker` auto-trusts as insecure.

```bash
# Create the repository (lazy-starts the backing registry container)
aws ecr create-repository --repository-name floci-it/app --endpoint-url $AWS_ENDPOINT

# Authenticate
aws ecr get-login-password --endpoint-url $AWS_ENDPOINT \
  | docker login --username AWS --password-stdin \
        000000000000.dkr.ecr.us-east-1.localhost:5000

# Push
docker pull alpine:3.19
docker tag  alpine:3.19 000000000000.dkr.ecr.us-east-1.localhost:5000/floci-it/app:v1
docker push             000000000000.dkr.ecr.us-east-1.localhost:5000/floci-it/app:v1

# Pull from a clean local image store
docker rmi  000000000000.dkr.ecr.us-east-1.localhost:5000/floci-it/app:v1
docker pull 000000000000.dkr.ecr.us-east-1.localhost:5000/floci-it/app:v1
```

See the [ECR service docs](../services/ecr.md) for the full action surface, image-backed Lambda integration, and CDK `DockerImageFunction` support.

## Lambda on native Linux Docker (UFW)

When Floci runs **natively on a Linux host** (not Docker Desktop), Lambda function containers reach Floci's Runtime API server via the docker bridge gateway. On Ubuntu / Pop!_OS / Debian boxes with **UFW enabled**, the default `INPUT DROP` policy silently drops these packets and Lambda invocations time out with `Function.TimedOut`. This affects every Lambda packaging type — Zip *and* image-backed functions deployed via emulated ECR.

**One-time fix**, scoped to the docker bridge only (does not expose anything to the network — `docker0` is internal):

```bash
sudo ufw allow in on docker0 comment 'floci: containers reach host'
```

If you want to scope it tighter to just the Lambda Runtime API and the ECR registry port ranges:

```bash
sudo ufw allow in on docker0 to any port 9200:9299 proto tcp comment 'floci lambda runtime api'
sudo ufw allow in on docker0 to any port 5000:5099 proto tcp comment 'floci ecr registry'
```

**Docker Desktop** (macOS / Windows / Linux) does not need this — it routes container → host through the Docker VM, which Floci's `DockerHostResolver` detects automatically.

**Floci-in-Docker** (running the published Floci image inside a container) does not need this either — Lambda containers and Floci share the same docker network and reach each other via container IPs.

---

# Installation

Floci can be run three ways: as a Docker image, as a pre-built native binary, or built from source.

## Docker (Recommended)

No installation required beyond Docker itself.

```bash
docker pull hectorvent/floci:latest
```

| Tag | Description |
|---|---|
| `latest` | Native image — sub-second startup, low memory (**default**) |
| `x.y.z` | Native image — specific release version |
| `latest-jvm` | JVM image — most compatible |
| `x.y.z-jvm` | JVM image — specific release version |

### Requirements

- Docker 20.10+
- `docker compose` v2+ (plugin syntax, not standalone `docker-compose`)

## Native vs JVM

The `latest` tag is the native image — a self-contained executable with no JVM dependency.

```yaml title="docker-compose.yml"
services:
  floci:
    image: hectorvent/floci:latest   # native — recommended
    ports:
      - "4566:4566"
```

Use the JVM image if you need broader platform compatibility or encounter native image issues:

```yaml title="docker-compose.yml"
services:
  floci:
    image: hectorvent/floci:latest-jvm
    ports:
      - "4566:4566"
```

### Startup comparison

| Image | Tag | Typical startup | Idle memory |
|---|---|---|---|
| Native | `latest` / `x.y.z` | ~24 ms | ~13 MiB |
| JVM | `latest-jvm` / `x.y.z-jvm` | ~2 s | ~250 MB |

## Build from Source

### Prerequisites

- Java 25+
- Maven 3.9+
- (Optional) GraalVM Mandrel for native compilation

### Clone and run

```bash
git clone https://github.com/floci-io/floci.git
cd floci
mvn quarkus:dev          # dev mode with hot reload on port 4566
```

### Build a production JAR

```bash
mvn clean package -DskipTests
java -jar target/quarkus-app/quarkus-run.jar
```

### Build a native executable

```bash
mvn clean package -Pnative -DskipTests
./target/floci-runner
```

!!! note
    Native compilation requires GraalVM or Mandrel with the `native-image` tool on your PATH. Build time is typically 2–5 minutes.

---

# AWS CLI & SDK Setup

Floci accepts any non-empty credentials — no real AWS account is needed.

## AWS CLI Profile

Add a dedicated profile to `~/.aws/config` and `~/.aws/credentials`:

```ini title="~/.aws/config"
[profile floci]
region = us-east-1
output = json
```

```ini title="~/.aws/credentials"
[floci]
aws_access_key_id = test
aws_secret_access_key = test
```

Then use it with every command:

```bash
aws s3 ls --profile floci --endpoint-url http://localhost:4566
```

Or set it as the default for your shell session:

```bash
export AWS_PROFILE=floci
export AWS_ENDPOINT_URL=http://localhost:4566
```

## SDK Configuration

### Python (boto3)

```python
import boto3

def floci_client(service):
    return boto3.client(
        service,
        endpoint_url="http://localhost:4566",
        region_name="us-east-1",
        aws_access_key_id="test",
        aws_secret_access_key="test",
    )

s3   = floci_client("s3")
sqs  = floci_client("sqs")
dynamo = floci_client("dynamodb")
```

### Go

```go
import (
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
)

cfg, err := config.LoadDefaultConfig(context.TODO(),
    config.WithRegion("us-east-1"),
    config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
    config.WithEndpointResolverWithOptions(
        aws.EndpointResolverWithOptionsFunc(
            func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
                return aws.Endpoint{URL: "http://localhost:4566"}, nil
            },
        ),
    ),
)
```

**Go Example**


```go
// Go (AWS SDK v2)
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
		config.WithBaseEndpoint("http://localhost:4566"),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	_, err = client.CreateBucket(context.TODO(), &s3.CreateBucketInput{
		Bucket: aws.String("demo-bucket"),
	})
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String("demo-bucket"),
		Key:    aws.String("demo.txt"),
		Body:   strings.NewReader("hello from floci"),
	})
	if err != nil {
		log.Fatal(err)
	}

	out, err := client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket: aws.String("demo-bucket"),
	})
	if err != nil {
		log.Fatal(err)
	}

	if len(out.Contents) > 0 {
		fmt.Println(*out.Contents[0].Key)
	}
}

```

## Default Account ID

Floci uses account ID `000000000000` in all ARNs and queue URLs. For example:

```
arn:aws:sqs:us-east-1:000000000000:my-queue
http://localhost:4566/000000000000/my-queue
```

This can be changed via the `FLOCI_DEFAULT_ACCOUNT_ID` environment variable.


## License

MIT — use it however you want.