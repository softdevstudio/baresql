# BareSQL

BareSQL is one small program with two tools for SQL databases.

**BareSQL Audit** logs in to a PostgreSQL database and lists, for every
login, each column it can read, how it gets there (a grant, a role, a view,
a function, ownership) and whether the column holds personal data. It reads
the system catalogs and a small sample of values inside a read-only
transaction, and it never calls a function you wrote.

**BareSQL Portable** checks SQL that has to run on SQLite, PostgreSQL,
MySQL and MariaDB. `baresql lint` points out constructs that answer
differently on different engines, with a form that answers the same
everywhere. `baresql compare` runs the same script on real engines and
reports every statement whose answers differ. `baresql rules` lists the
differences BareSQL knows, each with a minimal example and the answer every
engine gave.

The program is a single file with no dependencies. It is written in Go with
the standard library only, and it talks to PostgreSQL, MySQL and MariaDB
over their own network protocols. SQLite runs through the `sqlite3`
program you have installed.

- [Install](#install)
- [BareSQL Audit](#baresql-audit)
- [BareSQL Portable](#baresql-portable)
- [Exit codes](#exit-codes)
- [What BareSQL is tested on](#what-baresql-is-tested-on)
- [Building and testing](#building-and-testing)

## Install

Download the archive for your system from the
[releases page](https://github.com/softdevstudio/baresql/releases), unpack
it and put `baresql` (or `baresql.exe`) on your PATH. Each release lists
SHA-256 checksums for its files.

Or build it yourself with Go 1.24 or later:

```
go install github.com/softdevstudio/baresql/cmd/baresql@latest
```

`baresql version` prints the version, and `baresql help <command>` the
flags of a command.

## BareSQL Audit

```
baresql audit [flags] [connection]
baresql diff old.json new.json
```

`baresql audit` answers one question for a PostgreSQL database: which login
can read which column, and how. For each login it lists every column it can
read and every way it gets there, marks the columns that hold personal data,
and adds findings for the setups that usually lead to leaks. It writes two
files:

- `report.html`, a page to read and share: findings first, then one table
  per login with the personal data it reaches and the route to each column.
- `audit.json`, the same facts for scripts and for `baresql diff`.

It also prints a short summary. Values read while looking for personal
data are never written anywhere: the report says which category a column
looks like (an email address, a card number) and why, never what it holds.

### Connecting

The connection is a `postgres://` URL or a `key=value` string, as for
`psql`:

```
baresql audit "postgres://auditor@db.example.com/shop?sslmode=verify-full"
baresql audit "host=db.example.com dbname=shop user=auditor"
```

Whatever the connection leaves out comes from the usual `PG*` variables
(`PGHOST`, `PGUSER`, `PGPASSWORD` ...) and from `~/.pgpass`, so the password
never has to be on the command line. TLS follows `sslmode` (`disable`,
`prefer` by default, `require`, `verify-ca`, `verify-full`) with
`sslrootcert`; `sslrootcert=system` uses the system's certificates and
implies `verify-full`. Unix sockets work as with `psql`
(`host=/var/run/postgresql`). Logins use SCRAM-SHA-256 (with channel
binding over TLS), MD5 or a cleartext password, whichever the server asks
for.

### Which login to audit with

Any login can run the audit: the catalogs it reads are visible to every
role, and the server's own `has_*_privilege` functions answer for every
role. Two things need more:

- Looking at values (to tell an `email` column from one named `notes` that
  holds email addresses) needs read access to the tables. A superuser, or a
  member of `pg_read_all_data`, can sample everything; another login samples
  what it may read.
- Telling MD5 passwords from SCRAM needs a superuser, who can read
  `pg_authid`.

Whatever the audit could not see is listed under "Not seen" in the report
and the summary, so a partial audit says what it left out.

### What the audit does to the database

It opens one connection, starts a read-only transaction (`BEGIN ISOLATION
LEVEL REPEATABLE READ, READ ONLY`), sets `statement_timeout`, `lock_timeout`
and `search_path` for that transaction only, and turns row security off for
its own sampling queries (a non-superuser then sees an error instead of
silently filtered rows, and the table is left out of sampling). It never
writes, never calls user-defined functions (views are read from their stored query trees, and sampling
reads tables and materialized views only) and rolls the transaction back
at the end.

### Flags

| Flag | Meaning |
|---|---|
| `--out DIR` | where to write report.html and audit.json (default `baresql-report`) |
| `--login NAME` | audit only these logins (repeat, or separate with commas) |
| `--schema NAME` | audit only these schemas (repeat, or separate with commas) |
| `--no-sample` | read no table data: find personal data by column names only |
| `--sample-rows N` | rows to read per table when sampling, 1 to 10000 (default 200) |
| `--baseline FILE` | an earlier audit.json: exit 1 if any login can now reach more |
| `--timeout D` | time limit for each query, such as `30s` (default 60s) |
| `--format F` | what to print: `summary` (default), `lines` (one fact per line) or `json` |

### Routes

A route is how a login reaches a column. The report shows each one in
words:

- `granted to analyst through reporting > analyst`: a grant on the table to
  a role the login belongs to (here through two levels of membership).
  `column granted to` marks a column-level grant.
- `owner app`: the login owns the table, or belongs to the role that does.
- `member of pg_read_all_data` (PostgreSQL 14 and later).
- `superuser`.
- `after SET ROLE admin: ...`: membership without inheritance (`NOINHERIT`,
  or `INHERIT FALSE` on PostgreSQL 16 and later) still lets the login switch
  roles.
- `through view orders_v (rights of app)`: a view reads the table with its
  owner's rights, so the reader needs no access to the table itself. With
  `security_invoker` (PostgreSQL 15 and later) it says `rights of the
  reader`; PostgreSQL checks such a view as the reader even when another
  view reads it.
- `; all rows (...)`, `; rows allowed by policy ...` or `; no rows (...)`:
  what row security lets the login see on that route.
- `; reading it fails: the view needs app.accounts.email, which the reader
  may not read`: PostgreSQL checks every column a view's query reads (in the
  select list, joins, WHERE and subqueries), with the rights the view runs
  with, and refuses the whole query when one is missing, even a query that
  selects other columns of the view. The grant is there, and gives nothing
  until the missing access is added.
- `with grant option`: the login may pass the access on.
- `may run billing.charge(integer)`: a security-definer function that reads
  the table with its owner's rights. BareSQL never runs it.

The audit computes each route from the catalogs and then asks the server
(`has_column_privilege` and its relatives) whether the login really has the
access. When the two disagree the server wins, and the finding
`unexplained-access` lists the places.

### Personal data

A column counts as personal data when its name or its sampled values say
so. Names are split into words (`customerEmail2` reads as customer, email,
2) and matched in English and the common European languages. Values are
checked with real tests where there are any: card numbers pass the Luhn
check and have a card issuer's prefix, IBANs pass their mod-97 check,
national ID numbers their check digits, JSON documents are read key by key.
A column holds a category when enough sampled values pass; the evidence is
in the report.

The categories are part of audit.json and never renamed:

| Category | Meaning |
|---|---|
| `email` | email address |
| `phone` | phone number |
| `name` | person's name |
| `address` | postal address |
| `birth-date` | date of birth |
| `national-id` | national ID number |
| `passport` | passport or licence number |
| `tax-id` | tax ID |
| `card` | payment card number |
| `bank-account` | bank account (IBAN) |
| `password` | password or password hash |
| `secret` | key, token or secret |
| `ip-address` | IP address |
| `location` | precise location |
| `health` | health data |
| `sensitive` | special category data (religion, ethnicity, beliefs ...) |
| `gender` | gender |
| `insurance-id` | insurance number |

### Findings

Each finding has a code that never changes, a severity and a subject (a
login, a table, a function).

| Code | Severity | What it means |
|---|---|---|
| `public-reads-personal` | high | every role, including roles created later, can read personal data in a table |
| `superuser-login` | medium | a login is a superuser |
| `set-role-superuser` | high | a login can SET ROLE to a superuser |
| `read-all-data` | medium | a login is a member of pg_read_all_data or pg_write_all_data |
| `createrole-escalation` | high | a login has CREATEROLE before PostgreSQL 16, where it can grant itself any role but superuser |
| `see-all-queries` | low | a login can read the text of every session's queries |
| `server-file-access` | high | a login can read or write files, or run programs, on the database server |
| `grant-option-personal` | medium | a login may grant read access to personal data to others |
| `admin-option` | medium | a login can add members to a role that reads personal data |
| `foreign-server-mapping` | low | a login can use a foreign server through a user mapping |
| `secdef-no-search-path` | high | a security-definer function others can run does not set search_path |
| `secdef-executable` | medium | a security-definer function runs with the rights of an owner who reads personal data |
| `rls-no-policies` | low | row security is on for a table without any policy |
| `rls-owner-login` | medium | row security is not forced and the table's owner can log in |
| `view-bypasses-row-security` | high | a view reads a row-security table with rights that skip its policies |
| `default-grants-to-public` | medium | default privileges grant future tables or functions to PUBLIC |
| `md5-passwords` | medium | logins have MD5 password hashes |
| `connection-not-encrypted` | low | the audit's own connection did not use TLS |
| `cleartext-password-login` | medium | the server asked for a cleartext password over an unencrypted connection |
| `unexplained-access` | medium | the catalogs and the server's own checks disagree; please report it |

### Comparing audits

```
baresql diff old.json new.json
baresql audit --baseline last-week.json postgres://...
```

`baresql diff` lists what grew (a login reaches something it did not reach
before, or a column it reads now holds more kinds of personal data), what
is gone and the other changes, such as new routes and new findings. Routes
that give no rows today (row security with no policy for the login, a view
that fails) don't count as reach, so a policy or a grant that makes them
give rows counts as growth. It exits 1 when any login reaches more than
before, so a scheduled job can run the audit with `--baseline` and fail
when access grows. `--format json` gives the comparison as JSON.

### Limits

- One database per run. Roles belong to the whole server; tables are
  audited one database at a time.
- Row security is judged per table: the report says whether policies apply
  on a route, and does not evaluate the policies' conditions.
- Write access is listed per table and not followed through views or
  functions.
- Passwords with characters outside ASCII are sent as they are, without
  SASLprep normalization; a server that stored such a password normalized
  may refuse it.

## BareSQL Portable

```
baresql lint [flags] file.sql ...
baresql compare --sqlite --postgres URL --mysql URL --mariadb URL [flags] file.sql ...
baresql rules [code]
```

### lint

`baresql lint` reads SQL files (`-` reads standard input) and reports the
constructs that fail, or answer differently, on at least one of SQLite,
PostgreSQL, MySQL and MariaDB. It connects to nothing.

```
$ baresql lint shop.sql
shop.sql:6:8: warning: || joins text on SQLite and PostgreSQL. On MySQL and MariaDB it means OR, so 'a' || 'b' is 0. [pipes-concat]
    name || ' (' || stock || ')'
    Use CONCAT(a, b), which all four engines have (SQLite from version 3.44). CONCAT treats NULL differently on different engines; see concat-null.
shop.sql:7:8: warning: Dividing an integer by an integer drops the fraction on SQLite and PostgreSQL; MySQL and MariaDB keep it. [integer-division]
    SUM(stock) / COUNT(*)
    Multiply one side by 1.0 for the fraction and round it to the places you need (see decimal-division): ROUND(7 * 1.0 / 2, 2) is 3.5 on every engine. For a whole number on every engine, take the remainder off first: (7 - 7 % 2) / 2.
shop.sql:9:37: error: PostgreSQL has no = between a boolean and a number. [boolean-as-number]
    active = 1
    Compare and insert TRUE and FALSE (active = TRUE, or simply WHERE active). All four engines accept them.
shop.sql:10:8: error: A selected column that is neither grouped nor aggregated takes its value from any row of the group on SQLite and MariaDB; PostgreSQL and MySQL refuse the query. [ungrouped-column]
    name
    Add the column to GROUP BY, or say which value you want with an aggregate such as MAX(b).
shop.sql:10:14: error: A selected column that is neither grouped nor aggregated takes its value from any row of the group on SQLite and MariaDB; PostgreSQL and MySQL refuse the query. [ungrouped-column]
    price
    Add the column to GROUP BY, or say which value you want with an aggregate such as MAX(b).
shop.sql:12:8: error: IFNULL() works on SQLite, MySQL and MariaDB; PostgreSQL refuses it. [function-not-everywhere]
    IFNULL(stock, 0)
    Use COALESCE(a, b).

6 problems: 4 errors, 2 warnings. 4 notes not shown (differences that depend on the data); --all shows them.
```

Each problem has a severity:

- `error`: at least one engine refuses the statement.
- `warning`: every engine runs it, and they answer differently.
- `info`, a note: the answers differ only for some data, such as text in
  mixed case or columns that hold NULL. Notes are left out unless you pass
  `--all`.

Lint learns column types from the `CREATE TABLE` statements it reads, so
pass the schema file along with the queries (`baresql lint schema.sql
queries.sql`) and it can tell an integer division from a decimal one.

| Flag | Meaning |
|---|---|
| `--all` | also report notes |
| `--engines LIST` | only differences among these engines matter, such as `sqlite,postgres` |
| `--ignore CODES` | leave these rule codes out (repeat, or separate with commas) |
| `--format F` | `text` (default) or `json` |

`--engines` keeps a problem when the construct it found answers
differently, by meaning, among the engines named: each construct has
answers recorded on all four engines (the rule's example, or its own row in
the catalog where a rule covers several forms, such as INSERT IGNORE and
INSERT OR IGNORE). So `--engines sqlite,postgres` drops the MySQL-only
problems from a project that tests on SQLite and runs on PostgreSQL.

A comment `-- baresql:ignore integer-division` on the line of a problem, or
on the line before it, silences that rule there. Several codes are separated
by commas; `-- baresql:ignore` alone silences every rule on that line.

### compare

`baresql compare` runs scripts on two or more real engines, statement by
statement, and prints the statements whose answers differ.

```
$ export PGPASSWORD=... MYSQL_PWD=...
$ baresql compare --sqlite --postgres postgres://dev@localhost/scratch \
    --mysql mysql://dev@localhost:3306/ shop.sql
Compared on SQLite 3.45.1, PostgreSQL 16.15, MySQL 8.0.46.
...
shop.sql:7  SELECT SUM(stock) / COUNT(*) FROM products
  SQLite and PostgreSQL: 3
  MySQL: 3.3333
  lint: integer-division, division-by-zero
...
11 statements: 2 answered alike, 7 differ, 2 skipped.
```

| Flag | Meaning |
|---|---|
| `--sqlite` | run on SQLite, in a new temporary database |
| `--sqlite=FILE` | run on SQLite, in a temporary copy of FILE (the file itself is never opened) |
| `--sqlite-program PATH` | the sqlite3 program to use (default: `sqlite3` on PATH) |
| `--postgres URL` | run on PostgreSQL |
| `--mysql URL` | run on MySQL |
| `--mariadb URL` | run on MariaDB |
| `--all` | list every statement, also those answered alike |
| `--format F` | `text` (default) or `json` |

Connections are URLs: `postgres://user@host/db` (or any form `baresql
audit` takes), `mysql://user@host:3306/` and `mariadb://user@host:3307/`.
MySQL and MariaDB URLs take `tls=disable|preferred|require|verify`,
`tlsca=FILE` and `socket=PATH`. Leave passwords out of the URLs and set
`PGPASSWORD` or `MYSQL_PWD`.

**Each engine works in a throwaway space.** On PostgreSQL the whole script
runs in one transaction that is rolled back at the end, with a scratch
schema first on the search path. On MySQL and MariaDB it runs in a new
scratch database (`baresql_compare_` and random letters) that is dropped at
the end. On SQLite it runs in a temporary file, through `sqlite3 -safe`,
which refuses ATTACH, VACUUM INTO, readfile(), writefile(),
load_extension() and the shell's own commands.

compare skips statements that would leave that space: transaction control
(BEGIN, COMMIT, ROLLBACK, SAVEPOINT, PREPARE TRANSACTION, XA), USE,
statements about databases, schemas, users and roles, GRANT and REVOKE,
server commands such as LOAD DATA, COPY and SELECT ... INTO OUTFILE, SET
statements that change a global or persisted setting anywhere in their
list, dynamic SQL (MySQL's PREPARE ... FROM and EXECUTE IMMEDIATE, also
inside routines), tables that connect to other servers (FEDERATED, CONNECT,
SPIDER) and the calls that read files where the engine runs (fsdir() in the
sqlite3 shell, which `-safe` leaves on, and LOAD_FILE()). These checks read
each statement the way MySQL and MariaDB do as well, so code inside a
`/*! ... */` comment counts.

Each engine also makes sure it runs one statement at a time. PostgreSQL
gets every statement on its own through the extended query protocol, which
takes exactly one: a second statement hidden from BareSQL's splitter (behind
a `#`, which PostgreSQL reads as an operator, or a backslash it keeps in a
string) is refused, and if the transaction ends anyway compare stops at once.
sqlite3 gets nothing that SQLite would read as two statements, and MySQL
and MariaDB refuse more than one by themselves, since BareSQL's client never
asks for multi-statements. On MySQL and MariaDB, where changes can't be
undone, compare also refuses every statement that names a table in another
database (`other_db.t`, anywhere in the statement); the list of databases
comes from the server when compare starts.

A statement may run for 60 seconds on every engine (PostgreSQL's
statement_timeout, MySQL's max_execution_time for SELECT, MariaDB's
max_statement_time, a timer for sqlite3). Other statements on MySQL meet a
client limit of 75 seconds, after which compare stops.

That keeps an ordinary script, and the usual ways one goes wrong, inside
the throwaway space. It is no sandbox against a script written to get out:
a script runs with the rights of the logins you give. Use logins that may
do no more than the script needs, on servers meant for testing.

Scripts written for the mysql program may switch the delimiter: between
`DELIMITER //` and `DELIMITER ;` statements end at `//`, so a procedure's
body stays in one statement. `--sqlite shop.db`, with a space, would make
shop.db a script; compare points to `--sqlite=shop.db` instead.

**Answers are compared by meaning.** Two answers are the same when they
hold the same rows with the same values:

- numbers by value: `3.5` equals `3.5000` and `3` equals `3.0`; a float
  equals a decimal when they agree to about 12 significant digits
  (`0.30000000000000004` equals `0.3`);
- `true` equals `1` and `false` equals `0`;
- dates and times however they are printed (`2024-02-01 00:00:00` equals
  `2024-02-01T00:00:00+00:00`);
- JSON documents by structure, bytes by value;
- text exactly, so `'a'` and `'A'` differ, NULL differs from the empty
  string, and text is never a number: `'7'` differs from `7`;
- row order only when the statement has an ORDER BY of its own. Rows that
  tie in that ORDER BY may come in any order, and compare reports that too:
  sort on a column that makes the order unique.

Column names count only when the values match and the names differ in case
(PostgreSQL folds unquoted names to lower case), since code that reads
columns by name breaks there.

compare needs sqlite3 3.37 or later, for its safe mode.

### rules

`baresql rules` lists the differences lint knows. `baresql rules CODE`
shows one: what differs and why, a minimal example with the answer each
engine gave, a form that answers the same everywhere (where one exists)
and what to write instead. `--format json` gives the whole catalog with
its answers.

The answers come from running each example on real engines; the file
`portable/answers.json` keeps them, and CI runs every example again on each
engine version it tests. Lint's messages for functions, operators, types
and the other forms with a row in the catalog come from those runs too, and
say where an answer depends on the version ("MD5() works on PostgreSQL,
MySQL (before 9.6) and MariaDB"). So does each problem's severity: an error
when an engine refuses the construct, a warning when they all run it. Some builds
of sqlite3 leave out its math functions (LOG, POWER, TRUNC and others) or
fold case beyond A to Z; the catalog says which answers assume the usual
build.

### Rule codes

Rule codes never change.

| Code | Severity | Title |
|---|---|---|
| `integer-division` | warning | Integer division |
| `decimal-division` | warning | Dividing decimals |
| `pipes-concat` | warning | \|\| joins text on some engines only |
| `concat-null` | warning | CONCAT with a NULL argument |
| `text-compare-case` | info | Text comparisons and letter case |
| `like-case` | info | LIKE and letter case |
| `text-number-compare` | error | Comparing text with a number |
| `double-quotes` | warning | Double quotes |
| `null-order` | info | Where NULL sorts |
| `ungrouped-column` | error | Columns missing from GROUP BY |
| `cast-to-text` | warning | CAST to text |
| `cast-to-number` | error | CAST to an integer or a decimal |
| `cast-to-date` | warning | CAST to a date |
| `date-arithmetic` | warning | Adding numbers to dates |
| `division-by-zero` | info | Division by zero |
| `decimal-as-float` | info | DECIMAL on SQLite |
| `float-precision` | warning | REAL and FLOAT precision |
| `char-padding` | info | CHAR(n) and trailing spaces |
| `length-bytes` | info | LENGTH counts bytes on MySQL |
| `lower-non-ascii` | info | LOWER and UPPER outside A to Z |
| `substring-start` | warning | SUBSTR from position 0 or below |
| `backslash-escapes` | warning | Backslashes in strings |
| `like-escape` | warning | Escaping % and _ in LIKE |
| `log-base` | warning | LOG with one argument |
| `caret-operator` | error | The ^ operator |
| `dash-comment` | warning | -- comments need a space on MySQL |
| `column-name-case` | warning | Upper-case column names |
| `table-name-case` | error | Table names in a different case |
| `type-not-enforced` | error | SQLite keeps values that do not fit the column |
| `boolean-as-number` | error | Booleans and numbers |
| `auto-increment` | error | Numbering new rows |
| `backticks` | error | Backticks around names |
| `hash-comment` | error | # comments |
| `nulls-first-last` | error | NULLS FIRST and NULLS LAST |
| `limit-comma` | error | LIMIT offset, count |
| `fetch-first` | error | FETCH FIRST n ROWS ONLY |
| `alias-in-where` | error | Select-list names in WHERE |
| `alias-in-having` | error | Select-list names in HAVING |
| `limit-in-subquery` | error | LIMIT inside IN (...) |
| `update-same-table` | error | UPDATE or DELETE that reads its own table |
| `update-join` | error | UPDATE and DELETE with a join |
| `full-outer-join` | error | FULL OUTER JOIN |
| `distinct-on` | error | DISTINCT ON |
| `count-distinct-columns` | error | COUNT(DISTINCT a, b) |
| `greatest-least` | error | GREATEST and LEAST |
| `insert-ignore` | error | Inserting only new rows |
| `upsert` | error | Insert or update (upsert) |
| `returning` | error | RETURNING |
| `varchar-length` | error | VARCHAR without a length |
| `type-not-everywhere` | error | Column types that exist on some engines only |
| `table-options` | error | MySQL table options |
| `text-default` | error | DEFAULT on TEXT columns |
| `index-on-text` | error | Keys on TEXT columns |
| `index-if-not-exists` | error | CREATE INDEX IF NOT EXISTS |
| `truncate` | error | TRUNCATE |
| `create-or-replace` | error | CREATE OR REPLACE VIEW |
| `function-not-everywhere` | error | Functions that exist on some engines only |
| `operator-not-everywhere` | error | Operators that exist on some engines only |

### Limits

- lint reads tokens and recognizes the shapes of expressions. It does not
  parse SQL fully, so it can miss a construct written in an unusual way,
  and it knows column types only from the CREATE TABLE statements it reads.
- Some differences have no rule yet: ALTER TABLE syntax (ALTER COLUMN ...
  TYPE, MODIFY, CHANGE), names that are reserved words on some engines
  (user, key, rank, groups), aggregate FILTER (WHERE ...), FROM DUAL,
  TRIM(LEADING ... FROM ...) and SUBSTRING(x FROM ... FOR ...), the digits
  of AVG over integers, ROUND of floating point halves (SQLite rounds 2.5
  away from zero, the others to even), and GROUP BY and DISTINCT under MySQL's case
  folding. compare shows them when they come up.
- JSON functions and operators, stored procedures and triggers are left
  out: their syntax differs too much between engines to compare.
- Answers are recorded with the engines' default settings. Server modes
  (`sql_mode` on MySQL and MariaDB, `standard_conforming_strings` on
  PostgreSQL) and collations change some of them.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | nothing to report |
| 1 | the command found something: lint problems, answers that differ, access that grew past a baseline |
| 2 | the command could not run: bad arguments, no connection, unreadable files |

`baresql audit` exits 0 when it finishes, findings or not, unless
`--baseline` finds growth.

## What BareSQL is tested on

Every change runs the tests on Linux, macOS and Windows, and against real
servers in separate jobs: PostgreSQL 13, 14, 15, 16, 17 and 18, MySQL 8.0,
8.4 and 9.7, MariaDB 10.11, 11.4, 11.8 and 12.3, and the sqlite3 of the
Linux and macOS runners. The Audit tests build databases with planted
leaks and near misses and check the exact list of facts and findings for
each PostgreSQL version. The Portable catalog runs on every engine version
in the list.

## Building and testing

```
go build -o baresql ./cmd/baresql
go test ./...
```

Tests that need a database server read its address from
`BARESQL_TEST_PG`, `BARESQL_TEST_MYSQL` or `BARESQL_TEST_MARIADB`, and use
the `sqlite3` program for SQLite; without them those tests are skipped.
`scripts/dev-engines.sh` sets up local servers where Docker is missing.
CLAUDE.md and AGENTS.md describe how the project is maintained.

## License

Apache License 2.0. Copyright 2026 SoftDevStudio.com. See LICENSE and
NOTICE.
