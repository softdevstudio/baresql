# Changelog

## 0.2.0

Not released yet.

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
