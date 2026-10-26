---
title: "Reference"
type: page
draft: false
---

Every command and flag of `baresql`, as of version {{v:version}}. `baresql help` lists the commands in the terminal and `baresql help COMMAND` shows the flags of one. A flag works with one dash or two, so `-out` and `--out` are the same. The [README on GitHub](https://github.com/softdevstudio/baresql#readme) is the full manual.

```
baresql audit [flags] [connection]      who can read what in a PostgreSQL database
baresql diff old.json new.json          compare two audits and list what changed
baresql lint [flags] file.sql ...       SQL that answers differently on SQLite, PostgreSQL, MySQL, MariaDB
baresql compare [engines] file.sql ...  run SQL on several engines and show where the answers differ
baresql rules [code]                    the differences lint knows, with each engine's recorded answer
baresql version                         print the version
baresql help [command]                  show help
```

## audit

```
baresql audit [flags] [connection]
```

{{readme:Flags}}

The connection is a `postgres://` URL or a `key=value` string, as for `psql`. Whatever it leaves out comes from the usual `PG*` variables (`PGHOST`, `PGUSER`, `PGPASSWORD` and the rest) and from `~/.pgpass`, so the password never has to be on the command line. TLS follows `sslmode`: `disable`, `prefer` (the default), `require`, `verify-ca` or `verify-full`, with `sslrootcert` for the certificate authority; `sslrootcert=system` uses the system's certificates and implies `verify-full`. Unix sockets work as with `psql` (`host=/var/run/postgresql`). The login uses SCRAM-SHA-256 (with channel binding over TLS), MD5 or a cleartext password, whichever the server asks for.

## diff

```
baresql diff [--format text|json] old.json new.json
```

Compares two `audit.json` files and lists the reach that grew, the reach that is gone and the other changes. `--format json` prints the comparison as JSON. It exits 1 when any login can reach more than before.

## lint

```
baresql lint [flags] file.sql ...
```

{{readme:lint}}

`-` reads standard input. Lint learns column types from the `CREATE TABLE` statements it reads, so pass the schema along with the queries. A comment `-- baresql:ignore integer-division` on the line of a problem, or the line before it, silences that rule there; several codes are separated by commas, and `-- baresql:ignore` alone silences every rule on the line.

## compare

```
baresql compare --sqlite --postgres URL --mysql URL --mariadb URL [flags] file.sql ...
```

{{readme:compare}}

Name two engines or more. Connections are URLs: `postgres://user@host/db` (or any form `baresql audit` takes), `mysql://user@host:3306/` and `mariadb://user@host:3307/`. MySQL and MariaDB URLs take `tls=disable|preferred|require|verify`, `tlsca=FILE` and `socket=PATH`. Leave passwords out of the URLs and set `PGPASSWORD` or `MYSQL_PWD`.

A statement may run for 60 seconds on every engine. On MySQL, statements other than SELECT meet a client limit of 75 seconds, after which compare stops. SQLite runs through the `sqlite3` program, version 3.37 or later, in its safe mode.

## rules

```
baresql rules [--format text|json] [code]
```

Without a code, lists every difference BareSQL Portable knows. With one, shows what differs and why, a minimal example with the answer each engine gave, a form that answers the same everywhere and what to write instead. `--format json` prints the whole catalog with its recorded answers.

## Exit Codes

{{readme:Exit codes}}

`baresql audit` exits 0 when it finishes, findings or not, unless `--baseline` finds that access grew.

## Output for Scripts

`baresql audit --format lines` prints one fact per line, sorted, the form the tests compare exactly:

```
read <login> <table>.<column> [<categories>] <route>
write <login> <table> <PRIVILEGE>[(<columns>)] <route>
function <login> <function> <route>
server <login> <capability> <route>
foreign-server <login> <server> <route>
default <creator> <schema> <objects> <PRIVILEGE> to <grantee>
finding <severity> <code> <subject>
```

The categories are the codes below, `-` for none. A `~` after them marks a view column computed from personal data, such as a name built from first and last names. `audit.json` holds the server, the settings of the run, every table and column with what it holds, each login with what it can read, change and run, the findings and what the audit couldn't see.

`baresql lint --format json` prints a list of problems, each with its code, severity, file, line, column, the text it flagged, the message and the advice. `baresql compare --format json` and `baresql rules --format json` print what their text forms show.

## Personal Data Categories

These codes are part of `audit.json` and never renamed.

{{readme:Personal data}}

## Finding Codes

{{readme:Findings}}

## Rule Codes

The {{v:rules}} rule codes of BareSQL Portable are listed on the [BareSQL Portable page](https://baresql.com/portable/), each with a post of its own.
