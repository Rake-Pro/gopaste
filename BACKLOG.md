# gopaste - Backlog

Outstanding and future work. Shipped work is recorded in CHANGELOG.md.
Status keys: `[ ]` todo, `[~]` in progress, `[?]` needs decision.

## Open
- [ ] Postgres conformance run against a real DB (needs GOPASTE_TEST_PG)
- [ ] Live visual QA in a real browser (highlight.js across languages, mobile)
- [ ] Expiry countdown in status bar (needs backend to expose expiry)

## Admin console - follow-ups
The console shipped (OIDC + local, hidden, server-side sessions; see CHANGELOG).
Remaining niceties:
- [ ] Audit-log admin actions to a sink, not just zerolog (deletes/purges).
- [ ] Optional: column sort + bulk-select delete in the console.
- [ ] List pagination beyond `DefaultListLimit` (500) for very large stores.

## Future / maybe
- [ ] Optional backends: s3, redis (behind the same interface)
- [ ] Prometheus /metrics endpoint
- [ ] Paste size + count metrics, structured access logs to a sink
- [ ] Optional API-token auth for writes
