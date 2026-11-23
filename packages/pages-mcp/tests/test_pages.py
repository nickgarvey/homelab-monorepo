"""The store's rules against a moto S3, and the MCP HTTP app on top of it."""

from __future__ import annotations

import boto3
import pytest
from moto import mock_aws
from starlette.testclient import TestClient

from pages_mcp import server, store

HEADERS = {"accept": "application/json, text/event-stream", "content-type": "application/json",
           "host": "mcp.home.garvey.sh", "mcp-protocol-version": "2025-11-25"}


@pytest.fixture
def pages(monkeypatch):
    for k in ("AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"):
        monkeypatch.setenv(k, "test")
    with mock_aws():
        s3 = boto3.client("s3", region_name="us-east-1")
        s3.create_bucket(Bucket="pages")
        yield store.Pages(s3)


def test_create_read_list(pages):
    r = pages.create("trips/2026/index.html", "<h1>Japan</h1>")
    assert r["url"] == "https://pages.home.garvey.sh/trips/2026/"
    assert r["permanent"] is True
    pages.create("report.html", "<p>hi</p>")

    got = pages.read("trips/2026/index.html")
    assert got["content"] == "<h1>Japan</h1>" and got["truncated"] is False
    head = pages.s3.head_object(Bucket="pages", Key="report.html")
    assert head["ContentType"] == "text/html; charset=utf-8"

    listing = pages.list()
    assert listing["total"] == 2
    assert {f["path"] for f in listing["files"]} == {"report.html", "trips/2026/index.html"}
    assert [f["path"] for f in pages.list("trips")["files"]] == ["trips/2026/index.html"]
    assert pages.list("/trips/")["folder"] == "trips/"


def test_existing_file_is_never_replaced(pages):
    pages.create("a.html", "first")
    with pytest.raises(store.Refused, match="already exists"):
        pages.create("a.html", "second")
    assert pages.read("a.html")["content"] == "first"


@pytest.mark.parametrize("path", ["/a.html", "a/../b.html", "a//b.html", "a.exe", "a", ".hidden.html",
                                  "a b.html", "x/", "../etc.html"])
def test_bad_paths(pages, path):
    with pytest.raises(store.Refused):
        pages.create(path, "x")


def test_read_missing_and_large(pages):
    with pytest.raises(store.Refused, match="no file"):
        pages.read("nope.html")
    pages.s3.put_object(Bucket="pages", Key="big.txt", Body=b"x" * (store.MAX_READ_BYTES + 10))
    got = pages.read("big.txt")
    assert got["truncated"] is True and len(got["content"]) == store.MAX_READ_BYTES


def test_too_large(pages):
    with pytest.raises(store.Refused, match="limit"):
        pages.create("big.html", "x" * (store.MAX_CREATE_BYTES + 1))


def rpc(client, method, params=None):
    return client.post("/mcp", headers=HEADERS, json={"jsonrpc": "2.0", "id": 1, "method": method,
                                                      "params": params or {}})


def test_http_app(pages):
    app = server.build(pages, ["mcp.home.garvey.sh"])
    with TestClient(app, base_url="http://mcp.home.garvey.sh") as client:
        init = rpc(client, "initialize", {"protocolVersion": "2025-11-25", "capabilities": {},
                                          "clientInfo": {"name": "test", "version": "0"}})
        assert init.status_code == 200, init.text
        assert "permanent" in init.json()["result"]["instructions"]

        tools = {t["name"]: t for t in rpc(client, "tools/list").json()["result"]["tools"]}
        assert set(tools) == {"list_pages", "read_page", "create_page"}
        assert tools["list_pages"]["annotations"]["readOnlyHint"] is True
        assert tools["create_page"]["annotations"]["destructiveHint"] is False
        assert "PERMANENT" in tools["create_page"]["description"]

        r = rpc(client, "tools/call", {"name": "create_page", "arguments": {"path": "x.html", "content": "hi"}})
        assert r.json()["result"]["isError"] is False, r.text
        r = rpc(client, "tools/call", {"name": "create_page", "arguments": {"path": "x.html", "content": "hi"}})
        assert r.json()["result"]["isError"] is True
        assert "already exists" in r.json()["result"]["content"][0]["text"]

        bad = client.post("/mcp", headers={**HEADERS, "host": "evil.example"},
                          json={"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}})
        assert bad.status_code == 421
        assert client.get("/healthz").json() == {"status": "ok"}
