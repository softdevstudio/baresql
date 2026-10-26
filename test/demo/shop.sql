-- A small shop, written against SQLite in the tests.
CREATE TABLE products (id INTEGER PRIMARY KEY, name VARCHAR(40), price DECIMAL(10, 2), stock INTEGER, active BOOLEAN);
INSERT INTO products VALUES (1, 'Mug', 7.50, 10, TRUE), (2, 'mug lid', 2.25, 0, FALSE), (3, 'Teapot', 30.00, NULL, TRUE);

SELECT name FROM products WHERE name = 'mug';
SELECT name || ' (' || stock || ')' FROM products ORDER BY id;
SELECT SUM(stock) / COUNT(*) FROM products;
SELECT name FROM products ORDER BY stock;
SELECT COUNT(*) FROM products WHERE active = 1;
SELECT name, price FROM products GROUP BY active;
BEGIN;
SELECT IFNULL(stock, 0) FROM products ORDER BY id;
COMMIT;
