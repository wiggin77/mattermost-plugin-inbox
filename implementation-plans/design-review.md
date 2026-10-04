# Design Review — Mattermost Inbox

Date: 2026-10-04. Reviewed against the v0.5.0 codebase, in preparation for Gmail support and
interactive email actions. The resulting work is planned in [inbox-v2.md](inbox-v2.md).

## Verdict

The overall shape is right; the sync model and data handling are not.

Mirroring each mailbox into a bot-owned private channel, with threads for conversations and
delegated per-user OAuth, is a good fit for Mattermost and is kept. The webhook-driven sync with a
time-filtered polling fallback is fragile, loses mail in several cases, and forces a public inbound
endpoint on admins. Storage is keyed by user with unscoped mapping keys, which blocks multiple
accounts. The reply flow sends to the root email's sender with no visibility of recipients, which is
the opposite of what Reply / Reply All / editable recipients need.

## Keep

- Bot-owned private channel per mailbox, one thread per conversation.
- Delegated OAuth per user, tokens AES-GCM encrypted at rest.
- Cluster-aware background jobs (`cluster.Schedule`).
- KV store as the only persistence (see "Storage" below for why not SQL).
- Server-driven UI (interactive messages and dialogs) so mobile works.

## Change

| # | Finding | Consequence | Change |
|---|---|---|---|
| 1 | Sync is webhook-first. Fallback polling uses `receivedDateTime ge <last sync>` with `$top=50`, ignores `@odata.nextLink`, then sets the cursor to *now*. | Bursts over 50 messages are silently dropped. Messages moved into the Inbox with an older `receivedDateTime` are never seen. Clock skew loses mail. Timely delivery requires Microsoft to reach the Mattermost site over public HTTPS, which many self-hosted deployments don't allow. | **Cursor-based delta sync is the single source of truth** (Graph `messages/delta`, Gmail `history.list`). Push notifications become "sync now" hints. Push is optional and auto-detected. |
| 2 | Graph message IDs are the default mutable IDs. | When a user files or moves a message in Outlook its ID changes. Replies from Mattermost then fail, and updates/deletes no longer match. | Send `Prefer: IdType="ImmutableId"` on every Graph call. |
| 3 | Refreshed OAuth tokens are never persisted; every client is built from the connect-time token. | Microsoft rotates refresh tokens. The original expires (~90 days) and the connection silently dies. | Persisting token source; tokens stored under their own key. |
| 4 | No per-mailbox serialization. Webhooks and the poll job can process the same message at once. | Both pass the dedup check and post twice. | Per-account cluster mutex around every sync. |
| 5 | Bodies truncated at 60,000 bytes; the server limit is 16,383 runes. | Long emails fail `CreatePost` and are lost (only logged). | Truncate to the server limit with an "Open in Outlook/Gmail" link. |
| 6 | Each reply post renders the full body, including quoted history. | Threads repeat the entire conversation in every post. | Use Graph `uniqueBody` for follow-ups; strip quoted blocks for Gmail. |
| 7 | Reply sends to the **root** email's sender. Recipients are never shown. Confirmation is all-or-nothing. Files attached to the Mattermost reply are ignored. | Wrong target in multi-message threads; no Reply All; no way to see or change who receives the email. | New compose service: Reply / Reply all / Forward buttons, dialog with editable recipients, thread-reply prompt, attachments. |
| 8 | Loop prevention marks the *original* message ID as sent, so it never matches the echo of our own reply. | Ineffective (harmless today only because the subscription watches Inbox, not Sent). | Track the `Internet-Message-ID` of what we send; skip inbound copies that match. |
| 9 | KV keys are per user; `conv_`/`msg_` keys aren't scoped to a mailbox; the subscription lookup scans every user; index lists are read-modify-write JSON arrays. | Blocks multiple accounts; O(n) webhook routing; index updates can be lost across nodes. | Account-scoped schema with atomic index updates and O(1) routing indexes. |
| 10 | The `post_` mapping duplicates data already in the email post's props. | Extra writes and an extra key type to keep consistent. | Read routing data from the root post's props, **only if the post's author is the bot** (users can set props on their own posts). |
| 11 | Reply-action button trusts its context without checking ownership. | A crafted request could act on another user's thread. | All actions and dialogs reference server-side state by ID and verify the owner. |
| 12 | The webapp is the empty starter template. | Node/Webpack/Jest in the build for nothing. | Remove the webapp; all UI is server-driven. |

## Setup Simplification

### Outlook admin steps

| Step | Today | Proposed |
|---|---|---|
| Register Azure app, redirect URI, API permissions, client secret | Required | Required (inherent to delegated OAuth) |
| Enter Tenant ID, Client ID, Client Secret | Required | Required |
| Click **Regenerate** for Encryption Key and Webhook Secret | Required (plugin refuses to start without them) | **Removed** — generated on activation if empty |
| Make the Site URL reachable by Microsoft over public HTTPS | Effectively required (fallback polling is lossy) | **Optional** — detected automatically; delta polling is reliable without it |
| Figure out the exact redirect URI | Read the manifest footer | `/inbox setup` prints exact URIs and checks the credentials |

### Gmail admin steps

| Step | Proposed |
|---|---|
| Google Cloud project, enable Gmail API, OAuth consent screen, web OAuth client | Required |
| Enter Client ID / Client Secret | Required |
| Pub/Sub topic, IAM grant, authenticated push subscription | **Optional** "real-time" add-on; polling works without it |

### Additional setup aids

- **`/inbox setup`** (system admins): prints redirect URIs and webhook URLs. Checks the Site URL and
  validates each provider's client credentials. To check credentials, it exchanges a dummy
  authorization code: `invalid_client` means bad credentials, `invalid_grant` means they're fine.
  It also reports how many accounts have real-time push vs polling.
- **Reuse Mattermost's SSO app** (optional toggle per provider): read `Office365Settings` /
  `GoogleSettings` via `GetUnsanitizedConfig()` so an organization that already signs in with
  Microsoft or Google doesn't need a second app registration. The admin still adds the plugin's
  redirect URI and mail scopes to that app.

## Alternatives Considered

| Alternative | Why not |
|---|---|
| **Organization-wide consent** (Azure application permissions / Google domain-wide delegation, no per-user OAuth) | Not needed (product decision). It also grants the app access to every mailbox in scope and relies on trusted email mapping. |
| **Generic IMAP/SMTP** | Doesn't avoid an OAuth app for Microsoft 365 or Gmail. It also needs one long-lived IDLE connection per account pinned to a single cluster node. Could be added later as a third provider for other hosts. |
| **Plugin-owned SQL tables** | Viable now that the minimum server is Postgres-only (v11). But every access pattern is a point lookup, a small per-user list, or a TTL'd draft, all of which KV handles well. SQL would add migrations and schema ownership for little gain. Revisit if search across mail or reporting is ever needed. |
| **Webapp compose modal** | Richer editor and address chips, but nothing on mobile. Server dialogs on 11.7+ provide multiselect address fields with autocomplete. |
| **Pub/Sub pull instead of push** (Gmail) | Removes the public-endpoint requirement but adds a service-account key the admin must upload. Polling already covers deployments that aren't reachable. |
| **Webhook payload as the source of truth** (today's design) | Notifications are best-effort, can arrive out of order, and carry no history after an outage. Delta sync makes them unnecessary for correctness. |
