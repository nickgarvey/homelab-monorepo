"""MCP tools for the user's static pages at https://pages.home.garvey.sh: list, read, create. No delete."""

from __future__ import annotations

import os
from typing import Any

from mcp.server import MCPServer
from mcp.server.mcpserver.exceptions import ToolError
from mcp.server.transport_security import TransportSecuritySettings
from mcp.types import ToolAnnotations
from starlette.requests import Request
from starlette.responses import JSONResponse

from . import store

INSTRUCTIONS = f"""\
Publish static pages (HTML, with CSS, JS, SVG and other text files) at
{store.SITE}, which only the user can open, after logging in.

Pages are permanent. Once a file is created, this server can neither change
nor delete it; only the user can, by hand. So before creating one, make sure
it is finished and the path is the one you want, and tell the user that it
will stay. A path that already exists is refused: pick a new one (e.g. add
-v2) rather than expecting to replace a file.

Every new file goes in <origin>/<topic>/, where <origin> is where the page
comes from (e.g. a project or the conversation's subject area) and <topic> is
what it is about:

- <origin>/<topic>/<page>.html for a single page, e.g. homelab/ceph/osd-latency.html
- <origin>/<topic>/<subfolder>/... for a page with several files, e.g.
  homelab/ceph/rebalance/index.html with homelab/ceph/rebalance/style.css

Never at the top level or directly under <origin>, and no index.html directly
under <origin>/<topic>/. The server refuses those paths. Look at list_pages
first and reuse an existing <origin> and <topic> where one fits.

Folders exist as soon as a file is created inside them. A folder's index.html
is served at the folder's URL. Links between files of one page should be
relative. Use list_pages to see what exists (newest first) and read_page to
read a file.
"""

READ = ToolAnnotations(read_only_hint=True, open_world_hint=False)
CREATE = ToolAnnotations(read_only_hint=False, destructive_hint=False, idempotent_hint=False,
                         open_world_hint=False)


def build(pages: store.Pages, allowed_hosts: list[str]) -> Any:
    mcp = MCPServer(name="pages", title="Static pages", instructions=INSTRUCTIONS)

    def call(fn: Any, *args: Any) -> Any:
        try:
            return fn(*args)
        except store.Refused as e:
            raise ToolError(str(e)) from e

    @mcp.tool(annotations=READ)
    def list_pages(folder: str = "", limit: int = 200) -> dict[str, Any]:
        """List files under a folder ("" for everything, or e.g. "trips"), including those in subfolders,
        newest first, with each file's path, URL, size and last-modified time."""
        return call(pages.list, folder, limit)

    @mcp.tool(annotations=READ)
    def read_page(path: str) -> dict[str, Any]:
        """Read one file by its path (as list_pages shows it, e.g. "trips/japan.html"). Files over 1 MB are
        truncated."""
        return call(pages.read, path)

    @mcp.tool(annotations=CREATE)
    def create_page(path: str, content: str) -> dict[str, Any]:
        """Create a new file at <origin>/<topic>/<page>.html or <origin>/<topic>/<subfolder>/..., such as
        "homelab/ceph/osd-latency.html" or "homelab/ceph/rebalance/index.html" (folders are created as
        needed; nothing at the top level or directly under <origin>, no index.html directly under
        <origin>/<topic>/), and return its URL. PERMANENT: the file cannot be changed or deleted afterwards through
        this server, and an existing path is refused. Allowed: .html .htm .css .js .mjs .json .svg .txt .md
        .csv .xml, UTF-8 text up to 5 MB."""
        return call(pages.create, path, content)

    @mcp.custom_route("/healthz", methods=["GET"])
    async def healthz(_: Request) -> JSONResponse:
        return JSONResponse({"status": "ok"})

    return mcp.streamable_http_app(
        streamable_http_path=os.environ.get("MCP_PATH", "/mcp"),
        stateless_http=True,
        json_response=True,
        transport_security=TransportSecuritySettings(allowed_hosts=allowed_hosts),
        host="0.0.0.0",
    )
