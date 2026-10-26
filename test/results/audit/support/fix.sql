SET ROLE shop_owner;
DROP VIEW support.customer_lookup;
CREATE VIEW support.customer_lookup AS
  SELECT c.id, c.first_name || ' ' || c.last_name AS customer_name, c.email, c.phone
  FROM shop.customers c;
GRANT SELECT ON support.customer_lookup TO shop_support_team;
RESET ROLE;
