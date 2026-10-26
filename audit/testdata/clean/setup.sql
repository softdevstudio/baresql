-- Runs as a superuser in database baresql_fx_clean.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA app AUTHORIZATION clean_owner;
CREATE SCHEMA reports AUTHORIZATION clean_owner;
GRANT USAGE ON SCHEMA app TO clean_app, clean_counter;
GRANT USAGE ON SCHEMA reports TO clean_reports;
SET ROLE clean_owner;

CREATE TABLE app.customers (id integer PRIMARY KEY, email text, full_name text, created_at timestamptz);
INSERT INTO app.customers SELECT g, 'buyer' || g || '@example.com', 'Buyer ' || g, now() FROM generate_series(1, 30) g;
CREATE TABLE app.orders (id integer PRIMARY KEY, customer_id integer REFERENCES app.customers, total numeric(10,2), created_at timestamptz);
INSERT INTO app.orders SELECT g, 1 + g % 30, g * 5, '2026-10-01'::timestamptz + g * interval '1 hour' FROM generate_series(1, 40) g;

CREATE VIEW reports.daily_sales AS
  SELECT created_at::date AS day, count(*) AS orders, sum(total) AS revenue FROM app.orders GROUP BY 1;

GRANT SELECT, INSERT, UPDATE ON app.customers TO clean_app;
GRANT SELECT, INSERT ON app.orders TO clean_app;
GRANT SELECT ON app.orders TO clean_counter;
GRANT SELECT ON reports.daily_sales TO clean_reports;
RESET ROLE;

CREATE FUNCTION reports.order_count() RETURNS bigint
  LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS 'SELECT count(*) FROM app.orders';
ALTER FUNCTION reports.order_count() OWNER TO clean_counter;
REVOKE EXECUTE ON FUNCTION reports.order_count() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION reports.order_count() TO clean_reports;
