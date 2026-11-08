"""Full syncs are refused, except one download into a pristine collection at bootstrap."""

from __future__ import annotations

import pytest
from anki.collection import Collection
from anki.sync_pb2 import SyncCollectionResponse as R

from anki_mcp import guard
from anki_mcp.guard import FullSyncRefused


def test_full_sync_requested_later_is_refused(anki, phone):
    anki.add_cards("s.md", "A", [{"key": "k", "front": "f", "back": "b"}])
    # The user changes a notetype on the phone: that forces a full sync, which the phone uploads.
    phone.sync()
    m = phone.col.models.by_name("Basic")
    phone.col.models.add_field(m, phone.col.models.new_field("Extra"))
    phone.col.models.update_dict(m, skip_checks=True)
    phone.sync()
    server_before = phone.snapshot()

    with pytest.raises(FullSyncRefused):
        anki.list_decks()
    with pytest.raises(FullSyncRefused):
        anki.add_cards("s.md", "A", [{"key": "k2", "front": "f2", "back": "b"}])
    phone.sync()
    assert phone.snapshot() == server_before


def test_bootstrap_refuses_upload_to_empty_server(sync_endpoint, tmp_path):
    a = guard.Anki(str(tmp_path / "c" / "collection.anki2"), str(tmp_path / "c" / "state"),
                   guard.SyncServer(sync_endpoint, "test", "test-password"))
    a.start()  # serves, but every call refuses the upload
    with pytest.raises(FullSyncRefused):
        a.list_decks()
    a.close()
    probe = Collection(str(tmp_path / "probe.anki2"))
    auth = probe.sync_login("test", "test-password", sync_endpoint)
    assert probe.sync_collection(auth, False).required == R.NO_CHANGES  # still empty
    probe.close()


def test_existing_collection_without_snapshot_is_quarantined(phone, make_anki, tmp_path):
    a = make_anki("x")
    a.close()
    (tmp_path / "x" / "state" / "snapshot.json").unlink()
    b = make_anki("x")
    assert b.quarantine and "no snapshot" in b.quarantine[0]
