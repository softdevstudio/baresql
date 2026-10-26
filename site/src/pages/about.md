---
title: "About"
type: page
draft: false
---

BareSQL.com is the home of BareSQL, one small program with two tools for SQL databases. BareSQL Audit tells you which PostgreSQL login can read which column, and how. BareSQL Portable tells you where SQL meant for SQLite, PostgreSQL, MySQL and MariaDB fails or gives different answers. Both are free and open source.

## The Name

The bare in BareSQL is meant two ways, one for each tool.

BareSQL Audit lays a database's access bare. Grants pile up over the years, roles sit inside other roles, views read tables with their owner's rights and default privileges hand new tables to everyone. The audit cuts through all of that and lists, for every login, each column it can read and the exact route that gets it there.

BareSQL Portable is about bare SQL: what's left of a query once each engine's own dialect is stripped away, the part that gives the same answer on all four engines. Every rule it knows ends with that bare form where one exists, tried on all four engines.

The program itself is bare too: one file, written in Go with nothing beyond its standard library. Nothing gets installed in your database, and the program makes no connection except the ones you give it. To run SQL on SQLite, compare calls the `sqlite3` program, which comes with macOS and installs from the usual package sources on Linux and Windows.

## Why It Exists

"Who can read the customers' email addresses?" sounds like a simple question about a PostgreSQL database. It rarely is. The answer depends on role membership, inheritance, views, security-definer functions and row security, and the usual way to find out is to read catalog tables for an afternoon. The question comes up before an audit, and again whenever a new login appears, say for a contractor or an AI agent. BareSQL Audit answers it in seconds and writes the answer down, so the next audit can be compared with this one.

SQL that has to run on more than one engine has its own trap. Teams test on SQLite and run PostgreSQL in production, or move an app from MySQL to PostgreSQL, and find out late that the same query answers differently: 7 / 2 is 3 on one engine and 3.5000 on another, and NULL sorts first on one and last on the next. BareSQL Portable finds those spots before they ship, and its compare command runs a script on the real engines to show every answer that differs.

## How It's Made

BareSQL's tests run on every change, on Linux, macOS and Windows, and against the real engines: PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7, MariaDB 10.11, 11.4, 11.8 and 12.3, and the sqlite3 program of the Linux and macOS test machines. The audit is checked line by line against databases with planted leaks and near misses. Every answer BareSQL Portable quotes was recorded on all four engines. Every number on this site comes from those recordings, which are kept in the code repository. An independent review went through both tools before the first release. What it found was fixed, or listed among the known gaps in the README.

BareSQL is a [SoftDevStudio.com](https://softdevstudio.com) project. The code is on [GitHub](https://github.com/softdevstudio/baresql) under the Apache License 2.0. Bug reports and ideas are welcome on the [contact page](https://baresql.com/contact/).
