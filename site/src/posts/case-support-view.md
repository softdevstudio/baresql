---
title: "A Support View Showed Card Numbers to Every Support Login"
date: 2026-10-26
draft: false
tags: ["case studies", "BareSQL Audit"]
featured_image: "https://baresql.com/images/case-support-view.png"
---

This case study runs on the sample shop database that BareSQL's tests build, so the shop is made up, and the outputs are what BareSQL printed. Its support team looks customers up through one view, `support.customer_lookup`, and has no grant on the customers table itself, which sounds like the careful way to do it.

The view was meant to show a name, an email address and a phone number, enough to help someone who calls in. At some point a column was added to it. The team has two logins, `shop_alice` and `shop_bob`. Bob is in the team without inheriting its rights, a setup often taken to mean he can't read what the team reads.

![The table of logins that can read personal data in the BareSQL Audit report, with payment card numbers among what shop_alice and shop_bob reach, then the start of the list of columns that hold personal data.](https://baresql.com/images/case-support-view.png)

The audit covers just those two logins:

```
baresql audit --login shop_alice,shop_bob "postgres://postgres@localhost/baresql_fx_shop"
```

```
{{file:audit/support/before/summary.txt}}
```

## No Finding, and Still the Problem

None of the three findings is about support. The findings flag setups that are risky in any database, such as a table every role can read, and a view that hands its readers card numbers can be exactly what someone intended. So the audit doesn't judge it. It does put the fact where a reviewer can't miss it: in the report's table of who can read personal data, both support logins reach "payment card number". These are the routes:

```
{{grep:audit/support/before/lines.txt:card_number}}
```

The view runs with the rights of its owner, `shop_owner`, so anyone who may read the view reads the full card numbers, without any grant on `shop.customers`. The report also gives its evidence for the column: sampled values that pass the card number check.

## SET ROLE Is Not a Wall

Bob's lines start with "after SET ROLE shop_support_team". A member that doesn't inherit a role's rights holds none of them when he logs in, and gets all of them with one statement whenever he likes. For an audit that means he reads what the team reads, and the report lists him with the team, marked as "some only after SET ROLE". Membership without inheritance is useful for keeping a login's everyday rights small. It does nothing to keep data from a member who wants it.

## The Fix and the Second Audit

The column goes out of the view. As the view's owner:

```
{{file:audit/support/fix.sql}}
```

The audit the next day, compared with the first:

```
{{file:audit/support/diff.txt}}
```

The diff lists {{v:support_gone}} pieces of reach as gone, the card numbers for both logins through both routes, and nothing grew. The rest of what support reads is unchanged, and so is the summary, since none of its findings was about this view.

## Reading a Report for This Kind of Leak

Read the "Who can read personal data" table row by row, and ask of each login whether every kind of data in its row belongs there. The support logins' row should hold names, email addresses and phone numbers. Payment card numbers didn't belong there, and after the fix they're gone.

The [sample reports page](https://baresql.com/sample-reports/) has both reports in full.
