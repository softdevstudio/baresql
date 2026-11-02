---
title: "BareSQL Audit Now Reads Every Database of a Server and Says What Row Security Lets Through"
date: 2026-11-02
draft: false
tags: ["release", "BareSQL Audit"]
featured_image: "https://baresql.com/images/release-0-3.png"
---

Roles in PostgreSQL belong to the whole server, and tables to one database. Until now an audit followed that split: one database per run. A login that could read customer emails in the shop database and contacts in the CRM database needed two reports and someone to add them up.

The other gap was row security. The report said which policy applied to a login on each table, and stopped there. Whether `tenant_isolation` let the app see one tenant's rows or every tenant's was left for you to work out from the policy's text.

BareSQL 0.3 closes both.

![A two-database audit: one login reads tables in both databases, another may not connect to the second, and a policy picks rows by a setting any session can change](https://baresql.com/images/release-0-3.png)

## Several Databases in One Report

`baresql audit --database shop --database crm` audits both, and `--all-databases` audits every database that accepts connections. Each database is read over its own connection, in its own read-only transaction, and the report names every table after its database first: `crm.public.contacts`. Each login appears once, with what it reaches everywhere, and the report lists the databases it may not connect to.

Findings about a role, such as a superuser login, appear once. Findings about one database's tables, functions or default privileges say which database they're about. `baresql diff` and `--baseline` compare only audits of the same databases, because a database that drops out of the newer audit would otherwise hide whatever grew there.

## Row Security in Words

BareSQL reads a policy's condition without running it. A condition of `true` now gives "all rows (policy support_read lets every row through)", where 0.2 said only that the policy applied. `false` gives no rows. A column compared with the current role's name, with constants or with a setting read through `current_setting()` gets the condition in words on the route: "rows allowed by policy tenant_isolation (where tenant_id matches the setting app.tenant_id, which any session can set)". Conditions with subqueries or function calls are named, not read.

That last clause matters. A setting such as `app.tenant_id` is the usual way to keep tenants apart, and the application sets it when it connects. Any session can set it too, with a plain `SET`, so anyone holding the application's password can read every tenant's rows. The new finding `rls-session-setting` points at each policy like that and the logins it covers, unless another policy ties the rows to the role's own name.

## Checked Twice

An independent review went through the new code before the release and found ten problems. Among them: a 0.2 report used as a baseline would have reported growth after the upgrade with no change in the database, two of the three common shapes of tenant policies slipped past the new finding, and findings from two databases could merge into one that named neither. All of them are fixed, each with a test. The test database for this release has two databases and a policy of every shape BareSQL reads, and the audit of it is checked line by line.

The [download page](https://baresql.com/download/) has the release, and the [changelog](https://github.com/softdevstudio/baresql/blob/main/CHANGELOG.md) lists every change.
