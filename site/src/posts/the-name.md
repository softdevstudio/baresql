---
title: "What the Bare in BareSQL Stands For"
date: 2026-10-26
draft: false
tags: ["the name", "BareSQL"]
featured_image: "https://baresql.com/images/the-name.png"
---

BareSQL is one small program with two tools in it, and the name had to fit both. Bare does that by meaning two things at once: access laid bare, and SQL stripped down to the part every engine agrees on.

The first sense belongs to BareSQL Audit. To lay something bare is to take the covers off and show what's underneath. In a PostgreSQL database the covers are the layers between a login and a column: roles inside roles, grants made years ago to a group nobody remembers, views that read tables with their owner's rights, functions that run as someone else. The audit takes those layers apart and lists, for each login, every column it can read and the route that gets it there.

![The word BareSQL with its two meanings: access laid bare, for BareSQL Audit, and bare SQL that answers the same on every engine, for BareSQL Portable.](https://baresql.com/images/the-name.png)

## Bare SQL

The second sense belongs to BareSQL Portable. Every engine wraps SQL in its own dialect, such as MySQL's backticks and `LIMIT 20, 10` or PostgreSQL's `::` casts and `DISTINCT ON`. Bare SQL is what's left when that wrapping comes off. It's the plain core that SQLite, PostgreSQL, MySQL and MariaDB read the same way and answer the same way.

That core can be found by experiment. Each of the {{v:rules}} differences BareSQL Portable knows comes with a form that was run on all four engines and gave the same answer there, where such a form exists. Some don't have one: no date arithmetic reads the same on all four, and lint says so instead of inventing a fix.

## A Bare Program

The name also fits how BareSQL is built. The download is one file, and the code in it uses nothing beyond Go's standard library. PostgreSQL, MySQL and MariaDB are reached through BareSQL's own code for their network protocols, and SQLite through the `sqlite3` program. The audit reads the system catalogs inside a read-only transaction and never calls a function you wrote. A tool that logs in to production databases should be easy to read through, and a small one with nothing hidden in it is.

## Why the Two Tools Share It

Both tools answer a question that's hard to answer by reading SQL alone. Who can read this column? Will this query give the same answer on the other engine? Each answer is a fact about a real database, so BareSQL takes it from one. The audit asks the PostgreSQL server itself. Lint quotes the answers the four engines gave when the catalog was recorded, and compare runs your script on them. That's the bare part, in the end: what the database did, written down.
