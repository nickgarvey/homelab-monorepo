"""MCP tools over tools.py: read-only views of Nightscout (glucose, Loop, pump, CGM, therapy settings)."""

from __future__ import annotations

import os
from collections.abc import Callable
from datetime import UTC, datetime
from typing import Any

from mcp.server import MCPServer
from mcp.server.mcpserver.exceptions import ToolError
from mcp.server.transport_security import TransportSecuritySettings
from mcp.types import ToolAnnotations
from starlette.requests import Request
from starlette.responses import JSONResponse

from . import tools
from .nightscout import Nightscout, NightscoutError

INSTRUCTIONS = """\
Read-only data about the user's diabetes devices from their Nightscout: Dexcom
CGM glucose, the Loop app (insulin on board, carbs on board, forecast), the
Omnipod pump, and their therapy settings. Nothing here can change a setting or
deliver insulin.

Every time comes in three forms: UTC, local wall-clock time, and relative to
now. Lead with the relative or local form, and say how old a value is when it
is more than a few minutes old: a stale reading means the phone stopped
uploading, not that glucose is steady.

The local times are in the zone named in `time_zone`. Without a `timezone`
argument that is the pump's time zone, which can lag behind the user after
travel. When you know where the user is (they said so, or it is in context),
pass `timezone` as an IANA name such as "Asia/Tokyo".
"""

READ = ToolAnnotations(read_only_hint=True, open_world_hint=False)


def build(ns: Nightscout, cfg: tools.Config, allowed_hosts: list[str],
          now: Callable[[], datetime] = lambda: datetime.now(UTC)) -> Any:
    mcp = MCPServer(name="diabetes", title="Diabetes devices", instructions=INSTRUCTIONS)

    async def call(fn: Any, **kwargs: Any) -> Any:
        try:
            return await fn(ns, now(), cfg, **kwargs)
        except (NightscoutError, ValueError) as e:
            raise ToolError(str(e)) from None

    @mcp.tool(annotations=READ)
    async def current_status(timezone: str | None = None) -> dict[str, Any]:
        """Right now: the newest glucose reading and trend, insulin and carbs on board, Loop's forecast (eventual
        and lowest), the recommended bolus, when Loop last ran, pump suspended/bolusing and reservoir, any active
        override, and phone battery. `warnings` lists stale data.
        `timezone`: IANA zone for local times, e.g. "Europe/Paris"; default: the pump's time zone."""
        return await call(tools.current_status, timezone=timezone)

    @mcp.tool(annotations=READ)
    async def device_lifetimes(timezone: str | None = None) -> dict[str, Any]:
        """When the Omnipod pod and the CGM sensor were started, when each expires and when its grace period ends,
        plus the Loop app's version. Use this for "when do I change my pod/sensor?".
        `timezone`: IANA zone for local times, e.g. "Europe/Paris"; default: the pump's time zone."""
        return await call(tools.device_lifetimes, timezone=timezone)

    @mcp.tool(annotations=READ)
    async def glucose_history(hours: float = 24, end: str | None = None,
                              timezone: str | None = None) -> dict[str, Any]:
        """CGM glucose over the `hours` (max 336) before `end` (ISO 8601; default now): summary (mean, SD, CV, GMI,
        time in range 70-180, below 70 and 54, above 180 and 250, coverage) and up to 300 evenly spaced readings.
        `timezone`: IANA zone for local times, e.g. "Europe/Paris"; default: the pump's time zone."""
        return await call(tools.glucose_history, hours=hours, end=end, timezone=timezone)

    @mcp.tool(annotations=READ)
    async def treatments(hours: float = 24, end: str | None = None, event_type: str | None = None,
                         include_temp_basals: bool = False, timezone: str | None = None) -> dict[str, Any]:
        """Treatments over the `hours` (max 336) before `end` (ISO 8601; default now), oldest first, with daily
        bolus and carb totals. `event_type` filters to one Nightscout type: "Correction Bolus" (Loop's boluses,
        automatic or not), "Carb Correction", "Temp Basal", "Temporary Override", "Site Change", "Sensor Start".
        Temp basals are left out unless asked for (Loop sets one every 5 minutes).
        `timezone`: IANA zone for local times, e.g. "Europe/Paris"; default: the pump's time zone."""
        return await call(tools.treatments, hours=hours, end=end, event_type=event_type,
                          include_temp_basals=include_temp_basals, timezone=timezone)

    @mcp.tool(annotations=READ)
    async def therapy_settings(timezone: str | None = None) -> dict[str, Any]:
        """The active Loop therapy settings: basal schedule, insulin sensitivity, carb ratio, correction targets,
        insulin action time, Loop's dosing limits and override presets. Schedule start times are on the pump's
        clock (`schedule_time_zone`).
        `timezone`: IANA zone for local times, e.g. "Europe/Paris"; default: the pump's time zone."""
        return await call(tools.therapy_settings, timezone=timezone)

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
