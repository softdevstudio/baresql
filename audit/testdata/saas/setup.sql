-- Runs as a superuser in database baresql_fx_saas.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA app AUTHORIZATION saas_owner;
CREATE SCHEMA support AUTHORIZATION saas_owner;
GRANT USAGE ON SCHEMA app TO saas_app, saas_admin_app, saas_support, saas_migrator;
GRANT USAGE ON SCHEMA support TO saas_support;
SET ROLE saas_owner;

CREATE TABLE app.tenants (id integer PRIMARY KEY, name text);
INSERT INTO app.tenants VALUES (1, 'Acme'), (2, 'Globex'), (3, 'Initech');

CREATE TABLE app.accounts (
  id integer PRIMARY KEY,
  tenant_id integer REFERENCES app.tenants,
  email text,
  full_name text,
  phone text
);
INSERT INTO app.accounts VALUES
{{- range $i := seq 1 30}}
  ({{$i}}, {{pick "1" "2" "3"}}, 'member{{$i}}@tenant.example', '{{pick "Ada" "Grace" "Linus" "Ken"}} {{pick "Lovelace" "Hopper" "Thompson"}}', '{{phone}}'){{if lt $i 30}},{{else}};{{end}}
{{- end}}
ALTER TABLE app.accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.accounts TO saas_app
  USING (tenant_id = current_setting('app.tenant_id')::integer);

CREATE TABLE app.tickets (
  id integer PRIMARY KEY,
  tenant_id integer REFERENCES app.tenants,
  account_id integer REFERENCES app.accounts,
  subject text,
  body text,
  created_at timestamptz
);
INSERT INTO app.tickets VALUES
{{- range $i := seq 1 25}}
  ({{$i}}, {{pick "1" "2" "3"}}, {{$i}}, 'Ticket {{$i}}', 'Cannot log in since the update', now()){{if lt $i 25}},{{else}};{{end}}
{{- end}}
ALTER TABLE app.tickets ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.tickets TO saas_app
  USING (tenant_id = current_setting('app.tenant_id')::integer);
CREATE POLICY support_read ON app.tickets FOR SELECT TO saas_support USING (true);

-- Forced: even the owner sees only what a policy allows, so a view owned
-- by saas_owner hands out no invoice rows.
CREATE TABLE app.invoices (id integer PRIMARY KEY, tenant_id integer, amount numeric(10,2));
INSERT INTO app.invoices SELECT g, 1 + g % 3, g * 10 FROM generate_series(1, 15) g;
ALTER TABLE app.invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.invoices TO saas_app
  USING (tenant_id = current_setting('app.tenant_id')::integer);
CREATE VIEW support.invoice_summary AS SELECT id, amount FROM app.invoices;

-- Row security on and no policy at all: nobody but the owner reads it.
CREATE TABLE app.api_keys (id integer PRIMARY KEY, tenant_id integer, api_key text);
INSERT INTO app.api_keys VALUES (1, 1, '{{stripe}}'), (2, 2, '{{stripe}}'), (3, 3, '{{stripe}}');
ALTER TABLE app.api_keys ENABLE ROW LEVEL SECURITY;

-- What the agent was meant to get: tickets with the customer's name and
-- address. The view runs with saas_owner's rights, and saas_owner owns
-- accounts without forcing row security, so the view hands out every
-- tenant's accounts.
CREATE VIEW support.ticket_context AS
  SELECT t.id, t.subject, a.email, a.full_name
  FROM app.tickets t JOIN app.accounts a ON a.id = t.account_id;
--@ pg>=15
-- The same view with the reader's rights. The agent may not read
-- app.accounts, so PostgreSQL refuses every query on this view, even one
-- that selects only the ticket columns.
CREATE VIEW support.ticket_context_invoker WITH (security_invoker = true) AS
  SELECT t.id, t.subject, a.email, a.full_name
  FROM app.tickets t JOIN app.accounts a ON a.id = t.account_id;
GRANT SELECT ON support.ticket_context_invoker TO saas_support;
-- Done right: tickets only, with the reader's rights, so the support policy
-- picks the rows. The view on top runs with saas_owner's rights, and
-- PostgreSQL still checks the one below it as the reader.
CREATE VIEW support.ticket_list WITH (security_invoker = true) AS
  SELECT id, subject FROM app.tickets;
CREATE VIEW support.ticket_overview AS SELECT id, subject FROM support.ticket_list;
GRANT SELECT ON support.ticket_list, support.ticket_overview TO saas_support;
--@ end

GRANT SELECT, INSERT, UPDATE, DELETE ON app.accounts, app.tickets TO saas_app;
GRANT SELECT ON app.tenants TO saas_app;
GRANT SELECT ON app.tenants, app.accounts, app.tickets, app.api_keys TO saas_admin_app;
GRANT SELECT ON app.tickets TO saas_support;
GRANT SELECT ON support.ticket_context, support.invoice_summary TO saas_support;
RESET ROLE;

CREATE TABLE app.audit_log (id integer PRIMARY KEY, tenant_id integer, actor_email text, action text);
ALTER TABLE app.audit_log OWNER TO saas_migrator;
INSERT INTO app.audit_log SELECT g, 1 + g % 3, 'actor' || g || '@tenant.example', 'login' FROM generate_series(1, 20) g;
ALTER TABLE app.audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.audit_log USING (tenant_id = current_setting('app.tenant_id')::integer);
