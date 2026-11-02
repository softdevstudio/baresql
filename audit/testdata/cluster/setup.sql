-- Runs as a superuser in database baresql_fx_cluster.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA app AUTHORIZATION cluster_owner;
GRANT USAGE ON SCHEMA app TO cluster_app, cluster_analyst;
SET ROLE cluster_owner;

-- Each author sees their own notes.
CREATE TABLE app.notes (id integer PRIMARY KEY, author name, body text);
INSERT INTO app.notes SELECT g, 'cluster_app', 'note ' || g FROM generate_series(1, 5) g;
ALTER TABLE app.notes ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_notes ON app.notes TO cluster_app USING (author = current_user);
GRANT SELECT, INSERT ON app.notes TO cluster_app;

-- Tenants picked by a setting, narrowed to one region by a restrictive
-- policy.
CREATE TABLE app.orders (id integer PRIMARY KEY, tenant_id integer, region text, email text);
INSERT INTO app.orders SELECT g, 1 + g % 3, 'eu', 'buyer' || g || '@example.com' FROM generate_series(1, 30) g;
ALTER TABLE app.orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON app.orders TO cluster_app USING (tenant_id = current_setting('app.tenant')::integer);
CREATE POLICY eu_only ON app.orders AS RESTRICTIVE TO cluster_app USING (region = 'eu');
GRANT SELECT ON app.orders TO cluster_app;

-- A policy that lets nothing through, one that lets everything through,
-- and one BareSQL doesn't read.
CREATE TABLE app.archive (id integer PRIMARY KEY, email text);
INSERT INTO app.archive SELECT g, 'old' || g || '@example.com' FROM generate_series(1, 30) g;
ALTER TABLE app.archive ENABLE ROW LEVEL SECURITY;
CREATE POLICY closed ON app.archive FOR SELECT TO cluster_analyst USING (false);
GRANT SELECT ON app.archive TO cluster_analyst;

CREATE TABLE app.reports (id integer PRIMARY KEY, title text);
INSERT INTO app.reports SELECT g, 'report ' || g FROM generate_series(1, 5) g;
ALTER TABLE app.reports ENABLE ROW LEVEL SECURITY;
CREATE POLICY all_reports ON app.reports FOR SELECT TO cluster_analyst USING (true);
GRANT SELECT ON app.reports TO cluster_analyst;

CREATE TABLE app.drafts (id integer PRIMARY KEY, title text);
ALTER TABLE app.drafts ENABLE ROW LEVEL SECURITY;
CREATE POLICY editors ON app.drafts FOR SELECT TO cluster_analyst USING (pg_has_role(current_user, 'cluster_owner', 'member'));
GRANT SELECT ON app.drafts TO cluster_analyst;

-- Every row through a permissive policy, the tenant picked by a
-- restrictive one that reads a setting: still the session's choice.
CREATE TABLE app.ledger (id integer PRIMARY KEY, tenant_id integer, amount numeric(10,2));
ALTER TABLE app.ledger ENABLE ROW LEVEL SECURITY;
CREATE POLICY open_all ON app.ledger TO cluster_app USING (true);
CREATE POLICY ledger_tenant ON app.ledger AS RESTRICTIVE TO cluster_app USING (tenant_id = current_setting('app.tenant')::integer);
GRANT SELECT ON app.ledger TO cluster_app;
RESET ROLE;

-- A schema the analyst may not use, holding a table whose policy lets
-- every row through: pg_stats hides it while row security applies, so it
-- gives nothing.
CREATE SCHEMA hidden AUTHORIZATION cluster_owner;
SET ROLE cluster_owner;
CREATE TABLE hidden.people (id integer PRIMARY KEY, email text);
INSERT INTO hidden.people SELECT g, 'p' || g || '@example.com' FROM generate_series(1, 30) g;
ALTER TABLE hidden.people ENABLE ROW LEVEL SECURITY;
CREATE POLICY everyone ON hidden.people FOR SELECT TO cluster_analyst USING (true);
GRANT SELECT ON hidden.people TO cluster_analyst;
RESET ROLE;
