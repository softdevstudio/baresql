-- The same schema, rewritten to run on both MySQL and PostgreSQL, with
-- the data the queries are tried on.
CREATE TABLE customers (
  id INTEGER PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  email VARCHAR(255) NOT NULL,
  country CHAR(2),
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE invoices (
  id INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL,
  amount DECIMAL(10, 2) NOT NULL,
  status VARCHAR(10) NOT NULL,
  issued_on DATE NOT NULL
);

INSERT INTO customers VALUES
  (1, 'Ann Lee', 'ann@example.com', 'DE', '2026-01-05 10:00:00'),
  (2, 'bob stone', 'bob@example.com', NULL, '2026-02-11 09:30:00'),
  (3, 'Émile Roux', 'emile@example.com', 'FR', '2026-03-20 18:45:00');

INSERT INTO invoices VALUES
  (1, 1, 120.00, 'paid', '2026-04-01'),
  (2, 1, 80.50, 'open', '2026-05-01'),
  (3, 2, 300.00, 'Paid', '2026-05-03'),
  (4, 3, 45.25, 'open', '2026-05-20');
