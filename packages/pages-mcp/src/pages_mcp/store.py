"""The `pages` bucket as Claude may use it: list, read, and create new files; never change or delete one.

The RGW role this runs as (pages-mcp in modules/services/rgw-roles.nix) has no
delete permission. Overwriting is refused here: create checks that the key is
free first.
"""

from __future__ import annotations

import posixpath
import re
from typing import Any

from botocore.exceptions import ClientError

SITE = "https://pages.home.garvey.sh"

# Text files a static page is made of. Caddy picks Content-Type from the
# extension when it serves them; the S3 metadata is set to match.
CONTENT_TYPES = {
    ".html": "text/html; charset=utf-8",
    ".htm": "text/html; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".js": "text/javascript; charset=utf-8",
    ".mjs": "text/javascript; charset=utf-8",
    ".json": "application/json",
    ".svg": "image/svg+xml",
    ".txt": "text/plain; charset=utf-8",
    ".md": "text/markdown; charset=utf-8",
    ".csv": "text/csv; charset=utf-8",
    ".xml": "application/xml",
}

MAX_CREATE_BYTES = 5 * 1024 * 1024
MAX_READ_BYTES = 1024 * 1024
SEGMENT = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*")


class Refused(Exception):
    """A request the rules do not allow; the message says why."""


def check_path(path: str) -> str:
    """A file path such as "notes/2026/trip.html": folders separated by "/", no leading slash."""
    if len(path) > 512:
        raise Refused("path is longer than 512 characters")
    parts = path.split("/")
    for part in parts:
        if not SEGMENT.fullmatch(part) or part in (".", ".."):
            raise Refused(f"invalid path {path!r}: each folder and file name must start with a letter or digit "
                          "and contain only letters, digits, '.', '_' and '-', separated by single '/'")
    ext = posixpath.splitext(parts[-1])[1].lower()
    if ext not in CONTENT_TYPES:
        raise Refused(f"invalid path {path!r}: the file must end in one of {', '.join(sorted(CONTENT_TYPES))}")
    return path


LAYOUT = ("New files go in <origin>/<topic>/: either <origin>/<topic>/<page>.html or inside a subfolder, "
          "<origin>/<topic>/<subfolder>/... . <origin> is the app creating the page (claude-web, "
          "claude-desktop, claude-code, claude-duet, codex-desktop, ...), <topic> what it is about. Nothing at the top level, directly under "
          "<origin>, and no index.html directly under <origin>/<topic>/.")


def check_layout(path: str) -> str:
    """The folder layout for new files (LAYOUT); reading and listing are not restricted."""
    parts = path.split("/")
    if len(parts) < 3:
        raise Refused(f"{path!r} is not allowed here. {LAYOUT}")
    if len(parts) == 3 and parts[2].lower() in ("index.html", "index.htm"):
        raise Refused(f"{path!r}: index.html is not allowed directly under <origin>/<topic>/; name the page, "
                      f"or put it in a subfolder ({parts[0]}/{parts[1]}/<subfolder>/index.html). {LAYOUT}")
    return path


def check_folder(folder: str) -> str:
    folder = folder.strip("/")
    if folder == "":
        return ""
    for part in folder.split("/"):
        if not SEGMENT.fullmatch(part) or part in (".", ".."):
            raise Refused(f"invalid folder {folder!r}")
    return folder + "/"


def url(key: str) -> str:
    if key == "index.html":
        return SITE + "/"
    if key.endswith("/index.html"):
        return f"{SITE}/{key[:-len('index.html')]}"
    return f"{SITE}/{key}"


class Pages:
    def __init__(self, s3: Any, bucket: str = "pages") -> None:
        self.s3 = s3
        self.bucket = bucket

    def list(self, folder: str = "", limit: int = 200) -> dict[str, Any]:
        prefix = check_folder(folder)
        files = []
        for page in self.s3.get_paginator("list_objects_v2").paginate(Bucket=self.bucket, Prefix=prefix):
            for o in page.get("Contents", []):
                files.append({"path": o["Key"], "url": url(o["Key"]), "size": o["Size"],
                              "last_modified": o["LastModified"].isoformat()})
        files.sort(key=lambda f: f["last_modified"], reverse=True)
        limit = max(1, min(limit, 1000))
        return {"folder": prefix, "total": len(files), "files": files[:limit]}

    def read(self, path: str) -> dict[str, Any]:
        key = check_path(path)
        try:
            head = self.s3.head_object(Bucket=self.bucket, Key=key)
        except ClientError as e:
            if e.response["Error"]["Code"] in ("NoSuchKey", "404", "NotFound"):
                raise Refused(f"no file at {path!r}; list_pages shows what exists") from e
            raise
        size = head["ContentLength"]
        extra = {"Range": f"bytes=0-{MAX_READ_BYTES - 1}"} if size > MAX_READ_BYTES else {}
        o = self.s3.get_object(Bucket=self.bucket, Key=key, **extra)
        body = o["Body"].read()
        return {"path": key, "url": url(key), "size": size, "truncated": size > len(body),
                "last_modified": o["LastModified"].isoformat(),
                "content": body.decode("utf-8", errors="replace")}

    def exists(self, key: str) -> bool:
        try:
            self.s3.head_object(Bucket=self.bucket, Key=key)
            return True
        except ClientError as e:
            if e.response["Error"]["Code"] in ("NoSuchKey", "404", "NotFound"):
                return False
            raise

    def create(self, path: str, content: str) -> dict[str, Any]:
        key = check_layout(check_path(path))
        data = content.encode("utf-8")
        if len(data) > MAX_CREATE_BYTES:
            raise Refused(f"content is {len(data)} bytes; the limit is {MAX_CREATE_BYTES}")
        if self.exists(key):
            raise Refused(f"{path!r} already exists, and existing files cannot be changed or deleted. "
                          "Choose a new path (for example add -v2 to the name).")
        ext = posixpath.splitext(key)[1].lower()
        self.s3.put_object(Bucket=self.bucket, Key=key, Body=data, ContentType=CONTENT_TYPES[ext])
        return {"path": key, "url": url(key), "size": len(data), "permanent": True}
