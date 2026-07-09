#!/usr/bin/env bash

# This script is intended to store the current metrics output from the refaas instance.
curl -s http://localhost:8080/metrics > "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/examples/metrics/metrics-$(date +%Y%m%d%H%M%S).json"