-- Habit Helper's data: schema habit, which PostgREST serves to role
-- habit_agent (k8s/manifests/habit-helper/README.md). tern applies it as
-- the database owner, habit_helper, in one transaction.
--
-- Dates (entries.day, cadence.next_check, cadence.last_check) are the user's
-- local dates, given by the client. A default that needs today uses the
-- database's current_date, which is UTC.

-- Only PostgREST's login connects (the owner always may), and nobody makes
-- temporary tables.
REVOKE ALL ON DATABASE habit_helper FROM PUBLIC;
GRANT CONNECT ON DATABASE habit_helper TO habit_helper_api;

CREATE SCHEMA habit;

-- Trigger functions, in a schema PostgREST does not serve, so they are no
-- /rpc endpoints. Triggers run them without checking EXECUTE.
CREATE SCHEMA habit_private;

CREATE FUNCTION habit_private.touch() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END
$$;

-- One row per habit. name is the key the CLI and the agent use; a rename
-- carries over to every table.
CREATE TABLE habit.habits (
  name text PRIMARY KEY
    CHECK (name = btrim(name) AND length(name) BETWEEN 1 AND 100),
  -- "if <event>, then <behavior>"
  cue text CHECK (btrim(cue) <> ''),
  motivation text CHECK (btrim(motivation) <> ''),
  preparation text CHECK (btrim(preparation) <> ''),
  status text NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'paused', 'retired')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- "Meditate" and "meditate" are one habit.
CREATE UNIQUE INDEX habits_name_lower ON habit.habits (lower(name));

-- What happened on a day: one row per habit and day. Logging a day again
-- updates it (POST with Prefer: resolution=merge-duplicates).
CREATE TABLE habit.entries (
  habit text NOT NULL REFERENCES habit.habits ON UPDATE CASCADE,
  day date NOT NULL,
  outcome text NOT NULL CHECK (outcome IN ('done', 'missed')),
  why text CHECK (btrim(why) <> ''),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (habit, day)
);

-- What the user said about a habit's motivation, cue or preparation, kept
-- over time; habits holds only the current version.
CREATE TABLE habit.notes (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  habit text NOT NULL REFERENCES habit.habits ON UPDATE CASCADE,
  kind text NOT NULL CHECK (kind IN ('motivation', 'cue', 'prep')),
  body text NOT NULL CHECK (btrim(body) <> ''),
  at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notes_habit_at ON habit.notes (habit, at);

-- When to check in on a habit next, and the state the tapering rule needs:
-- the interval grows after a run of done days and shrinks after misses. The
-- CLI owns the rule; the agent may override it, giving a reason. Every habit
-- gets a row when it is added, due today.
CREATE TABLE habit.cadence (
  habit text PRIMARY KEY REFERENCES habit.habits ON UPDATE CASCADE,
  -- Days between check-ins; 60 is the longest wake-up the worker accepts.
  interval_days integer NOT NULL DEFAULT 1 CHECK (interval_days BETWEEN 1 AND 60),
  next_check date NOT NULL DEFAULT current_date,
  -- Consecutive done and consecutive missed days, as of last_check.
  streak integer NOT NULL DEFAULT 0 CHECK (streak >= 0),
  misses integer NOT NULL DEFAULT 0 CHECK (misses >= 0),
  -- The last day the rule (or an override) set this row from.
  last_check date,
  -- Why the agent overrode the rule; null when the rule set the row.
  reason text CHECK (btrim(reason) <> ''),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION habit_private.add_cadence() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO habit.cadence (habit) VALUES (NEW.name);
  RETURN NULL;
END
$$;

CREATE TRIGGER add_cadence AFTER INSERT ON habit.habits
  FOR EACH ROW EXECUTE FUNCTION habit_private.add_cadence();
CREATE TRIGGER touch BEFORE UPDATE ON habit.habits
  FOR EACH ROW EXECUTE FUNCTION habit_private.touch();
CREATE TRIGGER touch BEFORE UPDATE ON habit.entries
  FOR EACH ROW EXECUTE FUNCTION habit_private.touch();
CREATE TRIGGER touch BEFORE UPDATE ON habit.cadence
  FOR EACH ROW EXECUTE FUNCTION habit_private.touch();

REVOKE ALL ON ALL FUNCTIONS IN SCHEMA habit_private FROM PUBLIC;

-- habit_agent reads, adds and changes rows in schema habit: no DELETE, no
-- TRUNCATE, and no DDL (it owns nothing and may create nothing). Tables
-- later migrations add get the same.
GRANT USAGE ON SCHEMA habit TO habit_agent;
GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA habit TO habit_agent;
ALTER DEFAULT PRIVILEGES IN SCHEMA habit
  GRANT SELECT, INSERT, UPDATE ON TABLES TO habit_agent;
