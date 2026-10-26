-- A small booking app. Its tests build this schema in SQLite, in memory;
-- production runs it on PostgreSQL.
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email VARCHAR(255) NOT NULL,
  name VARCHAR(100),
  is_admin BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE bookings (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users (id),
  starts_at TIMESTAMP NOT NULL,
  nights INTEGER NOT NULL,
  price_cents INTEGER NOT NULL,
  cancelled_at TIMESTAMP
);

INSERT INTO users VALUES
  (1, 'ann@example.com', 'Ann Lee', FALSE, '2026-01-05 10:00:00'),
  (2, 'Bob@Example.com', 'Bob Stone', TRUE, '2026-02-11 09:30:00'),
  (3, 'cleo@example.com', NULL, FALSE, '2026-03-20 18:45:00');

INSERT INTO bookings VALUES
  (1, 1, '2026-06-01 15:00:00', 3, 36000, NULL),
  (2, 1, '2026-07-10 15:00:00', 2, 25000, '2026-06-30 08:00:00'),
  (3, 2, '2026-06-15 15:00:00', 5, 61000, NULL),
  (4, 3, '2026-08-01 15:00:00', 1, 13000, NULL);
