# BareSQL: where the next session starts

BareSQL is one Go binary, `baresql`, with two tools:

- BareSQL Audit (`baresql audit`, `baresql diff`) logs in to PostgreSQL and
  lists, for every login, each column it can read, the route that gives it
  (grant, role, view, function, ownership, row security) and whether the
  column holds personal data. It never writes and never calls user
  functions.
- BareSQL Portable (`baresql lint`, `baresql compare`, `baresql rules`)
  checks SQL meant for SQLite, PostgreSQL, MySQL and MariaDB: lint flags
  constructs that answer differently per engine, compare runs a script on
  real engines and reports where the answers differ.

Go, standard library only: the Go module proxy is blocked in Claude's
sandbox, and a tool that reads production databases is easier to trust
with no third-party code.

Layout:

- `internal/pgwire`: PostgreSQL wire client (TLS, SCRAM-SHA-256, MD5,
  cleartext, unix sockets).
- `internal/mysqlwire`: MySQL and MariaDB wire client (TLS,
  caching_sha2_password, mysql_native_password, sha256_password, clear
  password).
- `internal/testenv`: which engines a test may use (see Workflow).
- `internal/sqltext`: tokenizer and statement splitter for all four
  dialects (MySQL backslash escapes, `#` comments, dollar quotes, trigger
  bodies).
- `audit`: BareSQL Audit; `audit/testdata`: fixture databases with exact
  expectations per PostgreSQL version.
- `portable`: BareSQL Portable. `catalog.json` (written by hand: every
  rule's text, example and rewrite), `answers.json` (written by
  `BARESQL_RECORD=1 go test ./portable -run 'TestCatalog$'` with all four
  engines), `rules.go` (one check per rule, in catalog order).
- `cmd/baresql`: CLI.

Site repo: github.com/photozeitgeist/baresql.com (Hugo, Launch template,
content and config values only; never touch the theme).

## Workflow (low maintenance)

- Pull before working: ChatGPT/Codex also works on this repo (see
  AGENTS.md).
- The owner does not publish releases. While work is in progress VERSION
  and `version.go` end in `-dev`, and the release workflow skips them. To
  release: finish the CHANGELOG.md section, drop `-dev` in both files, push
  to main. When CI is green, `release.yml` builds Linux/macOS/Windows
  archives, smoke-tests them (`scripts/smoke.sh`, on Linux against real
  servers), tags `vX.Y.Z` and publishes. Then watch the run, check the
  release files and links, and run a downloaded binary. Afterwards raise
  VERSION to the next `X.Y.Z-dev` with a new CHANGELOG section.
- Engines for tests: BARESQL_TEST_PG, BARESQL_TEST_MYSQL and
  BARESQL_TEST_MARIADB hold connection strings, SQLite is the `sqlite3`
  program. A missing engine skips its tests unless BARESQL_REQUIRE_ENGINES
  lists it (pg, mysql, mariadb, sqlite, or all); CI sets it in every job.
  CI starts each engine version in Docker with `scripts/ci-engine.sh`.
- Claude's sandbox has no Docker. `scripts/dev-engines.sh setup` installs
  PostgreSQL 16, MySQL 8.0, MariaDB 10.11 and sqlite3 under
  /var/tmp/baresql-dev; the first time, run each server as a background
  job (`scripts/dev-engines.sh pg`, `mysql`, `mariadb`) and
  `scripts/dev-engines.sh passwords` once. After that,
  `scripts/dev-engines.sh run go test ./...` starts all three for one
  command and stops them again (background jobs are killed after two hours).
- Portable rules: a new rule is an entry in `portable/catalog.json` and a
  check in `portable/rules.go` at the same place in the order. Record its
  answers with `scripts/dev-engines.sh run env BARESQL_RECORD=1 go test
  ./portable -run 'TestCatalog$'`. `TestCatalogRules` checks that lint
  flags each example and not its rewrite. When a CI engine version answers
  differently, its annotation prints the key to add to answers.json by hand
  (recording keeps such keys): `"mysql 8.4"` for one release line,
  `"mysql >=9.6"` from a version on (find the version that changed it in
  the engine's release notes). An answer that holds only from some version
  is `since` in catalog.json instead; one that needs a SQLite build feature
  is `requires` (`math`, `ascii-case`; detected in `engineFeatures`).
  Lint's messages and `baresql rules` describe all of these
  (`VersionNotes`).
- Engine versions in CI: PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7,
  MariaDB 10.11, 11.4, 11.8 and 12.3. When a version is released or
  reaches end of life, change the lists in `.github/workflows/ci.yml`.
- Every number on the site comes from `test/results/`, written by
  `scripts/record-results.sh`. Re-record after any change that could move
  a number, and update the site from the new files.
- An independent review agent looks at code and site before a release is
  called done.
- main is the default branch. No side branches.
- GitHub job logs can't be downloaded from these sessions, but annotations
  can. Workflows print `::error::` lines for that reason
  (`scripts/ci-test.sh`). Read the real error before any rerun; at most two
  fix-and-retry rounds on one failure, then stop and report what's known
  and the options.
- Don't push to main while a release run is going (the release must tag
  main's head). The release runs after any CI run on main completes and
  builds the commit that CI run tested.
- Finding codes (Audit) and rule codes (Portable) are a public interface:
  never rename one. Add new codes instead, and document them in README.md.
- Fake secrets in fixtures (card numbers, keys, tokens) are generated when
  the test runs, never committed: GitHub push protection rejects them.

## State

0.1.0 in development (started 2026-10-26). Done: repo setup, CI with one
job per engine version, release workflow; pgwire and mysqlwire; BareSQL
Audit (audit, diff, --baseline, report.html, audit.json, four fixtures);
BareSQL Portable (lint with 57 rules, compare, rules, the catalog recorded
on SQLite 3.45.1, PostgreSQL 16.15, MySQL 8.0.46, MariaDB 10.11.14);
README as the manual; smoke.sh exercises every command.

## Gaps and to-do

- PostgreSQL 19 is due on 2026-11-19: add it to the CI list after release.
- Audit: one database per run; row security is judged per table (policy
  conditions are not evaluated); write access through views and functions
  is not traced; passwords outside ASCII are sent without SASLprep.
- Portable: lint reads tokens, not a full parse, and knows column types
  only from CREATE TABLE in the files it reads; JSON functions, stored
  procedures and triggers are not covered; collation-dependent sort order
  (PostgreSQL's depends on the cluster locale) is not a rule; MariaDB's
  trailing-space equality ('a' = 'a ' is true there) is not a rule.
- compare runs scripts with the logins' rights; it skips statements that
  leave the scratch space and refuses writes to other databases on
  MySQL/MariaDB, but it is no sandbox (README says so).
- Answers recorded on one version per engine; CI checks the others and
  version keys are added from its annotations.

## Next

Approved by the owner on 2026-10-26: build 0.1.0 with both tools, in this
order, pushing after each step:

1. Repo setup, CI and the release workflow. (done)
2. PostgreSQL and MySQL wire clients, with tests against real servers.
   (done)
3. BareSQL Audit for PostgreSQL: `baresql audit <dsn>` writes report.html
   and audit.json; routes through direct and column grants, group roles
   (inherit and SET ROLE), PUBLIC, ownership, superuser and
   pg_read_all_data, views and materialized views with owner rights,
   security-definer functions (flagged, never called), row security,
   default privileges, foreign server mappings, server file and program
   access, and write rights. Personal data by column names and up to 200
   sampled values per column (values never appear in reports;
   `--no-sample` turns sampling off). `baresql diff old.json new.json`, and
   `--baseline audit.json` to fail when reach grows. Planted fixture
   databases with exact expectations, near misses, PostgreSQL 13 and later,
   fuzzing, a 1,000-table and 50-login timing run. (done)
4. BareSQL Portable for SQLite, PostgreSQL, MySQL and MariaDB: `baresql
   lint` (static rules with portable rewrites), `baresql compare` (each
   engine in a throwaway space: a rolled-back transaction on PostgreSQL, a
   scratch database on MySQL and MariaDB, a temporary file on SQLite;
   answers compared by meaning), `baresql rules` (catalog with a minimal
   example, recorded answers per engine and the portable rewrite; CI
   re-runs the catalog). (done)
5. Record results, independent review, fixes, re-record.
6. baresql.com: pages Audit, Portable, Quick Start, Sample Reports,
   Reference, Download, About (with the name), Contact
   (info@baresql.com); posts: launch, the name, an explainer per tool, how
   each was tested, case studies and use scenarios, one post per catalog
   difference; sample report gallery including a correctly set up
   database.
7. Release 0.1.0 through the workflow, check the downloaded binary, add
   BareSQL to softdevstudio.com with a short note. The owner connects the
   domain at the end.
