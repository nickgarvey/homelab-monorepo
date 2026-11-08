"""The MCP HTTP app: tool listing, a call, a refusal as a tool error, host checking, health."""

from __future__ import annotations

from starlette.testclient import TestClient

from anki_mcp import server

HEADERS = {"accept": "application/json, text/event-stream", "content-type": "application/json",
           "host": "mcp.home.garvey.sh", "mcp-protocol-version": "2025-11-25"}


def rpc(client, method, params=None, id=1, headers=HEADERS):
    return client.post("/mcp", headers=headers,
                       json={"jsonrpc": "2.0", "id": id, "method": method, "params": params or {}})


def test_http_app(anki, phone):
    app = server.build(anki, ["mcp.home.garvey.sh"])
    with TestClient(app, base_url="http://mcp.home.garvey.sh") as client:
        init = rpc(client, "initialize", {"protocolVersion": "2025-11-25", "capabilities": {},
                                          "clientInfo": {"name": "test", "version": "0"}})
        assert init.status_code == 200, init.text
        assert "card_writing_guide" in init.json()["result"]["instructions"]

        tools = {t["name"]: t for t in rpc(client, "tools/list").json()["result"]["tools"]}
        assert {"list_decks", "find_cards", "due_summary", "add_cards", "delete_notes", "trash_notes",
                "card_writing_guide"} <= tools.keys()
        assert tools["list_decks"]["annotations"]["readOnlyHint"] is True
        assert tools["delete_notes"]["annotations"]["destructiveHint"] is True

        # Every card writer sees the rules: in add_cards' description, and in full from the guide tool.
        assert tools["card_writing_guide"]["annotations"]["readOnlyHint"] is True
        assert tools["add_cards"]["annotations"]["idempotentHint"] is True
        assert "key must be stable" in tools["add_cards"]["description"]
        assert server.card_guide.SUMMARY in tools["add_cards"]["description"]
        r = rpc(client, "tools/call", {"name": "card_writing_guide", "arguments": {}})
        assert "# Writing good Anki cards" in r.json()["result"]["content"][0]["text"], r.text

        r = rpc(client, "tools/call", {"name": "add_cards", "arguments": {
            "source_path": "n.md", "deck": "Web", "cards": [{"key": "k", "front": "f", "back": "b"}]}})
        assert r.json()["result"].get("isError") is not True, r.text

        foreign = phone.col.db.scalar("select id from notes where sfld = 'hola'")
        r = rpc(client, "tools/call", {"name": "delete_notes", "arguments": {"note_ids": [foreign]}})
        result = r.json()["result"]
        assert result["isError"] is True and "read-only" in result["content"][0]["text"]

        assert rpc(client, "tools/list", headers=dict(HEADERS, host="evil.example")).status_code == 421
        assert client.get("/healthz").json() == {"status": "ok"}
