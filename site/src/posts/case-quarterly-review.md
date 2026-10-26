---
title: "A Quarterly Access Review Finds a New Table of Bank Accounts That Every Role Can Read"
date: 2026-10-26
draft: false
tags: ["case studies", "BareSQL Audit"]
featured_image: "https://baresql.com/images/case-quarterly-review.png"
---

The shop in this case study is made up: it's the sample shop database that BareSQL's tests build, and the outputs are what BareSQL printed. It reviews database access once a quarter. The first review with BareSQL Audit covers every login and ends with a file kept for next time, `audit.json`.

Three months later the review runs again against that file. The reviewer then reads what changed, with no need to compare two long reports by eye.

![The audit of 1 October run with the July audit as its baseline: a new high finding for shop.refunds, which every role can read, the comparison with the audit of 1 July, and exit status 1.](https://baresql.com/images/case-quarterly-review.png)

On 1 July the audit gives this summary:

```
{{file:audit/quarterly/q3/summary.txt}}
```

## The Quarter's Changes

During the quarter, three changes go in, each reviewed on its own if at all. A refunds table with customers' bank accounts is created for the payments team. The new intern joins the support team. And the analyst loses access to the products table, which the analyst's work no longer needs:

```
{{file:audit/quarterly/changes.sql}}
```

## The Second Review

On 1 October the audit runs with the July file as its baseline:

```
baresql audit --baseline q3/audit.json "postgres://postgres@localhost/baresql_fx_shop"
```

It prints the new summary and the comparison, and exits 1, because access grew. Here is the part of the comparison about the refunds table, with only its IBAN lines:

```
{{grep:audit/quarterly/diff.txt:^(Compared|Reach|Other)|iban|~ finding}}
```

Nobody granted the refunds table to anyone. The July report had already said why it would leak, in a medium finding: "New tables that shop_owner creates in schema shop are granted SELECT to PUBLIC." Default privileges are set once and then forgotten, and they apply to every table the role creates later. The refunds table was the first one with bank accounts in it, so in October the same mechanism produced a high finding, `public-reads-personal`, and every login could read the IBANs.

## The Intern Got More Than a Team Membership

Of the {{v:quarterly_grew}} new pieces of reach, {{v:quarterly_intern}} came to the intern with one GRANT. Joining the support team brought everything the team reads, including the customers' card numbers through the support view:

```
{{grep:audit/quarterly/diff.txt:shop_intern (shop\.customers|support\.customer_lookup)\.card_number}}
```

Whether an intern on the support desk should see card numbers is a question for the business, and the [support view case study](https://baresql.com/a-support-view-showed-card-numbers-to-every-support-login/) takes it up. The point here is that a membership brings all of a group's reach at once, and this diff shows it line by line, where the GRANT statement showed nothing.

## The Access That Went

The diff also lists {{v:quarterly_gone}} pieces of reach that are gone: the analyst's four columns of the products table, as intended. A review that only looks at growth would miss a removal that went wrong, so the diff lists both.

## What the Shop Changed

The refunds table loses its PUBLIC grant, and the default privileges stop handing new tables to PUBLIC:

```
{{file:audit/quarterly/fix.sql}}
```

The second statement removes the cause, the default privilege that the July finding was about. The next morning the audit runs again, with the October file as its baseline. It finds {{v:quarterly_fixed_gone}} pieces of reach gone and none grown, and the July finding is gone along with the October one. Without its {{v:quarterly_fixed_gone}} lines of reach, the comparison reads:

```
{{grep:audit/quarterly/fixed-diff.txt:^(Compared|Reach|Other)|~ finding}}
```

The report's other findings were there before the quarter began, and each needs a decision of its own. The same comparison can also run every night from a scheduler. With `--baseline` the job fails on the day access grows, so the next table like this one is caught the morning after it appears. All three reports are on the [sample reports page](https://baresql.com/sample-reports/).
