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
  rule's text, example and rewrite, and a row with a probe for each form
  a rule flags), `answers.json` (written by `BARESQL_RECORD=1 go test
  ./portable -run 'TestCatalog$'` with all four engines), `rules.go` (one
  check per rule, in catalog order).
- `cmd/baresql`: CLI.
- `test/demo`: SQL of the demo projects the site uses (shop.sql, an app
  tested on SQLite, a MySQL app moving to PostgreSQL); `test/results`:
  what `scripts/record-results.sh` writes (never edit by hand).
- `site`: the generator of baresql.com. `site/src` holds the hand-written
  pages, posts and config with markers for values from `test/results`,
  `site/rules_site.py` the title and opening of each rule post; `python3
  site/build.py` writes a checkout of the site repo (default
  `../baresql.com`, or BARESQL_SITE) and `python3 site/check.py` checks it
  (site/README.md).

Site repo: github.com/photozeitgeist/baresql.com (Hugo, Launch template,
content and config values only; never touch the theme). Its content is
generated: edit `site/src`, never the site repo's content.

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
  flags each example and not its rewrite, flags each row's probe as that
  row (rules with several forms call `hitForm` with the row's name, so
  `--engines` and the message use the row's answers), and that severities
  match the answers: error when an engine refuses, otherwise not. A rule's
  severity is its catalog entry's. When a CI engine version answers
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
  `scripts/dev-engines.sh run scripts/record-results.sh` (about a minute).
  Re-record after any change that could move a number, then regenerate the
  site (`python3 site/build.py`, `python3 site/check.py`) and push the site
  repo. The audits there are the fixture tests' own, run by
  `TestRecordResults` in audit and portable. Re-record once more in the
  release commit, after dropping `-dev`, so the sample reports carry the
  released version.
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

0.1.0 was released on 2026-10-26 by the release workflow (tag v0.1.0);
the downloaded linux/amd64 archive matched SHA256SUMS and ran. It has
BareSQL Audit (audit, diff, --baseline, report.html, audit.json; five
fixtures: shop, saas, clinic, clean, and edge for corner cases such as a
real role named "PUBLIC"; views checked the way PostgreSQL checks them,
including views that fail) and BareSQL Portable (lint with 58 rules and 193
catalog rows, compare, rules; the catalog recorded on SQLite 3.45.1,
PostgreSQL 16.15, MySQL 8.0.46, MariaDB 10.11.14 and green on every CI
version). README is the manual; smoke.sh exercises every command. Two
independent reviews of the code and two of the site came before the
release, and test/results was recorded on the release commit. baresql.com
is generated by site/build.py and pushed to its repo; the owner connects
the domain. softdevstudio.com lists BareSQL with a launch note.

0.2.0-dev is open and has nothing in it yet.

## Gaps and to-do

- PostgreSQL 19 is due on 2026-11-19: add it to the CI list after release.
- Audit: one database per run; row security is judged per table (policy
  conditions are not evaluated); write access through views and functions
  is not traced; passwords outside ASCII are sent without SASLprep. From
  the review, left for later: a child table read only through its parent
  gets no line of its own (the parent's line carries its data); phone
  numbers inside arrays are found by column name only; the bare words
  `pan` and `tin` count as card and tax ID.
- Portable: lint reads tokens, not a full parse, and knows column types
  only from CREATE TABLE in the files it reads; JSON is covered only as
  far as JSON_EXTRACT, `->>` and JSONB exist, and stored procedures and
  triggers not at all; collation-dependent sort order
  (PostgreSQL's depends on the cluster locale) is not a rule; MariaDB's
  trailing-space equality ('a' = 'a ' is true there) is not a rule. The
  differences the review found without a rule (README, Limits: ALTER
  TABLE forms, reserved words, FILTER, FROM DUAL, AVG digits, ROUND of
  halves and the rest) are planned for 0.2, which starts at the owner's
  word.
- compare runs scripts with the logins' rights; it skips statements that
  leave the scratch space and refuses writes to other databases on
  MySQL/MariaDB, but it is no sandbox (README says so).
- Answers recorded on one version per engine; CI checks the others and
  version keys are added from its annotations.

## In progress: 0.2 (Phase A, Portable) and 0.3 (Phase B, Audit)

Approved by the owner on 2026-11-02 ("#6 abc and upkeep"), item 6 of
softdev-plan.md in photozeitgeist/jollyroger.biz: Phases A, B and C and
the upkeep. Each phase is its own release.

Phase A, 0.2.0: a rule for every difference the README listed without one.
Probed on SQLite 3.45.1, PostgreSQL 16.15, MySQL 8.0.46 and MariaDB
10.11.14 first; the catalog records them.

- `alter-column` (error), rows per form: ALTER COLUMN ... TYPE and SET/DROP
  NOT NULL (PostgreSQL only), MODIFY and CHANGE (MySQL and MariaDB only),
  ALTER COLUMN SET/DROP DEFAULT (not SQLite), several actions in one ALTER
  TABLE, ADD/DROP CONSTRAINT and ADD PRIMARY KEY (not SQLite), ADD/DROP
  COLUMN IF (NOT) EXISTS, RENAME without TO.
- `reserved-word` (error): names in name positions (column definitions,
  table names, aliases after AS, qualified names, INSERT column lists,
  known columns as bare items) that some engine reserves; one row per word,
  from a probe of every PostgreSQL and MySQL reserved word as a column name.
- `aggregate-filter` (error): COUNT(*) FILTER (WHERE ...); rewrite with CASE.
- `from-dual` (error): FROM DUAL; rewrite without it.
- `trim-from` (error): TRIM(LEADING/TRAILING/BOTH ... FROM ...) and
  TRIM(x FROM y) fail on SQLite; LTRIM/RTRIM with two arguments fail on
  MySQL and MariaDB.
- `substring-from` (error): SUBSTRING(x FROM a FOR b) fails on SQLite;
  rewrite SUBSTR(x, a, b).
- `drop-cascade` (error): DROP TABLE ... CASCADE/RESTRICT fail on SQLite;
  MySQL and MariaDB accept CASCADE and ignore it.
- `cast-to-float` (error): DOUBLE PRECISION and REAL (MariaDB refuses),
  DOUBLE (PostgreSQL refuses) and FLOAT (4-byte on MySQL and MariaDB).
- `avg-digits` (warning): AVG over integers or decimals gives 4 or 6
  digits on MySQL and MariaDB and 16 elsewhere; rewrite ROUND(AVG(x), n).
- `round-half` (warning): ROUND of a float half rounds away from zero on
  SQLite and to even elsewhere; rewrite via CAST(x AS DECIMAL(p, s)).
- `group-case` (info): GROUP BY, DISTINCT, COUNT(DISTINCT) and UNION
  over text fold case on MySQL and MariaDB; rewrite with LOWER().

Phase B, 0.3.0: `baresql audit` checks every database of a server in one
run (merged report, a database column), and evaluates row security
policies whose condition is a simple comparison with current_user, a
role or current_setting(), naming the policy that applies to each login.
Done when Audit runs on a two-database server and names the policy.

Phase C: PostgreSQL 19 in CI once it ships (due 2026-11-19).

Upkeep: README install links to releases/latest/download (done); this
section replaces the 0.1.0 plan; go.mod stays at go 1.24 (the sandbox has
only Go 1.24.7; release builds use the current stable Go).

Steps:
1. [x] upkeep
2. [ ] Phase A rules, catalog, answers, tests; README; record
3. [ ] independent review, fixes; release 0.2.0; site; note; plan
4. [ ] Phase B: multi-database audit, RLS evaluation, fixtures, tests
5. [ ] independent review, fixes; release 0.3.0; site; note; plan
6. [ ] Phase C: PostgreSQL 19 in CI after 2026-11-19
