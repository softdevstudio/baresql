-- Runs as a superuser in database baresql_fx_shop.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA shop AUTHORIZATION shop_owner;
CREATE SCHEMA support AUTHORIZATION shop_owner;
SET ROLE shop_owner;

CREATE TABLE shop.customers (
  id integer PRIMARY KEY,
  first_name text,
  last_name text,
  email text,
  phone text,
  city text,
  created_at timestamptz,
  card_number text,
  notes text,
  marketing_opt_in boolean
);
INSERT INTO shop.customers VALUES
{{- range $i := seq 1 40}}
  ({{$i}}, '{{pick "Anna" "Ben" "Chloe" "Dev" "Ema" "Femi"}}', '{{pick "Smith" "Okafor" "Rossi" "Novak"}}',
   'customer{{$i}}@example.com', '{{phone}}', '{{pick "Leeds" "Lyon" "Graz"}}', '2025-01-01'::timestamptz + interval '{{$i}} days',
   '{{card "visa"}}', '{{if eq $i 7}}asked us to write to her other address anna.alt@example.org{{else}}prefers phone calls{{end}}', {{if eq $i 3}}true{{else}}false{{end}}){{if lt $i 40}},{{else}};{{end}}
{{- end}}

CREATE TABLE shop.orders (
  id integer PRIMARY KEY,
  customer_id integer REFERENCES shop.customers,
  total numeric(10,2),
  tracking_number text,
  shipped_at date
);
-- Tracking numbers are 16 digits like card numbers, but fail the check digit.
INSERT INTO shop.orders VALUES
{{- range $i := seq 1 30}}
  ({{$i}}, {{$i}}, {{$i}}9.90, '{{notcard}}', '2025-03-01'::date + {{$i}}){{if lt $i 30}},{{else}};{{end}}
{{- end}}

-- "name" in a products table is a product name, not a person's.
CREATE TABLE shop.products (id integer PRIMARY KEY, name text, sku text, price numeric(10,2));
INSERT INTO shop.products SELECT g, 'Product ' || g, 'SKU-' || g, g * 3 FROM generate_series(1, 20) g;

CREATE TABLE shop.newsletter (id integer PRIMARY KEY, email text, subscribed_at date);
INSERT INTO shop.newsletter SELECT g, 'reader' || g || '@example.net', '2025-02-01'::date + g FROM generate_series(1, 25) g;

CREATE TABLE shop.users (
  id integer PRIMARY KEY,
  username text,
  password_hash text,
  last_login_ip inet,
  email_verified boolean
);
INSERT INTO shop.users VALUES
{{- range $i := seq 1 20}}
  ({{$i}}, 'user{{$i}}', '{{bcrypt}}', '{{ip}}', {{if eq $i 1}}false{{else}}true{{end}}){{if lt $i 20}},{{else}};{{end}}
{{- end}}

CREATE TABLE shop.api_credentials (id integer PRIMARY KEY, service text, credential text);
INSERT INTO shop.api_credentials VALUES
  (1, 'aws', '{{awskey}}'), (2, 'github', '{{ghtoken}}'), (3, 'aws', '{{awskey}}'), (4, 'github', '{{ghtoken}}');

-- The support team reads customers only through this view, which runs with
-- shop_owner's rights and hands out the full card number.
CREATE VIEW support.customer_lookup AS
  SELECT c.id, c.first_name || ' ' || c.last_name AS customer_name, c.email, c.phone, c.card_number
  FROM shop.customers c;

CREATE VIEW support.order_status AS
  SELECT o.id, o.shipped_at, o.tracking_number FROM shop.orders o;

-- Runs with shop_owner's rights and sets no search_path.
CREATE FUNCTION shop.export_customers() RETURNS SETOF shop.customers
  LANGUAGE sql SECURITY DEFINER AS 'SELECT * FROM shop.customers';
REVOKE EXECUTE ON FUNCTION shop.export_customers() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION shop.export_customers() TO shop_etl;

-- The same, done right: search_path is pinned.
CREATE FUNCTION shop.count_orders() RETURNS bigint
  LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS 'SELECT count(*) FROM shop.orders';

GRANT USAGE ON SCHEMA shop TO PUBLIC;
GRANT USAGE ON SCHEMA support TO shop_support_team;

GRANT SELECT, INSERT, UPDATE ON shop.customers, shop.orders TO shop_app;
GRANT SELECT ON shop.products TO shop_app;
GRANT SELECT, UPDATE (password_hash, last_login_ip) ON shop.users TO shop_app;

GRANT SELECT ON support.customer_lookup, support.order_status TO shop_support_team;

GRANT SELECT (id, city, created_at) ON shop.customers TO shop_analyst;
GRANT SELECT ON shop.orders, shop.products TO shop_analyst;

GRANT SELECT ON shop.newsletter TO PUBLIC;

-- Tables shop_owner creates later in shop are readable by everyone.
ALTER DEFAULT PRIVILEGES IN SCHEMA shop GRANT SELECT ON TABLES TO PUBLIC;
RESET ROLE;
