---
title: "Download BareSQL"
url: "/download/"
type: page
draft: false
---

The `baresql` command is one file with nothing else to install. Each archive below holds it with its license files, the README and the changelog. The links always point to the latest release.

<div style="overflow-x:auto">

| System | Download |
|---|---|
| Linux, x86-64 | [baresql_linux_amd64.tar.gz](https://github.com/softdevstudio/baresql/releases/latest/download/baresql_linux_amd64.tar.gz) |
| Linux, ARM64 | [baresql_linux_arm64.tar.gz](https://github.com/softdevstudio/baresql/releases/latest/download/baresql_linux_arm64.tar.gz) |
| macOS, Apple silicon | [baresql_darwin_arm64.tar.gz](https://github.com/softdevstudio/baresql/releases/latest/download/baresql_darwin_arm64.tar.gz) |
| macOS, Intel | [baresql_darwin_amd64.tar.gz](https://github.com/softdevstudio/baresql/releases/latest/download/baresql_darwin_amd64.tar.gz) |
| Windows, x86-64 | [baresql_windows_amd64.zip](https://github.com/softdevstudio/baresql/releases/latest/download/baresql_windows_amd64.zip) |

</div>

*Checksums are in [SHA256SUMS](https://github.com/softdevstudio/baresql/releases/latest/download/SHA256SUMS). Every release is listed on [GitHub](https://github.com/softdevstudio/baresql/releases).*

## Install

On Linux or macOS:

```
tar -xzf baresql_linux_amd64.tar.gz
sudo mv baresql /usr/local/bin/
baresql version
```

The binaries are static, so the Linux ones run on any distribution. The macOS binaries aren't signed by Apple, so macOS may block the first run; allow it in System Settings under Privacy & Security, or run `xattr -d com.apple.quarantine baresql`. On Windows, unzip the archive and put `baresql.exe` in a folder on your PATH.

Each binary is built by [the release workflow](https://github.com/softdevstudio/baresql/blob/main/.github/workflows/release.yml) on GitHub. Before a release goes out, the workflow runs every build it can through a short test, and the Linux x86-64 build through every command against real PostgreSQL, MySQL and MariaDB servers.

## What Else You Need

Nothing for BareSQL Audit: it talks to PostgreSQL over the network, or over a Unix socket, by itself. The same goes for `baresql lint` and `baresql rules`, which connect to nothing.

`baresql compare` runs SQLite through the `sqlite3` program, version 3.37 or later, which it finds on your PATH (or where `--sqlite-program` points). PostgreSQL, MySQL and MariaDB it reaches over the network with the connection strings you give it.

## Build It Yourself

You need Go 1.24 or later. BareSQL uses only Go's standard library, so the build downloads nothing else.

```
go install github.com/softdevstudio/baresql/cmd/baresql@latest
```

## License

BareSQL is open source under the Apache License 2.0, copyright 2026 SoftDevStudio.com. The binaries contain only BareSQL's own code and the Go runtime and standard library (BSD license), whose notice comes in each archive.
