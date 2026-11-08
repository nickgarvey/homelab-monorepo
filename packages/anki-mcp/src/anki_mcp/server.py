"""MCP tools for the Anki collection: read everything, change only what is under the Duet deck.

The tools are thin wrappers around guard.Anki, which enforces the rules. This
module never touches the collection itself.
"""

from __future__ import annotations

import inspect
import logging
import os
from typing import Any, Literal

from mcp.server import MCPServer
from mcp.server.mcpserver.exceptions import ToolError
from mcp.server.transport_security import TransportSecuritySettings
from mcp.types import ToolAnnotations
from starlette.requests import Request
from starlette.responses import JSONResponse

from . import card_guide, guard

log = logging.getLogger(__name__)

INSTRUCTIONS = f"""\
Read and manage the user's Anki collection, which syncs to their phone.

Everything under the deck `{guard.ROOT}` belongs to you: create, reorganise,
edit, reschedule and delete freely there. Every other deck is the user's and
is read-only; the server refuses changes to it. Deck names you pass are put
under `{guard.ROOT}` automatically ("Hamming::Ch 08" means
"{guard.ROOT}::Hamming::Ch 08").

Cards made from a note in the user's Obsidian vault go through add_cards with
that note's vault path as source_path and a stable, unique key per card;
calling add_cards again with the same key updates that card instead of
duplicating it. Deleting erases review history. When unsure, use trash_notes,
which keeps the history in {guard.TRASH}.

Before writing or rewriting cards, call card_writing_guide once and follow it:
one fact per card, no yes/no prompts, cloze for lists, a topic label on every
front and a source on every back.
"""

READ = ToolAnnotations(read_only_hint=True, open_world_hint=False)
WRITE = ToolAnnotations(read_only_hint=False, destructive_hint=False, open_world_hint=False)
UPSERT = ToolAnnotations(read_only_hint=False, destructive_hint=False, idempotent_hint=True, open_world_hint=False)
DESTRUCTIVE = ToolAnnotations(read_only_hint=False, destructive_hint=True, open_world_hint=False)


def build(anki: guard.Anki, allowed_hosts: list[str]) -> Any:
    mcp = MCPServer(name="anki", title="Anki", instructions=INSTRUCTIONS)

    def call(fn: Any, *args: Any, **kwargs: Any) -> Any:
        try:
            return fn(*args, **kwargs)
        except (guard.Refused, guard.Quarantined, guard.FullSyncRefused) as e:
            raise ToolError(str(e)) from e

    @mcp.tool(annotations=READ)
    def list_decks() -> list[dict[str, Any]]:
        """All decks with card counts, options preset, and whether they are yours (under Duet)."""
        return call(anki.list_decks)

    @mcp.tool(annotations=READ)
    def find_cards(query: str, limit: int = 50) -> dict[str, Any]:
        """Search notes with Anki search syntax (e.g. 'deck:Duet::Hamming', 'tag:review::source::*',
        'is:due', 'added:7', or plain text). Returns note id, note type, fields, tags, ownership, and per-card
        deck and scheduling (state, due_in_days, interval, reps, lapses, suspended/buried)."""
        return call(anki.find_cards, query, limit)

    @mcp.tool(annotations=READ)
    def due_summary() -> list[dict[str, Any]]:
        """New, learning and review counts due today for every deck."""
        return call(anki.due_summary)

    @mcp.tool(annotations=READ)
    def card_writing_guide() -> str:
        """How to write good Anki cards, with examples: read once before writing or rewriting cards."""
        return card_guide.GUIDE

    def add_cards(source_path: str, deck: str, cards: list[dict[str, Any]]) -> list[dict[str, Any]]:
        """Create or update cards from one source note.

        source_path: the vault path of the note the cards come from; stored in the Source field and as the tag
        review::source::<slug>. deck: target deck, under Duet (created if missing).
        cards: list of {key, type: "basic"|"cloze", front, back, extra_tags}. key must be stable and unique per
        source_path: the same source_path + key always maps to the same Anki note, so repeating a call updates
        the card (keeping its review history) instead of duplicating it. For cloze, front is the text with
        {{c1::...}} deletions and back is the extra text. A key's type cannot change later.
        Returns per card: key, status (created|updated|unchanged|error), note_id or error."""
        return call(anki.add_cards, source_path, deck, cards)

    # The card-writing rules ride on the tool description, which every client
    # reads before it can call the tool.
    mcp.tool(annotations=UPSERT,
             description=f"{inspect.cleandoc(add_cards.__doc__)}\n\n{card_guide.SUMMARY}")(add_cards)

    @mcp.tool(annotations=WRITE)
    def update_note(note_id: int, fields: dict[str, str] | None = None,
                    tags: list[str] | None = None) -> dict[str, Any]:
        """Edit one of your notes: set any of its fields by name, and/or replace its tags entirely.
        Scheduling is kept. Card text you write follows the same rules as add_cards (see
        card_writing_guide)."""
        return call(anki.update_note, note_id, fields, tags)

    @mcp.tool(annotations=WRITE)
    def move_cards(note_ids: list[int], deck: str) -> dict[str, Any]:
        """Move all cards of these notes to another deck under Duet (created if missing)."""
        return call(anki.move_cards, note_ids, deck)

    @mcp.tool(annotations=WRITE)
    def create_deck(name: str) -> dict[str, Any]:
        """Create a deck under Duet. New decks use the Duet options preset."""
        return call(anki.create_deck, name)

    @mcp.tool(annotations=WRITE)
    def rename_deck(name: str, new_name: str) -> dict[str, Any]:
        """Rename or re-parent a deck under Duet; both names stay under Duet."""
        return call(anki.rename_deck, name, new_name)

    @mcp.tool(annotations=WRITE)
    def set_card_state(note_ids: list[int], action: Literal["suspend", "unsuspend", "bury", "unbury"]) -> dict[str, Any]:
        """Suspend, unsuspend, bury (until tomorrow) or unbury all cards of these notes."""
        return call(anki.set_card_state, note_ids, action)

    @mcp.tool(annotations=WRITE)
    def set_due(note_ids: list[int], days: str) -> dict[str, Any]:
        """Reschedule all cards of these notes as review cards due in `days`: "0" today, "3" in three days,
        "3-7" a random day in that range; a trailing "!" also sets the interval (e.g. "10!")."""
        return call(anki.set_due, note_ids, days)

    @mcp.tool(annotations=DESTRUCTIVE)
    def forget_cards(note_ids: list[int], reset_counts: bool = False) -> dict[str, Any]:
        """Reset all cards of these notes to new, discarding their scheduling (history stays in the review
        log). reset_counts also zeroes reps and lapses."""
        return call(anki.forget, note_ids, reset_counts)

    @mcp.tool(annotations=WRITE)
    def set_deck_options(deck: str, new_per_day: int | None = None, reviews_per_day: int | None = None,
                         learn_steps_minutes: list[float] | None = None,
                         relearn_steps_minutes: list[float] | None = None,
                         separate_preset: bool = False) -> dict[str, Any]:
        """Change daily limits and learning steps of a Duet deck's options preset. Presets are shared by
        every deck that uses them; separate_preset=true first gives this deck its own copy."""
        return call(anki.set_deck_options, deck, new_per_day, reviews_per_day, learn_steps_minutes,
                    relearn_steps_minutes, separate_preset)

    @mcp.tool(annotations=DESTRUCTIVE)
    def trash_notes(note_ids: list[int]) -> dict[str, Any]:
        """Soft delete: move all cards of these notes to Duet::Trash and suspend them, keeping their history.
        The user empties the trash."""
        return call(anki.trash_notes, note_ids)

    @mcp.tool(annotations=DESTRUCTIVE)
    def delete_notes(note_ids: list[int]) -> dict[str, Any]:
        """Permanently delete these notes and their cards, including review history. Prefer trash_notes when
        unsure."""
        return call(anki.delete_notes, note_ids)

    @mcp.tool(annotations=DESTRUCTIVE)
    def delete_deck(name: str) -> dict[str, Any]:
        """Permanently delete a deck under Duet with its subdecks and all their cards. The Duet root itself
        cannot be deleted."""
        return call(anki.delete_deck, name)

    @mcp.custom_route("/healthz", methods=["GET"])
    async def healthz(_: Request) -> JSONResponse:
        if anki.quarantine is not None:
            return JSONResponse({"status": "quarantined", "reasons": anki.quarantine}, status_code=503)
        return JSONResponse({"status": "ok"})

    return mcp.streamable_http_app(
        streamable_http_path=os.environ.get("MCP_PATH", "/mcp"),
        stateless_http=True,
        json_response=True,
        transport_security=TransportSecuritySettings(allowed_hosts=allowed_hosts),
        host="0.0.0.0",
    )
