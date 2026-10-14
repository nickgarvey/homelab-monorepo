-- The admin chat's access (habit-admin on Matrix, packages/agents/internal/
-- habitadmin): role habit_admin, which PostgREST also switches to, may see
-- and change every row and delete a habit for good. An archived habit
-- (archived_at set) and its entries, notes and cadence are hidden from
-- habit_agent by row-level security, so the coach cannot see, change or
-- restore it; its status is kept, so unarchiving restores it as it was.

ALTER TABLE habit.habits ADD COLUMN archived_at timestamptz;

GRANT USAGE ON SCHEMA habit TO habit_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA habit TO habit_admin;
ALTER DEFAULT PRIVILEGES IN SCHEMA habit
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO habit_admin;

-- The owner, habit_helper, is not subject to these (no FORCE), and neither
-- are the foreign keys' checks and cascades.
ALTER TABLE habit.habits ENABLE ROW LEVEL SECURITY;
ALTER TABLE habit.entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE habit.notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE habit.cadence ENABLE ROW LEVEL SECURITY;

CREATE POLICY admin ON habit.habits TO habit_admin USING (true) WITH CHECK (true);
CREATE POLICY admin ON habit.entries TO habit_admin USING (true) WITH CHECK (true);
CREATE POLICY admin ON habit.notes TO habit_admin USING (true) WITH CHECK (true);
CREATE POLICY admin ON habit.cadence TO habit_admin USING (true) WITH CHECK (true);

-- The coach sees habits that are not archived, and only their rows. The
-- subqueries on habits are themselves filtered by its policy. WITH CHECK
-- keeps it from archiving a habit, or adding rows to an archived one.
CREATE POLICY live ON habit.habits TO habit_agent
  USING (archived_at IS NULL) WITH CHECK (archived_at IS NULL);
CREATE POLICY live ON habit.entries TO habit_agent
  USING (EXISTS (SELECT 1 FROM habit.habits h WHERE h.name = habit))
  WITH CHECK (EXISTS (SELECT 1 FROM habit.habits h WHERE h.name = habit));
CREATE POLICY live ON habit.notes TO habit_agent
  USING (EXISTS (SELECT 1 FROM habit.habits h WHERE h.name = habit))
  WITH CHECK (EXISTS (SELECT 1 FROM habit.habits h WHERE h.name = habit));
CREATE POLICY live ON habit.cadence TO habit_agent
  USING (EXISTS (SELECT 1 FROM habit.habits h WHERE h.name = habit))
  WITH CHECK (EXISTS (SELECT 1 FROM habit.habits h WHERE h.name = habit));

-- Deletes a habit and everything about it, in one transaction, and says
-- how many entries and notes went. PostgREST serves it as
-- /rpc/delete_habit; only habit_admin may run it, and it runs with the
-- caller's rights, so habit_agent could not delete through it anyway.
CREATE FUNCTION habit.delete_habit(habit_name text) RETURNS json
LANGUAGE plpgsql AS $$
DECLARE
  e integer;
  n integer;
BEGIN
  DELETE FROM habit.entries WHERE habit = habit_name;
  GET DIAGNOSTICS e = ROW_COUNT;
  DELETE FROM habit.notes WHERE habit = habit_name;
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM habit.cadence WHERE habit = habit_name;
  DELETE FROM habit.habits WHERE name = habit_name;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no habit %', habit_name USING ERRCODE = 'P0002';
  END IF;
  RETURN json_build_object('entries', e, 'notes', n);
END
$$;

REVOKE ALL ON FUNCTION habit.delete_habit(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION habit.delete_habit(text) TO habit_admin;
