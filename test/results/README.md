# Recorded results

Written by `scripts/record-results.sh`; do not edit by hand. The site
quotes these files. `versions.txt` says when, from which commit and on
which engine versions they were recorded.

- `audit/<fixture>/`: BareSQL Audit of each planted database in
  audit/testdata (shop, saas, clinic, and clean, a database set up with
  care): `report.html` and `audit.json` as `baresql audit` writes them,
  `summary.txt` as it prints, `lines.txt` as `--format lines` prints. The
  fixture tests check these audits line by line.
- `audit/counts.txt`: logins, readable columns, findings and the number of
  facts checked exactly per PostgreSQL version, per fixture.
- `audit/agent/`: an AI support agent's login (saas fixture) before
  go-live, `fix.sql`, and after; `diff.txt` is `baresql diff`.
- `audit/support/`: a support view that hands out card numbers (shop
  fixture), the fix and the audit after it.
- `audit/quarterly/`: the shop audited at the start of one quarter (`q3`)
  and the next (`q4`), with the changes made in between; `diff.txt` is
  `baresql diff q3/audit.json q4/audit.json` and `baseline.txt` what
  `baresql audit --baseline q3/audit.json` prints. `fix.sql` is the fix,
  `fixed/` the audit after it and `fixed-diff.txt` its comparison with q4.
- `audit/scale.txt`: the audit of 1,000 tables and 50 logins, timed.
- `portable/rules.txt`, `rules.json`, `rules/<code>.txt`: `baresql rules`,
  `--format json` and `baresql rules <code>`.
- `portable/examples/<code>.txt`: `baresql lint --all` on each rule's own
  example.
- `portable/counts.txt`: rules, catalog rows and recorded answers.
- `portable/shop-*`, `sqlite-tests-*`, `mysql-to-postgres-*`: lint and
  compare on the demo projects in test/demo.
