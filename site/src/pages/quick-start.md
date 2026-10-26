---
title: "Quick Start"
type: page
draft: false
---

BareSQL is one program, `baresql`. BareSQL Audit is its `audit` and `diff` commands, and BareSQL Portable is `lint`, `compare` and `rules`. Get it from the [download page](https://baresql.com/download/) and check that it runs:

```
baresql version
```

## Audit a PostgreSQL Database

Give `baresql audit` a connection string, the same as for `psql`. Leave the password out of it: BareSQL reads `PGPASSWORD` and `~/.pgpass` like every PostgreSQL tool.

```
export PGPASSWORD=...
baresql audit "postgres://auditor@db.example.com/shop?sslmode=verify-full"
```

It prints a short summary. This one is for the sample shop database BareSQL is tested on:

```
{{file:audit/shop/summary.txt}}
```

Open `baresql-report/report.html` in a browser. It starts with the findings. Then come the logins that can read personal data and the columns that hold it, and after those a section for each login, with every column it can read and the route that gets it there. The [sample reports](https://baresql.com/sample-reports/) show what that looks like for four databases.

Any login can run the audit, since the catalogs it reads are visible to every role. To check values for personal data, it needs read access to the tables, so a superuser or a member of `pg_read_all_data` sees the most. Whatever the audit could not see is listed under "Not seen". It works in a read-only transaction and never calls a function you wrote. Values it samples are never written anywhere; `--no-sample` turns sampling off.

## Watch for Access That Grows

Keep `audit.json`. The next audit can be compared with it:

```
baresql diff last-month.json baresql-report/audit.json
```

`baresql diff` lists what changed between the two, starting with the reach that grew. To fail a scheduled job when any login reaches more than before, run the audit with a baseline:

```
baresql audit --baseline last-month.json "postgres://auditor@db.example.com/shop"
```

It exits 1 when access grew. The [quarterly review case study](https://baresql.com/a-quarterly-access-review-finds-a-new-table-of-bank-accounts-that-every-role-can-read/) walks through one.

## Check SQL for Other Engines

`baresql lint` reads SQL files and points out what fails or answers differently on SQLite, PostgreSQL, MySQL and MariaDB. Give it the schema as well, so it knows the column types:

```
baresql lint schema.sql queries.sql
```

This is lint on the shop's queries:

```
{{file:portable/shop-lint.txt}}
```

Each problem says what to write instead. If only two engines matter, say so, and the differences between the others drop out:

```
baresql lint --engines sqlite,postgres schema.sql queries.sql
```

A comment `-- baresql:ignore integer-division` on the line of a problem, or the line before it, silences that rule there.

## Run the Same Script on Real Engines

`baresql compare` runs a script on two or more engines, statement by statement, and shows every answer that differs:

```
export PGPASSWORD=... MYSQL_PWD=...
baresql compare --sqlite --postgres postgres://dev@localhost/scratch \
    --mysql mysql://dev@localhost:3306/ shop.sql
```

Each engine works in a throwaway space: a transaction rolled back at the end on PostgreSQL, a scratch database dropped at the end on MySQL and MariaDB, and a temporary file on SQLite. Point it at servers meant for testing, with logins that may do no more than the script needs. The [BareSQL Portable page](https://baresql.com/portable/) has an example of its output.

## Look Up a Difference

`baresql rules` lists the {{v:rules}} differences BareSQL knows. `baresql rules integer-division` shows one, with the answer each engine gave:

```
{{lines:portable/rules/integer-division.txt:1-30}}
```

Each difference also has its own post here, listed on the [BareSQL Portable page](https://baresql.com/portable/).

## Use It in CI

The exit code says what happened. Lint exits 1 when it reports a problem, and compare when answers differ. `baresql diff` and an audit with `--baseline` exit 1 when access grew. Every command exits 2 when it could not run, and 0 otherwise. A CI step can fail on portability problems like this:

```
- name: Check the SQL
  run: baresql lint --engines sqlite,postgres db/schema.sql db/queries/*.sql
```

The [reference](https://baresql.com/reference/) lists every command and flag.
