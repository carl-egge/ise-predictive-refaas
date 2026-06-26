#!/bin/bash

# Check if a file argument was provided
if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <config-json-file>"
    exit 1
fi

CONFIG_FILE=$1

# Check if the file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo "Error: File '$CONFIG_FILE' not found."
    exit 1
fi

# Send the configuration to the /reconfigure endpoint
echo "Reconfiguring pipeline using $CONFIG_FILE..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST -H "Content-Type: application/json" -d "@$CONFIG_FILE" http://localhost:8080/reconfigure)

# Separate the response body from the HTTP status code
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 201 ]; then
    echo "Successfully reconfigured the pipeline."
else
    echo "Failed to reconfigure pipeline. HTTP Status: $HTTP_CODE"
    echo "Response: $RESPONSE_BODY"
    exit 1
fi