---
title: "How BareSQL Audit Was Tested Against Planted Leaks on Six PostgreSQL Versions"
date: 2026-10-26
draft: false
tags: ["testing", "BareSQL Audit"]
featured_image: "https://baresql.com/images/audit-tested.png"
---

An access audit can be wrong in two directions. If it misses a route, a leak goes unnoticed because the report said all was well. If it invents one, people spend a day chasing access that was never there and stop trusting the report. So BareSQL Audit is tested against databases where every right answer is known in advance, and against the one judge that can't be argued with, the PostgreSQL server itself.

The tests build five databases from SQL each time they run. A shop, a multi-tenant app and a clinic hold the kinds of mistakes that turn up in real databases. A fourth database is set up with care and should produce no findings at all. The fifth holds the corner cases an independent review found before the first release. The people in them are invented. Their card numbers and national ID numbers, and the API keys and tokens planted in a few tables, are generated on every run, the numbers with valid checksums. None of these values sits in a committed file. The detection tests also use a few published example numbers, such as the test card 4111 1111 1111 1111.

![The number of facts BareSQL Audit must get exactly right in each test database, on each PostgreSQL version from 13 to 18.](https://baresql.com/images/audit-tested.png)

## Every Fact, Exactly

For each database, a file lists every fact the audit must report: each column a login can read and by which route, each table it can change, each function it may run, each finding. Some facts hold only from a certain PostgreSQL version, and the file marks them. The test runs the audit and compares its output with the file line by line, so one extra route fails the test as surely as a missing one. CI does this on PostgreSQL 13, 14, 15, 16, 17 and 18, each in a job of its own.

{{table:audit-facts}}

The counts grow with the versions because PostgreSQL grew. `pg_read_all_data` arrived in 14, so the shop's reporting login reads every table from then on. Views with `security_invoker` arrived in 15, and the multi-tenant app uses them. PostgreSQL 16 split role membership into separate INHERIT and SET options, and the clinic's HR, locum and auditor logins each use a different combination of the two.

## The Server Has the Last Word

BareSQL works each route out from the system catalogs. Then, for every login, table and column, it asks the server's own privilege functions whether the access is really there. If BareSQL and the server ever disagree, the audit reports the mismatch, and the tests fail on any mismatch at all. The corner cases go through the same check, so the access in their expected facts can't drift away from what the server grants.

## Near Misses

A test that only plants leaks would reward an audit that flags everything. So the databases are full of things that look wrong and aren't. The shop's tracking numbers have 16 digits like card numbers and fail the card check. Its products have a `name` column that isn't a person's name. The multi-tenant app forces row security on invoices, so a view owned by the table owner hands out no invoice rows, and the audit has to report that route as giving no rows. The careful database must come out with no findings at all. Beside the databases, the name rules are tested on columns such as `zip_file`, `health_score` and `treatment_arm`, which must not count as personal data.

## Sampling That Runs No Code

Reading values to recognize personal data is the one moment the audit touches table data, so the tests make sure nothing can run during it. One table has a type whose owner wrote a cast to text that would break the sample if it ever ran. Another hangs foreign tables below a partitioned table and below a parent table, through a wrapper that fails on any read. The audit has to sample both trees without touching the foreign ones, and still report what the local partitions hold.

## Who Runs the Audit

Most audits in the tests run as a superuser, since that sees the most. One runs as the shop's analyst, a login with a few ordinary grants and no special rights. Its report must list the same reach for every login as the superuser's, and must name what it couldn't see: the password types, which only a superuser may read, and the tables it may not sample.

## Scale and Fuzzing

A separate test builds {{v:scale_tables}} tables of {{v:scale_columns}} columns and {{v:scale_logins}} logins and times a full audit with sampling. On a machine with {{v:scale_cpus}} CPUs it took {{v:scale_seconds}} seconds and listed {{v:scale_found}} readable login-columns. The parsers that read what the server sends, such as the stored query trees of views and the network protocol itself, are fuzzed on every change.

## The Review Before the Release

Before 0.1.0, an independent review tried to break the audit on a live server. It found cases where the report was wrong: a type's own cast that sampling could have run, a foreign table that could be read through its parent, email addresses that could appear as JSON keys in the evidence, a role named "PUBLIC" that could be mistaken for PUBLIC. Each one was fixed and now has a test of its own, most of them as numbered cases in the fifth test database, so none can come back unnoticed. The [README](https://github.com/softdevstudio/baresql#limits) lists what the audit still leaves out, under Limits.

The counts above and the timing are kept in the code repository with the recorded audits, and the [sample reports page](https://baresql.com/sample-reports/) shows every one of those audits as BareSQL wrote it.
