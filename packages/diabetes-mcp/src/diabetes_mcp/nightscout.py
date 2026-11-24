"""A read-only client for Nightscout's v1 API.

The token goes in the `token` query parameter: Nightscout accepts only a JWT
in a Bearer header, not an access token. It is read from a file on every
request, so a rotated token is picked up without a restart. Errors never
include the request URL, which carries the token.
"""

from __future__ import annotations

from collections.abc import Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import httpx

# Bounds `find[created_at]` so v1 does not apply its default of the last few
# days, which would hide a 10-day sensor's start.
EPOCH = "2000-01-01"


def iso(t: datetime) -> str:
    """Nightscout stores created_at as UTC text and compares it as text, so match its format."""
    return t.astimezone(UTC).strftime("%Y-%m-%dT%H:%M:%S.") + f"{t.microsecond // 1000:03d}Z"


class NightscoutError(Exception):
    pass


def token_from_file(path: str) -> Callable[[], str]:
    return lambda: Path(path).read_text().strip()


class Nightscout:
    def __init__(self, base_url: str, token: Callable[[], str], transport: httpx.AsyncBaseTransport | None = None,
                 timeout: float = 20.0):
        self._token = token
        self._client = httpx.AsyncClient(base_url=base_url.rstrip("/") + "/api/v1/", timeout=timeout,
                                         transport=transport)

    async def aclose(self) -> None:
        await self._client.aclose()

    async def _get(self, path: str, params: list[tuple[str, str]]) -> Any:
        try:
            r = await self._client.get(path, params=[*params, ("token", self._token())])
        except httpx.RequestError as e:
            raise NightscoutError(f"cannot reach Nightscout ({type(e).__name__})") from None
        except OSError as e:
            raise NightscoutError(f"cannot read the Nightscout token ({type(e).__name__})") from None
        if r.status_code != 200:
            raise NightscoutError(f"Nightscout answered {r.status_code} for /api/v1/{path}")
        try:
            return r.json()
        except ValueError:
            raise NightscoutError(f"Nightscout sent invalid JSON for /api/v1/{path}") from None

    async def entries(self, count: int, start: datetime | None = None,
                      end: datetime | None = None) -> list[dict[str, Any]]:
        params = [("count", str(count)), ("find[type]", "sgv")]
        if start:
            params.append(("find[date][$gte]", str(int(start.timestamp() * 1000))))
        if end:
            params.append(("find[date][$lte]", str(int(end.timestamp() * 1000))))
        return await self._get("entries.json", params)

    async def devicestatus(self, count: int = 1) -> list[dict[str, Any]]:
        return await self._get("devicestatus.json", [("count", str(count))])

    async def treatments(self, count: int, start: datetime | None = None, end: datetime | None = None,
                         event_type: str | None = None, exclude_event_type: str | None = None) -> list[dict[str, Any]]:
        params = [("count", str(count)), ("find[created_at][$gte]", iso(start) if start else EPOCH)]
        if end:
            params.append(("find[created_at][$lte]", iso(end)))
        if event_type:
            params.append(("find[eventType]", event_type))
        if exclude_event_type:
            params.append(("find[eventType][$ne]", exclude_event_type))
        return await self._get("treatments.json", params)

    async def latest_treatment(self, event_type: str) -> dict[str, Any] | None:
        found = await self.treatments(1, event_type=event_type)
        return found[0] if found else None

    async def profile(self) -> dict[str, Any] | None:
        found = await self._get("profile.json", [("count", "1")])
        return found[0] if found else None
