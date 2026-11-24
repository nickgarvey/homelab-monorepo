"""Entry point: serve the diabetes MCP over streamable HTTP."""

from __future__ import annotations

import logging
import os
from zoneinfo import ZoneInfo

import uvicorn

from . import server, tools
from .nightscout import Nightscout, token_from_file


def main() -> None:
    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"),
                        format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    # httpx logs every request URL at INFO, and the URL carries the Nightscout token.
    logging.getLogger("httpx").setLevel(logging.WARNING)
    env = os.environ.get
    cfg = tools.Config(
        home=ZoneInfo(env("HOME_TIMEZONE", "America/Los_Angeles")),
        pod_hours=float(env("POD_LIFETIME_HOURS", "72")),
        pod_grace_hours=float(env("POD_GRACE_HOURS", "8")),
        cgm_hours=float(env("CGM_LIFETIME_HOURS", "240")),
        cgm_grace_hours=float(env("CGM_GRACE_HOURS", "12")),
    )
    ns = Nightscout(env("NIGHTSCOUT_URL", "https://nightscout.home.garvey.sh"),
                    token_from_file(env("NIGHTSCOUT_TOKEN_FILE", "/run/secrets/nightscout/token")))
    hosts = env("MCP_ALLOWED_HOSTS", "mcp.home.garvey.sh,localhost:*,127.0.0.1:*").split(",")
    app = server.build(ns, cfg, [h.strip() for h in hosts if h.strip()])
    uvicorn.run(app, host="::", port=int(env("PORT", "8000")), log_level="info")


if __name__ == "__main__":
    main()
