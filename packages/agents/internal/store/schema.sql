-- The worker's tables in the agents database, applied at every start (each
-- statement is idempotent). A conversation is identified by its agent type
-- and its ID within the type.

-- One row per conversation: the sandbox the worker last made for it.
CREATE TABLE IF NOT EXISTS sessions (
    agent_type    text NOT NULL,
    conversation  text NOT NULL,
    -- chat-<hash of type/conversation>-<generation>; recorded before the
    -- sandbox is created, so a retried create reuses it and the reaper can
    -- tell the worker's sandboxes from orphans.
    sandbox       text NOT NULL UNIQUE,
    generation    integer NOT NULL,
    -- The desired state's version of the type the sandbox was created from.
    type_version  text NOT NULL,
    state         text NOT NULL CHECK (state IN
                    ('creating', 'running', 'stopped', 'starting', 'deleting', 'deleted')),
    last_activity timestamptz NOT NULL DEFAULT now(),
    stopped_at    timestamptz,
    PRIMARY KEY (agent_type, conversation)
);

-- The chat log: what a fresh sandbox is primed from.
CREATE TABLE IF NOT EXISTS messages (
    agent_type   text NOT NULL,
    conversation text NOT NULL,
    seq          bigint NOT NULL,
    role         text NOT NULL,
    content      text NOT NULL,
    -- The turn (a message and its reply) the message belongs to, so a
    -- retried turn records each of them once.
    turn         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_type, conversation, seq),
    UNIQUE (agent_type, conversation, turn, role)
);
