---
title: "What Changed in the Answers When a MySQL App Moved to PostgreSQL"
date: 2026-10-26
draft: false
tags: ["case studies", "BareSQL Portable"]
featured_image: "https://baresql.com/images/case-mysql-to-postgres.png"
---

This case study uses a demo project in BareSQL's code repository: a made-up invoicing app that has run on MySQL for years and is moving to PostgreSQL. The project holds the app's MySQL schema, the same schema rewritten for both engines with some data, and eleven queries as the app ran them on MySQL. The outputs below are what BareSQL printed.

The plan sounds simple: rewrite the schema and fix whatever PostgreSQL refuses. The trouble is in the queries PostgreSQL doesn't refuse. They run and return rows, and some of the rows are different.

![baresql compare on the invoicing app's queries: a search for Bob Stone finds his row only on MySQL, where the name is stored as bob stone, and counting paid invoices gives 1 on PostgreSQL and 2 on MySQL, which also counts an invoice whose status is Paid.](https://baresql.com/images/case-mysql-to-postgres.png)

The first step costs nothing, since lint connects to nothing:

```
baresql lint --engines mysql,postgres schema-mysql.sql queries.sql
```

It reports {{v:m2p_lint_problems}} problems: {{v:m2p_lint_errors}} errors, {{v:m2p_lint_warnings}} warnings, and {{v:m2p_lint_notes}} notes hidden because they depend on the data. The errors in the schema are the expected ones: backticks, AUTO_INCREMENT, DATETIME, ENUM and `ENGINE=InnoDB`, each with what PostgreSQL wants instead. In the queries it flags `DATEDIFF()`, `LIMIT 2, 2`, `GROUP_CONCAT()` and `ON DUPLICATE KEY UPDATE`, which PostgreSQL refuses, and {{v:m2p_lint_warnings}} warnings for queries both engines run with different answers.

## Running Both Engines on the Same Data

With the schema rewritten to run on both, `baresql compare` runs the queries on MySQL and PostgreSQL side by side:

```
baresql compare --mysql mysql://dev@localhost:3306/ --postgres postgres://dev@localhost/scratch schema.sql queries.sql
```

Of {{v:m2p_statements}} statements, {{v:m2p_alike}} answered alike and {{v:m2p_differ}} differ. Among those {{v:m2p_differ}} are the {{v:m2p_refused}} refusals lint found. The other {{v:m2p_silent}} run on both engines without an error, and these are the ones a migration has to decide about.

## Text That Compares Differently

MySQL's default collation ignores letter case and accents, so the app has always found customers and invoices that PostgreSQL won't:

```
{{blocks:portable/mysql-to-postgres-compare.txt:'Bob Stone'|'émile%'|status = 'paid'}}
```

The third one is the expensive kind. One invoice was saved with the status `'Paid'`, and for years MySQL counted it with the paid ones. On PostgreSQL the paid count drops from 2 to 1, and the revenue report with it. The fix is a decision: make the data consistent, with every status in lower case, or compare with `LOWER(status) = 'paid'` where case shouldn't matter. Accent-insensitive search needs more on PostgreSQL than `LOWER()`, so a search box that relied on it needs its own plan.

## NULL, Division and Digits

```
{{blocks:portable/mysql-to-postgres-compare.txt:SELECT CONCAT\(|COUNT\(DISTINCT}}
```

On MySQL one NULL argument makes the whole CONCAT NULL, so the customer without a country had no address label at all. PostgreSQL skips the NULL and prints `'bob stone ()'`. Neither is what anyone wants, and `COALESCE(country, '')` makes the choice explicit on both. The average number of invoices per customer was 1.3333 on MySQL and is 1 on PostgreSQL, which divides integers as integers. Splitting an amount three ways gives six decimals on MySQL and sixteen on PostgreSQL, and `ROUND(amount / 3, 2)` gives the same on both. Customers with no country sort first on MySQL and last on PostgreSQL.

## What the Move Looks Like Now

The work is now a list: {{v:m2p_refused}} statements to rewrite and {{v:m2p_silent}} answers to decide on. Compare only sees the data it runs on, so the closer the script's data is to production, odd `'Paid'` rows and all, the more it finds. Each code in the output has its own post with the answer every engine gave, listed on the [BareSQL Portable page](https://baresql.com/portable/).
