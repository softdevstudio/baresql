-- The queries the app runs. Every one passes the test suite on SQLite.

-- Sign-up: skip an address that is already registered.
INSERT OR IGNORE INTO users (id, email, name, created_at)
  VALUES (1, 'ann@example.com', 'Ann Lee', '2026-01-05 10:00:00');

-- The admin search box.
SELECT id, email FROM users WHERE email LIKE 'bob%';

-- Who may open the admin pages.
SELECT email FROM users WHERE is_admin = 1;

-- The greeting: name, or the address when there is no name.
SELECT COALESCE(name, email) FROM users ORDER BY id;

-- Guests by name, for the front desk.
SELECT b.id, u.name FROM bookings b JOIN users u ON u.id = b.user_id ORDER BY u.name;

-- Each guest with one of their bookings.
SELECT u.id, u.email, b.starts_at FROM users u JOIN bookings b ON b.user_id = u.id
  GROUP BY u.id ORDER BY u.id;

-- Check-out dates.
SELECT id, date(starts_at, '+' || nights || ' days') AS checkout FROM bookings ORDER BY id;

-- Revenue per month.
SELECT strftime('%Y-%m', starts_at) AS month, SUM(price_cents) FROM bookings
  GROUP BY month ORDER BY month;

-- Average booking value.
SELECT AVG(price_cents) FROM bookings WHERE cancelled_at IS NULL;

-- A booking from the web form, where the price arrives as text.
INSERT INTO bookings VALUES (5, 2, '2026-09-01 15:00:00', 2, '240.00', NULL);

-- Accounts opened since February.
SELECT COUNT(*) FROM users WHERE created_at >= '2026-02-01';
