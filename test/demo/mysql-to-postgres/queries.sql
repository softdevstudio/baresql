-- The queries of the app, as they ran on MySQL.

-- The search box.
SELECT id, email FROM customers WHERE name = 'Bob Stone';
SELECT id, email FROM customers WHERE name LIKE 'émile%';

-- Paid invoices.
SELECT COUNT(*) FROM invoices WHERE status = 'paid';

-- The address label.
SELECT CONCAT(name, ' (', country, ')') FROM customers ORDER BY id;

-- Invoices per customer.
SELECT COUNT(*) / COUNT(DISTINCT customer_id) FROM invoices;

-- Paying in three parts.
SELECT id, amount / 3 FROM invoices ORDER BY id;

-- Days since each invoice was issued, as of 1 June.
SELECT id, DATEDIFF('2026-06-01', issued_on) FROM invoices ORDER BY id;

-- Customers per country.
SELECT country, COUNT(*) FROM customers GROUP BY country ORDER BY country;

-- Page 2 of the customer list.
SELECT id, name FROM customers ORDER BY id LIMIT 2, 2;

-- The names on one line.
SELECT GROUP_CONCAT(name ORDER BY name SEPARATOR ', ') FROM customers;

-- Mark an invoice paid, adding it when it is missing.
INSERT INTO invoices (id, customer_id, amount, status, issued_on)
  VALUES (2, 1, 80.50, 'paid', '2026-05-01')
  ON DUPLICATE KEY UPDATE status = 'paid';
