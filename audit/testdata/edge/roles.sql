-- Setups from the independent review of 0.1.0 where an audit could get
-- the answer wrong. setup.sql says what the server really does in each.
SET password_encryption = 'scram-sha-256';
DROP ROLE IF EXISTS "PUBLIC";
CREATE ROLE edge_dbowner NOLOGIN;
CREATE ROLE edge_owner NOLOGIN;
CREATE ROLE edge_typeowner NOLOGIN;
-- Support inherits a grant that row security turns into no rows, and may
-- SET ROLE edge_bypass, which bypasses row security (not inherited).
CREATE ROLE edge_cardgroup NOLOGIN;
CREATE ROLE edge_bypass NOLOGIN BYPASSRLS IN ROLE edge_cardgroup;
CREATE ROLE edge_support LOGIN IN ROLE edge_bypass;
-- Views over a schema their owner or reader may not use.
CREATE ROLE edge_viewowner NOLOGIN;
CREATE ROLE edge_viewreader LOGIN;
CREATE ROLE edge_invreader LOGIN;
-- A login without CONNECT on the database.
CREATE ROLE edge_noconnect LOGIN;
-- An owner that revoked its own privileges.
CREATE ROLE edge_selfrevoker NOLOGIN;
CREATE ROLE edge_app LOGIN IN ROLE edge_selfrevoker;
-- pg_read_all_data through SET ROLE only.
CREATE ROLE edge_analyst LOGIN NOINHERIT;
--@ pg>=14
GRANT pg_read_all_data TO edge_analyst;
--@ end
-- Reads a parent table whose child has row security of its own.
CREATE ROLE edge_events LOGIN;
-- A real role named "PUBLIC": one member may only SET ROLE to it, the
-- other inherits it.
CREATE ROLE "PUBLIC" NOLOGIN;
CREATE ROLE edge_setpublic LOGIN NOINHERIT IN ROLE "PUBLIC";
CREATE ROLE edge_inhpublic LOGIN IN ROLE "PUBLIC";
-- A login whose name holds a line break and a fake finding.
CREATE ROLE "edge_x
finding low all-clear nothing-to-see" LOGIN;
-- Reads a foreign table without a user mapping.
CREATE ROLE edge_remote LOGIN;
