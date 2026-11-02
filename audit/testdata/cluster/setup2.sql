-- Runs as a superuser in database baresql_fx_cluster_b. cluster_app may
-- not connect here; cluster_analyst reads the contacts.
REVOKE CONNECT ON DATABASE baresql_fx_cluster_b FROM PUBLIC;
GRANT CONNECT ON DATABASE baresql_fx_cluster_b TO cluster_analyst;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA crm AUTHORIZATION cluster_owner;
GRANT USAGE ON SCHEMA crm TO cluster_analyst, cluster_app;
SET ROLE cluster_owner;
CREATE TABLE crm.contacts (id integer PRIMARY KEY, email text, full_name text);
INSERT INTO crm.contacts SELECT g, 'contact' || g || '@example.com', 'Contact ' || g FROM generate_series(1, 30) g;
GRANT SELECT ON crm.contacts TO cluster_analyst, cluster_app;
RESET ROLE;
