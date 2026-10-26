---
title: "How BareSQL Portable Was Tested on Four Real Database Engines"
date: 2026-10-26
draft: false
tags: ["testing", "BareSQL Portable"]
featured_image: "https://baresql.com/images/portable-tested.png"
---

Every message `baresql lint` prints is a claim about four database engines. "GROUP_CONCAT() works on SQLite, MySQL and MariaDB; PostgreSQL refuses it." Claims like that go stale, since engines add and drop functions between versions. Written from memory, a catalog of differences would be wrong in places on the day it shipped. So the answers BareSQL Portable quotes are the ones the engines gave when the catalog ran. A few messages also name the release in which an engine changed, such as SQLite 3.44 for `CONCAT()`, and those come from the engines' release notes.

Each of the {{v:rules}} differences in the catalog has a minimal example and, where one exists, a rewrite that should answer the same on every engine. Differences that cover many forms, such as the functions some engines lack, have a row for each form, {{v:rows}} rows in all. Every example, rewrite and row was run on SQLite {{v:sqlite}}, PostgreSQL {{v:postgres}}, MySQL {{v:mysql}} and MariaDB {{v:mariadb}}, and the {{v:answers}} answers they gave are what lint and the rule posts on this site quote.

![The engine versions every change to BareSQL is tested on: PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7, MariaDB 10.11, 11.4, 11.8 and 12.3, and SQLite.](https://baresql.com/images/portable-tested.png)

## Recording the Answers

A test with a recording switch runs the whole catalog on all four engines and writes what each one answered into a file in the repository: the rows, or the error. Each example runs in a fresh space with its own setup. When a setup statement fails, the answer is marked as such and the tests refuse to keep it, so a broken setup can't pass itself off as an engine refusing the example. Rows that create tables drop them again, so one row can't change the next one's answer.

## Checking Them on Every Version

The answers are recorded on one version of each engine. CI checks them on the others. On every change it runs the whole catalog on PostgreSQL 13 to 18, MySQL 8.0, 8.4 and 9.7 and MariaDB 10.11, 11.4, 11.8 and 12.3, each in a job of its own, and on the sqlite3 of its Linux and macOS machines. When an engine answers differently from the recording, the job fails and names the example.

A version that really answers differently gets an answer of its own, kept beside the recorded ones, and lint's messages describe it. There are {{v:version_keys}} so far: `MD5()` needs an optional component on MySQL from 9.6, for one, and `IF()` works on SQLite from 3.48. Some builds of sqlite3 leave out its math functions or fold case beyond A to Z, and the answers that assume the usual build say so.

## Lint Is Tested Against the Answers

Lint's own tests read the catalog too. Each example must be flagged by its rule, and each rewrite must not be. For a rule with many forms, each row's probe must be flagged as that row, so the message and the `--engines` filter use that form's own answers. The severity lint reports is checked on each example and probe: an error exactly when at least one engine refused it, and a warning or a note when they all ran it. So one rule can report an error for one statement and a warning or a note for another, according to the form each statement takes. For functions, operators and types, the message is built from the recorded answers. The other messages, like the summaries that explain each difference, are written by hand.

## Compare Runs on the Engines Too

`baresql compare` has tests of its own on the same four engines. The three demo projects on this site also go through it on the real engines each time the results are recorded. The [BareSQL Portable page](https://baresql.com/portable/) and the [quick start](https://baresql.com/quick-start/) quote the small shop's outputs, and the [case studies](https://baresql.com/tags/case-studies/) quote the app that tests on SQLite and the MySQL app moving to PostgreSQL.

Other tests of compare try to get out of its throwaway space, since compare runs scripts on real servers. One hides a second statement behind a `#`. A splitter reading MySQL sees one statement there, while PostgreSQL, which reads `#` as an operator, sees two and has to refuse the pair. Others put code inside the `/*! ... */` comments that MySQL runs, or name tables in other databases, and each of those has to be skipped or refused. Scripts that change the delimiter must split exactly the way the mysql program splits them. Other tests check that the scratch database and temporary files are gone after a run, and that a SQLite file given with `--sqlite=FILE` is left exactly as it was. The tokenizer and lint itself are also fuzzed on every change.

## The Review Before the Release

Before 0.1.0, an independent review tried to break both lint and compare. For compare it found ways a script could have left the throwaway space, all fixed and now among the tests above. For lint it found differences the catalog doesn't cover yet, such as names that are reserved words on some engines, aggregate FILTER, FROM DUAL and the rounding of floating point halves. Those are listed under Limits in the [README](https://github.com/softdevstudio/baresql#limits-1), and compare shows them when a script runs into one.

Everything quoted here, from the answers to the demo outputs, is in the code repository, and the [BareSQL Portable page](https://baresql.com/portable/) lists every difference with its own post.
