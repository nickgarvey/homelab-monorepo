"""Layer 2: changes that bypass the guard never leave the server."""

from __future__ import annotations

import pytest

from anki_mcp import guard
from anki_mcp.guard import Anki, Quarantined


def tripped(anki: Anki, phone, damage) -> None:
    phone.sync()
    before = phone.snapshot()
    damage(anki._raw_collection_for_tests())
    with pytest.raises(Quarantined):
        anki.list_decks()
    assert anki.quarantine
    phone.sync()
    assert phone.snapshot() == before, "the forbidden change reached the sync server"


def foreign_nid(col) -> int:
    return col.db.scalar("select id from notes where sfld = 'user card 1'")


def test_edit_of_foreign_note(anki, phone):
    def damage(col):
        note = col.get_note(foreign_nid(col))
        note["Back"] = "clobbered"
        col.update_note(note)
    tripped(anki, phone, damage)


def test_delete_of_foreign_note(anki, phone):
    tripped(anki, phone, lambda col: col.remove_notes([foreign_nid(col)]))


def test_delete_of_foreign_deck(anki, phone):
    tripped(anki, phone, lambda col: col.decks.remove([col.decks.id("Spanish", create=False)]))


def test_rename_of_foreign_deck_into_duet(anki, phone):
    tripped(anki, phone, lambda col: col.decks.rename(col.decks.id("Spanish", create=False), "Duet::Stolen"))


def test_move_of_owned_card_out_of_duet(anki, phone):
    nid = anki.add_cards("s.md", "A", [{"key": "k", "front": "f", "back": "b"}])[0]["note_id"]

    def damage(col):
        col.set_deck(col.db.list("select id from cards where nid = ?", nid), col.decks.id("Spanish", create=False))
    tripped(anki, phone, damage)


def test_suspend_of_foreign_card(anki, phone):
    tripped(anki, phone, lambda col: col.sched.suspend_cards(
        col.db.list("select id from cards where nid = ?", foreign_nid(col))))


def test_notetype_change(anki, phone):
    def damage(col):
        m = col.models.by_name("Basic")
        col.models.add_field(m, col.models.new_field("Extra"))
        col.models.update_dict(m, skip_checks=True)
    tripped(anki, phone, damage)


def test_change_to_shared_preset(anki, phone):
    def damage(col):
        conf = col.decks.get_config(1)
        conf["new"]["perDay"] = 999
        col.decks.update_config(conf)
    tripped(anki, phone, damage)


def test_quarantine_survives_restart(anki, make_anki, phone):
    tripped(anki, phone, lambda col: col.remove_notes([foreign_nid(col)]))
    anki.close()
    again = make_anki("mcp")
    assert again.quarantine
    with pytest.raises(Quarantined):
        again.find_cards("")


def test_damage_while_stopped_quarantines_at_start(anki, make_anki, phone, tmp_path):
    from anki.collection import Collection
    phone.sync()
    before = phone.snapshot()
    anki.close()
    raw = Collection(str(tmp_path / "mcp" / "collection.anki2"))
    note = raw.get_note(foreign_nid(raw))
    note["Back"] = "clobbered offline"
    raw.update_note(note)
    raw.close()

    again = make_anki("mcp")  # starts, does not raise
    assert again.quarantine and "outside Duet" in again.quarantine[0]
    phone.sync()
    assert phone.snapshot() == before

    again.close()
    again.reset()
    fresh = make_anki("mcp")
    assert fresh.quarantine is None
    assert fresh.find_cards("Back:clobbered*")["total"] == 0


def test_owned_changes_pass_the_audit(anki):
    nid = anki.add_cards("s.md", "A", [{"key": "k", "front": "f", "back": "b"}])[0]["note_id"]
    col = anki._raw_collection_for_tests()
    note = col.get_note(nid)
    note["Back"] = "raw but owned"
    col.update_note(note)
    anki.list_decks()
    assert anki.quarantine is None


def test_local_offset_change_passes_the_audit(anki):
    # Anki rewrites localOffset on its own when the UTC offset changes.
    col = anki._raw_collection_for_tests()
    col.set_config("localOffset", (col.get_config("localOffset", 0) or 0) + 60)
    anki.list_decks()
    assert anki.quarantine is None


def test_audit_unit_new_notetype_only_when_allowed(anki):
    col = anki._raw_collection_for_tests()
    snap = guard.read_state(col)
    m = col.models.new("Sneaky")
    col.models.add_field(m, col.models.new_field("F"))
    t = col.models.new_template("C")
    t["qfmt"], t["afmt"] = "{{F}}", "{{F}}"
    col.models.add_template(m, t)
    col.models.add(m)
    assert any("Sneaky" in v for v in guard.audit(col, snap))
    assert not any("Sneaky" in v for v in guard.audit(col, snap, allow_new_notetypes={m["id"]}))
    # An existing notetype is never allowed, even by id.
    basic = col.models.by_name("Basic")
    col.models.add_field(basic, col.models.new_field("X"))
    col.models.update_dict(basic, skip_checks=True)
    assert any("Basic" in v for v in guard.audit(col, snap, allow_new_notetypes={m["id"], basic["id"]}))


def test_failed_first_sync_keeps_the_notetype_allowance(phone, make_anki, tmp_path, monkeypatch):
    calls = {"n": 0}
    real = guard.SyncServer.sync

    def flaky(self, col):
        calls["n"] += 1
        if calls["n"] == 2:  # the setup sync, right after the bootstrap download
            raise OSError("network down")
        return real(self, col)

    monkeypatch.setattr(guard.SyncServer, "sync", flaky)
    with pytest.raises(OSError):
        make_anki("flaky")
    monkeypatch.setattr(guard.SyncServer, "sync", real)
    again = make_anki("flaky")
    assert again.quarantine is None
    assert again.list_decks()
    assert not (tmp_path / "flaky" / "state" / "created-notetypes.json").exists()
