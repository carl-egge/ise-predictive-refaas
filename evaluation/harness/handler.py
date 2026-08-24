"""Python-side measurement harness - the mirror of bench_handler.go.txt.

[H6] requires the Go and Python sides of the energy comparison to be
methodologically symmetric. That symmetry is enforced here structurally
rather than by convention: both harnesses read the *same* fixture payloads as
JSON Lines on stdin, invoke the function once per line, and write the *same*
envelope - a marker line followed by {"response": ...} or {"error": ...} - to
stdout. The driver (cmd/runtime) therefore measures two processes doing
identical work through identical I/O, and any difference it records is a
difference between the two language runtimes rather than between two
harnesses.

Reading N payloads in one process is what separates cold start from steady
state without instrumenting the inside of either harness: the driver runs the
same executable once with 1 payload and once with N, and takes the difference
(see cmd/runtime). Keeping all timing outside the harness is deliberate -
in-harness clocks would measure Python's time module against Go's runtime
clock and add a per-language bias to the very quantity being compared.

stdout discipline mirrors [A18]: the function's own prints go to stderr for
the duration of the call, and the marker separates anything that escapes
regardless.
"""

import importlib.util
import io
import json
import os
import sys
import traceback

# Keep in sync with harnessOutputMarker in internal/builder/test_handler.txt,
# internal/builder/validator.go and evaluation/harness/bench_handler.go.txt.
HARNESS_OUTPUT_MARKER = "__REFAAS_HARNESS_OUTPUT__"

# Handler names tried in order. The dataset's artifacts are AWS Lambda
# functions, so lambda_handler dominates; the rest are the conventional
# fallbacks.
HANDLER_NAMES = ("lambda_handler", "handler", "main")


class LambdaContext:
    """Minimal stand-in for the AWS Lambda context object.

    Eight functions in evaluation_set read context attributes
    (EVALUATION_DATASET.md §6.7). None echo them into their output, so fixed
    values cannot change a recorded expectation - but an AttributeError here
    would abort the invocation and make the function look unmeasurable, which
    is a worse outcome than a stub.
    """

    function_name = "refaas-benchmark"
    function_version = "$LATEST"
    memory_limit_in_mb = 512
    invoked_function_arn = (
        "arn:aws:lambda:us-east-1:000000000000:function:refaas-benchmark"
    )
    aws_request_id = "00000000-0000-0000-0000-000000000000"
    log_group_name = "/aws/lambda/refaas-benchmark"
    log_stream_name = "2026/01/01/[$LATEST]0000000000000000000000000000000000"

    def get_remaining_time_in_millis(self):
        return 300000


def load_handler(path):
    """Import the function module and return its handler.

    The module is imported once, before any payload is read, so module-level
    statements - which run at import time in Lambda too - are charged to
    startup rather than to the first invocation. That is what makes the
    two-point cold/steady split meaningful.
    """
    spec = importlib.util.spec_from_file_location("refaas_function", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load %s as a Python module" % path)
    module = importlib.util.module_from_spec(spec)
    sys.modules["refaas_function"] = module
    spec.loader.exec_module(module)

    for name in HANDLER_NAMES:
        fn = getattr(module, name, None)
        if callable(fn):
            return fn
    raise RuntimeError(
        "no handler found in %s (looked for %s)" % (path, ", ".join(HANDLER_NAMES))
    )


def main(argv):
    if len(argv) != 2:
        print("usage: handler.py <main.py>", file=sys.stderr)
        return 2

    real_stdout = sys.stdout
    # The function shares this process's stdout, and a faithful translation
    # target does too. Point it at stderr for the whole run so prints land in
    # diagnostics, exactly as the Go harness does.
    sys.stdout = sys.stderr

    try:
        handler = load_handler(argv[1])
    except Exception as exc:  # noqa: BLE001 - reported, not swallowed
        print("harness: %s" % exc, file=sys.stderr)
        traceback.print_exc(file=sys.stderr)
        return 3

    context = LambdaContext()
    invocations = 0

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        output = {}
        try:
            event = json.loads(line)
        except ValueError as exc:
            output["error"] = "invalid payload: %s" % exc
        else:
            try:
                output["response"] = handler(event, context)
            except BaseException as exc:  # noqa: BLE001 - mirrors the Go envelope
                output["error"] = "%s: %s" % (type(exc).__name__, exc)

        try:
            encoded = json.dumps(output, indent=2, default=str)
        except (TypeError, ValueError) as exc:
            encoded = json.dumps({"error": "unserializable response: %s" % exc})

        print(HARNESS_OUTPUT_MARKER, file=real_stdout)
        print(encoded, file=real_stdout)
        invocations += 1

    # Flushed once at the end, not per invocation: the Go harness buffers
    # through bufio and flushes once, so flushing per line here would charge
    # the Python side one write syscall per invocation that the Go side never
    # pays - an asymmetry in the measurement rather than in the runtimes.
    real_stdout.flush()

    if invocations == 0:
        print("harness: no payloads on stdin", file=sys.stderr)
        return 4
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
