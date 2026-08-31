#!/bin/bash
#
# Drive a whole artifact set through a running ReFaaS service, one function at
# a time, and archive everything the evaluation needs.
#
# This is the script that produces the thesis numbers, so it is committed
# rather than improvised: runs/batch-20260807-132133.csv was produced by an ad
# hoc version of this that no longer exists, which made that run impossible to
# reproduce exactly.
#
# Usage:
#   ./scripts/run-benchmark.sh evaluation/evaluation_set            # full set
#   ./scripts/run-benchmark.sh evaluation/function_set 20260824-1   # explicit run id
#   ./scripts/run-benchmark.sh -c scripts/chatai.json evaluation/evaluation_set
#
# The pipeline config (default scripts/benchmark.json) is POSTed to /reconfigure
# before the first upload. Without it the service would run the embedded dev
# pipeline and produce a full set of results for the wrong experiment.
#
# Environment:
#   REFAAS_URL     service base URL           (default http://localhost:8080)
#   JOB_TIMEOUT    seconds to wait per job    (default 1800)
#   POLL_INTERVAL  seconds between polls      (default 3)
#
# Outputs, all under runs/:
#   batch-<id>.csv                one row per function
#   packages-<id>/<fn>.zip        the translated package (completed or not)
#   packages-<id>.zip             the above, archived for cmd/runtime -packages
#   metrics-<id>.json             GET /metrics at the end of the run
#   manifest-<id>.txt             what was run, against what, with which config
#
# The service writes its own append-only run log to runs/run-<ts>.jsonl; that
# is the file cmd/energy reads. This script archives the things around it.

set -euo pipefail

CONFIG="scripts/benchmark.json"
SKIP_RECONFIGURE=""
POSITIONAL=()
while [ $# -gt 0 ]; do
    case "$1" in
        -c|--config) CONFIG="$2"; shift 2 ;;
        --no-reconfigure) SKIP_RECONFIGURE=1; shift ;;
        -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
        *) POSITIONAL+=("$1"); shift ;;
    esac
done

ARTIFACT_DIR="${POSITIONAL[0]:-}"
RUN_ID="${POSITIONAL[1]:-$(date +%Y%m%d-%H%M%S)}"
REFAAS_URL="${REFAAS_URL:-http://localhost:8080}"
JOB_TIMEOUT="${JOB_TIMEOUT:-1800}"
POLL_INTERVAL="${POLL_INTERVAL:-3}"

if [ -z "$ARTIFACT_DIR" ] || [ ! -d "$ARTIFACT_DIR" ]; then
    echo "Usage: $0 [-c config.json] [--no-reconfigure] <artifact-dir> [run-id]" >&2
    exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

RUNS_DIR="runs"
PKG_DIR="${RUNS_DIR}/packages-${RUN_ID}"
CSV="${RUNS_DIR}/batch-${RUN_ID}.csv"
MANIFEST="${RUNS_DIR}/manifest-${RUN_ID}.txt"
mkdir -p "$PKG_DIR"

# -- preflight -------------------------------------------------------------
# Every check here is something that, left unverified, wastes hours: the run
# is sequential and takes 2-4 h on the 95-function set.

if ! curl -fsS -m 5 "${REFAAS_URL}/metrics" >/dev/null 2>&1; then
    echo "ERROR: no ReFaaS service at ${REFAAS_URL} (start it with: go run ./cmd/refaas)" >&2
    exit 1
fi

ARTIFACTS=()
while IFS= read -r f; do ARTIFACTS+=("$f"); done < <(find "$ARTIFACT_DIR" -maxdepth 1 -name '*.zip' | sort -V)
if [ "${#ARTIFACTS[@]}" -eq 0 ]; then
    echo "ERROR: no .zip artifacts under ${ARTIFACT_DIR}" >&2
    exit 1
fi

# working_tree_dirty - is the *code* that produced this run uncommitted?
#
# Deliberately ignores runs/ . Since the run records became tracked files,
# this script writes the manifest, the CSV and the packages into the working
# tree as it goes, so a plain `git status --porcelain` reports "dirty" on
# every run by construction - including a run made from a pristine checkout.
# That made the field useless exactly when it matters: telling a reader
# whether git_commit above fully describes the code under test.
working_tree_dirty() {
    local out
    # Porcelain v1: two status chars then a space, then the path - so match on
    # the path field rather than anywhere in the line, or a file like
    # docs/runs/x.md would be excluded too.
    out=$(git status --porcelain 2>/dev/null | awk 'substr($0,4) !~ /^runs\//') || true
    [ -n "$out" ] && echo yes || echo no
}

# -- service environment ---------------------------------------------------
# REQUIRE_META, FLOCI_ENABLED and LLM_CALL_INTERVAL are read by the *service*
# process, not by this script. Recording them from this shell records what the
# operator happened to export here, which is not what shaped the run: in the
# 2026-08-30 evaluation run all three were set on the service and the manifest
# still said "unset" - exactly the silent mismatch a manifest exists to
# prevent, and it only shows up later when two runs are compared.
#
# So read them from the service's own /proc entry when the service is local,
# and say "unknown" rather than guess when it is not. Never print a value that
# was not observed on the process that actually used it.

service_url_part() { printf '%s' "$REFAAS_URL" | sed -E 's#^[a-z]+://##; s#/.*##'; }

find_service_pid() {
    case "$(service_url_part | sed -E 's#:.*##')" in
        localhost|127.0.0.1|::1|"") ;;
        *) return 1 ;;
    esac
    local port pid
    port="$(service_url_part | sed -nE 's#.*:([0-9]+)$#\1#p')"
    [ -n "$port" ] || port=8080
    pid=$(ss -ltnpH "sport = :${port}" 2>/dev/null | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2)
    if [ -z "$pid" ]; then
        # ss missing or the socket hidden; fall back to the binary name.
        pid=$(pgrep -f '[r]efaas' 2>/dev/null | tail -1)
    fi
    [ -n "$pid" ] || return 1
    [ -r "/proc/${pid}/environ" ] || return 1
    printf '%s' "$pid"
}

SERVICE_PID="$(find_service_pid || true)"

# service_env <VAR> - the value the service process was started with.
service_env() {
    local val
    if [ -z "$SERVICE_PID" ]; then
        echo "unknown (service not local or not inspectable)"
        return
    fi
    val=$(tr '\0' '\n' < "/proc/${SERVICE_PID}/environ" 2>/dev/null | grep -m1 "^$1=" | cut -d= -f2-)
    if [ -z "$val" ]; then echo "unset (service env)"; else echo "$val"; fi
}

SVC_REQUIRE_META="$(service_env REQUIRE_META)"
SVC_FLOCI_ENABLED="$(service_env FLOCI_ENABLED)"
SVC_LLM_INTERVAL="$(service_env LLM_CALL_INTERVAL)"

# Benchmark mode makes a missing meta.json a 400 instead of an unattributable
# result. Warn rather than fail: the service owns that setting, not this script.
case "$SVC_REQUIRE_META" in
    true|1) ;;
    unknown*)
        echo "WARNING: could not read the service's REQUIRE_META (service not local?)." >&2
        echo "         If it was started without it, an artifact missing meta.json is" >&2
        echo "         translated anyway and is not attributable to a dataset element." >&2
        ;;
    *)
        echo "WARNING: the running service was started WITHOUT REQUIRE_META. An artifact" >&2
        echo "         missing meta.json will be translated anyway and its result will" >&2
        echo "         not be attributable to a dataset element." >&2
        ;;
esac

# Apply the pipeline configuration before anything is uploaded.
#
# This is not a convenience. A freshly started service runs the *embedded
# default.yaml* - the deliberately short dev pipeline on a 3B model - so a run
# that forgets to reconfigure produces a full set of results for the wrong
# experiment, and nothing in the output says so. /reconfigure also wipes the
# in-memory metrics, so it has to happen before the first upload rather than
# after.
CONFIG_SHA="(not applied)"
if [ -n "$SKIP_RECONFIGURE" ]; then
    echo "WARNING: --no-reconfigure: using whatever pipeline the service already has." >&2
    echo "         A freshly started service runs the dev pipeline (default.yaml, 3B model)." >&2
else
    if [ ! -f "$CONFIG" ]; then
        echo "ERROR: pipeline config ${CONFIG} not found (pass -c <file>, or --no-reconfigure)" >&2
        exit 1
    fi
    code=$(curl -s -o /tmp/reconfigure-out.$$ -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" -d "@${CONFIG}" "${REFAAS_URL}/reconfigure" || echo "000")
    if [ "$code" != "201" ]; then
        echo "ERROR: /reconfigure with ${CONFIG} returned HTTP ${code}:" >&2
        head -c 500 "/tmp/reconfigure-out.$$" >&2; echo >&2
        rm -f "/tmp/reconfigure-out.$$"
        exit 1
    fi
    rm -f "/tmp/reconfigure-out.$$"
    CONFIG_SHA="$(sha256sum "$CONFIG" 2>/dev/null | cut -c1-16)"
    echo "Applied pipeline config: ${CONFIG} (sha256 ${CONFIG_SHA})"
fi

{
    echo "run_id:        ${RUN_ID}"
    echo "started:       $(date -Is)"
    echo "config:        ${CONFIG} (sha256 ${CONFIG_SHA})"
    echo "artifacts:     ${ARTIFACT_DIR} (${#ARTIFACTS[@]} functions)"
    echo "service:       ${REFAAS_URL}"
    echo "host:          $(uname -srm) / $(hostname)"
    echo "git_commit:    $(git rev-parse HEAD 2>/dev/null || echo unknown)"
    echo "git_dirty:     $(working_tree_dirty)"
    echo "job_timeout_s: ${JOB_TIMEOUT}"
    echo "require_meta:  ${SVC_REQUIRE_META}"
    echo "floci_enabled: ${SVC_FLOCI_ENABLED}"
    echo "llm_interval:  ${SVC_LLM_INTERVAL}"
} > "$MANIFEST"

echo "Run ${RUN_ID}: ${#ARTIFACTS[@]} artifacts from ${ARTIFACT_DIR}"
echo "Manifest: ${MANIFEST}"
echo

# Resume support: a run that dies at function 70 must not have to redo 69.
# Presence of the downloaded package is the completion marker, since that is
# the last thing written per function.
if [ ! -f "$CSV" ]; then
    echo "function,job_id,http_upload,final_status,elapsed_s" > "$CSV"
fi

completed=0
failed=0
errors=0
skipped=0

for artifact in "${ARTIFACTS[@]}"; do
    fn="$(basename "$artifact" .zip)"

    if [ -f "${PKG_DIR}/${fn}.zip" ]; then
        echo "  ${fn}: already downloaded, skipping (delete ${PKG_DIR}/${fn}.zip to redo)"
        skipped=$((skipped + 1))
        continue
    fi

    start=$(date +%s)

    # -- upload ------------------------------------------------------------
    upload=$(curl -s -w "\n%{http_code}" -F "file=@${artifact}" "${REFAAS_URL}/" || true)
    upload_code="$(printf '%s' "$upload" | tail -n1)"
    job_id="$(printf '%s' "$upload" | sed '$d' | tr -d '\r\n[:space:]')"

    if [ "$upload_code" != "201" ] || [ -z "$job_id" ]; then
        echo "  ${fn}: upload failed (HTTP ${upload_code}): ${job_id}"
        echo "${fn},,${upload_code},upload_failed,0" >> "$CSV"
        errors=$((errors + 1))
        continue
    fi

    # -- poll --------------------------------------------------------------
    # 202 = still queued/running, 200 = completed, 406 = finished but not
    # completed (the package is still returned, and is worth archiving as
    # evidence), 404 = unknown. GET deletes the result, so it is fetched once.
    out="${PKG_DIR}/${fn}.zip"
    status=""
    while :; do
        now=$(date +%s)
        if [ $((now - start)) -ge "$JOB_TIMEOUT" ]; then
            status="timeout"
            curl -s -X POST "${REFAAS_URL}/stop/${job_id}" >/dev/null || true
            break
        fi
        code=$(curl -s -o "${out}.part" -w "%{http_code}" "${REFAAS_URL}/${job_id}" || echo "000")
        case "$code" in
            202) sleep "$POLL_INTERVAL" ;;
            200) mv "${out}.part" "$out"; status="finished:dl200"; break ;;
            406) mv "${out}.part" "$out"; status="finished:dl406"; break ;;
            *)   status="http_${code}"; break ;;
        esac
    done
    rm -f "${out}.part"

    elapsed=$(( $(date +%s) - start ))
    echo "${fn},${job_id},${upload_code},${status},${elapsed}" >> "$CSV"

    case "$status" in
        finished:dl200) completed=$((completed + 1)); marker="OK  " ;;
        finished:dl406) failed=$((failed + 1));       marker="FAIL" ;;
        *)              errors=$((errors + 1));       marker="ERR " ;;
    esac
    printf '  %-10s %s %-16s %4ds\n' "$fn" "$marker" "$status" "$elapsed"
done

# -- archive ---------------------------------------------------------------

curl -s "${REFAAS_URL}/metrics" > "${RUNS_DIR}/metrics-${RUN_ID}.json" || true

if command -v zip >/dev/null 2>&1; then
    (cd "$RUNS_DIR" && zip -qr "packages-${RUN_ID}.zip" "packages-${RUN_ID}")
fi

{
    echo "finished:      $(date -Is)"
    echo "completed:     ${completed}"
    echo "failed:        ${failed}"
    echo "errors:        ${errors}"
    echo "skipped:       ${skipped}"
} >> "$MANIFEST"

total=$((completed + failed + errors))
echo
echo "Run ${RUN_ID} finished: ${completed} completed, ${failed} failed, ${errors} errors (${total} attempted, ${skipped} skipped)"
if [ "$total" -gt 0 ]; then
    echo "  success rate: $(( completed * 100 / total ))%"
fi
echo
echo "  ${CSV}"
echo "  ${RUNS_DIR}/metrics-${RUN_ID}.json"
echo "  ${RUNS_DIR}/packages-${RUN_ID}.zip"
echo
echo "Next:"
echo "  go run ./cmd/energy runs/run-*.jsonl"
echo "  go run ./cmd/runtime -artifacts ${ARTIFACT_DIR} -packages ${RUNS_DIR}/packages-${RUN_ID}.zip \\"
echo "      -out evaluation/runtime.json -report evaluation/runtime-report-${RUN_ID}.json"
