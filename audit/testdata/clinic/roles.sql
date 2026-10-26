-- A clinic: doctors inherit staff rights through a chain of groups, front
-- desk reads a few patient columns, research reads a partition and a
-- materialized view, IT can read server files, billing may pass its grant
-- on, the lab logs in with an old MD5 password and reaches a foreign server.
SET password_encryption = 'scram-sha-256';
CREATE ROLE clinic_dbowner NOLOGIN;
CREATE ROLE clinic_owner NOLOGIN;
CREATE ROLE clinic_staff NOLOGIN;
CREATE ROLE clinic_doctor NOLOGIN IN ROLE clinic_staff;
CREATE ROLE clinic_drsmith LOGIN IN ROLE clinic_doctor;
CREATE ROLE clinic_frontdesk LOGIN IN ROLE clinic_staff;
CREATE ROLE clinic_research LOGIN;
CREATE ROLE clinic_it LOGIN CREATEROLE IN ROLE pg_read_server_files;
CREATE ROLE clinic_billing LOGIN;
CREATE ROLE clinic_hr LOGIN;
SET password_encryption = 'md5';
CREATE ROLE clinic_lab LOGIN PASSWORD 'lab-pass';
SET password_encryption = 'scram-sha-256';
--@ pg>=16
-- HR may add doctors without being one.
GRANT clinic_doctor TO clinic_hr WITH ADMIN TRUE, INHERIT FALSE, SET FALSE;
-- A locum doctor has to SET ROLE first; an auditor inherits but can't SET ROLE.
CREATE ROLE clinic_locum LOGIN;
GRANT clinic_doctor TO clinic_locum WITH INHERIT FALSE, SET TRUE;
CREATE ROLE clinic_auditor LOGIN;
GRANT clinic_doctor TO clinic_auditor WITH INHERIT TRUE, SET FALSE;
--@ end
--@ pg<16
-- Before 16 an admin option comes with the membership.
GRANT clinic_doctor TO clinic_hr WITH ADMIN OPTION;
--@ end
