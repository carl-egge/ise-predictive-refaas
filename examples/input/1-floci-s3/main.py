import json
import os

import boto3
from botocore.config import Config


def handler(event, context):
    bucket = event.get("bucket")
    if not bucket:
        raise ValueError("bucket is required")

    key = event.get("key", "message.json")
    message = event.get("message", "")

    endpoint = os.getenv("AWS_ENDPOINT_URL")
    region = os.getenv("AWS_REGION", "us-east-1")
    access_key = os.getenv("AWS_ACCESS_KEY_ID", "test")
    secret_key = os.getenv("AWS_SECRET_ACCESS_KEY", "test")

    force_path_style = os.getenv("AWS_S3_FORCE_PATH_STYLE", "").lower() in (
        "1",
        "true",
        "yes",
    )
    s3_config = None
    if force_path_style or endpoint:
        s3_config = Config(s3={"addressing_style": "path"})

    s3_client = boto3.client(
        "s3",
        region_name=region,
        endpoint_url=endpoint,
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        config=s3_config,
    )

    body = json.dumps({"message": message})
    s3_client.put_object(Bucket=bucket, Key=key, Body=body.encode("utf-8"))

    return {
        "statusCode": 200,
        "body": json.dumps({"ok": True, "bucket": bucket, "key": key}),
    }