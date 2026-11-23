"""Entry point: serve the pages MCP tools over streamable HTTP.

S3 credentials come from the environment: the pod's JWT-SVID file and role ARN
(AWS_WEB_IDENTITY_TOKEN_FILE, AWS_ROLE_ARN), traded with RGW's STS.
"""

from __future__ import annotations

import logging
import os

import boto3
import uvicorn
from botocore.config import Config

from . import server, store


def main() -> None:
    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"),
                        format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    s3 = boto3.client("s3", endpoint_url=os.environ.get("S3_ENDPOINT", "https://rgw.home.garvey.sh"),
                      config=Config(s3={"addressing_style": "path"}))
    pages = store.Pages(s3, os.environ.get("PAGES_BUCKET", "pages"))
    hosts = os.environ.get("MCP_ALLOWED_HOSTS", "mcp.home.garvey.sh,localhost:*,127.0.0.1:*").split(",")
    app = server.build(pages, [h.strip() for h in hosts if h.strip()])
    uvicorn.run(app, host="::", port=int(os.environ.get("PORT", "8000")), log_level="info")


if __name__ == "__main__":
    main()
