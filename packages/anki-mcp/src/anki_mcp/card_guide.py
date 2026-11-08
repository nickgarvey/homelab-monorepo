"""How to write good cards: shown to every client that writes cards through this server.

SUMMARY is appended to the add_cards tool description, which every client reads
before calling it. GUIDE is the full text, returned by the card_writing_guide
tool. Sources: Piotr Wozniak's "Twenty rules of formulating knowledge" and
Andy Matuschak's "How to write good prompts", as applied to the Duet::Valkey
decks in November 2026.
"""

SUMMARY = """\
Writing cards (call card_writing_guide for the full guide with examples):
- One fact per card. If the answer has an "and", split it.
- No yes/no prompts; ask for the thing itself ("What does X do?", not "Does X do Y?").
- Lists and sets: cloze with one item hidden per card, in a fixed order; never "name all N".
- Keep easily confused facts (similar numbers or names) on separate cards, and put the
  distinguishing context in the prompt.
- Add "why" cards for the reasons behind important facts, not only bare trivia.
- Start every front with a short topic label (e.g. <small>Valkey replication</small><br>) so the
  card makes sense when decks are mixed.
- End every back with its source and, for fast-moving topics, a version or date stamp.
- A prompt has exactly one right answer, recallable in a few seconds.
- Choose basic vs cloze carefully: a key's type can't change without losing its review history.
- Before overwriting a card, check with find_cards that the user hasn't edited it in the app."""

GUIDE = """\
# Writing good Anki cards

A card is a retrieval exercise: it is worth something only if answering it makes
the user pull one specific piece of knowledge out of memory. Vague, compound or
guessable prompts get reviewed for months without teaching anything, and every
bad card costs review time forever. Fewer, sharper cards beat many loose ones.

## Understand first, then write

Only make cards from material that is already understood and checked. If a
fact came from your own memory rather than a source, verify it before it
becomes a card, and say on the card when it is general knowledge with no source.

## One fact per card (minimum information)

A card that asks for two things gets marked wrong when either is forgotten, and
the user can't tell which part failed.

- Bad: "What is the default of repl-diskless-sync-delay, and what is it for?"
- Good: "What is the default of repl-diskless-sync-delay?" plus a separate
  "Why does Valkey wait before starting a diskless sync?"

If the answer contains "and", or the back has more than one line of real
content, split it.

## No yes/no prompts

A yes/no prompt can be answered by guessing or by vague familiarity.

- Bad: "Can SYNC do a partial resync?"
- Good: "What is the difference between SYNC and PSYNC?"

Ask for the thing itself: the value, the name, the mechanism, the reason.

## Lists and sets: cloze, one item at a time

"Name all four values of X" is a set, and sets are very hard to recall
reliably. Turn an enumeration into one cloze note with each item as its own
deletion (`{{c1::...}}`, `{{c2::...}}`, ...), keeping the items in a fixed,
meaningful order. Each deletion becomes its own card, and the rest of the list
gives context.

- Good: "repl-diskless-load values: {{c1::disabled}}, {{c2::on-empty-db}},
  {{c3::swapdb}}, {{c4::flush-before-load}}"

Use one deletion per card. Never hide two items under the same number.

## Fight interference

Facts that look alike (several 10-second defaults, similarly named configs or
commands) get confused with each other. Give each its own card, put the
distinguishing name in full in the prompt, and add a contrast card ("What's
the difference between A and B?") when the confusion is the thing to learn.

## Ask why, not only what

Bare trivia is forgotten and doesn't connect to anything. For important facts,
add a card for the reason behind them.

- Good: "Why does a Valkey replica keep replid2 after a failover?"

## Context cues

Cards from different decks are reviewed mixed together. Start every front with
a short topic label so the prompt can be read on its own:

    <small>Valkey replication</small><br>What does PSYNC reply when ...?

Keep the rest of the prompt self-contained. Don't rely on the previous card.

## Precise prompts, short answers

- A prompt has exactly one right answer. If several answers would be fair, add
  context until only one is.
- Answers are short: a word, a number, a phrase or one sentence. Anything
  longer is several cards.
- Keep wording consistent across cards on the same topic.

## Sources and dates

End every back with where the fact came from and, for things that change
(software versions, prices, policies), when it was true:

    <small>Source: src/replication.c:1172<br>Valkey 9.2.0-rc2 (2026-11-04)</small>

That lets the user, or a later edit, check the card and spot when it has gone
out of date.

## Basic or cloze

- Basic (front/back): questions with one answer: values, definitions, reasons,
  differences.
- Cloze: lists, ordered steps, and statements where the surrounding sentence is
  the best cue.

Decide before creating: a card's key keeps its type, and changing type means
recreating the card, which loses its review history.

## Maintaining cards

- Keep each card's key stable. Calling add_cards again with the same
  source_path and key updates the card in place and keeps its history.
- Before overwriting a card, check it with find_cards. If the user edited it
  in the Anki app, ask before replacing their edit.
- To remove a card, use trash_notes (keeps history in Duet::Trash), not
  delete_notes, unless the user asks for a permanent delete.
- New cards arrive at the deck's daily new-card limit, so a big batch takes
  days to show up. That's expected, not a bug.

## Sources

- Piotr Wozniak, "Effective learning: Twenty rules of formulating knowledge":
  https://www.supermemo.com/en/archives1990-2015/articles/20rules
- Andy Matuschak, "How to write good prompts: using spaced repetition to create
  understanding": https://andymatuschak.org/prompts/
"""
