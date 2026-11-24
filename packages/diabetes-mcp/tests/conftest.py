"""A fake Nightscout serving the recorded-shape fixtures (made-up values) through httpx.MockTransport."""

from __future__ import annotations

import json
from datetime import UTC, datetime
from pathlib import Path
from zoneinfo import ZoneInfo

import httpx
import pytest

from diabetes_mcp import tools
from diabetes_mcp.nightscout import Nightscout

FIXTURES = Path(__file__).parent / "fixtures"
TOKEN = "test-token-abc123"
# 10:30 PST on the day the fixtures were recorded.
NOW = datetime(2026, 11, 24, 18, 30, tzinfo=UTC)


def load(name: str):
    return json.loads((FIXTURES / name).read_text())


class FakeNightscout:
    def __init__(self):
        self.data = {name: load(f"{name}.json") for name in ("entries", "devicestatus", "profile", "treatments")}
        self.requests: list[httpx.Request] = []
        self.status = 200

    def handler(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        if request.url.params.get("token") != TOKEN:
            return httpx.Response(401)
        if self.status != 200:
            return httpx.Response(self.status)
        name = request.url.path.rsplit("/", 1)[-1].removesuffix(".json")
        docs = list(self.data[name])
        params = request.url.params
        if name == "treatments":
            if (want := params.get("find[eventType]")):
                docs = [d for d in docs if d["eventType"] == want]
            if (skip := params.get("find[eventType][$ne]")):
                docs = [d for d in docs if d["eventType"] != skip]
            if (lo := params.get("find[created_at][$gte]")):
                docs = [d for d in docs if d["created_at"] >= lo]
            if (hi := params.get("find[created_at][$lte]")):
                docs = [d for d in docs if d["created_at"] <= hi]
            docs.sort(key=lambda d: d["created_at"], reverse=True)
        if name == "entries":
            if (lo := params.get("find[date][$gte]")):
                docs = [d for d in docs if d["date"] >= int(lo)]
            if (hi := params.get("find[date][$lte]")):
                docs = [d for d in docs if d["date"] <= int(hi)]
            docs.sort(key=lambda d: d["date"], reverse=True)
        return httpx.Response(200, json=docs[: int(params.get("count", "10"))])


@pytest.fixture
def fake():
    return FakeNightscout()


@pytest.fixture
def ns(fake):
    return Nightscout("https://nightscout.test", lambda: TOKEN, transport=httpx.MockTransport(fake.handler))


@pytest.fixture
def cfg():
    return tools.Config(home=ZoneInfo("America/Los_Angeles"))
