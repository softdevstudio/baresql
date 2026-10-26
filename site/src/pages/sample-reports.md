---
title: "Sample Reports"
type: page
draft: false
---

These reports are exactly what BareSQL Audit wrote for the databases its tests build. Each database is made up, with invented people in it, and planted with mistakes that turn up in real ones, plus near misses that look like mistakes and aren't. For the four databases below, the tests check every line of the audit on PostgreSQL 13 to 18, so a change to BareSQL that alters one of them fails before it ships.

Open a report to read it the way a reviewer would. The `audit.json` beside it holds the same facts for scripts and for `baresql diff`.

## An Online Shop

An app login, a support team that reads customers through a view, an analyst with column grants, a reporting login in `pg_read_all_data`, a newsletter table that every role can read and an export function that runs with its owner's rights.

![The top of the BareSQL Audit report for the sample shop: two high and four medium findings, the first of them a newsletter table that every role can read.](https://baresql.com/images/sample-shop.png)

```
{{file:audit/shop/summary.txt}}
```

[report.html](https://baresql.com/samples/shop/report.html) · [audit.json](https://baresql.com/samples/shop/audit.json)

## A Multi-Tenant App

Row security keeps each tenant's rows apart for the app login. An admin console bypasses row security. An AI support agent gets a view that was meant to show tickets and also hands out every tenant's accounts, since the view runs with the rights of a table owner who isn't held to the policies.

![The top of the BareSQL Audit report for the sample multi-tenant app: one high, one medium and one low finding, the first of them a view that skips row security.](https://baresql.com/images/sample-saas.png)

```
{{file:audit/saas/summary.txt}}
```

[report.html](https://baresql.com/samples/saas/report.html) · [audit.json](https://baresql.com/samples/saas/audit.json)

## A Clinic

Doctors inherit their rights through a chain of groups, and the front desk reads a few patient columns. Research gets one partition and a materialized view. Billing may pass its grant on. Further out, IT can read files on the database server while HR can add doctors without being one. The lab still logs in with an old MD5 password, and it reaches a foreign server.

![The top of the BareSQL Audit report for the sample clinic: one high, three medium and one low finding, the first of them a login that can read files on the database server.](https://baresql.com/images/sample-clinic.png)

```
{{file:audit/clinic/summary.txt}}
```

[report.html](https://baresql.com/samples/clinic/report.html) · [audit.json](https://baresql.com/samples/clinic/audit.json)

## A Database Set Up With Care

The app reads and writes what it needs and nothing more. Reports read daily totals through a view. The one security-definer function sets its own search_path, and only the reports login may run it. Its owner is a role that reads no personal data. An audit of a database like this should find nothing to flag, and BareSQL doesn't.

![The top of the BareSQL Audit report for the database set up with care: nothing to flag, and one login, the app, that reads personal data.](https://baresql.com/images/sample-clean.png)

```
{{file:audit/clean/summary.txt}}
```

[report.html](https://baresql.com/samples/clean/report.html) · [audit.json](https://baresql.com/samples/clean/audit.json)

## Before and After

Each of the three BareSQL Audit case studies runs an audit, changes the database and runs it again. The reports from before and after are here. The quarterly review has a third, from the morning after the fix. In these stories the tests check only the facts each one turns on.

<div style="overflow-x:auto">

| Case study | Before | After |
|---|---|---|
| [An AI support agent's login before it goes live](https://baresql.com/what-an-ai-support-agent-could-read-before-its-database-login-went-live/) | [report](https://baresql.com/samples/agent-before/report.html) · [json](https://baresql.com/samples/agent-before/audit.json) | [report](https://baresql.com/samples/agent-after/report.html) · [json](https://baresql.com/samples/agent-after/audit.json) |
| [A support view that showed card numbers](https://baresql.com/a-support-view-showed-card-numbers-to-every-support-login/) | [report](https://baresql.com/samples/support-before/report.html) · [json](https://baresql.com/samples/support-before/audit.json) | [report](https://baresql.com/samples/support-after/report.html) · [json](https://baresql.com/samples/support-after/audit.json) |
| [A quarterly access review](https://baresql.com/a-quarterly-access-review-finds-a-new-table-of-bank-accounts-that-every-role-can-read/) | [report](https://baresql.com/samples/quarterly-q3/report.html) · [json](https://baresql.com/samples/quarterly-q3/audit.json) | [report](https://baresql.com/samples/quarterly-q4/report.html) · [json](https://baresql.com/samples/quarterly-q4/audit.json); after the fix: [report](https://baresql.com/samples/quarterly-fixed/report.html) · [json](https://baresql.com/samples/quarterly-fixed/audit.json) |

</div>

To make reports like these of your own database, start with the [quick start](https://baresql.com/quick-start/).
