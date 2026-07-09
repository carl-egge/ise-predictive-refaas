#!/bin/bash

# Download output package for a completed refaas job and extract it
# Usage: ./scripts/download-job.sh <job-uuid>

set -e

UUID=$1

if [ -z "$UUID" ]; then
    echo "Usage: $0 <job-uuid>" >&2
    exit 1
fi

# Create timestamped output directory
TIMESTAMP=$(date +"%Y-%m-%d_%H-%M-%S")
OUTPUT_DIR="examples/output/${TIMESTAMP}"
mkdir -p "$OUTPUT_DIR" || { echo "Failed to create directory: $OUTPUT_DIR" >&2; exit 1; }

# Download the zip file
cd "$OUTPUT_DIR" || { echo "Failed to change directory to $OUTPUT_DIR" >&2; exit 1; }
echo "Downloading job ${UUID}..."
curl -O "http://localhost:8080/${UUID}" || { echo "Failed to download job ${UUID}" >&2; exit 1; }

# Unzip the contents
echo "Extracting contents..."
unzip "${UUID}" || { echo "Failed to extract ${UUID}" >&2; exit 1; }

# Clean up the zip file
# rm "${UUID}"

echo "Successfully downloaded and extracted job ${UUID} to ${OUTPUT_DIR}"
