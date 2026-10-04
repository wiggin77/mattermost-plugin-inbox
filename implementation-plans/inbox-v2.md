# Inbox v2 — Implementation Plan

Rationale for the changes below is in [design-review.md](design-review.md). Task-level execution
plans per phase are indexed in [README.md](README.md).

## Goals

1. Reliable sync that doesn't depend on inbound webhooks.
2. Simpler admin setup.
3. Interactive email posts: **Reply**, **Reply all**, **Forward**, editable recipient lists,
   **Mark read/unread**, **Archive**, and a link to open the message in Outlook or Gmail.
4. Gmail as a second provider.
5. A store designed for multiple accounts per user (more than one per provider comes later).

## Decisions

| Topic | Decision |
|---|---|
| Data migration | None. v0.5.0 has no customer deployments. |
| Minimum server | **11.7.0** (current ESR). Needed for dialog multiselect + dynamic lookup (11.0), the multiselect default-value fix (11.6), and the lookup fixes (11.7). |
| Source of truth | Cursor-based delta sync per account. Push notifications only trigger a sync. |
| Real-time push | Optional for both providers. Outlook: auto-detected. Gmail: enabled when Pub/Sub is configured. |
| Accounts per user | Store supports N; v1 allows one per provider. |
| Channel model | One private channel per account. |
| Compose UX | Buttons on each email open a dialog (To/Cc/Bcc chips + message). Replying directly in a thread shows a prompt with recipients and Reply / Reply all / Edit recipients / Don't send. |
| Quoting | Outgoing replies and forwards include the quoted original (Outlook does it natively; Gmail matches). |
| Archive/delete from provider | Archive keeps the post and marks it "Archived". Trash or permanent delete removes the post. |
| Organization-wide consent | Not planned. |
| Webapp | Removed. All UI is server-driven (mobile parity). |
| Persistence | KV store only. |
| Gmail client | `google.golang.org/api/gmail/v1` + `idtoken`. |
| Remote mailbox ownership | One active Mattermost user per remote mailbox. |
| "Don't ask again" | Per-account `AutoSend` setting: `off`, `reply`, `reply_all`. |

## Scopes

| Provider | Scopes | Change from today |
|---|---|---|
| Outlook | `User.Read`, `Mail.ReadWrite`, `Mail.Send`, `offline_access` | `Mail.Read` → `Mail.ReadWrite` (mark read, archive, reply drafts); `User.Read` made explicit |
| Gmail | `gmail.modify`, `gmail.send` | `gmail.modify` covers read + label changes; restricted scope |

## Architecture

```
                 mail.Provider (per type): OAuth config, auth options, NewClient
                       │                                   │
             mail/outlook (msgraph)                mail/gmail (gmail/v1)
                       │                                   │
                 mail.Client (per account) ─────────────────┘
                       │
  push hint ──┐        ▼
  poll tick ──┼─► sync.Engine.SyncAccount ──► email posts (bot) in account channel
  /inbox sync ┘     (cluster mutex, delta cursor, dedup, threading, state)
                                                           │
  [Reply][Reply all][Forward][Actions▾] ──► compose.Service ──► mail.Client.Send / Modify
  thread reply ──► MessageHasBeenPosted ──► prompt ──┘
```

### Packages

| Package | Contents |
|---|---|
| `server/mail` | Neutral types (`Message`, `Address`, `Attachment`, `Change`, `Outgoing`, `MessageState`, `PushState`), `Provider`/`Client` interfaces, errors (`ErrNotFound`, `ErrCursorExpired`, `ErrReauthRequired`). |
| `server/mail/outlook` | Adapter over `server/msgraph` (delta, immutable IDs, draft-based send, move/patch). |
| `server/mail/gmail` | Gmail client: history, MIME walker, MIME composer, label changes, Pub/Sub push verification. |
| `server/msgraph` | Low-level Graph HTTP client (kept; extended for delta, drafts, attachments, upload sessions). |
| `server/sync` | `SyncAccount`, change application, channel management, poll scheduler. |
| `server/compose` | Recipient computation, drafts, dialogs, address lookup, contact cache, send orchestration. |
| `server/email` | Rendering (email post, state footer, outgoing HTML/text, quote stripping). |
| `server/store/kvstore` | Account-scoped schema. |
| `server/command` | `/inbox` including `setup`. |

### Interfaces

```go
type Provider interface {
    Type() ProviderType
    DisplayName() string
    Capabilities() Capabilities // {Compose, StateActions}; gates email-post buttons
    OAuth2Config(redirectURL string) *oauth2.Config
    AuthCodeOptions() []oauth2.AuthCodeOption
    NewClient(ts oauth2.TokenSource) Client
}

type Client interface {
    Profile(ctx context.Context) (*Profile, error)
    InitialCursor(ctx context.Context, since time.Time) (string, error)
    ListChanges(ctx context.Context, cursor string) (changes []Change, next string, more bool, err error)
    GetMessage(ctx context.Context, id string) (*Message, error)
    GetAttachment(ctx context.Context, messageID string, att Attachment) ([]byte, error)
    Send(ctx context.Context, out *Outgoing) (*SentInfo, error)
    SetRead(ctx context.Context, messageID string, read bool) error
    Archive(ctx context.Context, messageID string) error
    WebLink(msg *Message) string
    Watch(ctx context.Context, req WatchRequest) (*PushState, error)
    RenewWatch(ctx context.Context, state *PushState) (*PushState, error)
    StopWatch(ctx context.Context, state *PushState) error
}

type ChangeType int // Created, Updated, StateChanged, Archived, Deleted

type Outgoing struct {
    Mode            ComposeMode // Reply, ReplyAll, Forward
    TargetMessageID string
    To, Cc, Bcc     []Address
    HTML, Text      string
    Attachments     []OutgoingAttachment
    MessageID       string // RFC 5322 Message-ID we assign, for loop prevention
}
```

## Storage

All keys stay within the 50-byte KV limit; `hash()` is the existing 16-hex truncated SHA-256.

| Key | Value | Notes |
|---|---|---|
| `acct_{accountID}` | `Account` | Updated only via `SetAtomicWithRetries` |
| `tok_{accountID}` | encrypted `oauth2.Token` | Written by the persisting token source |
| `uaccts_{mmUserID}` | `[]accountID` | Atomic updates |
| `accounts` | `[]accountID` | Global index for the scheduler |
| `remote_{hash(provider\|remoteID)}` | `RemoteBinding` | Survives disconnect: ownership, reconnect reuse, Gmail push routing |
| `sub_{hash(subscriptionID)}` | `accountID` | Graph webhook routing |
| `c_{accountID}_{hash(threadID)}` | `ConversationMapping` | Account-prefixed so `ListKeys(WithPrefix)` can purge |
| `m_{accountID}_{hash(messageID)}` | `MessageMapping` | Dedup + post ID + last rendered `MessageState` |
| `sent_{hash(accountID\|internetMessageID)}` | `bool`, 24h TTL | Loop prevention |
| `draft_{draftID}` | `Draft`, 1h TTL | Compose state for dialogs and thread prompts |
| `contacts_{accountID}` | `[]ContactEntry` (≤ 500, LRU) | Address autocomplete |
| `oauth_{state}` | `OAuthState{MattermostUserID, Provider, TeamID}`, 5 min TTL | |

The `post_` mapping is removed. Email posts carry `inbox_account_id`, `inbox_message_id` and
`inbox_thread_id` props. Readers trust those props **only when `post.UserId == botUserID`**.

```go
type Account struct {
    ID, MattermostUserID string
    Provider             mail.ProviderType
    RemoteID, RemoteEmail string
    InboxChannelID       string
    SyncCursor           string
    LastSyncAt           int64
    Push                 *mail.PushState   // nil = not attempted; Status: active | unavailable(reason) | expired
    NeedsReauth          bool
    AutoSend             string            // off | reply | reply_all
    CreatedAt            int64
}

type ConversationMapping struct {
    AccountID, ThreadID, RootPostID, Subject string
    LastInboundMessageID                     string // default target for thread replies
}

type MessageMapping struct {
    AccountID, MessageID, ThreadID, PostID string
    InternetMessageID                      string
    IsRoot                                 bool
    State                                  mail.MessageState // Read, Archived — avoids no-op post updates
}

type RemoteBinding struct {
    Provider                               mail.ProviderType
    RemoteID, MattermostUserID, AccountID string
    ChannelID                              string
}

type Draft struct {
    ID, AccountID, UserID, RootPostID string
    TargetMessageID                   string
    Mode                              mail.ComposeMode
    To, Cc, Bcc                       []mail.Address
    SourcePostID                      string // thread-reply path: the user's post
    PromptPostID                      string // thread-reply path: the bot prompt to update
}
```

Reconnect rules: same MM user reconnecting the same mailbox reuses `AccountID` and `ChannelID`.
Another user is rejected while the bound account is active. A binding to a deleted account is
replaced.

## Sync Engine

### `SyncAccount(accountID)`

1. Acquire `cluster.NewMutex(api, "sync_"+accountID)`. If another trigger arrives while held, set a
   `resync` flag the holder re-checks before releasing, so triggers coalesce and none are lost.
2. Load the account and client. On `invalid_grant`, set `NeedsReauth`, post once in the channel and
   stop.
3. Loop `ListChanges(cursor)`; apply each page; persist the cursor **after each page** so a crash
   resumes where it left off. On `ErrCursorExpired`, set `cursor = InitialCursor(LastSyncAt)` and
   continue. By contract, `InitialCursor(since)` yields every Inbox message received after `since`;
   it is also used at connect with `now`.
4. Update `LastSyncAt`.

### Applying changes

| Change | Action |
|---|---|
| Created | Skip if `m_` exists or `sent_` matches its Internet-Message-ID. Fetch, render, post (root or thread reply), store mappings, update `LastInboundMessageID`, add addresses to the contact cache. |
| Updated | Outlook only: re-render if content changed. |
| StateChanged | Compare with `MessageMapping.State`; if different, update the email post's state footer. |
| Archived | Set the state to archived; update the footer. Post is kept. |
| Deleted | Delete the post and `m_`; if it was the root, also delete `c_` so the next message starts a new thread. |

### Outlook delta

- `GET /me/mailFolders('inbox')/messages/delta` with `$select` for the needed fields and headers
  `Prefer: IdType="ImmutableId", odata.maxpagesize=50`. The cursor is the `@odata.deltaLink`.
- Initial cursor at connect: delta with `$filter=receivedDateTime ge {connectTime}`. **Verify** this
  filter is accepted on message delta; if not, page through an initial `$select=id` delta without
  processing anything to reach a deltaLink.
- `@removed` entries: `GET /me/messages/{id}` (immutable ID). 404 or in Deleted Items → Deleted.
  Otherwise → Archived (it was moved elsewhere, e.g. the Archive folder).
- `isRead` changes come back as updated entries → StateChanged.
- Expired deltaLink (HTTP 410 `syncStateNotFound`) → new delta from `LastSyncAt`.

### Gmail history

`history.list(startHistoryId, historyTypes=messageAdded,messageDeleted,labelAdded,labelRemoved)`:

| Record | Condition | Change |
|---|---|---|
| `messageAdded` | has `INBOX`, not `SPAM`/`DRAFT`/`SENT`-only | Created |
| `labelAdded` | `INBOX` | Created if unknown, else StateChanged (un-archived) |
| `labelRemoved` | `INBOX` | Archived |
| `labelAdded` | `TRASH` | Deleted |
| `messageDeleted` | — | Deleted |
| `labelAdded`/`labelRemoved` | `UNREAD` | StateChanged |

404 on `startHistoryId` → `ErrCursorExpired` → `messages.list q="in:inbox after:<LastSyncAt>"`, then
reset to the current profile `historyId`.

### Push as a hint

- **Outlook**: on connect and in the renewal job, try `POST /subscriptions` for the Inbox, with a
  `lifecycleNotificationUrl`. Graph's validation handshake fails if the site isn't reachable. In
  that case `Push.Status = unavailable` with the reason, retried daily, and the account is polled.
  The notification handler checks `clientState`, maps `sub_` → account, returns `202` and triggers
  `SyncAccount`. Lifecycle events (`reauthorizationRequired`, `subscriptionRemoved`, `missed`) renew
  or recreate the subscription and trigger a sync.
- **Gmail**: when `GmailPubSubTopic` is set, `users.watch` (INBOX label filter; **verify** that
  trashing and archiving trigger notifications, else drop the filter). The handler verifies the
  Pub/Sub OIDC token (`idtoken.Validate` + expected service-account `email`), maps `remote_` →
  account, returns `204` and triggers `SyncAccount`. Re-watch when within 48h of expiry.

### Scheduler

A single cluster job ticks every minute on the leader. An account is due when it has no active push
and `LastSyncAt` is older than `PollingIntervalMinutes` (default 2), or when it has active push and
`LastSyncAt` is older than 15 minutes (safety net). Due accounts run through a bounded worker pool
(default 10). Accounts with `NeedsReauth` are skipped.

## Email Posts

```
#### Q3 Budget
**From:** Alice <alice@x.com>
**To:** me@x.com · **Cc:** Bob <bob@x.com>, Carol <carol@y.com>
**Date:** Oct 4, 2026 at 9:12 AM
---
<body: uniqueBody (Outlook follow-ups) / quote-stripped (Gmail follow-ups) / full body (roots)>

[Open in Gmail](…)
┌ attachment ───────────────────────────────────────────┐
│ [Reply] [Reply all] [Forward]  [Actions ▾]            │
│ footer: Unread · Inbox                                 │
└────────────────────────────────────────────────────────┘
```

- Body truncated at 16,000 runes with "*[Truncated — Open in Gmail]*".
- `Actions ▾` is an interactive select: *Mark as read* / *Mark as unread*, *Archive*. Its options
  and the footer are re-rendered whenever the state changes, from either side.
- Button and select contexts carry only `account_id` and `message_id`. Handlers load everything
  else server-side and verify `account.MattermostUserID == request.UserId`.
- Oversized inbound attachments: note with the size and the provider link (as today).

## Compose

### Recipient computation (provider-neutral, `compose/recipients.go`)

- **Reply**: To = Reply-To ∥ From. If the target was sent by the account itself, To = its original
  To.
- **Reply all**: To = (Reply-To ∥ From) + original To; Cc = original Cc.
- **Forward**: empty.
- Always remove the account's own address, dedupe case-insensitively, and keep display names.

### Path 1 — button → dialog

1. Click **Reply / Reply all / Forward** → create `Draft` (target = that email) → `OpenInteractiveDialog`
   using the action's `TriggerId`.
2. Dialog (title ≤ 24 chars, e.g. "Reply all"):
   - **To**, **Cc**, **Bcc**: `select`, `multiselect: true`, `data_source: dynamic`,
     `data_source_url: /plugins/com.mattermost.plugin-inbox/api/v1/compose/lookup`, defaults
     pre-filled (comma-joined).
   - **Message**: `textarea`, max 3000 (required for Reply/Reply all, optional for Forward).
   - `state` = draft ID only.
3. Lookup endpoint returns `LookupDialogResponse{Items}`: the typed text itself if it parses as an
   address, plus prefix matches from `contacts_{accountID}`, plus the current defaults. **Verify** the
   lookup request payload shape and that defaults not present in a lookup result still render as
   chips.
4. Submit → load draft, verify `draft.UserID == request.UserId`, validate addresses (field-level
   `Errors` on failure), send, delete draft. Then create a post **as the user** in the thread with
   the message text, prop `inbox_outgoing=true` (ignored by the hook), and a footer
   "📤 Sent to Alice, Bob · cc Carol".

### Path 2 — typing in the thread

1. `MessageHasBeenPosted`: ignore bot posts, posts with `inbox_outgoing`, and non-replies. Load the
   root post; continue only if it's a bot email post whose account belongs to `post.UserId` and whose
   channel matches.
2. Target = `ConversationMapping.LastInboundMessageID`.
3. `AutoSend` = `reply` / `reply_all` → send immediately and post "📤 Sent to …".
4. Otherwise create a `Draft` (`SourcePostID` = the user's post) and a bot prompt in the thread:
   ```
   Send as email?  To: Alice  ·  Reply all would add: Bob, Carol
   [Send reply] [Send reply all] [Edit recipients…] [Don't send]
   ```
   *Edit recipients…* opens the dialog without the Message field. The Send buttons offer an "always"
   variant via the `Actions ▾`-style select ("Always send replies" / "Always send reply all"), which
   sets `AutoSend`.
5. On send, re-read the user's post (to pick up edits) and include its **file attachments**. Update
   the prompt to "📤 Sent to …" with no buttons.

### Sending

- **Outlook**: `createReply` / `createReplyAll` / `createForward` (draft with native quoting and
  threading) → `PATCH` recipients and prepend our HTML to the draft body → attachments (≤ 3 MB
  direct, larger via upload session) → read the draft's `internetMessageId` → `send`.
- **Gmail**: `messages.get(format=full)` → build `multipart/mixed` (alternative text+HTML with a
  `gmail_quote` block, then attachments; Forward re-attaches the original's attachments) with a
  generated `Message-ID`, `In-Reply-To`/`References` and a `Re:`/`Fwd:` subject. Strip CR/LF from
  every header value. Then `messages.send{raw, threadId}`.
- Both: record `sent_{hash(accountID|internetMessageID)}`. Outbound attachments are capped by
  `MaxAttachmentSizeMB` and the provider limit (Gmail 35 MB total).

### Message state actions

| Action | Outlook | Gmail |
|---|---|---|
| Mark read/unread | `PATCH isRead` | `modify` ±`UNREAD` |
| Archive | `move` → well-known `archive` | `modify` −`INBOX` |

Optimistic footer update in the action response; the following sync confirms it via StateChanged.

## Configuration

| Key | Type | Notes |
|---|---|---|
| `OutlookEnabled` | bool | |
| `OutlookUseSSOApp` | bool | Use `Office365Settings` credentials from the server config |
| `OutlookTenantID`, `OutlookClientID`, `OutlookClientSecret` | text | Hidden in effect when `OutlookUseSSOApp` |
| `OutlookWebhookSecret` | generated | Auto-filled on activation |
| `GmailEnabled` | bool | |
| `GmailUseSSOApp` | bool | Use `GoogleSettings` credentials |
| `GmailClientID`, `GmailClientSecret` | text | |
| `GmailPubSubTopic` | text | Blank = polling only |
| `GmailPushServiceAccount`, `GmailPushAudience` | text | Required when the topic is set; audience defaults to the webhook URL |
| `EncryptionKey` | generated | Auto-filled on activation |
| `PollingIntervalMinutes` | number | Default 2 |
| `MaxAttachmentSizeMB` | number | Inbound and outbound |
| `EnableDiagnostics` | bool | |

`OnActivate` fills empty generated secrets and saves them via `SavePluginConfig`. The plugin starts
even when no provider is configured; `/inbox connect` and `/inbox setup` explain what's missing.

## HTTP Routes

| Route | Auth |
|---|---|
| `GET /api/v1/oauth2/{provider}/connect` | MM user |
| `GET /api/v1/oauth2/{provider}/complete` | OAuth state |
| `POST /api/v1/webhook/outlook`, `POST /api/v1/webhook/outlook/lifecycle` | `clientState` |
| `POST /api/v1/webhook/gmail` | Pub/Sub OIDC |
| `POST /api/v1/action/email` | MM user + ownership (buttons and Actions select on email posts) |
| `POST /api/v1/action/prompt` | MM user + draft ownership (thread prompt) |
| `POST /api/v1/compose/submit` | MM user + draft ownership |
| `POST /api/v1/compose/lookup` | MM user + draft ownership |
| `GET /api/v1/user/status`, `POST /api/v1/user/disconnect` | MM user |

## Slash Command

```
/inbox connect <outlook|gmail>
/inbox disconnect [account]
/inbox status
/inbox sync [account]
/inbox settings [account] autosend <off|reply|reply_all>
/inbox setup                      (system admins)
/inbox help
```

`[account]` is an email address or provider name. When it's omitted inside an inbox channel, the
account owning that channel is used.

`/inbox setup` prints redirect URIs and webhook URLs, checks the Site URL, validates credentials
with a dummy code exchange (`invalid_client` = bad, `invalid_grant` = OK), validates the Pub/Sub
config, and reports push vs polling counts.

## Channels

- Name `inbox-{username}-{provider}` (suffix `-2`, `-3`… later); display name `Inbox: {email}`
  (≤ 64 runes).
- Team: `OAuthState.TeamID` from the slash command, falling back to the user's first team.
- Disconnect preserves the channel; reconnect reuses it via `RemoteBinding`.

## Testing

- **Unit**
  - Recipient computation table: reply/reply-all/forward, Reply-To, self-sent target, self removal,
    dedupe, display names.
  - Dialog builders: every element within server limits (title 24, text 150, textarea/select 3000)
    and validated with `Dialog.IsValid`.
  - Draft and action ownership checks; bot-author check on props.
  - Outlook: delta paging, `@removed` classification, 410 handling, immutable-ID header present,
    draft send sequence, upload session threshold.
  - Gmail: MIME walker fixtures (alternative, mixed, related/inline, non-UTF-8, encoded words);
    composer (threading headers, quoting, attachments, CRLF stripping); history mapping table; push
    verifier.
  - Renderer: truncation at 16,000 runes, quote stripping, state footer.
  - Engine: dedup, sent-loop skip, concurrent `SyncAccount`, cursor persisted per page, root delete.
- **Integration** (testcontainers): `testhelper/mock_graph.go` extended (delta, drafts, move, patch,
  subscriptions with validation handshake success/failure) and new `testhelper/mock_gmail.go`.
  Scenarios: sync without push; push hint → sync; Reply all from dialog; thread reply with an
  attachment; mark read both directions; archive keeps post; Outlook + Gmail on one user.
- **Manual**: real O365 tenant and Google Workspace "Internal" app on 11.7 ESR, web + desktop +
  **mobile** (dynamic multiselect behaviour on mobile is the main unknown).

## Phases

Each phase is a separate PR that leaves `master` releasable.

### Phase 1 — Foundation (Outlook)
- `min_server_version` 11.7.0; remove the webapp.
- `server/mail` + `mail/outlook`; account-scoped KV schema; drop `post_`.
- Delta sync engine with immutable IDs, per-account mutex, per-page cursor, scheduler + worker pool.
- Push-as-hint with auto-detection and lifecycle notifications.
- Persisting token source, `NeedsReauth`, auto-generated secrets.
- Renderer: truncation fix, `uniqueBody`, provider link.
- Config renames, routes, `/inbox` account selector, `/inbox setup`.
- **Done when:** Outlook syncs reliably with and without a reachable Site URL; existing reply flow
  works (temporarily replying to `LastInboundMessageID`).
- **Estimate:** 5–6 days.

### Phase 2 — Interactive email posts (Outlook)
- Compose service, drafts, dialogs with dynamic multiselect, contact cache, lookup endpoint.
- Reply / Reply all / Forward buttons; thread prompt; `AutoSend`; outbound attachments.
- Draft-based send with `sent_` loop prevention.
- Actions select (read/unread, archive) and state footer; scope change to `Mail.ReadWrite`.
- **Done when:** all compose paths and state actions work on web, desktop, and mobile against a real
  tenant.
- **Estimate:** 5–6 days.

### Phase 3 — Gmail read path
- Gmail provider/client, OAuth (`prompt=consent`), history sync, resync path, MIME walker,
  attachments, quote stripping, state changes, rate-limit backoff, `mock_gmail.go`.
- **Done when:** Gmail syncs on the poll interval, including archive/trash/read state.
- **Estimate:** 3–4 days.

### Phase 4 — Gmail compose and actions
- MIME composer, `Send` for all three modes, label-based read/archive.
- **Done when:** Phase 2 behaviour is identical for Gmail.
- **Estimate:** 2–3 days.

### Phase 5 — Gmail push (optional real-time)
- Pub/Sub config, webhook, OIDC verification, watch/renew/stop.
- **Estimate:** 2 days.

### Phase 6 — Setup polish and docs
- `UseSSOApp` toggles.
- README admin guide (Azure; Google Cloud incl. consent screen Internal vs External; optional
  Pub/Sub). Update `CLAUDE.md`.
- Binary-size check for `google.golang.org/api`; full manual pass.
- **Estimate:** 1–2 days.

**Total:** ~18–23 days.

### Later
- Multiple accounts per provider: remove the per-provider limit check.
- Attachments in the compose dialog via the file-upload element (server 11.10+, behind a version
  check).
- Show replies sent from other clients (sync Sent Items / `SENT` label into threads).

## Risks

- **Mobile support for dynamic multiselect** — prototype early in Phase 2. Fallback: comma-separated
  textareas (max 3000) for recipients.
- **Dialog message limit (3000 chars)** — longer replies use the thread path, which has no limit and
  supports attachments.
- **Google verification** — `gmail.modify` is restricted: External apps need verification + an
  annual security assessment. Internal Workspace apps avoid it. Testing-mode refresh tokens expire
  after 7 days.
- **Graph delta initial filter** — unverified; a fallback is described above.
- **Graph throttling** — polling every 2 minutes is ~1 request per mailbox per 2 minutes, well
  under per-mailbox limits; the worker pool bounds concurrency.
- **Dependency weight** of `google.golang.org/api` across five target binaries.
- **KV growth** — `c_`/`m_` keys are account-prefixed, so a future purge-on-disconnect is cheap;
  not in scope.
