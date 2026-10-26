SET ROLE shop_owner;
CREATE TABLE shop.refunds (id integer PRIMARY KEY, order_id integer, iban text, amount numeric(10,2));
INSERT INTO shop.refunds VALUES ... twelve refunds with IBANs, made by the test ...;
RESET ROLE;
GRANT shop_support_team TO shop_intern;
REVOKE SELECT ON shop.products FROM shop_analyst;
