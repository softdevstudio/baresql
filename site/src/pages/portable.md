---
title: "BareSQL Portable"
url: "/portable/"
type: page
draft: false
---

BareSQL Portable checks SQL that has to run on SQLite, PostgreSQL, MySQL and MariaDB. It knows {{v:rules}} differences between those engines: {{v:rule_errors}} where at least one engine refuses the statement, {{v:rule_warnings}} where every engine runs it and the answers differ, and {{v:rule_notes}} where the answers differ only for some data. Each one was run on all four engines, and each comes with a form that answers the same everywhere, where one exists.

It has three commands. `baresql lint` reads SQL files and points at the differences without connecting to anything. `baresql compare` runs a script on real engines and shows every answer that differs. `baresql rules` explains each difference with the answer every engine gave.

## lint

```
baresql lint schema.sql queries.sql
```

Lint learns the column types from the CREATE TABLE statements it reads, so give it the schema along with the queries. That's how it tells an integer division from a decimal one, or a text column compared with a number. This is its report on the sample shop's queries:

```
{{file:portable/shop-lint.txt}}
```

Every problem has a severity. An **error** means at least one engine refuses the statement. A **warning** means every engine runs it and the answers differ. A **note** means the answers differ only for some data, such as text in mixed case or a column that holds NULL; notes are left out unless you pass `--all`.

The list of differences further down gives each rule's usual severity. Lint reports the severity of what it actually found, so one rule can be an error in one statement and a note in another. Dividing by a column is a note, since the answers differ only when the column holds 0, but `1 / 0` written out is an error, because PostgreSQL refuses it.

Most projects use only two of the four engines. `--engines sqlite,postgres` keeps only the differences between those two, judged by the answers recorded for the exact construct lint found, so a MySQL-only problem drops out of a project that tests on SQLite and runs on PostgreSQL. A comment `-- baresql:ignore integer-division` on the line of a problem, or the line before it, silences that rule there.

## compare

```
baresql compare --sqlite --postgres postgres://dev@localhost/scratch \
    --mysql mysql://dev@localhost:3306/ --mariadb mariadb://dev@localhost:3307/ shop.sql
```

compare runs the script statement by statement on every engine you name and prints the statements whose answers differ. Answers are compared by meaning: 3.5 equals 3.5000, true equals 1, dates match however they're printed, and row order counts only under an ORDER BY. Text is compared exactly, so `'Mug'` differs from `'mug'`, and text never equals a number. Here is the shop's script on all four engines:

```
{{file:portable/shop-compare-4.txt}}
```

Each engine works in a throwaway space: a transaction rolled back at the end on PostgreSQL, a scratch database dropped at the end on MySQL and MariaDB, and a temporary file on SQLite, run through `sqlite3 -safe`. compare skips the statements that would leave that space, such as transaction control, statements about databases and users, and SET GLOBAL. On MySQL and MariaDB it refuses any statement that names another database. It is no sandbox against a script written to get out, so point it at servers meant for testing.

## rules

`baresql rules` lists every difference, and `baresql rules CODE` shows one: what differs and why, a minimal example with the answer each engine gave, the form that answers the same everywhere and what to write instead. `--format json` gives the whole catalog with its answers.

## The {{v:rules}} Differences

Each difference has its own post with the recorded answers, every form BareSQL checks and what lint prints for it.

{{index:rules}}

## How the Answers Were Recorded

Every example, rewrite and form in the catalog was run on SQLite {{v:sqlite}}, PostgreSQL {{v:postgres}}, MySQL {{v:mysql}} and MariaDB {{v:mariadb}}, and the {{v:answers}} answers are kept in the code repository. On every change, CI runs the whole catalog again on PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7, MariaDB 10.11, 11.4, 11.8 and 12.3, and the sqlite3 of its Linux and macOS machines, and fails when an answer changes. Where a version really answers differently, the catalog keeps that answer too, and lint's messages say so ("MD5() works on PostgreSQL, MySQL (before 9.6) and MariaDB; SQLite refuses it"). Some builds of sqlite3 leave out its math functions or fold case beyond A to Z; the catalog says which answers assume the usual build.

Lint's messages for functions, operators and types come from those runs, and so does each problem's severity. The [testing post](https://baresql.com/how-baresql-portable-was-tested-on-four-real-database-engines/) has the details.

## Limits

Lint reads tokens and recognizes the shapes of expressions. It doesn't parse SQL fully, so it can miss a construct written in an unusual way. JSON counts only where it decides whether a statement runs at all, as with JSON_EXTRACT, `->>` and the JSONB type. The rest of JSON, stored procedures and triggers are left out, since their syntax differs too much between engines to compare. Answers are recorded with each engine's default settings: server modes such as MySQL's sql_mode, and collations, change some of them.

Some differences have no rule yet: ALTER TABLE syntax, names that are reserved words on some engines (user, key, rank), aggregate FILTER, FROM DUAL, the digits of AVG over integers and ROUND of floating point halves among them. compare shows them when a script runs into one.
