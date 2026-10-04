# Implementation Plans — Inbox v2

| Doc | Purpose |
|---|---|
| [design-review.md](design-review.md) | Why the v0.5.0 design changes; setup simplification; rejected alternatives |
| [inbox-v2.md](inbox-v2.md) | Target design spec: decisions, architecture, storage, sync, compose, config |
| [phase-1-foundation.md](phase-1-foundation.md) | Outlook on the new foundation: accounts, `mail` layer, delta sync, push hints, setup |
| [phase-2-interactive-outlook.md](phase-2-interactive-outlook.md) | Reply / Reply all / Forward, recipient dialogs, thread prompt, read/archive (Outlook) |
| [phase-3-gmail-read.md](phase-3-gmail-read.md) | Gmail provider: OAuth, history sync, MIME parsing (polling) |
| [phase-4-gmail-compose.md](phase-4-gmail-compose.md) | Gmail send (MIME composer) and read/archive actions |
| [phase-5-gmail-push.md](phase-5-gmail-push.md) | Optional Gmail real-time via Pub/Sub |
| [phase-6-setup-docs.md](phase-6-setup-docs.md) | SSO-app reuse, admin/user docs, release hardening |

## Order and dependencies

```
Phase 1 (PRs 1a → 1b → 1c → 1d)
   ├─► Phase 2 (Outlook interactive) ─┐
   └─► Phase 3 (Gmail read) ──────────┴─► Phase 4 (Gmail compose) ─► Phase 5 (Gmail push) ─► Phase 6
```

Phases 2 and 3 can run in parallel after Phase 1. Phase 4 needs both.

## Conventions

- Each phase starts with **verification spikes**. Record the results in the PR description, and
  update the phase plan if a spike contradicts it.
- Tasks are numbered `P{phase}.{n}` and grouped into PRs. Every PR compiles, passes
  `make check-style test`, and leaves `master` releasable.
- **Verify:** marks an assumption that hasn't been checked against the API or server source yet.
- Keep `CLAUDE.md` accurate in the same PR that changes commands, packages, KV keys or versions.
