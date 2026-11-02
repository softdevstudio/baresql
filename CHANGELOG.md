# Changelog

## 0.4.0 (in progress)

- PostgreSQL 19 in CI, once it ships (due 2026-11-19).

## 0.3.0

BareSQL Audit reads several databases in one run and reads the simple
shapes of row security policies. An independent review came before the
release; what it found is fixed with tests (audit/review_030_test.go).

- `--database NAME` (repeat) or `--all-databases`: each database is audited
  over its own connection and read-only transaction, and the report names
  every relation, function and foreign server after its database
  (`shop.app.orders`). Each login appears once; `cannot_connect_to` lists
  the databases it may not connect to. Findings about one database's
  objects or grants name the database; findings about roles and the server
  appear once. `diff` and `--baseline` refuse to compare audits of
  different databases (exit 2), except two `--all-databases` audits, where
  a database created or dropped is reported as a change.
- Policy conditions are read when they are true, false, or a column
  compared with `current_user`, `session_user`, constants or
  `current_setting()`, joined with AND and OR. True gives `all rows (policy
  p lets every row through)`, false (or a restrictive policy that is
  false, or an ALL policy with only WITH CHECK, when reading) gives no
  rows, and the rest add the condition in words to the route
  (`rows_where` in audit.json), naming the policies BareSQL can't read.
- New finding `rls-session-setting`: a permissive or restrictive policy
  picks rows by a setting such as `app.tenant_id`, which any session can
  change, so the logins it covers can read rows meant for others, unless a
  restrictive policy ties the rows to the role's own name. Its subject
  names the table and the policy.
- A policy that lets every row through no longer counts as "statistics
  only" for a schema the login can't use: pg_stats hides such tables.
- On PostgreSQL 17 and later, a superuser auditor's connections for
  several databases turn login event triggers off.
- Audits from 0.2 work as baselines: a true policy, written as "rows allowed
  by policy p" then, is no growth.

## 0.2.0

BareSQL Portable gets a rule for every difference the 0.1.0 README listed
without one: 11 new rules, 69 in all. Each was run on SQLite, PostgreSQL,
MySQL and MariaDB, and CI runs them again on every engine version it tests.

- `alter-column`: 21 forms of ALTER TABLE, each with the engines that
  refuse it: ALTER COLUMN ... TYPE and SET/DROP NOT NULL (PostgreSQL only),
  MODIFY and CHANGE (MySQL and MariaDB only), ALTER COLUMN ... SET or DROP
  DEFAULT, several actions in one statement, ADD/DROP CONSTRAINT, ADD
  INDEX, DROP CHECK, DROP INDEX/PRIMARY KEY/FOREIGN KEY, ADD COLUMN IF NOT
  EXISTS, DROP COLUMN IF EXISTS or CASCADE, ALTER TABLE IF EXISTS, AFTER
  and FIRST, RENAME without TO, RENAME a TO b without COLUMN, and the
  ALGORITHM and LOCK options.
- `reserved-word`: names that are reserved words on some engines (rank,
  key, user, groups, row and 78 more), wherever lint can tell a word is a
  name. After AS in a select list, where PostgreSQL takes any word, only
  the other engines count.
- `aggregate-filter`: COUNT(*) FILTER (WHERE ...), which MySQL and MariaDB
  lack.
- `from-dual`: FROM DUAL, which SQLite and PostgreSQL lack.
- `trim-from`: TRIM(LEADING/TRAILING/BOTH ... FROM ...) and TRIM(x FROM s),
  which SQLite lacks, and the two-argument TRIM, LTRIM and RTRIM, which
  MySQL and MariaDB lack.
- `substring-from`: SUBSTRING(s FROM a FOR b), which SQLite lacks, the
  FOR-only form and SUBSTR ... FROM, and SUBSTRING(s FROM 'pattern'), a
  regular expression on PostgreSQL only.
- `drop-cascade`: DROP TABLE ... CASCADE or RESTRICT; SQLite refuses both,
  and MySQL and MariaDB ignore CASCADE.
- `cast-to-float`: CAST to DOUBLE PRECISION or REAL (MariaDB refuses),
  DOUBLE (PostgreSQL refuses) and FLOAT (4 bytes on MySQL and MariaDB).
- `avg-digits`: AVG of integers or decimals has 4 more decimal places than
  the values on MySQL and MariaDB and about 16 digits elsewhere; averages
  that are rounded or compared aren't flagged.
- `round-half`: ROUND of a floating point half rounds away from zero on
  SQLite and to even elsewhere, PostgreSQL has no ROUND(float, n), and
  SQLite rounds decimals to digits through a binary float (ROUND(0.15, 1)
  is 0.1 there and 0.2 elsewhere).
- `group-case` (a note, as it depends on the data): GROUP BY, DISTINCT,
  COUNT(DISTINCT), UNION, INTERSECT and EXCEPT over text merge 'x'
  and 'X' on MySQL and MariaDB.

Fixed in older rules: table-options no longer flags ON UPDATE CASCADE
after a REFERENCES clause in a column definition, and lint looks up a
column correctly when a statement names the same table twice.

Also: the README links each release archive directly
(releases/latest/download).

## 0.1.0

The first release: BareSQL Audit for PostgreSQL and BareSQL Portable for
SQLite, PostgreSQL, MySQL and MariaDB, in one binary for Linux, macOS and
Windows. An independent review of both tools came before it; what it
found is fixed or listed under Limits in the README.

BareSQL Audit (PostgreSQL 13 to 18):

- `baresql audit` lists, for every login, each column it can read, every
  route to it (grants, role membership with INHERIT and SET ROLE, PUBLIC,
  ownership, superuser, pg_read_all_data, views with their owner's rights,
  security-definer functions, row security, parent tables) and whether it
  holds personal data, found from column names and sampled values that never
  leave the program. It writes report.html and audit.json and has 20
  findings.
- Views are followed the way PostgreSQL checks them: every column a view's
  query reads, with the owner's or (security_invoker) the reader's rights.
  A view that PostgreSQL would refuse is listed as one that fails.
- Logins without CONNECT on the database are marked (`cannot_connect`), and
  a grant in a schema without USAGE shows as the statistics it still gives
  through pg_stats.
- It works in a read-only transaction and never calls a user-defined
  function: values are read through their types' output functions, each
  table on its own with ONLY (or through its top parent when the login may
  read a partition only that way), and foreign tables never. JSON keys in the evidence show
  as `*` unless they are words the rules know. What it could not see is
  listed in the report.
- `baresql diff old.json new.json` and `baresql audit --baseline` exit 1
  when any login reaches more than before, row security that lets more rows
  through included.

BareSQL Portable:

- `baresql lint` reports 58 kinds of SQL that fail or answer differently
  on SQLite, PostgreSQL, MySQL and MariaDB, each with what to write
  instead. `--engines` narrows it to the engines a project uses.
- `baresql compare` runs scripts on real engines, each in a throwaway space,
  and reports the statements whose answers differ, compared by meaning. It
  sends PostgreSQL one statement at a time, refuses statements that name
  another database on MySQL and MariaDB, and skips what would leave the
  throwaway space. Scripts for the mysql program may use DELIMITER.
- `baresql rules` shows each difference with a minimal example, every
  engine's recorded answer and a form that answers the same everywhere.
