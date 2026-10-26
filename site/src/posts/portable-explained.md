---
title: "How BareSQL Portable Finds SQL That Answers Differently on Four Engines"
date: 2026-10-26
draft: false
tags: ["BareSQL Portable", "explainers"]
featured_image: "https://baresql.com/images/portable-explained.png"
---

Most SQL that fails on another engine fails loudly, with a syntax error on the first run. The expensive kind runs everywhere and answers differently. On the sample shop's products, `SUM(stock) / COUNT(*)` is 3 on SQLite and PostgreSQL and 3.3333 on MySQL. `ORDER BY last_order` puts the customers who never ordered first on three engines and last on PostgreSQL. A search with LIKE finds Ann on SQLite and misses her on PostgreSQL, and `'1' = 1` is false on SQLite and true on the other three. None of these raise an error.

BareSQL Portable looks for both kinds in SQL meant for SQLite, PostgreSQL, MySQL and MariaDB. `baresql lint` reads SQL files without connecting to anything. `baresql compare` runs a script on real engines and shows the answers that differ. `baresql rules` shows the catalog behind lint, with every engine's answer to each difference.

![baresql compare running the sample shop's queries on SQLite, PostgreSQL, MySQL and MariaDB: a search for mug in lower case finds the product Mug only on MySQL and MariaDB, and a sum divided by a count is 3 on SQLite and PostgreSQL and 3.3333 on the other two.](https://baresql.com/images/portable-explained.png)

## Lint Knows Each Dialect

Lint starts with a tokenizer that knows all four dialects, from MySQL's backslash escapes and `#` comments to PostgreSQL's dollar quotes. It splits the files into statements, learns the column types from the CREATE TABLE statements it meets, and then looks for the shapes of the {{v:rules}} differences it knows. Types matter: `price / 3` is a decimal division and `stock / 3` an integer one, and the two differ in different ways.

Each problem it reports has a severity that comes from real answers. It's an error when at least one engine refuses the statement, a warning when every engine runs it and the answers differ, and a note when they differ only for some data, such as text in mixed case or a column that holds NULL. Notes stay hidden unless you pass `--all`.

Most projects use only two of the four engines. With `--engines sqlite,postgres`, lint keeps a problem only when the exact construct it found answers differently between those two. It knows that from the answers recorded for that construct, so a difference that only matters on MySQL drops out.

## Every Message Comes From a Run

Behind lint sits a catalog. Each difference has a minimal example and, where one exists, a rewrite that answers the same everywhere. A difference that covers many forms, such as the functions some engines lack, has a row for each form, {{v:rows}} rows in all. Every example, rewrite and row was run on SQLite {{v:sqlite}}, PostgreSQL {{v:postgres}}, MySQL {{v:mysql}} and MariaDB {{v:mariadb}}, and the {{v:answers}} answers are kept in the code repository. When lint says that MD5() works on PostgreSQL, MySQL before 9.6 and MariaDB, and that SQLite refuses it, the answers behind it are recorded ones. The 9.6 comes from MySQL's release notes, which say that version moved MD5() into an optional component, and CI finds it missing on 9.7. Where no rewrite answers the same on all four, the catalog says so plainly.

On every change, CI runs the whole catalog again on PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7 and MariaDB 10.11, 11.4, 11.8 and 12.3, and fails when an answer changes. The [testing post](https://baresql.com/how-baresql-portable-was-tested-on-four-real-database-engines/) has the details.

## Compare Runs the Real Thing

Lint reads code, and code can be written in ways lint doesn't recognize. `baresql compare` asks the engines instead. It runs a script statement by statement on every engine you name and prints the statements whose answers differ, with each engine's answer.

Answers are compared by meaning. 3.5 equals 3.5000, true equals 1, a date matches however it's printed, and rows count in order only under an ORDER BY of the statement's own. Text is compared exactly, so `'Mug'` differs from `'mug'` and the text `'7'` differs from the number 7. When rows that tie in an ORDER BY come back in another order, compare says so, since the next run may order them differently again.

Each engine works in a throwaway space: a transaction rolled back at the end on PostgreSQL, a scratch database dropped at the end on MySQL and MariaDB, and a temporary file on SQLite, run through `sqlite3 -safe`. Statements that would leave that space are skipped, such as transaction control, statements about databases and users, and SET GLOBAL. On MySQL and MariaDB, where a change can't be rolled back, compare also refuses any statement that names another database. That keeps an ordinary script and its usual mistakes inside the space. It is no sandbox against a script written to escape, so give compare logins that may do no more than the script needs.

## What Lint Leaves Out

Lint reads tokens and the shapes of expressions. It doesn't parse SQL fully, and it knows column types only from the CREATE TABLE statements it reads. JSON functions, stored procedures and triggers are left out, because their syntax differs too much between engines to compare. Answers are recorded with each engine's default settings, and server modes such as MySQL's `sql_mode` and collations change some of them. Some differences have no rule yet; the [BareSQL Portable page](https://baresql.com/portable/) lists them, and compare shows them when a script runs into one.
