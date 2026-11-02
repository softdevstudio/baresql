---
title: "BareSQL Portable Now Flags Schema Changes and Reserved Words That Break on Other Engines"
date: 2026-11-02
draft: false
tags: ["release", "BareSQL Portable"]
featured_image: "https://baresql.com/images/release-0-2.png"
---

The first release of BareSQL Portable came with a list of differences it knew about and had no rule for yet. A migration that changes a column, a table with a column called `rank`, a dashboard that counts with `FILTER`, a query copied from Oracle that ends in `FROM DUAL`. compare would show them when a script ran into one. lint said nothing.

BareSQL 0.2 closes that list. It has eleven new rules, {{v:rules}} in all, and every one was run on SQLite, PostgreSQL, MySQL and MariaDB before its text was written.

The catalog now holds {{v:rows}} rows and {{v:answers}} recorded answers. CI runs each of them again on PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7 and MariaDB 10.11, 11.4, 11.8 and 12.3.

![The eleven rules new in BareSQL 0.2, as baresql rules lists them](https://baresql.com/images/release-0-2.png)

## Schema Changes

[ALTER TABLE forms](https://baresql.com/{{slug:alter-column}}/) is the rule most migrations will meet. PostgreSQL changes a column with `ALTER COLUMN ... TYPE` and `SET NOT NULL`, MySQL and MariaDB with `MODIFY` and `CHANGE`, and SQLite can do neither. The rule knows 21 forms, among them several actions in one statement, which SQLite refuses, `ADD COLUMN IF NOT EXISTS`, which MySQL refuses, and `RENAME a TO b` without the word COLUMN, which MySQL and MariaDB refuse.

[Reserved words](https://baresql.com/{{slug:reserved-word}}/) covers names. A column called `rank` works on SQLite, PostgreSQL and MariaDB and fails on MySQL 8. `user` fails on PostgreSQL, `key` on MySQL and MariaDB. The rule has a row for each of 83 words people give to tables and columns, each tried as a column name on all four engines. After AS in a select list PostgreSQL takes any word, and lint knows that too.

[DROP TABLE with CASCADE](https://baresql.com/{{slug:drop-cascade}}/) is the quiet one. PostgreSQL drops the foreign keys that point at the table, MySQL and MariaDB read the word and ignore it, and SQLite refuses it.

## Queries and Functions

Four rules cover syntax one or two engines lack: [FILTER on aggregates](https://baresql.com/{{slug:aggregate-filter}}/), [FROM DUAL](https://baresql.com/{{slug:from-dual}}/), [TRIM with LEADING or FROM](https://baresql.com/{{slug:trim-from}}/) and [SUBSTRING with FROM and FOR](https://baresql.com/{{slug:substring-from}}/). Each comes with a form that runs everywhere, where one exists. For trimming characters other than spaces none does: SQLite wants `LTRIM(s, '0')`, and MySQL and MariaDB refuse a second argument to LTRIM.

## Answers That Differ Without an Error

The last four rules are about queries that run everywhere and give different answers.

- [CAST to a floating point type](https://baresql.com/{{slug:cast-to-float}}/): `FLOAT` is 8 bytes on SQLite and PostgreSQL and 4 bytes on MySQL and MariaDB, and `DOUBLE PRECISION` fails on MariaDB.
- [Digits of AVG](https://baresql.com/{{slug:avg-digits}}/): the average of 1, 2 and 2 is 1.6667 on MySQL and 1.6666666666666667 on PostgreSQL.
- [ROUND of a floating point half](https://baresql.com/{{slug:round-half}}/): 2.5 stored as a float rounds to 3 on SQLite and to 2 everywhere else, and `ROUND(0.15, 1)` is 0.1 on SQLite and 0.2 on the others.
- [GROUP BY on text in mixed case](https://baresql.com/{{slug:group-case}}/): Berlin and berlin are one group on MySQL and MariaDB and two on SQLite and PostgreSQL.

These depend on the data as much as on the SQL, so lint needs to know a column's type and applies them to columns it has seen in a CREATE TABLE. GROUP BY on text is a note rather than a warning, since most columns never hold two spellings of one value.

## Getting It

The [download page](https://baresql.com/download/) links the archives for Linux, macOS and Windows. `baresql lint` on an existing project will now report more than before, and `-- baresql:ignore code` on a line silences a rule there. The [changelog](https://github.com/softdevstudio/baresql/blob/main/CHANGELOG.md) lists every change.
