---
title: "What an AI Support Agent Could Read Before Its Database Login Went Live"
date: 2026-10-26
draft: false
tags: ["case studies", "BareSQL Audit"]
featured_image: "https://baresql.com/images/case-ai-agent.png"
---

This case study runs on the sample multi-tenant database that BareSQL's tests build. The software company in it is made up: it runs one PostgreSQL database for all its customers, the tenants, and row security keeps each tenant's rows apart. Every output below is what BareSQL printed.

The support team wants an AI agent to answer tickets, so the agent gets a login of its own, `saas_agent`, in the `saas_support` group that support's views are granted to. The views were written for people, months ago, and nobody remembers exactly what they return. So before the login goes live, the team audits that one login.

![The findings of the BareSQL Audit report for the support agent's login before go-live: a high finding for the view support.ticket_context, which skips row security, then the email addresses and names the agent can read.](https://baresql.com/images/case-ai-agent.png)

The command and what it prints:

```
baresql audit --login saas_agent "postgres://postgres@localhost/baresql_fx_saas"
```

```
{{file:audit/agent/before/summary.txt}}
```

## The View That Hands Out Every Tenant

The high finding is about `support.ticket_context`, the view that shows a ticket with the customer's name and email address. These are its routes to the customers' data, as `--format lines` prints them:

```
{{grep:audit/agent/before/lines.txt:^read saas_agent app\.accounts\.}}
```

A view reads its tables with its owner's rights unless it says otherwise, and this one's owner, `saas_owner`, owns `app.accounts`. Row security doesn't apply to a table's owner unless the table forces it, and this one doesn't. So the policies that keep tenants apart never run for this view, and the agent, asked about one ticket, can read the name and email address of every customer with a ticket, in every tenant. A prompt that talks the agent into listing them would be enough.

## The Safer View That Fails

A second view, `support.ticket_context_invoker`, was written later with `security_invoker`, so it reads with the agent's own rights. It's granted to support as well. The audit lists it and says it gives nothing:

```
{{grep:audit/agent/before/lines.txt:^read saas_agent support\.ticket_context_invoker\.email }}
```

The agent may not read `app.accounts` itself, and PostgreSQL checks every column a view's query reads, so it refuses every query on this view, even one that selects only the ticket columns. The agent's developers would have met that error on the first test and gone back to the view that works.

## What the Audit Leaves to You

The agent reads tickets with "rows allowed by policy support_read". BareSQL names the policy and doesn't judge its condition, so that part is left to a person. Here the condition was `USING (true)`: support could read every tenant's tickets, too.

## The Fix

Both views should read with the reader's rights, and support should see one tenant at a time, the one its session works for. The team made four changes:

```
{{file:audit/agent/fix.sql}}
```

And audited again:

```
{{file:audit/agent/after/summary.txt}}
```

The high finding is gone. The two others are about tables the agent doesn't touch: an audit log whose owner can log in and so skips row security, and a table of API keys with row security on and no policy, which only its owner and roles that bypass row security can read. Every route of the agent to a customer's data now ends in "rows allowed by policy support_read", and the policy now limits rows to the session's tenant.

## Why the Diff Says Reach Grew

`baresql diff` on the two audits reports {{v:agent_grew}} new pieces of reach:

```
{{lines:audit/agent/diff.txt:1-8}}
```

For a fix, that looks odd at first. The direct column grant gives support `app.accounts.id`, which it didn't read before, and the view that failed now works. All of it comes with the new tenant policy. The diff counts any route that starts giving rows as growth, since that is exactly what a reviewer has to look at, and here the reviewer can see each one and why.

The team keeps the second `audit.json`. From now on a nightly `baresql audit --baseline` fails as soon as the agent's login reaches anything new, before anyone has asked the agent about it. Both reports are on the [sample reports page](https://baresql.com/sample-reports/).
