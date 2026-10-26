---
title: "Queries That Pass Their Tests on SQLite and Break on PostgreSQL in Production"
date: 2026-10-26
draft: false
tags: ["case studies", "BareSQL Portable"]
featured_image: "https://baresql.com/images/case-sqlite-tests.png"
---

This case study uses a demo project in BareSQL's code repository: a made-up booking app with a schema of two tables and eleven queries, written the way an app tested on SQLite tends to write them. Its test suite stays fast the usual way. Each test builds the schema in an in-memory SQLite database, runs the app's queries and throws the database away.

Production runs on PostgreSQL. Every query passes every test, and the question is how many of them do the same thing in production. The outputs below are what BareSQL printed.

![baresql compare running the booking app's queries on SQLite and PostgreSQL: a search for emails starting with bob finds Bob on SQLite and nobody on PostgreSQL, and PostgreSQL refuses is_admin = 1.](https://baresql.com/images/case-sqlite-tests.png)

Since the app only ever meets two engines, lint gets told so:

```
baresql lint --engines sqlite,postgres schema.sql queries.sql
```

## What Lint Finds Without Running Anything

```
{{file:portable/sqlite-tests-lint.txt}}
```

Each of the {{v:sqlite_tests_lint_problems}} problems is an error, a statement PostgreSQL refuses outright: `INSERT OR IGNORE` for sign-ups, `is_admin = 1` for the admin check, a GROUP BY that leaves a column out, SQLite's own `date()` and `strftime()`, and a price that arrives as the text `'240.00'` for an integer column, which SQLite quietly turns into the number 240. On PostgreSQL each of them fails the first time it runs, so they're quick to find, though for this app the first time would be in production.

## What Only Shows Up With Data

Lint also says that {{v:sqlite_tests_notes}} notes are hidden. Notes are differences that depend on the data, so lint can't know whether they matter here. `baresql compare` can, because it runs the queries:

```
baresql compare --sqlite --postgres postgres://dev@localhost/scratch schema.sql queries.sql
```

Of the differences it reports, {{v:sqlite_tests_silent}} raise no error on either engine:

```
{{blocks:portable/sqlite-tests-compare.txt:LIKE 'bob%'|ORDER BY u\.name$}}
```

The admin search box finds Bob on SQLite, where LIKE ignores letter case for A to Z, and finds nobody on PostgreSQL, where it doesn't. The front desk's guest list puts the guest without a name first on SQLite and last on PostgreSQL. Neither would fail a test that checks the query runs, and both change what a person sees. The fixes are short: `LOWER(email) LIKE 'bob%'`, and `ORDER BY u.name IS NULL, u.name` wherever the position of NULL matters.

In all, compare ran {{v:sqlite_tests_statements}} statements on both engines: {{v:sqlite_tests_alike}} answered alike and {{v:sqlite_tests_differ}} differ. The {{v:sqlite_tests_refused}} refusals among them are the errors lint found, with the same codes.

## Running the Tests on Both

The fix that lasts is to run the tests on PostgreSQL as well, at least in CI. Until that's in place, BareSQL gives the list before the change: every query that would break, with the reason and what to write instead. Lint can also sit in CI on its own, since it needs no database:

```
- name: Check the SQL
  run: baresql lint --engines sqlite,postgres db/schema.sql db/queries/*.sql
```

The [rule posts](https://baresql.com/portable/) explain each difference with the answers every engine gave, including four of the ones found here: [LIKE and letter case](https://baresql.com/{{slug:like-case}}/), [where NULL sorts](https://baresql.com/{{slug:null-order}}/), [booleans and numbers](https://baresql.com/{{slug:boolean-as-number}}/) and [values that don't fit the column](https://baresql.com/{{slug:type-not-enforced}}/).
