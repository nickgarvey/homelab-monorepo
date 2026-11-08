"""The only module that opens the Anki collection.

Everything under the root deck `Duet` belongs to the LLM; everything else is
read-only. Two independent layers enforce that:

1. Every write method in `Anki` checks ownership, under the collection lock,
   before it changes anything.
2. Before every sync, `audit` compares every pending local change (usn = -1)
   with the snapshot of owned ids taken at the last sync. Any change that is
   not to something owned at that time, or created since inside `Duet`, stops
   the sync and quarantines the server until a human clears it.

Full syncs are refused in both directions, except one download into a
pristine local collection at bootstrap.
"""

from __future__ import annotations

import hashlib
import json
import logging
import os
import re
import threading
from collections import defaultdict
from dataclasses import asdict, dataclass
from typing import Any, Callable, Iterable, TypeVar

from anki.collection import Collection
from anki.consts import (
    CARD_TYPE_LRN,
    CARD_TYPE_NEW,
    CARD_TYPE_RELEARNING,
    CARD_TYPE_REV,
    MODEL_CLOZE,
    QUEUE_TYPE_MANUALLY_BURIED,
    QUEUE_TYPE_SIBLING_BURIED,
    QUEUE_TYPE_SUSPENDED,
)
from anki.notes import NoteFieldsCheckResult
from anki.sync_pb2 import SyncCollectionResponse
from anki.utils import base91

log = logging.getLogger(__name__)

ROOT = "Duet"
TRASH = f"{ROOT}::Trash"
ROOT_PRESET = "Duet"

# The two note types the server creates once, at bootstrap. Adding a new
# notetype syncs normally (tested against anki-sync-server 26.09.3); changing
# an existing one would force a full sync, so they are never modified.
NOTETYPES: dict[str, dict[str, Any]] = {
    "basic": {
        "name": "Review Basic",
        "cloze": False,
        "fields": ["Front", "Back", "Source"],
        "qfmt": "{{Front}}",
        "afmt": "{{FrontSide}}<hr id=answer>{{Back}}",
    },
    "cloze": {
        "name": "Review Cloze",
        "cloze": True,
        "fields": ["Text", "Back Extra", "Source"],
        "qfmt": "{{cloze:Text}}",
        "afmt": "{{cloze:Text}}<br>{{Back Extra}}",
    },
}

# Collection config keys that ordinary note, card and deck operations touch
# (add-card position counters, last-used deck and notetype). Anything else
# pending in the config table fails the audit.
ALLOWED_CONFIG_KEYS = {
    "activeDecks",
    "addToCur",
    "curDeck",
    "curModel",
    "lastUnburied",
    # The UTC offset Anki stores for the scheduler's day boundary. Anki itself
    # rewrites it whenever the offset it runs at differs (this pod runs in UTC,
    # the phone in local time, and DST moves the phone's).
    "localOffset",
    "newBury",
    "nextPos",
    "sortBackwards",
    "sortType",
}
ALLOWED_CONFIG_PATTERNS = [re.compile(r"^_nt_\d+_lastDeck$"), re.compile(r"^_deck_\d+_lastNotetype$")]

FULL_SYNC = {
    SyncCollectionResponse.FULL_SYNC: "full sync",
    SyncCollectionResponse.FULL_DOWNLOAD: "full download",
    SyncCollectionResponse.FULL_UPLOAD: "full upload",
}

GRAVE_KINDS = {0: "cards", 1: "notes", 2: "decks"}


class Refused(Exception):
    """A write that would touch something outside Duet, or is otherwise not allowed."""


class Quarantined(Exception):
    """The pre-sync audit found a change it does not allow; nothing syncs until a human resolves it."""


class FullSyncRefused(Exception):
    """The sync server asked for a full sync. Resolved by hand, never by this server."""


def is_owned_name(name: str) -> bool:
    """Deck names are case-insensitive in Anki."""
    folded = name.casefold()
    root = ROOT.casefold()
    return folded == root or folded.startswith(root + "::")


def full_deck_name(name: str) -> str:
    """Puts a deck name under Duet: "Hamming::Ch 08" -> "Duet::Hamming::Ch 08"."""
    parts = [p.strip() for p in name.split("::")]
    if not parts or any(not p for p in parts):
        raise Refused(f"invalid deck name {name!r}")
    if parts[0].casefold() == ROOT.casefold():
        parts[0] = ROOT
    else:
        parts.insert(0, ROOT)
    return "::".join(parts)


def guid_for(source_path: str, key: str) -> str:
    """A stable note GUID: the same source path and key always give the same note."""
    # A JSON pair, so no (path, key) split of the same characters collides.
    digest = hashlib.sha256(json.dumps([source_path, key]).encode()).digest()
    return base91(int.from_bytes(digest[:8], "big"))


def source_tag(source_path: str) -> str:
    stem = re.sub(r"\.md$", "", source_path.strip(), flags=re.IGNORECASE)
    slug = re.sub(r"[^a-z0-9]+", "-", stem.lower()).strip("-")
    return f"review::source::{slug or 'unknown'}"


def clean_tag(tag: str) -> str:
    return re.sub(r"\s+", "_", tag.strip())


@dataclass
class State:
    """Ids in the collection, and which of them are owned. Saved after every sync."""

    scm: int
    notetypes: set[int]
    decks: set[int]
    owned_decks: set[int]
    presets: set[int]
    owned_presets: set[int]
    notes: set[int]
    owned_notes: set[int]
    cards: set[int]
    owned_cards: set[int]

    def to_json(self) -> str:
        return json.dumps({k: sorted(v) if isinstance(v, set) else v for k, v in asdict(self).items()})

    @classmethod
    def from_json(cls, text: str) -> State:
        raw = json.loads(text)
        return cls(**{k: set(v) if isinstance(v, list) else v for k, v in raw.items()})


def read_state(col: Collection) -> State:
    decks = col.decks.all()
    owned_decks = {d["id"] for d in decks if is_owned_name(d["name"])}

    preset_users: dict[int, set[int]] = defaultdict(set)
    for d in decks:
        if not d.get("dyn"):
            preset_users[d["conf"]].add(d["id"])
    presets = {c["id"] for c in col.decks.all_config()}
    owned_presets = {p for p in presets if preset_users.get(p) and preset_users[p] <= owned_decks}

    cards: set[int] = set()
    owned_cards: set[int] = set()
    note_cards: dict[int, list[bool]] = defaultdict(list)
    for cid, nid, did, odid in col.db.execute("select id, nid, did, odid from cards"):
        # A card in a filtered deck counts by its home deck.
        owned = (odid or did) in owned_decks
        cards.add(cid)
        if owned:
            owned_cards.add(cid)
        note_cards[nid].append(owned)

    return State(
        scm=col.db.scalar("select scm from col"),
        notetypes=set(col.db.list("select id from notetypes")),
        decks={d["id"] for d in decks},
        owned_decks=owned_decks,
        presets=presets,
        owned_presets=owned_presets,
        notes=set(col.db.list("select id from notes")),
        owned_notes={nid for nid, flags in note_cards.items() if flags and all(flags)},
        cards=cards,
        owned_cards=owned_cards,
    )


def _was_foreign(oid: int, known: set[int], owned: set[int]) -> bool:
    return oid in known and oid not in owned


def audit(col: Collection, snap: State, allow_new_notetypes: Iterable[int] = ()) -> list[str]:
    """Every pending change that the ownership rules do not allow, as readable lines.

    A pending row is allowed if its object was owned at the last sync (snap),
    or did not exist then, and is owned now (or no longer exists). Pending
    notetype rows are allowed only for ids in allow_new_notetypes that did not
    exist at the last sync (the one-time bootstrap creation).
    """
    cur = read_state(col)
    bad: list[str] = []
    allowed_ids = set(allow_new_notetypes)

    if cur.scm != snap.scm:
        bad.append(f"collection schema changed (scm {snap.scm} -> {cur.scm})")

    for ntid, name in col.db.all("select id, name from notetypes where usn = -1"):
        if ntid in snap.notetypes or ntid not in allowed_ids:
            bad.append(f"notetype {ntid} ({name!r}) changed")

    for did, in col.db.all("select id from decks where usn = -1"):
        if _was_foreign(did, snap.decks, snap.owned_decks) or did not in cur.owned_decks:
            bad.append(f"deck {did} outside {ROOT} changed")

    for pid, in col.db.all("select id from deck_config where usn = -1"):
        if _was_foreign(pid, snap.presets, snap.owned_presets) or pid not in cur.owned_presets:
            bad.append(f"deck options preset {pid} not owned by {ROOT} changed")

    for nid, in col.db.all("select id from notes where usn = -1"):
        if _was_foreign(nid, snap.notes, snap.owned_notes) or nid not in cur.owned_notes:
            bad.append(f"note {nid} outside {ROOT} changed")

    for cid, in col.db.all("select id from cards where usn = -1"):
        if _was_foreign(cid, snap.cards, snap.owned_cards) or cid not in cur.owned_cards:
            bad.append(f"card {cid} outside {ROOT} changed")

    for cid, in col.db.all("select distinct cid from revlog where usn = -1"):
        if _was_foreign(cid, snap.cards, snap.owned_cards) or (cid in cur.cards and cid not in cur.owned_cards):
            bad.append(f"review log entry for card {cid} outside {ROOT}")

    known = {"cards": (snap.cards, snap.owned_cards), "notes": (snap.notes, snap.owned_notes),
             "decks": (snap.decks, snap.owned_decks)}
    for oid, kind in col.db.all("select oid, type from graves where usn = -1"):
        what = GRAVE_KINDS.get(kind)
        if what is None:
            bad.append(f"deletion of unknown kind {kind} (id {oid})")
        elif _was_foreign(oid, *known[what]):
            bad.append(f"deletion of {what[:-1]} {oid} outside {ROOT}")

    for key, in col.db.all("select key from config where usn = -1"):
        if key not in ALLOWED_CONFIG_KEYS and not any(p.match(key) for p in ALLOWED_CONFIG_PATTERNS):
            bad.append(f"collection config {key!r} changed")

    return bad


class SyncServer:
    """The account on the sync server that the collection syncs with. Media sync is off."""

    def __init__(self, endpoint: str, username: str, password: str):
        self.endpoint = endpoint
        self.username = username
        self._password = password
        self._auth = None

    def login(self, col: Collection) -> None:
        self._auth = col.sync_login(self.username, self._password, self.endpoint)

    def sync(self, col: Collection) -> SyncCollectionResponse:
        out = col.sync_collection(self._auth, False)
        if out.new_endpoint:
            self._auth.endpoint = out.new_endpoint
        if out.server_message:
            log.info("sync server message: %s", out.server_message)
        return out

    def full_download(self, col: Collection, out: SyncCollectionResponse) -> None:
        col.close_for_full_sync()
        col.full_upload_or_download(auth=self._auth, server_usn=out.server_media_usn, upload=False)
        col.reopen(after_full_sync=True)


T = TypeVar("T")


def _queue_name(queue: int) -> str | None:
    return {QUEUE_TYPE_SUSPENDED: "suspended", QUEUE_TYPE_SIBLING_BURIED: "buried",
            QUEUE_TYPE_MANUALLY_BURIED: "buried"}.get(queue)


class Anki:
    """The collection, its sync, and every operation the MCP tools can perform."""

    def __init__(self, collection_path: str, state_dir: str, server: SyncServer):
        self._path = collection_path
        self._state_dir = state_dir
        self._server = server
        self._lock = threading.Lock()
        self._col: Collection | None = None
        self._snapshot: State | None = None
        self._quarantine: list[str] | None = None

    # -- lifecycle ---------------------------------------------------------

    @property
    def _snapshot_path(self) -> str:
        return os.path.join(self._state_dir, "snapshot.json")

    @property
    def _quarantine_path(self) -> str:
        return os.path.join(self._state_dir, "quarantine.json")

    @property
    def quarantine(self) -> list[str] | None:
        return self._quarantine

    def start(self) -> None:
        """Opens the collection, bootstraps it if new, and creates the note types and root deck."""
        os.makedirs(self._state_dir, exist_ok=True)
        os.makedirs(os.path.dirname(self._path) or ".", exist_ok=True)
        with self._lock:
            if os.path.exists(self._quarantine_path):
                with open(self._quarantine_path) as f:
                    self._quarantine = json.load(f)
                log.error("starting quarantined: %s", self._quarantine)
            self._col = Collection(self._path)
            self._server.login(self._col)
            if os.path.exists(self._snapshot_path):
                with open(self._snapshot_path) as f:
                    self._snapshot = State.from_json(f.read())
            if self._quarantine is not None:
                return
            if self._snapshot is None:
                self._bootstrap()
            if self._quarantine is None:
                # A tripped audit or a refused full sync here leaves the server
                # up, failing each tool call with the reason, not crash-looping.
                try:
                    self._setup()
                except (Quarantined, FullSyncRefused) as e:
                    log.error("setup did not sync: %s", e)

    def reset(self) -> None:
        """Deletes the local collection and its state, so the next start bootstraps from the sync server.

        The recovery for a quarantine or a refused full sync once the cause is
        understood: the sync server's copy never received the refused change.
        """
        with self._lock:
            if self._col is not None:
                raise RuntimeError("reset with the collection open")
            base = os.path.basename(self._path)
            folder = os.path.dirname(self._path) or "."
            for name in os.listdir(folder) if os.path.isdir(folder) else []:
                if name.startswith(base):
                    os.remove(os.path.join(folder, name))
            for name in ("snapshot.json", "quarantine.json", "created-notetypes.json"):
                path = os.path.join(self._state_dir, name)
                if os.path.exists(path):
                    os.remove(path)

    def close(self) -> None:
        with self._lock:
            if self._col is not None:
                self._col.close()
                self._col = None

    def _bootstrap(self) -> None:
        col = self._col
        pristine = (col.db.scalar("select count() from notes") == 0
                    and col.db.scalar("select count() from cards") == 0
                    and col.db.scalar("select ls from col") == 0)
        if not pristine:
            self._set_quarantine(["local collection exists but has no snapshot; delete it to bootstrap again"])
            return
        out = self._server.sync(col)
        if out.required in (SyncCollectionResponse.FULL_DOWNLOAD, SyncCollectionResponse.FULL_SYNC):
            log.info("bootstrap: full download into the empty local collection")
            self._server.full_download(col, out)
        elif out.required == SyncCollectionResponse.FULL_UPLOAD:
            raise FullSyncRefused("bootstrap: the server collection is empty; refusing to upload over it")
        self._save_snapshot(read_state(col))

    def _setup(self) -> None:
        col = self._col
        created = self._created_notetypes()
        for spec in NOTETYPES.values():
            existing = col.models.by_name(spec["name"])
            if existing is None:
                created.add(self._create_notetype(spec))
                # Kept until a sync succeeds, so a failed first sync does not
                # turn this one allowed creation into a quarantine later.
                with open(self._created_path, "w") as f:
                    json.dump(sorted(created), f)
                continue
            fields = [f["name"] for f in existing["flds"]]
            if fields != spec["fields"] or (existing["type"] == MODEL_CLOZE) != spec["cloze"]:
                self._set_quarantine([f"notetype {spec['name']!r} exists with fields {fields}, expected {spec['fields']}"])
                return
        root = col.decks.by_name(ROOT)
        if root is None:
            col.decks.id(ROOT, create=True)
            root = col.decks.by_name(ROOT)
        if self._preset_id(ROOT_PRESET) is None:
            pid = col.decks.add_config_returning_id(ROOT_PRESET, clone_from=col.decks.get_config(1))
            root["conf"] = pid
            col.decks.save(root)
        self._sync()

    @property
    def _created_path(self) -> str:
        return os.path.join(self._state_dir, "created-notetypes.json")

    def _created_notetypes(self) -> set[int]:
        if not os.path.exists(self._created_path):
            return set()
        with open(self._created_path) as f:
            return set(json.load(f))

    def _create_notetype(self, spec: dict[str, Any]) -> int:
        mm = self._col.models
        m = mm.new(spec["name"])
        if spec["cloze"]:
            m["type"] = MODEL_CLOZE
        for name in spec["fields"]:
            mm.add_field(m, mm.new_field(name))
        t = mm.new_template("Cloze" if spec["cloze"] else "Card 1")
        t["qfmt"] = spec["qfmt"]
        t["afmt"] = spec["afmt"]
        mm.add_template(m, t)
        mm.add(m)
        return m["id"]

    def _preset_id(self, name: str) -> int | None:
        for c in self._col.decks.all_config():
            if c["name"] == name:
                return c["id"]
        return None

    # -- sync and audit ----------------------------------------------------

    def _save_snapshot(self, state: State) -> None:
        tmp = self._snapshot_path + ".tmp"
        with open(tmp, "w") as f:
            f.write(state.to_json())
        os.replace(tmp, self._snapshot_path)
        self._snapshot = state

    def _set_quarantine(self, reasons: list[str]) -> None:
        self._quarantine = reasons
        with open(self._quarantine_path, "w") as f:
            json.dump(reasons, f, indent=1)
        log.error("QUARANTINED, not syncing until a human resolves it: %s", reasons)

    def _check_quarantine(self) -> None:
        if self._quarantine is not None:
            raise Quarantined(
                "the Anki server is quarantined and will not sync until a human resolves it: "
                + "; ".join(self._quarantine[:20]))

    def _sync(self) -> None:
        self._check_quarantine()
        bad = audit(self._col, self._snapshot, self._created_notetypes())
        if bad:
            self._set_quarantine(bad)
            self._check_quarantine()
        out = self._server.sync(self._col)
        if out.required in FULL_SYNC:
            msg = (f"the sync server requires a {FULL_SYNC[out.required]}; refusing. "
                   "A human has to resolve this by hand.")
            log.error(msg)
            raise FullSyncRefused(msg)
        self._save_snapshot(read_state(self._col))
        if os.path.exists(self._created_path):
            os.remove(self._created_path)

    def _read(self, fn: Callable[[], T]) -> T:
        with self._lock:
            self._sync()
            return fn()

    def _write(self, fn: Callable[[], T]) -> T:
        with self._lock:
            self._sync()
            result = fn()
            self._sync()
            return result

    # -- ownership checks (layer 1) ----------------------------------------

    def _owned_deck_ids(self) -> set[int]:
        return {d.id for d in self._col.decks.all_names_and_ids() if is_owned_name(d.name)}

    def _note_owned(self, nid: int) -> bool:
        owned = self._owned_deck_ids()
        homes = self._col.db.list("select case when odid then odid else did end from cards where nid = ?", nid)
        return bool(homes) and all(h in owned for h in homes)

    def _owned_notes(self, note_ids: Iterable[int]) -> list[int]:
        ids = [int(n) for n in note_ids]
        if not ids:
            raise Refused("no note ids given")
        missing = [n for n in ids if not self._col.db.scalar("select 1 from notes where id = ?", n)]
        if missing:
            raise Refused(f"no such notes: {missing}")
        foreign = [n for n in ids if not self._note_owned(n)]
        if foreign:
            raise Refused(f"notes {foreign} are not (entirely) under {ROOT}; they are read-only")
        return ids

    def _cards_of(self, note_ids: list[int]) -> list[int]:
        return [cid for nid in note_ids for cid in self._col.db.list("select id from cards where nid = ?", nid)]

    def _owned_deck(self, name: str, create: bool = False, allow_root: bool = True) -> int:
        full = full_deck_name(name)
        if not allow_root and full == ROOT:
            raise Refused(f"not allowed on the root deck {ROOT} itself")
        deck = self._col.decks.by_name(full)
        if deck is not None:
            if not is_owned_name(deck["name"]):
                raise Refused(f"deck {deck['name']!r} is outside {ROOT}")
            return deck["id"]
        if not create:
            raise Refused(f"no deck {full!r}")
        before = {d.id for d in self._col.decks.all_names_and_ids()}
        did = self._col.decks.id(full, create=True)
        root_conf = self._col.decks.by_name(ROOT)["conf"]
        for new in {d.id for d in self._col.decks.all_names_and_ids()} - before:
            deck = self._col.decks.get(new)
            if not deck.get("dyn"):
                deck["conf"] = root_conf
                self._col.decks.save(deck)
        return did

    def _preset_owned(self, pid: int) -> bool:
        users = [d for d in self._col.decks.all() if not d.get("dyn") and d["conf"] == pid]
        return bool(users) and all(is_owned_name(d["name"]) for d in users)

    # -- reads ---------------------------------------------------------------

    def _deck_name(self, did: int) -> str:
        deck = self._col.decks.get(did, default=False)
        return deck["name"] if deck else f"<deck {did}>"

    def _note_view(self, nid: int) -> dict[str, Any]:
        col = self._col
        note = col.get_note(nid)
        today = col.sched.today
        cards = []
        for card in note.cards():
            info: dict[str, Any] = {
                "card_id": card.id,
                "deck": self._deck_name(card.odid or card.did),
                "state": {CARD_TYPE_NEW: "new", CARD_TYPE_LRN: "learning", CARD_TYPE_REV: "review",
                          CARD_TYPE_RELEARNING: "relearning"}.get(card.type, str(card.type)),
                "reps": card.reps,
                "lapses": card.lapses,
                "interval_days": card.ivl,
            }
            if (flag := _queue_name(card.queue)) is not None:
                info["queue"] = flag
            if card.type == CARD_TYPE_REV:
                info["due_in_days"] = (card.odue or card.due) - today
            elif card.type == CARD_TYPE_NEW:
                info["new_position"] = card.due
            cards.append(info)
        return {
            "note_id": nid,
            "notetype": note.note_type()["name"],
            "owned": self._note_owned(nid),
            "fields": dict(note.items()),
            "tags": note.tags,
            "cards": cards,
        }

    def list_decks(self) -> list[dict[str, Any]]:
        def run() -> list[dict[str, Any]]:
            counts = dict(self._col.db.all(
                "select case when odid then odid else did end, count() from cards group by 1"))
            out = []
            for d in self._col.decks.all():
                entry = {"deck_id": d["id"], "name": d["name"], "owned": is_owned_name(d["name"]),
                         "cards": counts.get(d["id"], 0), "filtered": bool(d.get("dyn"))}
                if not d.get("dyn"):
                    conf = self._col.decks.get_config(d["conf"])
                    entry["preset"] = conf["name"] if conf else None
                out.append(entry)
            return sorted(out, key=lambda e: e["name"].casefold())
        return self._read(run)

    def find_cards(self, query: str, limit: int = 50) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            nids = list(self._col.find_notes(query))
            return {"total": len(nids), "notes": [self._note_view(n) for n in nids[:max(1, min(limit, 500))]]}
        return self._read(run)

    def due_summary(self) -> list[dict[str, Any]]:
        def run() -> list[dict[str, Any]]:
            out: list[dict[str, Any]] = []

            def walk(node: Any, prefix: str) -> None:
                for child in node.children:
                    name = f"{prefix}::{child.name}" if prefix else child.name
                    out.append({"deck": name, "owned": is_owned_name(name), "new": child.new_count,
                                "learning": child.learn_count, "review": child.review_count})
                    walk(child, name)
            walk(self._col.sched.deck_due_tree(), "")
            return out
        return self._read(run)

    # -- writes (everything below runs only inside Duet) --------------------

    def add_cards(self, source_path: str, deck: str, cards: list[dict[str, Any]]) -> list[dict[str, Any]]:
        if not source_path.strip():
            raise Refused("source_path is required")
        if not cards:
            raise Refused("no cards given")

        def run() -> list[dict[str, Any]]:
            col = self._col
            did = self._owned_deck(deck, create=True)
            results = []
            for spec in cards:
                key = str(spec.get("key", "")).strip()
                kind = spec.get("type", "basic")
                result: dict[str, Any] = {"key": key}
                try:
                    if not key:
                        raise Refused("every card needs a key")
                    if kind not in NOTETYPES:
                        raise Refused(f"type must be basic or cloze, not {kind!r}")
                    result.update(self._upsert(did, source_path, key, kind, spec))
                except Refused as e:
                    result.update(status="error", error=str(e))
                results.append(result)
            return results
        return self._write(run)

    def _upsert(self, did: int, source_path: str, key: str, kind: str, spec: dict[str, Any]) -> dict[str, Any]:
        col = self._col
        nt = col.models.by_name(NOTETYPES[kind]["name"])
        names = NOTETYPES[kind]["fields"]
        values = {names[0]: str(spec.get("front", "")), names[1]: str(spec.get("back", "")),
                  names[2]: source_path}
        tags = [source_tag(source_path)] + [clean_tag(t) for t in spec.get("extra_tags") or [] if t.strip()]
        guid = guid_for(source_path, key)

        nid = col.db.scalar("select id from notes where guid = ?", guid)
        if nid is None:
            note = col.new_note(nt)
            note.guid = guid
            for k, v in values.items():
                note[k] = v
            note.tags = tags
            self._check_fields(note)
            col.add_note(note, did)
            return {"status": "created", "note_id": note.id}

        if not self._note_owned(nid):
            raise Refused(f"a note with this key exists outside {ROOT} (note {nid}); not touching it")
        note = col.get_note(nid)
        if note.mid != nt["id"]:
            raise Refused(f"key {key!r} is already a {note.note_type()['name']} note; "
                          "a note's type cannot change (that would force a full sync)")
        before = (dict(note.items()), sorted(note.tags))
        for k, v in values.items():
            note[k] = v
        note.tags = sorted(set(note.tags) | set(tags))
        self._check_fields(note)
        changed = (dict(note.items()), sorted(note.tags)) != before
        if changed:
            col.update_note(note)
        to_move = [c for c in note.card_ids() if col.db.scalar(
            "select case when odid then odid else did end from cards where id = ?", c) != did]
        if to_move:
            col.set_deck(to_move, did)
        return {"status": "updated" if changed or to_move else "unchanged", "note_id": nid}

    @staticmethod
    def _check_fields(note: Any) -> None:
        check = note.fields_check()
        if check == NoteFieldsCheckResult.EMPTY:
            raise Refused("the first field is empty")
        if check == NoteFieldsCheckResult.MISSING_CLOZE:
            raise Refused("a cloze card needs at least one {{c1::...}} deletion in front")

    def update_note(self, note_id: int, fields: dict[str, str] | None, tags: list[str] | None) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            nid, = self._owned_notes([note_id])
            note = self._col.get_note(nid)
            for k, v in (fields or {}).items():
                if k not in note.keys():
                    raise Refused(f"note {nid} has no field {k!r}; fields are {note.keys()}")
                note[k] = v
            if tags is not None:
                note.tags = [clean_tag(t) for t in tags if t.strip()]
            self._check_fields(note)
            self._col.update_note(note)
            return self._note_view(nid)
        return self._write(run)

    def move_cards(self, note_ids: list[int], deck: str) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            nids = self._owned_notes(note_ids)
            did = self._owned_deck(deck, create=True)
            cids = self._cards_of(nids)
            self._col.set_deck(cids, did)
            return {"moved_cards": len(cids), "deck": self._deck_name(did)}
        return self._write(run)

    def create_deck(self, name: str) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            did = self._owned_deck(name, create=True)
            return {"deck_id": did, "name": self._deck_name(did)}
        return self._write(run)

    def rename_deck(self, name: str, new_name: str) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            did = self._owned_deck(name, allow_root=False)
            target = full_deck_name(new_name)
            if target == ROOT:
                raise Refused(f"cannot rename onto the root deck {ROOT}")
            self._col.decks.rename(did, target)
            return {"deck_id": did, "name": self._deck_name(did)}
        return self._write(run)

    def set_card_state(self, note_ids: list[int], action: str) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            cids = self._cards_of(self._owned_notes(note_ids))
            sched = self._col.sched
            {"suspend": sched.suspend_cards, "unsuspend": sched.unsuspend_cards,
             "bury": lambda c: sched.bury_cards(c, manual=True), "unbury": sched.unbury_cards}[action](cids)
            return {"cards": len(cids), "action": action}
        if action not in ("suspend", "unsuspend", "bury", "unbury"):
            raise Refused(f"unknown action {action!r}")
        return self._write(run)

    def set_due(self, note_ids: list[int], days: str) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            cids = self._cards_of(self._owned_notes(note_ids))
            self._col.sched.set_due_date(cids, days)
            return {"cards": len(cids), "days": days}
        return self._write(run)

    def forget(self, note_ids: list[int], reset_counts: bool = False) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            cids = self._cards_of(self._owned_notes(note_ids))
            self._col.sched.schedule_cards_as_new(cids, restore_position=True, reset_counts=reset_counts)
            return {"cards": len(cids)}
        return self._write(run)

    def set_deck_options(self, deck: str, new_per_day: int | None, reviews_per_day: int | None,
                         learn_steps_minutes: list[float] | None, relearn_steps_minutes: list[float] | None,
                         separate_preset: bool) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            col = self._col
            did = self._owned_deck(deck)
            d = col.decks.get(did)
            if d.get("dyn"):
                raise Refused("filtered decks have no options preset")
            conf = col.decks.config_dict_for_deck_id(did)
            if separate_preset:
                pid = col.decks.add_config_returning_id(f"{ROOT_PRESET}: {d['name']}", clone_from=conf)
                d["conf"] = pid
                col.decks.save(d)
                conf = col.decks.get_config(pid)
            elif not self._preset_owned(conf["id"]):
                raise Refused(f"preset {conf['name']!r} is shared with decks outside {ROOT}; "
                              "pass separate_preset=true to give this deck its own")
            if new_per_day is not None:
                conf["new"]["perDay"] = int(new_per_day)
            if reviews_per_day is not None:
                conf["rev"]["perDay"] = int(reviews_per_day)
            if learn_steps_minutes is not None:
                conf["new"]["delays"] = [float(s) for s in learn_steps_minutes]
            if relearn_steps_minutes is not None:
                conf["lapse"]["delays"] = [float(s) for s in relearn_steps_minutes]
            col.decks.update_config(conf)
            return {"deck": d["name"], "preset": conf["name"], "new_per_day": conf["new"]["perDay"],
                    "reviews_per_day": conf["rev"]["perDay"], "learn_steps_minutes": conf["new"]["delays"],
                    "relearn_steps_minutes": conf["lapse"]["delays"]}
        return self._write(run)

    def delete_notes(self, note_ids: list[int]) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            nids = self._owned_notes(note_ids)
            return {"deleted_notes": self._col.remove_notes(nids).count}
        return self._write(run)

    def delete_deck(self, name: str) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            col = self._col
            did = self._owned_deck(name, allow_root=False)
            subtree = col.decks.deck_and_child_ids(did)
            marks = ",".join("?" * len(subtree))
            nids = col.db.list(
                f"select distinct nid from cards where (case when odid then odid else did end) in ({marks})", *subtree)
            foreign = [n for n in nids if not self._note_owned(n)]
            if foreign:
                raise Refused(f"deck holds cards of notes with cards outside {ROOT}: {foreign}")
            removed = col.decks.remove([did]).count
            return {"deleted_cards": removed, "deck": name}
        return self._write(run)

    def trash_notes(self, note_ids: list[int]) -> dict[str, Any]:
        def run() -> dict[str, Any]:
            nids = self._owned_notes(note_ids)
            did = self._owned_deck(TRASH, create=True)
            cids = self._cards_of(nids)
            self._col.set_deck(cids, did)
            self._col.sched.suspend_cards(cids)
            return {"trashed_cards": len(cids), "deck": TRASH}
        return self._write(run)

    # -- tests only ----------------------------------------------------------

    def _raw_collection_for_tests(self) -> Collection:
        """The unguarded collection. Tests use it to simulate bugs that bypass layer 1."""
        return self._col
