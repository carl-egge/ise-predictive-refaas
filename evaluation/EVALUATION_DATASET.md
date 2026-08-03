# Evaluation dataset: handoff for the translation pipeline

> Produced by the dataset pipeline repo (`ise-dataset-pipeline`). Documentation of the dataset that will be used for evaluation.

---

## 1. What you have

Two datasets. **They do not carry the same guarantees** and should not be reported together
without saying so.

| | `evaluation_set` | `function_set` |
|---|---|---|
| Functions | **95** | **14** |
| Tests | **392** (3–5 per function) | 41 (1–5 per function) |
| Origin | scraped from `the-stack`, curated | ReFaaS paper, hand-picked (v0) |
| Expected outputs | **recorded from the real Python function** | authored by the paper's authors |
| Harness-validated | **yes**, 10 deterministic runs per test | **no** |
| Use for | the main evaluation | comparison against the original paper |

`evaluation_set` is the primary benchmark. `function_set` is the legacy set, aligned to the same
schema so the same runner can execute it, but its expectations were never verified against
execution.

## 2. Artifact format

One ZIP per function, **flat**, exactly as the pipeline's packager expects:

```
f42.zip
├── main.py          <- archive root, no f42/ prefix
├── meta.json          <- function metrics extracted
└── test/
    ├── t1.json
    ├── t2.json
    └── t3.json
```

* `main.py` is a single self-contained file. Any repo-local imports were already inlined.
* Test file stems (`t1`, `t2`, …) become the test-case names when a `name` field is absent.
* `meta.json` holds the per-function metrics you need for grouping results (see §8).
* Archives are deterministic: repackaging unchanged input yields byte-identical ZIPs.


## 3. Test file schema

**This is your own schema.** It is the format of this repository's floci test runner
(`testcase.go`), which the dataset side ported and verified against your `output_test.go`
vectors. No adapter should be necessary.

```json
{
  "name": "store-message",
  "description": "Stores a message in S3 and checks the object was written.",
  "payload":        { "bucket": "audit", "key": "m1.json" },
  "expectedOutput": { "statusCode": 200 },
  "outputMode": "tolerant",
  "setup":       [ { "type": "s3.bucket", "bucket": "audit" } ],
  "sideEffects": [ { "type": "s3.objectExists", "bucket": "audit", "key": "m1.json" } ],
  "provenance":  { "method": "llm", "outputSource": "golden" }
}
```

| field | meaning |
|---|---|
| `payload` | opaque JSON value passed to the handler as its event. Never interpreted by either runner. |
| `expectedOutput` | optional. Absent means output validation is skipped (1 test in the set). |
| `outputMode` | `tolerant` (default), `strict`, or `shape`. See gotcha 2. |
| `setup` | resources to provision before invocation. 40 functions need these. |
| `sideEffects` | assertions checked after invocation. 13 functions use them. |
| `provenance` | dataset bookkeeping. **Your runner ignores it** (unknown JSON fields). Useful to you: `outputSource` tells you where the expectation came from. |

Primitives that actually occur in the data: `s3.bucket`, `s3.object`, `dynamodb.table`,
`dynamodb.item`, `sqs.queue`, `sfn.stateMachine`, `kinesis.stream`, `cognito.userPool`,
`cognito.user` (setup); `s3.objectExists`, `s3.objectContains`, `dynamodb.itemExists`,
`sqs.messageReceived` (assertions). DynamoDB and S3 dominate.

## 4. The trust contract

**What is guaranteed for `evaluation_set`:**

* Every test was executed **10 times against the original Python function** and produced identical
  results each time. Non-deterministic and slow (>10 s) candidates were rejected.
* Every expected output is either a verified prediction or, in 45.9% of cases, the **recorded
  behaviour** of the Python function replacing a wrong prediction.
* **No test expects an error.** Every reference run completed without an unhandled exception.
* Every function has at least 3 passing tests.

**What is NOT guaranteed:**

* **The functions are not certified correct.** Tests record what the code *does*, bugs included.
  If a function returns HTTP 400 where its author meant 200, the test asserts 400.
* `function_set` expectations were never executed. Treat failures there as "needs investigation",
  not as translation defects, until you have confirmed the Python original passes.

## 5. Reading a result

| observation | interpretation |
|---|---|
| test passes | the Go function matched Python behaviour on that input |
| output mismatch | **real divergence.** The expectation is recorded Python behaviour, not a guess |
| Go function raises | **real divergence.** No test expects an error (§4) |
| side-effect assertion fails | the Go function did not produce the AWS state the Python one did |
| whole function fails to deploy/build | translation or packaging problem, not a behavioural one; report separately |

Because expectations are recorded behaviour, a failing test is strong evidence. There is no
category of "the test was probably wrong" for `evaluation_set`.

## 6. Gotchas

1. **Any unhandled error is a failure.** Error-outcome tests were deliberately excluded from the
   dataset, so there is no case where raising is the expected result.
2. **27 tests (in 14 functions) use `outputMode: "shape"`** and compare types only, not values.
   These functions have non-deterministic output (timestamps, generated IDs). They cannot catch a
   value regression, so do not count them as strong evidence.
   Affected: `f1 f6 f22 f34 f37 f43 f48 f49 f54 f63 f67 f80 f86 f91`.
3. **Tolerant matching is subset matching.** Extra fields in the actual output do not fail a test.
   This is intentional, but it means a Go translation that returns *more* than Python still
   passes.
4. **40 of 95 functions need floci resources provisioned** via `setup`. Ensure the emulator is
   running and reachable before the run, otherwise these fail for infrastructure reasons and look
   like translation defects.
5. **Six functions contain external HTTP/SMTP call sites that no test exercises**
   (`f3 f14 f19 f29 f58 f61`). Their tests take other branches. A wrong translation of
   `requests.post(...)` will pass this benchmark. Do not claim HTTP-integration fidelity from
   these results.
6. **`main.py` is a whole file, not just the handler.** Module-level statements run at import,
   and helper functions may sit alongside `lambda_handler`.
7. **Eight functions read the Lambda context object** (`log_stream_name`,
   `invoked_function_arn`, `get_remaining_time_in_millis`). None of them echo context values into
   their output, so recorded expectations are safe. But `f45` derives the **region** from the
   invoked ARN, so deploy it in a region consistent with the reference run (`us-east-1`).
8. **Descriptions can be stale.** Where the golden rule replaced a prediction, the test's
   `name`/`description` was not regenerated. A test may read "valid answer" while correctly
   asserting an error response. **Trust `payload` + `expectedOutput`, not the prose.**
   These are flagged by `provenance.outputSource == "golden"`.
9. **One test (`f88/t1`) has no `expectedOutput`** and therefore only asserts that the function
   does not error.
10. **`function_set` needs the open internet.** `f9` and `f10` perform real external API calls,
    and `f10`'s payloads embed a live API key. They will fail in a sandboxed environment.

## 7. Runtime dependencies

The Python originals used these third-party packages. Relevant only if you compare behaviour
against a Python baseline; the Go translation should not need them.

| package | functions |
|---|---|
| `boto3` | 58 |
| `python-dateutil` | 18 |
| `requests` | 4 |
| `beautifulsoup4` | 2 |
| `urllib3` | 1 |
| none (standard library only) | 21 |

## 8. Composition, for grouping results

Functions are bucketed by cyclomatic complexity. `meta.json`
carries `bucket`, `cc`, `lloc`, `type`, `aws`, `imports`, `description` and provenance per
function.

| bucket | functions | complexity | LLOC | tests |
|---|---|---|---|---|
| A (cc ≤ 5) | 25 | 1–5 | 13–87 | 97 |
| B (cc ≤ 10) | 25 | 6–10 | 24–133 | 106 |
| C (cc ≤ 20) | 25 | 11–20 | 29–368 | 106 |
| D+ (cc > 20) | 20 | 21–147 | 111–473 | 83 |

D+ holds 20 rather than 25 because the source corpus contains only 437 candidates above
complexity 20 and all were processed. Reporting results per bucket is the intended way to show
whether translation quality degrades with complexity.

Other axes: 58 functions use AWS, 37 do not; 8 use the standard library only. Note that the
`type` field in `meta.json` over-reports network usage (it counts `urllib.parse` and
`http.HTTPStatus` as network), so prefer the `aws` flag and the requirements table above.

## 9. Suggested reporting

* Pass rate per bucket (A/B/C/D+), to show complexity sensitivity.
* Pass rate for AWS vs non-AWS functions, since these exercise different translation surfaces.
* Report `evaluation_set` and `function_set` separately, and state that `function_set`
  expectations are unverified.
* Exclude, or mark, the 27 shape-mode tests when claiming value-level equivalence.
* State the external-API coverage gap (gotcha 5) when scoping claims.