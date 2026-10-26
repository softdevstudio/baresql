-- An online shop: an app login, a support team reading customers through a
-- view, an analyst with column grants, a reporting login with
-- pg_read_all_data, a newsletter table everyone can read, and an export
-- function that runs with the owner's rights.
SET password_encryption = 'scram-sha-256';
CREATE ROLE shop_dbowner NOLOGIN;
CREATE ROLE shop_owner NOLOGIN;
CREATE ROLE shop_app LOGIN PASSWORD 'app-pass';
CREATE ROLE shop_support_team NOLOGIN;
CREATE ROLE shop_alice LOGIN IN ROLE shop_support_team;
-- bob is in the support team without inheriting: he reads nothing of it
-- until he runs SET ROLE shop_support_team.
CREATE ROLE shop_bob LOGIN NOINHERIT IN ROLE shop_support_team;
CREATE ROLE shop_analyst LOGIN;
CREATE ROLE shop_intern LOGIN;
CREATE ROLE shop_etl LOGIN;
--@ pg>=14
CREATE ROLE shop_reporting LOGIN IN ROLE pg_read_all_data;
--@ end
--@ pg<14
CREATE ROLE shop_reporting LOGIN;
--@ end
