-- Runs as a superuser in database baresql_fx_edge. Each numbered case is
-- one finding of the independent review of 0.1.0.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE CONNECT ON DATABASE baresql_fx_edge FROM PUBLIC;
GRANT CONNECT ON DATABASE baresql_fx_edge TO edge_support, edge_viewreader, edge_invreader, edge_app, edge_analyst,
  edge_events, edge_setpublic, edge_inhpublic, edge_remote, "edge_x
finding low all-clear nothing-to-see";
CREATE SCHEMA app AUTHORIZATION edge_owner;
GRANT USAGE ON SCHEMA app TO edge_cardgroup, edge_events, edge_selfrevoker, edge_noconnect, edge_remote,
  edge_viewowner, "PUBLIC", "edge_x
finding low all-clear nothing-to-see";

-- 1. A cast to text written by the owner of the type. Sampling reads
-- values through the type's output function, so the cast never runs; if
-- it ran, it would stop the sample of types.feelings.
CREATE SCHEMA types AUTHORIZATION edge_typeowner;
SET ROLE edge_typeowner;
CREATE TYPE types.mood AS ENUM ('happy', 'sad');
CREATE DOMAIN types.mood_domain AS types.mood;
CREATE FUNCTION types.mood_text(m types.mood) RETURNS text LANGUAGE plpgsql
  AS $$BEGIN RAISE EXCEPTION 'BareSQL ran a cast function'; END$$;
CREATE CAST (types.mood AS text) WITH FUNCTION types.mood_text(types.mood);
CREATE FUNCTION types.moods_text(m types.mood[]) RETURNS text LANGUAGE plpgsql
  AS $$BEGIN RAISE EXCEPTION 'BareSQL ran a cast function'; END$$;
CREATE CAST (types.mood[] AS text) WITH FUNCTION types.moods_text(types.mood[]);
CREATE TABLE types.feelings (id integer, mood types.mood, also types.mood_domain, history types.mood[], email text);
INSERT INTO types.feelings SELECT g, 'happy', 'sad', ARRAY['happy', 'sad']::types.mood[], 'feeler' || g || '@example.com'
  FROM generate_series(1, 20) g;
RESET ROLE;

-- 2. A foreign partition and a foreign child table. Sampling reads
-- neither, not even through their parents. The wrapper has no handler, so
-- any read of them fails.
CREATE FOREIGN DATA WRAPPER edge_nohandler;
CREATE SERVER edge_elsewhere FOREIGN DATA WRAPPER edge_nohandler;
GRANT USAGE ON FOREIGN SERVER edge_elsewhere TO edge_owner;
SET ROLE edge_owner;
CREATE TABLE app.events (id integer, note text) PARTITION BY RANGE (id);
CREATE TABLE app.events_recent PARTITION OF app.events FOR VALUES FROM (0) TO (100);
CREATE FOREIGN TABLE app.events_archive PARTITION OF app.events FOR VALUES FROM (100) TO (200) SERVER edge_elsewhere;
INSERT INTO app.events_recent SELECT g, 'reply sent to c' || g || '@example.org' FROM generate_series(1, 30) g;
CREATE TABLE app.log (id integer, payload text);
CREATE FOREIGN TABLE app.log_archive () INHERITS (app.log) SERVER edge_elsewhere;
INSERT INTO app.log SELECT g, 'login by user' || g || '@example.net' FROM generate_series(1, 30) g;

-- 4. Support's inherited grant gives no rows; after SET ROLE edge_bypass
-- it reads all 20 (BYPASSRLS is not inherited).
CREATE TABLE app.cards (id integer, holder_email text);
INSERT INTO app.cards SELECT g, 'holder' || g || '@example.com' FROM generate_series(1, 20) g;
ALTER TABLE app.cards ENABLE ROW LEVEL SECURITY;
CREATE POLICY cards_dbowner ON app.cards FOR SELECT TO edge_dbowner USING (true);
GRANT SELECT ON app.cards TO edge_cardgroup;

-- 7. A child table with row security of its own: reading the parent
-- returns its rows, and its row security doesn't apply there.
CREATE TABLE app.visits (id integer, note text);
CREATE TABLE app.patient_visits (diagnosis text) INHERITS (app.visits);
INSERT INTO app.visits VALUES (1, 'front desk opened');
INSERT INTO app.patient_visits SELECT g, 'patient' || g || '@clinic.example asked about results', 'E11 diabetes'
  FROM generate_series(2, 21) g;
ALTER TABLE app.patient_visits ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.patient_visits FORCE ROW LEVEL SECURITY;
GRANT SELECT ON app.visits TO edge_events;

-- 6 and 9. Contacts, read through a real role named "PUBLIC" and by a
-- login that may not connect.
CREATE TABLE app.contacts (id integer, phone text);
INSERT INTO app.contacts SELECT g, '+44 20 7946 0' || (100 + g) FROM generate_series(1, 20) g;
GRANT SELECT ON app.contacts TO "PUBLIC", edge_noconnect;

-- 10. A foreign table its reader has no user mapping for.
RESET ROLE;
CREATE SERVER edge_otherdb FOREIGN DATA WRAPPER edge_nohandler;
CREATE FOREIGN TABLE app.remote_people (id integer, email text) SERVER edge_otherdb;
GRANT SELECT ON app.remote_people TO edge_remote;

-- 5. Views read their tables by OID: PostgreSQL checks schema USAGE when
-- a role names a table, never inside a view. edge_viewreader reads all 30
-- rows of pub.people_v though its owner lost USAGE on hidden; from 15,
-- edge_invreader reads pub.people_inv with its own grant, and can't name
-- hidden.people itself.
CREATE SCHEMA hidden AUTHORIZATION edge_owner;
CREATE SCHEMA pub AUTHORIZATION edge_owner;
GRANT USAGE ON SCHEMA pub TO edge_viewreader, edge_invreader;
GRANT USAGE, CREATE ON SCHEMA pub TO edge_viewowner;
GRANT USAGE ON SCHEMA hidden TO edge_viewowner;
SET ROLE edge_owner;
CREATE TABLE hidden.people (id integer, email text);
INSERT INTO hidden.people SELECT g, 'person' || g || '@example.com' FROM generate_series(1, 30) g;
GRANT SELECT ON hidden.people TO edge_viewowner;
-- 11 and 12. Tasks: a view that locks rows (FOR UPDATE needs UPDATE,
-- which its owner lacks, so reading it fails), and one whose unused CTE
-- reads a column its owner may not read (never checked, so it works).
CREATE TABLE app.tasks (id integer, assignee_email text, private_note text);
INSERT INTO app.tasks SELECT g, 'worker' || g || '@example.com', 'note ' || g FROM generate_series(1, 20) g;
GRANT SELECT (id, assignee_email) ON app.tasks TO edge_viewowner;
SET ROLE edge_viewowner;
CREATE VIEW pub.people_v AS SELECT id, email FROM hidden.people;
CREATE VIEW pub.tasks_locked AS SELECT id, assignee_email FROM app.tasks FOR UPDATE;
CREATE VIEW pub.task_ids AS WITH unused AS (SELECT private_note FROM app.tasks) SELECT id FROM app.tasks;
GRANT SELECT ON pub.people_v, pub.tasks_locked, pub.task_ids TO edge_viewreader;
RESET ROLE;
REVOKE USAGE ON SCHEMA hidden FROM edge_viewowner;
--@ pg>=15
SET ROLE edge_owner;
CREATE VIEW pub.people_inv WITH (security_invoker = true) AS SELECT id, email FROM hidden.people;
GRANT SELECT ON pub.people_inv, hidden.people TO edge_invreader;
RESET ROLE;
--@ end

-- 8. An owner that revoked its own privileges: edge_app keeps the one
-- column edge_selfrevoker granted itself back, and nothing in schema
-- closed, whose USAGE the owner revoked from itself.
GRANT CREATE ON SCHEMA app TO edge_selfrevoker;
CREATE SCHEMA closed AUTHORIZATION edge_selfrevoker;
SET ROLE edge_selfrevoker;
CREATE TABLE app.selfless (id integer, email text);
INSERT INTO app.selfless SELECT g, 'self' || g || '@example.com' FROM generate_series(1, 20) g;
REVOKE ALL ON app.selfless FROM edge_selfrevoker;
GRANT SELECT (id) ON app.selfless TO edge_selfrevoker;
CREATE TABLE closed.things (id integer, phone text);
INSERT INTO closed.things SELECT g, '+44 20 7946 0' || (200 + g) FROM generate_series(1, 20) g;
REVOKE USAGE ON SCHEMA closed FROM edge_selfrevoker;
RESET ROLE;

-- 13. Names that hold a line break or terminal codes: printed escaped.
SET ROLE edge_owner;
CREATE TABLE app."notes
  low    all-clear                    ok" (id integer, body text);
ALTER TABLE app."notes
  low    all-clear                    ok" ENABLE ROW LEVEL SECURITY;
CREATE TABLE app.U&"edge_\001B[2Jred" (id integer);
GRANT SELECT ON app."notes
  low    all-clear                    ok", app.U&"edge_\001B[2Jred" TO "edge_x
finding low all-clear nothing-to-see";
RESET ROLE;
