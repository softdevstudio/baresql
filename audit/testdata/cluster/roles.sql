-- One server, two databases: an app database with row security of every
-- shape BareSQL reads, and a CRM database the app login may not connect
-- to. Audited together with --database.
SET password_encryption = 'scram-sha-256';
CREATE ROLE cluster_dbowner NOLOGIN;
CREATE ROLE cluster_owner NOLOGIN;
CREATE ROLE cluster_app LOGIN PASSWORD 'app-pass';
CREATE ROLE cluster_analyst LOGIN PASSWORD 'analyst-pass';
