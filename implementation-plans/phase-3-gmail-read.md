# Phase 3 — Gmail Read Path

> Spec: [inbox-v2.md](inbox-v2.md) · Depends on: Phase 1 (Phase 2 not required) · Estimate: 3–4 days · PRs: **3a** Gmail client library (`server/mail/gmail`, mocks, unit tests — not wired in), **3b** Wiring (config, manifest, registry, OAuth, setup checks, integration tests)

## Outcome

A user runs `/inbox connect gmail`, completes Google OAuth, and gets an `inbox-{username}-gmail`
channel. New inbox mail appears there on the poll interval, threaded by `threadId`, with attachments
up to `MaxAttachmentSizeMB`. Archive, trash, and read-state changes made in Gmail are reflected
according to the history mapping table. Revoked grants set `NeedsReauth`. `/inbox setup` validates
the Gmail credentials. Outlook behaviour is unchanged.

## Out of scope

- `Send`, `SetRead`, `Archive` (Phase 4). The Gmail client returns `mail.ErrNotSupported`
  ("not supported yet") for these. Email posts for Gmail accounts render **without** the
  Reply / Reply all / Forward buttons and the Actions select. The thread-reply prompt is not shown for
  Gmail accounts.
- Pub/Sub push, `Watch`/`RenewWatch`/`StopWatch` (Phase 5). These return `nil, nil`/no-op so the
  scheduler treats Gmail accounts as push-less and polls them.
- `UseSSOApp` toggle (Phase 6).
- Multiple Gmail accounts per user (per-provider limit from Phase 1 stays).

## Verification spikes (do first)

Half a day, before PR 3a. Record findings at the bottom of this file under "Spike results"; each one
either confirms the plan or changes a task below.

1. **Test environment.** Create a Google Cloud project, enable the Gmail API, configure an
   **Internal** OAuth consent screen in a test Workspace, and create a Web OAuth client with redirect
   URI `http://localhost:8065/plugins/com.mattermost.plugin-inbox/api/v1/oauth2/gmail/complete`.
   Store the client ID/secret in the team's shared vault (not the repo). Confirm that with
   `prompt=consent` + `access_type=offline` a refresh token is returned on **every** consent, not
   only the first.
2. **Header and body decoding in `format=full`.** Send test mails with: an RFC 2047 encoded-word
   subject (`=?UTF-8?B?...?=` and `=?ISO-8859-1?Q?...?=`), a `From` display name with encoded words,
   an ISO-8859-1 `text/plain` body, a Windows-1252 `text/html` body, and a `quoted-printable` part.
   Capture the raw `messages.get` JSON as test fixtures.
   - **Verify:** whether `payload.headers[].value` arrives already decoded. The walker always runs
     `mime.WordDecoder` either way, but the fixtures must reflect reality.
   - **Verify:** whether `body.data` is transfer-decoded (expected yes) and left in the original
     charset (expected yes, so charset conversion is needed).
3. **History semantics.** For one message in INBOX, perform and capture `history.list` output after
   each of: archive, move to Inbox again, mark read, mark unread, move to Trash, delete forever,
   apply a filter that skips the inbox, and send a reply to self.
   - **Verify:** which record type each produces and the `labelIds` on the embedded message. In
     particular, whether trash appears as `labelAdded TRASH` and/or `labelRemoved INBOX`. The
     mapping must not emit both Archived and Deleted for one trash.
4. **Expired cursor.** Call `history.list` with a very old or invalid `startHistoryId`.
   - **Verify:** that it is HTTP 404 (not 400). Capture the `googleapi.Error` shape so
     `isCursorExpired` matches on status + reason precisely.
5. **Binary size.** On a branch, add `google.golang.org/api/gmail/v1` + `option` and build with
   `make server` (all five targets: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64,
   windows-amd64). Record the before/after sizes from `server/dist/`.
   - Acceptable: < 15 MB growth per binary.
   - If larger, drop to a hand-rolled REST client modelled on `server/msgraph/client.go`. The
     interface boundary in P3.2 keeps this swap contained to `server/mail/gmail/api.go`.

## Tasks

### P3.1 Dependency and package skeleton (PR 3a)

**Files:** `go.mod`, `go.sum`, `server/mail/gmail/provider.go`, `server/mail/gmail/client.go`,
`server/mail/gmail/api.go`

**Steps:**
- `go get google.golang.org/api/gmail/v1 google.golang.org/api/option golang.org/x/oauth2/google`.
  Pin versions. Run `go mod tidy`.
- `provider.go`: `type Provider struct{ endpoints Endpoints }` implementing `mail.Provider`.
  - `Type()` returns `mail.ProviderGmail`; `DisplayName()` returns `"Gmail"`.
  - `OAuth2Config(redirectURL)` uses `google.Endpoint` (or `Endpoints.AuthURL/TokenURL` when set)
    with scopes `gmail.GmailModifyScope`, `gmail.GmailSendScope`.
  - `AuthCodeOptions()` returns `oauth2.AccessTypeOffline` and
    `oauth2.SetAuthURLParam("prompt", "consent")`.
- `Endpoints{AuthURL, TokenURL, APIBaseURL string}` with zero value = production. Populated from
  Phase 1's `DeveloperEndpoints` (P3.10).
- `api.go`: a narrow internal interface over the generated service, so tests and a possible
  hand-rolled fallback can swap it:
  ```go
  type api interface {
      getProfile(ctx) (*gmail.Profile, error)
      listHistory(ctx, startID uint64, pageToken string) (*gmail.ListHistoryResponse, error)
      listMessages(ctx, q, pageToken string) (*gmail.ListMessagesResponse, error)
      getMessage(ctx, id, format string) (*gmail.Message, error)
      getAttachment(ctx, msgID, attID string) (*gmail.MessagePartBody, error)
  }
  ```
  Implemented over `gmail.NewService(ctx, option.WithHTTPClient(oauth2.NewClient(ctx, ts)),
  option.WithEndpoint(base))`. The user ID is always `"me"`.
- `client.go`: `Client` implements `mail.Client`.
  - `Send`, `SetRead`, `Archive` return `mail.ErrNotSupported` (add it to `server/mail/errors.go`
    if Phase 1 didn't).
  - `Watch` returns `nil, nil`; `RenewWatch`/`StopWatch` are no-ops.
  - `Provider.Capabilities()` (defined in Phase 1, P1.5) returns
    `{Compose: false, StateActions: false}` until Phase 4. P3.9 relies on it.

**Tests:** compile-only in this task; behaviour is tested in later tasks.

**Done when:** `make check-style` passes; `go build ./server/...` succeeds.

### P3.2 Error mapping and rate-limit backoff (PR 3a)

**Files:** `server/mail/gmail/errors.go`, `server/mail/gmail/retry.go`

**Steps:**
- `mapErr(err)`:
  - `*googleapi.Error` 404 on `getMessage`/`getAttachment` → `mail.ErrNotFound`.
  - 404 on `listHistory` → `mail.ErrCursorExpired`.
  - `*oauth2.RetrieveError` with `ErrorCode == "invalid_grant"` (wrapped in `*url.Error` by the
    transport — unwrap with `errors.As`) → `mail.ErrReauthRequired`.
  - 401 after refresh → `mail.ErrReauthRequired`.
- `withRetry(ctx, fn)`: retry on 429, on 403 whose `Errors[].Reason` is `rateLimitExceeded` or
  `userRateLimitExceeded`, and on 5xx.
  - Honour a `Retry-After` header when present (`googleapi.Error.Header`), else exponential
    backoff 1s→16s with jitter, max 5 attempts, respecting `ctx.Done()`.
  - 403 with other reasons (e.g. `insufficientPermissions`) is not retried.
- Every `api` call in `client.go` goes through `withRetry` + `mapErr`.

**Tests:** `errors_test.go`, `retry_test.go` table tests using a fake `api`:
- 429 then success.
- 403 `userRateLimitExceeded` then success.
- 403 `insufficientPermissions` → immediate error.
- `Retry-After: 2` honoured (inject a clock/sleeper).
- Context cancel aborts.
- `invalid_grant` → `ErrReauthRequired`.

**Done when:** all error classes map as above and no retry loop can exceed ~31s total.

### P3.3 Profile and initial cursor (PR 3a)

**Files:** `server/mail/gmail/client.go`

**Steps:**
- `Profile`: `users.getProfile` → `mail.Profile{RemoteID: strings.ToLower(p.EmailAddress), Email:
  p.EmailAddress}`.
- `InitialCursor(ctx, since)`: return `strconv.FormatUint(profile.HistoryId, 10)`. `since` is
  ignored; nothing before connect is synced, matching Outlook.

**Tests:** fake `api`; mixed-case address is lowercased in `RemoteID` and preserved in `Email`;
historyId is formatted as a decimal string.

**Done when:** covered by tests.

### P3.4 History → changes (PR 3a)

**Files:** `server/mail/gmail/history.go`

**Steps:**
- `ListChanges(ctx, cursor)`: parse `cursor` as `uint64`; a parse failure returns
  `ErrCursorExpired`.
- Call `listHistory(start, pageToken)` with `historyTypes=messageAdded,messageDeleted,labelAdded,labelRemoved`.
  Encode the page token into the returned cursor so the engine's per-page persistence works:
  - Mid-pagination: `cursor = "<startId>:<pageToken>"`, `more = true`.
  - On the last page: `cursor = response.HistoryId`, `more = false`.
- Map records in order:

| Record | Condition (labels on embedded message) | Change |
|---|---|---|
| `messagesAdded` | has `INBOX`, not `SPAM`/`DRAFT`; skip if `SENT` without `INBOX` | Created |
| `labelsAdded` | added `INBOX` | Created (engine dedups; existing → StateChanged un-archived) |
| `labelsRemoved` | removed `INBOX`, message not in `TRASH` | Archived |
| `labelsAdded` | added `TRASH` | Deleted |
| `messagesDeleted` | — | Deleted |
| `labelsAdded`/`labelsRemoved` | `UNREAD` | StateChanged |

- Collapse multiple changes for the same message within one page, keeping the last terminal state
  (Deleted wins over everything; Created + Archived in the same page → Archived only if already
  known, else skip). This avoids posting a message that was archived seconds after arrival.
  **Verify** against spike 3 output and adjust the trash rule.
- Catch-up follows the Phase 1 `InitialCursor` contract. The engine handles `ErrCursorExpired` by
  calling `InitialCursor(ctx, LastSyncAt)`; connect calls it with `now`.
  - `InitialCursor(since)` records `getProfile().HistoryId` **first**.
  - If `since` is within a minute of now, it returns that historyId (nothing to catch up).
  - Otherwise it returns a catch-up cursor `list:<since.Unix()>:<historyId>[:<pageToken>]`.
  - `ListChanges` on a `list:` cursor pages `listMessages(q="in:inbox after:<since>")` and emits
    Created for each ID. After the last page it returns the recorded historyId as the cursor and
    continues with `history.list`.

**Tests:** `history_test.go` drives a fake `api` with JSON fixtures from spike 3. One case per table
row, plus:
- Pagination across two pages, checking the cursor format and `more`.
- Collapse rules.
- Malformed cursor.
- 404 → `ErrCursorExpired`; `InitialCursor(past)` yields a `list:` cursor that emits Created for listed messages, then the pre-recorded historyId.
- The `list:` cursor paginates `listMessages` via its page token.

**Done when:** every table row and the spike-3 fixtures produce the expected change sequence.

### P3.5 MIME walker and message conversion (PR 3a)

**Files:** `server/mail/gmail/mime.go`, `server/mail/gmail/testdata/*.json`

**Steps:**
- `GetMessage(ctx, id)`: `getMessage(id, "full")` → `toMailMessage`.
- `toMailMessage`:
  - `ID = m.Id`, `ThreadID = m.ThreadId`.
  - `Date = time.UnixMilli(m.InternalDate)`.
  - `Preview = html.UnescapeString(m.Snippet)`.
  - Labels → `State{Read: !has(UNREAD), Archived: !has(INBOX)}`.
  - `InternetMessageID` from the `Message-ID` header.
- Headers: case-insensitive lookup of `Subject`, `From`, `To`, `Cc`, `Reply-To`, `Message-ID`,
  `References`. Values go through `(&mime.WordDecoder{CharsetReader: charset.NewReaderLabel}).DecodeHeader`.
  Addresses are parsed with `mail.ParseAddressList`; on failure, fall back to a single
  `Address{Address: raw}`.
- Walker (recursive over `payload.Parts`):
  - `multipart/alternative`: pick `text/html` over `text/plain`.
  - `multipart/related`: the first child is the body; children with `Content-ID` are inline.
  - `multipart/mixed`: first non-attachment child is the body; the rest are attachments.
  - A part is an attachment when `Filename != ""` or `Content-Disposition: attachment`.
  - It is inline when `Content-Disposition: inline` with `Content-ID`, or a `Content-ID` child of
    `related`.
- Body bytes: `body.data`, decoded with `base64.URLEncoding`, falling back to
  `base64.RawURLEncoding`. Charset from the part's `Content-Type` `charset=` param, converted with
  `golang.org/x/net/html/charset` (`charset.NewReaderLabel`). Unknown charset → use bytes as-is.
- `mail.Attachment{ID: body.AttachmentId, Name, ContentType, Size: body.Size, Inline}`. `Data` is
  left empty unless `body.data` is present (small attachments).
- Missing HTML and text → `Body = Preview`.

**Tests:** `mime_test.go` with fixtures captured in spike 2 plus hand-built cases:
- alternative (html+text)
- mixed with two attachments
- related with inline image
- nested mixed→alternative→related
- ISO-8859-1 plain text
- Windows-1252 HTML
- encoded-word subject and From
- malformed address list
- text-only message
- empty body (snippet fallback)
- padded and unpadded base64url

**Done when:** all fixtures convert to the expected `mail.Message` (golden structs).

### P3.6 Attachments and web link (PR 3a)

**Files:** `server/mail/gmail/client.go`

**Steps:**
- `GetAttachment(ctx, msgID, att)`: if `att.Data` is set, return it. Otherwise call
  `getAttachment(msgID, att.ID)` and base64url-decode `Data`. The engine only calls this when
  `att.Size <= MaxAttachmentSizeMB` (Phase 1 behaviour).
- `WebLink(msg)`: `https://mail.google.com/mail/?authuser=<email>#all/<msg.ID>`.
  **Verify:** the link opens the right message for accounts not signed in as the default
  `authuser`. Alternative: `#search/rfc822msgid:<Message-ID>`, which works across ID formats; pick
  whichever works in the manual test.

**Tests:** inline data short-circuits the API call; API path decodes; a 404 maps to `ErrNotFound`;
`WebLink` format.

**Done when:** covered by tests.

### P3.7 Quote stripping for Gmail follow-ups (PR 3b)

**Files:** `server/email/quotes.go` (Phase 1 hook), `server/email/quotes_test.go`

**Steps:**
- Implement the Gmail strategy registered for `mail.ProviderGmail`, applied only to non-root
  posts. Before html→markdown conversion, parse with `golang.org/x/net/html` and remove:
  - `div.gmail_quote` and `div.gmail_quote_container`
  - `blockquote[type=cite]`
  - Apple Mail `blockquote` following a `div` with an "On … wrote:" attribution
  - Outlook-originated `div#divRplyFwdMsg` and everything after it, plus `div#appendonsend`
- For `text/plain` bodies, drop the trailing block starting at a line matching
  `^On .+wrote:$`, followed by `>`-prefixed lines.
- If stripping leaves fewer than 1 non-whitespace rune, keep the original body. This covers
  forwards and top-posting-free replies.

**Tests:** fixtures for Gmail web reply, Gmail mobile reply, Apple Mail reply, Outlook reply into a
Gmail thread, a plain-text reply, inline-reply (interleaved quotes, which must not be destroyed:
keep the original when quotes aren't trailing), and a forward.

**Done when:** follow-up posts in the integration test show only new text.

### P3.8 Configuration and manifest (PR 3b)

**Files:** `server/configuration.go`, `server/configuration_test.go`, `plugin.json`

**Steps:**
- Add `GmailEnabled bool`, `GmailClientID string`, `GmailClientSecret string`.
- `plugin.json`: three settings after the Outlook block, with help text:
  - `GmailClientSecret` has `secret: true`.
  - The footer gains the Gmail redirect URI pattern.
  - Pub/Sub fields are **not** added yet (Phase 5).
- `IsValid`: if `GmailEnabled`, both ID and secret are required. "At least one provider enabled"
  (from Phase 1) now counts Gmail.
- Re-run `make apply` so `server/manifest.go` is regenerated.

**Tests:** extend `TestConfigurationIsValid`:
- Gmail-only valid.
- Gmail enabled with a missing secret.
- Both disabled → error.
- Outlook-only still valid.

**Done when:** System Console shows the Gmail section; the plugin activates with Gmail-only
config.

### P3.9 Registry, OAuth wiring, and UI gating (PR 3b)

**Files:** `server/plugin.go` (provider registry), `server/oauth.go` / `server/api.go` (whichever
Phase 1 used for OAuth handlers), `server/command/command.go`, `server/email/renderer.go`,
`server/hooks.go`

**Steps:**
- Register `gmail.NewProvider(endpoints)` when `GmailEnabled`. Re-register on
  `OnConfigurationChange`.
- OAuth connect/complete need no new handler. Confirm the generic flow:
  - `AuthCodeOptions` are applied.
  - The token exchange works.
  - `Profile` → `RemoteBinding` lookup on `remote_{hash(gmail|lowercased email)}`.
  - Channel `inbox-{username}-gmail`.
  - `InitialCursor` is stored.
  - Welcome post says "Connected to Gmail as …".
- Autocomplete for `/inbox connect` lists `gmail` when enabled. **Verify** Phase 1 builds this
  from the registry dynamically; if not, update `getAutocompleteData`.
- Gating: when the client reports no compose support, the renderer omits the action attachment
  (buttons + Actions select) and `MessageHasBeenPosted` does not create a thread prompt. The
  "Open in Gmail" link is still rendered.
- `/inbox status` shows "Real-time: off (polling every N min)" for Gmail accounts.

**Tests:**
- Command tests: `connect gmail` returns an auth URL containing `access_type=offline`,
  `prompt=consent`, and both scopes.
- Renderer test: Gmail email post has no action attachment.
- Hook test: thread reply in a Gmail channel creates no prompt.

**Done when:** a manual connect against the real test project lands in the channel with a welcome
post.

### P3.10 Developer endpoints for Gmail and Google OAuth (PR 3b)

**Files:** `server/configuration.go` (Phase 1 `DeveloperEndpoints`), `server/plugin.go`

**Steps:**
- Add keys `GmailAPIBaseURL`, `GoogleAuthURL`, `GoogleTokenURL` to the hidden `DeveloperEndpoints`
  config (honoured only when `ServiceSettings.EnableDeveloper` is true, same as the Graph entries).
- Pass them into `gmail.Endpoints` at registration.
- Log a warning at activation when any override is active.

**Tests:** unit test that overrides are ignored when `EnableDeveloper` is false and applied when
true.

**Done when:** integration tests (P3.12) can point the plugin at `mock_gmail.go`.

### P3.11 `/inbox setup` Gmail checks (PR 3b)

**Files:** `server/setup.go` (Phase 1), `server/setup_test.go`

**Steps:**
- When `GmailEnabled`:
  - Print the redirect URI `{SiteURL}/plugins/com.mattermost.plugin-inbox/api/v1/oauth2/gmail/complete`.
  - Exchange a dummy code against the token URL with the configured credentials:
    - `invalid_grant` → "credentials OK".
    - `invalid_client` / `unauthorized_client` → "client ID or secret is wrong".
    - `redirect_uri_mismatch` → "add the redirect URI above to the OAuth client".
    - Network error → report as is.
  - Print the counts of Gmail accounts active, needing reauth, and polling.
- Note that real-time push is not configured, linking to Phase 5 docs once they exist.
- **Verify:** Google's response to a dummy code with a *valid* client and an unregistered redirect
  URI. It may return `redirect_uri_mismatch` before `invalid_grant`, which is useful extra signal.

**Tests:** httptest token endpoint returning each error body; assert the message for each.

**Done when:** running against the real project with a correct and then a wrong secret gives the
two expected outcomes.

### P3.12 Mock Gmail server and integration tests (PR 3a mock, PR 3b tests)

**Files:** `server/testhelper/mock_gmail.go`, `server/mail/gmail/client_test.go`,
`server/integration_gmail_test.go`

**Steps:**
- `MockGmailServer` (httptest, same style as `server/testhelper/mock_graph.go`), serving under
  `/gmail/v1/users/me/`:
  - `profile` and `history` (supports `startHistoryId`, `pageToken`, forced 404).
  - `messages` list with `q` (supports `in:inbox after:`) and `messages/{id}` with `format=full`.
  - `messages/{id}/attachments/{aid}`.
  - OAuth `/o/oauth2/auth`, which immediately redirects to `redirect_uri` with `code` and `state`.
  - `/token`, which issues an access token plus refresh token and returns `invalid_grant` when
    toggled.
- Helpers: `AddInboxMessage(fixture)`, `Archive(id)`, `Trash(id)`, `MarkRead(id, bool)`,
  `ExpireHistory()`, `RateLimitNext(n, reason)`, `RevokeGrant()`. Each mutates state and appends
  history records with monotonically increasing IDs.
- `client_test.go`: real `gmail.Client` against the mock via `option.WithEndpoint(mock.URL+"/")` +
  `option.WithHTTPClient`, exercising P3.3–P3.6 end to end over HTTP.
- `integration_gmail_test.go` (Docker/testcontainers harness from Phase 1; skipped with
  `SKIP_DOCKER_TESTS`):
  1. Configure the plugin with Gmail enabled and developer endpoints pointing at the mock (host
     access).
  2. Drive `/oauth2/gmail/connect` → mock auth → `complete`. Assert the channel exists and the
     welcome post is present.
  3. Add 2 messages in one thread plus 1 in another; trigger `/inbox sync`. Assert 2 root posts and
     1 threaded reply, with an attachment uploaded and the follow-up quote stripped.
  4. Archive → footer shows Archived and the post remains. Trash → post deleted.
  5. `ExpireHistory` + new message → appears via resync with no duplicates.
  6. `RateLimitNext(2, "userRateLimitExceeded")` → sync still succeeds.
  7. `RevokeGrant` → account `NeedsReauth`, a single bot notice, skipped by the scheduler.
  8. Outlook and Gmail connected for the same user → two channels, no cross-posting.

**Tests:** as listed.

**Done when:** `make test` passes locally with Docker and in CI.

## Integration & manual test plan

Run on Mattermost 11.7 ESR with the spike-1 Workspace project and a real Gmail mailbox.

| # | Scenario | Expected |
|---|---|---|
| 1 | `/inbox setup` with correct and wrong secret | "credentials OK" / "client ID or secret is wrong" |
| 2 | `/inbox connect gmail`, consent | Channel `inbox-{user}-gmail`, welcome post |
| 3 | Send a plain-text mail, an HTML mail with 2 attachments (one > limit), a non-UTF-8 mail | Posts render correctly; oversized attachment note with Gmail link |
| 4 | Reply from another client into an existing thread | Threaded reply, quoted history stripped |
| 5 | Archive in Gmail; move back to Inbox | Footer Archived; then back to Inbox, no duplicate post |
| 6 | Mark read/unread in Gmail | Footer state follows |
| 7 | Trash; delete forever (another message) | Posts removed |
| 8 | Message filtered to skip inbox | Not posted |
| 9 | Disconnect, reconnect | Same channel reused; new mail continues existing threads |
| 10 | Revoke app access at myaccount.google.com | Reauth notice once; `/inbox status` shows reconnect needed |
| 11 | Leave plugin disabled > 1 week (or simulate via mock) | Resync on enable; no lost or duplicate posts |
| 12 | Gmail email post on web, desktop, mobile | No Reply/Actions buttons; "Open in Gmail" opens the right message |

## PR checklist

- [ ] Spike results recorded below; tasks adjusted where spikes contradicted assumptions.
- [ ] Binary size delta per target recorded in the PR description.
- [ ] No new code comments beyond non-obvious rationale (repo/global style).
- [ ] `make check-style` and `make test` green; integration tests green in CI.
- [ ] `plugin.json` settings reviewed in System Console; `server/manifest.go` regenerated.
- [ ] Gmail compose/actions visibly absent, not broken, for Gmail accounts.
- [ ] Outlook regression: Outlook integration tests unchanged and passing.
- [ ] `CLAUDE.md` architecture section lists `server/mail/gmail`.
- [ ] Real-mailbox manual plan (table above) executed and results noted in the PR.

## Spike results

_To be filled in during the spikes._
