"""The MCP HTTP app: tools listed read-only, a call, an error as a tool error, host checking, health."""

from __future__ import annotations

from starlette.testclient import TestClient

from conftest import NOW
from diabetes_mcp import server

HEADERS = {"accept": "application/json, text/event-stream", "content-type": "application/json",
           "host": "mcp.home.garvey.sh", "mcp-protocol-version": "2025-11-25"}


def rpc(client, method, params=None, headers=HEADERS):
    return client.post("/mcp", headers=headers, json={"jsonrpc": "2.0", "id": 1, "method": method,
                                                      "params": params or {}})


def test_http_app(ns, fake, cfg):
    app = server.build(ns, cfg, ["mcp.home.garvey.sh"], now=lambda: NOW)
    with TestClient(app, base_url="http://mcp.home.garvey.sh") as client:
        init = rpc(client, "initialize", {"protocolVersion": "2025-11-25", "capabilities": {},
                                          "clientInfo": {"name": "test", "version": "0"}})
        assert init.status_code == 200, init.text
        assert "timezone" in init.json()["result"]["instructions"]

        tools = {t["name"]: t for t in rpc(client, "tools/list").json()["result"]["tools"]}
        assert tools.keys() == {"current_status", "device_lifetimes", "glucose_history", "treatments",
                                "therapy_settings"}
        assert all(t["annotations"]["readOnlyHint"] is True for t in tools.values())
        assert all("IANA" in t["description"] for t in tools.values())

        r = rpc(client, "tools/call", {"name": "device_lifetimes", "arguments": {}})
        result = r.json()["result"]
        assert result.get("isError") is not True, r.text
        assert "2026-11-27T07:25:25Z" in result["content"][0]["text"]

        r = rpc(client, "tools/call", {"name": "current_status", "arguments": {"timezone": "Nowhere/Land"}})
        assert r.json()["result"]["isError"] is True

        fake.status = 503
        r = rpc(client, "tools/call", {"name": "current_status", "arguments": {}})
        result = r.json()["result"]
        assert result["isError"] is True and "503" in result["content"][0]["text"]

        assert rpc(client, "tools/list", headers=dict(HEADERS, host="evil.example")).status_code == 421
        assert client.get("/healthz").json() == {"status": "ok"}
