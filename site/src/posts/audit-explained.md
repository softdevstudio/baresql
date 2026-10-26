---
title: "How BareSQL Audit Finds Every Column a PostgreSQL Login Can Read"
date: 2026-10-26
draft: false
tags: ["BareSQL Audit", "explainers"]
featured_image: "https://baresql.com/images/audit-explained.png"
---

PostgreSQL keeps the answer to "who can read this column?" in many places. Grants sit in access lists on tables and on single columns. Role memberships nest, and since PostgreSQL 16 each one says separately whether its rights are inherited and whether SET ROLE is allowed. Views read their tables with their owner's rights unless they say otherwise, row security policies filter what a table hands out, and default privileges hand out tables that don't exist yet.

BareSQL Audit puts those pieces together for every login in a database. This post follows it through one audit, from the first catalog query to the report.

![How shop_bob reaches the customers' card numbers in the sample shop: SET ROLE to shop_support_team, a grant on the view support.customer_lookup, and the view reading shop.customers with the rights of its owner, shop_owner.](https://baresql.com/images/audit-explained.png)

## One Snapshot of the Catalogs

The audit opens one connection and starts a transaction that is read-only and repeatable read, so every query sees the database as it was at the same moment. It reads the roles and their memberships, the access lists of the database, schemas, tables, columns and functions, the row security policies, the default privileges, the foreign servers with their user mappings, and the stored query of every view. Everything that follows, the sampling included, happens in that transaction, and it is rolled back at the end.

## From a Login to a Column

For each login the audit walks the membership graph twice. The first walk follows the memberships whose rights are inherited, and gives everything the login holds as soon as it connects. The second follows the ones that allow SET ROLE, and gives what it can switch to. A role reached only the second way shows up in the report as "after SET ROLE", because the login holds nothing of it until it switches. Superuser and BYPASSRLS never pass to members, so SET ROLE to a role that has them is listed even for a login that inherits everything else.

With those role sets, each table and column is a lookup in its access list, PUBLIC included. Ownership, superuser and `pg_read_all_data` give their own routes. A partitioned or parent table gets what its children hold, since a query on the parent returns their rows.

Views take the most work. PostgreSQL stores each view's query as a parse tree in `pg_rewrite`, and the audit reads that tree instead of running the view. It traces every output column back to the table columns it comes from, through joins, subqueries and other views. Then it checks the view the way PostgreSQL does: every column the query reads, in the select list, the joins, WHERE and the subqueries, must be readable with the rights the view runs with. Those are the owner's rights, or the reader's for a view with `security_invoker`. If one is missing, PostgreSQL refuses every query on the view, and the report says the route fails and which column is missing.

Each route also carries what row security does on it. A table owner who logs in reads every row unless row security is forced. A role with BYPASSRLS skips the policies. Otherwise the audit names the policies that apply to the role, and when none does, the route gives no rows at all.

## Asking the Server

All of that is BareSQL's own reading of the catalogs. To catch its mistakes, the audit then asks the server directly. `has_table_privilege` and `has_column_privilege` answer for every login, table and column, and `has_schema_privilege` and `has_database_privilege` answer for every schema and the database itself. The catalog reading explains each answer. When the two disagree, the server wins, and the finding `unexplained-access` lists the places, so a wrong answer can't hide in a long report.

## Recognizing Personal Data

A column holds personal data when its name or its values say so. The name is split into words, so `customerEmail2` reads as customer, email, 2, and the words are matched in English and the common European languages, with accents folded. Some words count only with a neighbor: `zip_code` is an address, and `zip_file` isn't.

Then the audit reads up to 200 rows of each table, by default, each table on its own. Each value is turned into text by its type's built-in output function, which a type's owner can't swap for a function of their own the way a cast can be swapped. The detectors do real checks. A card number has to pass the Luhn check and start like a known card network's. An IBAN has to pass its mod-97 check. National ID numbers are checked for the structure or check digits of US, UK, Spanish, French, Italian, Polish, Finnish and Swedish numbers. JSON documents are read key by key, and a key is named in the evidence only when it's a word the name rules know, since a key can be data itself.

The values stay in memory. The report says what a column holds and why ("40 of 40 sampled values are payment card numbers (valid check digit)"), and a view column inherits the verdict of the columns it comes from.

## Findings

With routes and personal data in hand, {{v:findings}} checks run over the same picture. Some are about who reaches personal data: every role reading a table through PUBLIC, a login that may pass its grant on to others, a view that skips row security, a table owner that can log in. Others are about doors that lead further: a security-definer function that doesn't set its search_path, a login that can read files on the server, a role with CREATEROLE before PostgreSQL 16, MD5 password hashes. The [BareSQL Audit page](https://baresql.com/audit/) lists them all.

## What It Leaves Out

The audit doesn't evaluate policy conditions. It reports that policy `support_read` applies on a route, and leaves what `tenant_id = current_setting('app.tenant_id')` lets through to you. Write access is listed per table and not followed through views or functions, and a security-definer function is listed with the role it runs as, since BareSQL never runs it to see what it reads. Each run covers one database. The [testing post](https://baresql.com/how-baresql-audit-was-tested-against-planted-leaks-on-six-postgresql-versions/) shows how all of this was checked against a real server.
