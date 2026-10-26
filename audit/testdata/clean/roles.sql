-- A database set up with care: the app reads and writes what it needs,
-- reports read aggregates through a view, and the one security-definer
-- function pins its search_path, can't be run by everyone, and belongs to
-- a role that reads no personal data. BareSQL should find nothing to flag.
SET password_encryption = 'scram-sha-256';
CREATE ROLE clean_dbowner NOLOGIN;
CREATE ROLE clean_owner NOLOGIN;
CREATE ROLE clean_counter NOLOGIN;
CREATE ROLE clean_app LOGIN PASSWORD 'app-pass';
CREATE ROLE clean_reports LOGIN;
