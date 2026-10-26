# BareSQL

BareSQL is one small program with two tools for SQL databases.

**BareSQL Audit** logs in to a PostgreSQL database and lists, for every
login, each column it can read, how it gets there (a grant, a role, a view,
a function, ownership) and whether the column holds personal data. It only
reads the system catalogs and a small sample of values, inside a read-only
transaction, and never calls a function you wrote.

**BareSQL Portable** checks SQL that has to run on SQLite, PostgreSQL,
MySQL and MariaDB. `baresql lint` points out constructs that give different
answers on different engines and shows a portable rewrite. `baresql compare`
runs the same script on real engines and reports where the answers differ.

BareSQL is in development. The first release, 0.1.0, is being built in this
repository; this file becomes the full manual with it.

## Build from source

You need Go 1.24 or later.

```
go build -o baresql ./cmd/baresql
./baresql version
```

## License

Apache License 2.0. Copyright 2026 SoftDevStudio.com. See LICENSE and
NOTICE.
