"""Entry point: bootstrap the collection, then serve MCP over streamable HTTP.

`anki-mcp reset` instead deletes the local collection and sync state (with the
server stopped), so the next start downloads a fresh copy.
"""

from __future__ import annotations

import logging
import os
import sys

import uvicorn

from . import guard, server


def main() -> None:
    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"),
                        format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    data = os.environ.get("ANKI_DATA_DIR", "/data")
    # SYNC_USERn format from k8s/manifests/anki: "user:password".
    with open(os.environ["ANKI_SYNC_CREDENTIAL_FILE"]) as f:
        username, _, password = f.read().strip().partition(":")
    anki = guard.Anki(
        collection_path=os.path.join(data, "collection.anki2"),
        state_dir=os.path.join(data, "state"),
        server=guard.SyncServer(os.environ["ANKI_SYNC_ENDPOINT"], username, password),
    )
    if sys.argv[1:] == ["reset"]:
        anki.reset()
        print("deleted the local collection and sync state; the next start bootstraps from the sync server")
        return
    anki.start()
    hosts = os.environ.get("MCP_ALLOWED_HOSTS", "mcp.home.garvey.sh,localhost:*,127.0.0.1:*").split(",")
    app = server.build(anki, [h.strip() for h in hosts if h.strip()])
    try:
        uvicorn.run(app, host="::", port=int(os.environ.get("PORT", "8000")), log_level="info")
    finally:
        anki.close()


if __name__ == "__main__":
    main()
