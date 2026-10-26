---
title: "BareSQL Launches With an Access Audit for PostgreSQL and a Portability Check for SQL"
date: 2026-10-26
draft: false
tags: ["BareSQL", "BareSQL Audit", "BareSQL Portable"]
featured_image: "https://baresql.com/images/launch.png"
---

BareSQL is a free, open-source command-line program with two tools in it. BareSQL Audit lists every column each PostgreSQL login can read, with the route that gets it there, and marks the ones that hold personal data. BareSQL Portable finds the SQL that fails or answers differently on SQLite, PostgreSQL, MySQL and MariaDB. Version {{v:version}} is the first release.

Who can read the customers' email addresses? In a PostgreSQL database the answer is spread over role memberships, grants made years ago, views that read tables with their owner's rights, default privileges and row security policies. Working it out by hand means an afternoon with the system catalogs, and the answer is out of date the next time someone runs a GRANT. BareSQL Audit logs in, reads the catalogs inside a read-only transaction and writes the answer down: a report for people and a JSON file for the next audit to be compared with.

![Two BareSQL commands in a terminal: baresql audit on the sample shop database, with its findings, and baresql lint on a query that divides two integers.](https://baresql.com/images/launch.png)

## Two Tools, One Program

The audit covers grants through roles at any depth, roles a login can switch to, ownership, `pg_read_all_data`, views and the rights they run with, security-definer functions, row security and parent tables. For every column it also asks the server's own privilege checks, and where BareSQL's reasoning and the server disagree, the report says so. It reads a sample of rows to recognize email addresses, card numbers and other personal data, and the values it reads never leave the program. `baresql diff` compares two audits, and `--baseline` turns the audit into a check that fails when any login can reach more than before.

SQL meant for more than one engine has its own trouble. A test suite runs on SQLite while production runs on PostgreSQL. Then a search that finds Bob in the tests finds nobody in production. `baresql lint` reads SQL files and points at {{v:rules}} kinds of construct that fail or answer differently, each with what to write instead. `baresql compare` runs a script on real engines and shows every statement whose answers differ. `baresql rules` explains each difference with the answer each engine gave.

Both tools ship as one file. BareSQL talks to PostgreSQL, MySQL and MariaDB over their network protocols with code of its own, and for SQLite it calls the `sqlite3` program. It's written in Go, with no third-party code at all.

## Tested on the Real Engines

Every change runs the tests against PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7 and MariaDB 10.11, 11.4, 11.8 and 12.3, each in a job of its own. The audit is checked line by line against databases with planted leaks and near misses, on each PostgreSQL version. Every answer BareSQL Portable quotes, {{v:answers}} of them, was recorded on real engines, and CI runs the whole catalog again on every version. An independent review went through both tools before this release, and what it found was fixed or written down as a known limit.

## Getting Started

The [download page](https://baresql.com/download/) has builds for Linux, macOS and Windows, and the [quick start](https://baresql.com/quick-start/) goes from the first audit to a CI step in a few minutes. The [sample reports](https://baresql.com/sample-reports/) show what the audit finds in four made-up databases, one of them set up with care, and five case studies try both tools on sample databases and demo projects, from an AI agent's new login to a move from MySQL to PostgreSQL.

BareSQL is open source under the Apache License 2.0, and the code is on [GitHub](https://github.com/softdevstudio/baresql). If it misses something in your database, or flags something it shouldn't, please write in: the [contact page](https://baresql.com/contact/) has the address.
