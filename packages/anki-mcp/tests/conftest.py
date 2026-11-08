"""Fixtures: a real anki-sync-server on localhost, a "phone" client, and the guarded server under test."""

from __future__ import annotations

import os
import shutil
import socket
import subprocess
import time
from pathlib import Path

import pytest
from anki.collection import Collection
from anki.sync_pb2 import SyncCollectionResponse as R

from anki_mcp import guard

USER, PASSWORD = "test", "test-password"


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def sync_endpoint(tmp_path: Path):
    exe = os.environ.get("ANKI_SYNC_SERVER_BIN") or shutil.which("anki-sync-server")
    assert exe, "set ANKI_SYNC_SERVER_BIN or put anki-sync-server on PATH"
    port = _free_port()
    env = dict(os.environ, SYNC_USER1=f"{USER}:{PASSWORD}", SYNC_BASE=str(tmp_path / "server"),
               SYNC_HOST="127.0.0.1", SYNC_PORT=str(port), RUST_LOG="warn")
    proc = subprocess.Popen([exe], env=env)
    deadline = time.time() + 30
    while time.time() < deadline:
        try:
            socket.create_connection(("127.0.0.1", port), timeout=0.2).close()
            break
        except OSError:
            time.sleep(0.1)
    else:
        proc.kill()
        raise RuntimeError("anki-sync-server did not start")
    yield f"http://127.0.0.1:{port}/"
    proc.terminate()
    proc.wait(10)


class Phone:
    """Another client of the same account: the user's phone or desktop."""

    def __init__(self, path: Path, endpoint: str):
        self.col = Collection(str(path))
        self.auth = self.col.sync_login(USER, PASSWORD, endpoint)

    def sync(self) -> int:
        out = self.col.sync_collection(self.auth, False)
        if out.required in (R.FULL_UPLOAD, R.FULL_SYNC):
            self.col.close_for_full_sync()
            self.col.full_upload_or_download(auth=self.auth, server_usn=out.server_media_usn, upload=True)
            self.col.reopen(after_full_sync=True)
        elif out.required == R.FULL_DOWNLOAD:
            self.col.close_for_full_sync()
            self.col.full_upload_or_download(auth=self.auth, server_usn=out.server_media_usn, upload=False)
            self.col.reopen(after_full_sync=True)
        return out.required

    def add_basic(self, deck: str, front: str, back: str = "b") -> int:
        did = self.col.decks.id(deck, create=True)
        note = self.col.new_note(self.col.models.by_name("Basic"))
        note["Front"], note["Back"] = front, back
        self.col.add_note(note, did)
        return note.id

    def snapshot(self) -> dict:
        """Everything about the user's own (non-Duet) content, to show it is unchanged."""
        db = self.col.db
        return {
            "notes": db.all("select id, guid, mid, tags, flds from notes order by id"),
            "cards": db.all("select id, nid, did, queue, due, ivl from cards order by id"),
            "decks": sorted((d.id, d.name) for d in self.col.decks.all_names_and_ids()),
        }


@pytest.fixture
def phone(tmp_path: Path, sync_endpoint: str):
    """A phone that already has a collection with the user's own deck, uploaded to the server."""
    (tmp_path / "phone").mkdir()
    p = Phone(tmp_path / "phone" / "collection.anki2", sync_endpoint)
    p.add_basic("Hamming::Ch 01", "user card 1")
    p.add_basic("Hamming::Ch 01", "user card 2")
    p.add_basic("Spanish", "hola")
    p.sync()
    yield p
    p.col.close()


@pytest.fixture
def make_anki(tmp_path: Path, sync_endpoint: str):
    made: list[guard.Anki] = []

    def make(name: str = "mcp") -> guard.Anki:
        d = tmp_path / name
        a = guard.Anki(str(d / "collection.anki2"), str(d / "state"),
                       guard.SyncServer(sync_endpoint, USER, PASSWORD))
        a.start()
        made.append(a)
        return a

    yield make
    for a in made:
        a.close()


@pytest.fixture
def anki(phone, make_anki) -> guard.Anki:
    return make_anki()
