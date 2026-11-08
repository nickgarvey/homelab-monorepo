"""Behaviour of the guarded collection against a real sync server."""

from __future__ import annotations

import pytest

from anki_mcp import guard
from anki_mcp.guard import Anki, Refused

SRC = "Books/Hamming/Ch 08.md"


def user_note(phone, front: str) -> int:
    return phone.col.db.scalar("select id from notes where sfld = ?", front)


def test_bootstrap_downloads_and_sets_up(anki: Anki, phone):
    decks = {d["name"]: d for d in anki.list_decks()}
    assert decks["Hamming::Ch 01"]["owned"] is False
    assert decks[guard.ROOT]["owned"] is True
    assert decks[guard.ROOT]["preset"] == guard.ROOT_PRESET
    # The phone gets the note types and root deck with a normal sync.
    before = phone.snapshot()
    assert phone.sync() in (0, 1)  # NO_CHANGES / NORMAL_SYNC
    assert phone.col.models.by_name("Review Basic") is not None
    assert phone.col.models.by_name("Review Cloze") is not None
    assert phone.col.decks.by_name(guard.ROOT) is not None
    after = phone.snapshot()
    assert after["notes"] == before["notes"] and after["cards"] == before["cards"]


def test_restart_keeps_state_and_syncs_normally(anki: Anki, make_anki, phone):
    anki.add_cards(SRC, "Hamming::Ch 08", [{"key": "k1", "front": "q", "back": "a"}])
    anki.close()
    again = make_anki("mcp")
    assert again.find_cards("deck:Duet::Hamming::Ch*")["total"] == 1


def test_guid_is_stable():
    assert guard.guid_for(SRC, "k1") == guard.guid_for(SRC, "k1")
    assert guard.guid_for(SRC, "k1") != guard.guid_for(SRC, "k2")
    assert guard.guid_for("a", "b\0c") != guard.guid_for("a\0b", "c")


def test_deck_names_go_under_root():
    assert guard.full_deck_name("Hamming::Ch 08") == "Duet::Hamming::Ch 08"
    assert guard.full_deck_name("duet :: X") == "Duet::X"
    assert guard.full_deck_name("Duet") == "Duet"
    with pytest.raises(Refused):
        guard.full_deck_name("A::::B")


def test_upsert_updates_in_place_and_keeps_scheduling(anki: Anki, phone):
    first = anki.add_cards(SRC, "Hamming::Ch 08", [
        {"key": "k1", "type": "basic", "front": "What is a code?", "back": "v1"},
        {"key": "k2", "type": "cloze", "front": "{{c1::Hamming}} distance", "back": "", "extra_tags": ["ch 8"]},
    ])
    assert [r["status"] for r in first] == ["created", "created"]
    nid = first[0]["note_id"]
    anki.set_due([nid], "5")
    due_before = anki.find_cards(f"nid:{nid}")["notes"][0]["cards"][0]

    second = anki.add_cards(SRC, "Hamming::Ch 08", [
        {"key": "k1", "type": "basic", "front": "What is a code?", "back": "v2"},
        {"key": "k2", "type": "cloze", "front": "{{c1::Hamming}} distance", "back": "", "extra_tags": ["ch 8"]},
    ])
    assert second[0] == {"key": "k1", "status": "updated", "note_id": nid}
    assert second[1]["status"] == "unchanged"
    view = anki.find_cards(f"nid:{nid}")["notes"][0]
    assert view["fields"]["Back"] == "v2"
    assert view["cards"][0]["due_in_days"] == due_before["due_in_days"]
    assert anki.find_cards("deck:Duet::Hamming::Ch*")["total"] == 2
    assert "review::source::books-hamming-ch-08" in view["tags"]
    assert view["fields"]["Source"] == SRC

    phone.sync()
    assert phone.col.db.scalar("select count() from notes where guid = ?", guard.guid_for(SRC, "k1")) == 1


def test_upsert_refuses_type_change(anki: Anki):
    anki.add_cards(SRC, "X", [{"key": "k", "type": "basic", "front": "f", "back": "b"}])
    r = anki.add_cards(SRC, "X", [{"key": "k", "type": "cloze", "front": "{{c1::f}}", "back": ""}])
    assert r[0]["status"] == "error" and "cannot change" in r[0]["error"]


def test_cloze_without_deletion_is_an_error(anki: Anki):
    r = anki.add_cards(SRC, "X", [{"key": "c", "type": "cloze", "front": "no deletion", "back": ""}])
    assert r[0]["status"] == "error"


def test_guard_refuses_foreign_notes(anki: Anki, phone):
    foreign = user_note(phone, "user card 1")
    for call in (
        lambda: anki.update_note(foreign, {"Back": "x"}, None),
        lambda: anki.move_cards([foreign], "Mine"),
        lambda: anki.set_card_state([foreign], "suspend"),
        lambda: anki.set_due([foreign], "1"),
        lambda: anki.forget([foreign]),
        lambda: anki.delete_notes([foreign]),
        lambda: anki.trash_notes([foreign]),
    ):
        with pytest.raises(Refused):
            call()
    assert anki.quarantine is None


def test_guard_refuses_moves_out_and_foreign_decks(anki: Anki):
    nid = anki.add_cards(SRC, "A", [{"key": "k", "front": "f", "back": "b"}])[0]["note_id"]
    # "Hamming::Ch 01" is put under Duet, so a move can never leave Duet.
    r = anki.move_cards([nid], "Hamming::Ch 01")
    assert r["deck"] == "Duet::Hamming::Ch 01"
    with pytest.raises(Refused):
        anki.delete_deck("Duet")
    with pytest.raises(Refused):
        anki.rename_deck("Duet", "Other")
    with pytest.raises(Refused):
        anki.rename_deck("A", "Duet")


def test_deck_options_only_on_owned_presets(anki: Anki):
    anki.create_deck("Opt")
    r = anki.set_deck_options("Opt", 7, 70, [1, 10], None, False)
    assert r["preset"] == guard.ROOT_PRESET and r["new_per_day"] == 7
    r = anki.set_deck_options("Opt", 3, None, None, None, True)
    assert r["preset"] == "Duet: Duet::Opt" and r["new_per_day"] == 3


def test_shared_preset_is_refused(anki: Anki, phone):
    # A Duet deck that the user pointed at their Default preset: changing it would change their decks too.
    phone.sync()
    did = phone.col.decks.id("Duet::Shared", create=True)
    deck = phone.col.decks.get(did)
    deck["conf"] = 1
    phone.col.decks.save(deck)
    phone.sync()
    with pytest.raises(Refused):
        anki.set_deck_options("Shared", 5, None, None, None, False)
    assert anki.set_deck_options("Shared", 5, None, None, None, True)["new_per_day"] == 5


def test_broad_control_inside_duet(anki: Anki, phone):
    r = anki.add_cards(SRC, "Topic::Sub", [{"key": str(i), "front": f"q{i}", "back": "a"} for i in range(4)])
    n0, n1, n2, n3 = (x["note_id"] for x in r)
    anki.rename_deck("Topic", "Renamed")
    anki.set_card_state([n0], "suspend")
    anki.set_card_state([n0], "unsuspend")
    anki.set_card_state([n1], "bury")
    anki.forget([n1])
    anki.update_note(n2, {"Front": "edited"}, ["t1", "t2"])
    t = anki.trash_notes([n3])
    assert t["deck"] == guard.TRASH
    view = anki.find_cards(f"nid:{n3}")["notes"][0]
    assert view["cards"][0]["queue"] == "suspended" and view["cards"][0]["deck"] == guard.TRASH
    anki.delete_notes([n2])
    anki.delete_deck("Renamed")
    assert anki.find_cards("deck:Duet::Renamed*")["total"] == 0
    assert anki.quarantine is None

    before = phone.snapshot()
    phone.sync()
    after = phone.snapshot()
    user_cards = lambda s: [c for c in s["cards"] if c[1] in {n[0] for n in before["notes"]}]
    assert user_cards(after) == user_cards(before)


def test_due_summary_and_list(anki: Anki):
    anki.add_cards(SRC, "S", [{"key": "k", "front": "f", "back": "b"}])
    summary = {d["deck"]: d for d in anki.due_summary()}
    assert summary["Duet::S"]["new"] == 1 and summary["Duet::S"]["owned"]
    assert summary["Spanish"]["owned"] is False


def test_user_moving_a_card_into_duet_makes_it_owned(anki: Anki, phone):
    nid = user_note(phone, "hola")
    cids = phone.col.db.list("select id from cards where nid = ?", nid)
    phone.col.set_deck(cids, phone.col.decks.id("Duet::Adopted", create=True))
    phone.sync()
    anki.update_note(nid, {"Back": "hello"}, None)
    assert anki.quarantine is None
