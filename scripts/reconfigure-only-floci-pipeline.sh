curl -X POST http://localhost:8080/reconfigure \
  -H "Content-Type: application/json" \
  -d '{
    "pipeline": {
      "options": {
        "floci_enabled": true,
        "floci_endpoint": "http://localhost:4566",
        "floci_region": "us-east-1",
        "floci_account_id": "000000000000"
      },
      "tasks": [
        { "id": "root", "task": "flociTester" }
      ]
    }
  }'