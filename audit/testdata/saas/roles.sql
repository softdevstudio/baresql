-- A multi-tenant app with row security: the app login sees one tenant at a
-- time, an admin console bypasses row security, and an AI support agent
-- gets a view that was meant to show tickets but also hands out every
-- tenant's accounts.
SET password_encryption = 'scram-sha-256';
CREATE ROLE saas_dbowner NOLOGIN;
CREATE ROLE saas_owner NOLOGIN;
CREATE ROLE saas_app LOGIN;
CREATE ROLE saas_admin_app LOGIN BYPASSRLS;
CREATE ROLE saas_support NOLOGIN;
CREATE ROLE saas_agent LOGIN IN ROLE saas_support;
-- A login that owns a table: sessions logged in as it skip row security.
CREATE ROLE saas_migrator LOGIN;
