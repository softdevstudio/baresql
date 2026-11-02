# Changelog

## 0.3.0

Not released yet.

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
