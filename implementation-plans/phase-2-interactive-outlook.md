# Phase 2 — Interactive Email Posts (Outlook)

> Spec: [inbox-v2.md](inbox-v2.md) · Depends on: Phase 1 · Estimate: 5–6 days · PRs: **2a** compose core + send (P2.1–P2.5), **2b** dialog path (P2.6–P2.8), **2c** thread prompt, state actions, cleanup (P2.9–P2.12)

## Outcome

Every Outlook email post shows **Reply**, **Reply all**, **Forward** and an **Actions ▾** menu
(Mark read / Mark unread, Archive), a state footer (`Unread · Inbox`) and an "Open in Outlook"
link. The buttons open a dialog with To/Cc/Bcc address chips (autocomplete) and a message box.
Replying directly in a thread shows a prompt with the recipients and Send reply / Send reply all /
Edit recipients… / Don't send, or sends straight away when the account's `AutoSend` is set. Outgoing
mail uses Graph drafts (native quoting and threading), carries the Mattermost post's files, and is
never re-imported. Read/archive state stays in sync in both directions.

## Out of scope

- Gmail (Phases 3–4).
- Attachments in the dialog (file-upload element, server 11.10+).
- Showing replies sent from other clients.
- Multiple accounts per provider.

## Verification spikes (do first)

Do these in **P2.1** before building the compose UI. Record the findings at the top of
`server/compose/dialog.go` only where they change behaviour; everything else goes in the PR
description.

1. **Lookup payload shape.** The public model (`server/public@v0.4.0/model/integration_action.go`)
   defines `LookupDialogResponse{Items []DialogSelectOption}` and `IsValidLookupURL` (accepts
   `/plugins/...`), but no lookup *request* type. **Verify:** read the server's interactive-dialog
   lookup handler (`server/channels/app/integration_action.go`, `LookupInteractiveDialog` or
   similar) for the POST body. Expected: `SubmitDialogRequest` with `type` set to a lookup marker,
   `submission` holding the field name and typed query, and `state`, `user_id`, `channel_id`.
   Also confirm whether the server forwards `Mattermost-User-ID` to plugin lookup URLs.
2. **Dynamic multiselect behaviour** on 11.7.x for web, desktop and mobile (current App Store and
   Play builds):
   - pre-filled comma-joined `default` renders as chips (11.6 fix);
   - defaults absent from later lookup results stay selected;
   - an echoed free-text option (typed address returned as an item) can be selected;
   - submitted value arrives as `[]any` or a comma string in `Submission`;
   - an empty lookup query is sent when the field opens.
3. **Select-action context.** **Verify** that interactive-message `select` actions deliver the chosen
   value as `Context["selected_option"]` (`PostActionOptions.SelectedOption` exists in the model).
4. **Graph draft semantics** against a real tenant with `Prefer: IdType="ImmutableId"`:
   - `createReply` / `createReplyAll` / `createForward` return a draft with `internetMessageId`
     already populated;
   - the draft's immutable ID survives `send`;
   - the draft body contains the quoted original as HTML that can be prefixed.

**Fallback if mobile fails item 2:** `dialogMode = textarea`. To/Cc/Bcc become `textarea` elements
(max 3000) with comma- or newline-separated addresses, parsed on submit with
`net/mail.ParseAddressList`, and field-level `Errors` on failure. The builder takes a
`RecipientFieldStyle` parameter so the decision is one constant. Selecting per client isn't possible
(the dialog is built before the client is known), so the fallback applies to all clients.

## Tasks

### P2.1 Spike: dynamic multiselect dialog prototype  *(PR 2a, throwaway code not merged)*
**Files:** scratch branch only — `server/api.go` (temporary route), `server/compose/spike.go`.
**Steps:**
1. Add a temporary `/inbox spike-dialog` subcommand. It needs a `TriggerId`, so use a bot post with
   a button whose action calls `OpenInteractiveDialog`. Fields: To/Cc/Bcc as `select`,
   `multiselect: true`, `data_source: "dynamic"`,
   `data_source_url: "/plugins/com.mattermost.plugin-inbox/api/v1/compose/lookup"`, defaults
   `"alice@x.com,bob@y.com"`; Message as `textarea` max 3000.
2. The lookup handler logs the raw body and headers and returns
   `LookupDialogResponse{Items: echo(query) + 3 fixed contacts}`.
3. Run every check in "Verification spikes" items 1–3 on web, desktop and mobile against an 11.7 ESR
   server.
**Tests:** none (manual).
**Done when:** the lookup payload shape is written up; a multiselect-vs-textarea decision is made;
the select-action `selected_option` key is confirmed.

### P2.2 Scope change to `Mail.ReadWrite` and re-consent  *(PR 2a)*
**Files:** `server/mail/outlook/provider.go` (scopes from Phase 1), `server/store/kvstore/types.go`
(`Account`), `server/oauth.go` (Phase 1 OAuth completion, P1.10),
`server/command/command.go`, `plugin.json` footer.
**Steps:**
1. Scopes become `User.Read Mail.ReadWrite Mail.Send offline_access`.
2. Add `Account.GrantedScopes []string`, filled from `token.Extra("scope")` at OAuth completion and
   on every token refresh in the persisting token source.
3. `Account.NeedsScopeUpgrade()` is true when a required scope is missing. Compose and state actions
   check it before calling Graph.
4. When an upgrade is needed: post one bot message in the account channel
   ("Reconnect to enable Reply/Archive: `/inbox connect outlook`"), deduplicated with
   `Account.ScopeNoticeSentAt`. Show it in `/inbox status`. Actions respond with an ephemeral
   explanation and a connect link.
5. Reconnect reuses the account through `RemoteBinding` (Phase 1), so this only re-consents.
**Tests:** `server/mail/outlook/provider_test.go` (scope list);
`server/store/kvstore/types_test.go` (`NeedsScopeUpgrade` for missing, superset and
case-insensitive scope strings).
**Done when:** an account connected with Phase 1 scopes gets the notice once; reconnecting clears it.

### P2.3 Neutral compose types and recipient computation  *(PR 2a)*
**Files:** `server/mail/types.go` (changed), new `server/compose/recipients.go`,
new `server/compose/recipients_test.go`.
**Steps:**
1. In `mail`, add `ComposeMode` (`ModeReply`, `ModeReplyAll`, `ModeForward`), `Outgoing`,
   `OutgoingAttachment{Name, ContentType string; Data []byte}`,
   `SentInfo{MessageID, InternetMessageID string}`, and `Message.ReplyTo []Address` if Phase 1
   omitted it.
2. `compose.Recipients(mode ComposeMode, orig *mail.Message, self string) (to, cc []mail.Address)`,
   implementing the spec rules:
   - Reply → Reply-To ∥ From; if the original is from `self`, its original To.
   - Reply all → (Reply-To ∥ From) + To; Cc = original Cc.
   - Forward → empty.
   - Remove `self`; dedupe case-insensitively on address, keeping the first non-empty display name.
3. `compose.FormatAddresses([]mail.Address) string` for prompts and footers ("Alice, Bob +2").
**Tests:** `recipients_test.go`, table-driven: Reply with and without Reply-To; Reply where the
original is from self; Reply all with self in To and in Cc; duplicate addresses with differing case;
empty From; Forward; display-name retention.
**Done when:** the table passes; there's no Mattermost or Graph dependency in the package.

### P2.4 msgraph client additions  *(PR 2a)*
**Files:** `server/msgraph/client.go`, `server/msgraph/types.go`, `server/msgraph/errors.go`,
new `server/msgraph/compose.go`, new `server/msgraph/compose_test.go`.
**Steps:**
1. Types: `DraftMessage` (id, internetMessageId, body, toRecipients, ccRecipients, bccRecipients),
   `FileAttachment` (`@odata.type: #microsoft.graph.fileAttachment`, name, contentType, contentBytes),
   `UploadSession{UploadURL string; ExpirationDateTime time.Time}`, `AttachmentItem`
   (attachmentType `file`, name, size, contentType).
2. Methods (all send `Prefer: IdType="ImmutableId"` — the Phase 1 `doJSON` already does):
   - `CreateReplyDraft(ctx, id)` → `POST /me/messages/{id}/createReply`
   - `CreateReplyAllDraft(ctx, id)` → `.../createReplyAll`
   - `CreateForwardDraft(ctx, id)` → `.../createForward`
   - `UpdateDraft(ctx, id, patch DraftPatch)` → `PATCH /me/messages/{id}` (recipients, body)
   - `AddFileAttachment(ctx, id, att)` → `POST /me/messages/{id}/attachments` (≤ 3 MB)
   - `CreateUploadSession(ctx, id, item)` → `POST /me/messages/{id}/attachments/createUploadSession`
   - `UploadChunks(ctx, session, data)` → `PUT` ranges of ≤ 4 MiB aligned to 320 KiB, **without**
     the bearer token (the upload URL is pre-authenticated)
   - `GetDraft(ctx, id)` → `GET /me/messages/{id}?$select=id,internetMessageId`
   - `SendDraft(ctx, id)` → `POST /me/messages/{id}/send`
   - `DeleteMessage(ctx, id)` → cleans up the draft on failure
   - `SetRead(ctx, id, read)` → `PATCH {isRead}`
   - `Move(ctx, id, destination)` → `POST /me/messages/{id}/move {destinationId:"archive"}`, returns
     the moved message.
3. `doJSON` gains a raw-body variant for upload chunks (no JSON, `Content-Range` header). Keep the
   429 retry logic in it.
4. Remove `ReplyToMessage` once P2.5 lands (it's still used by the Phase 1 flow until P2.9).
**Tests:** `compose_test.go` against `httptest`:
- correct method, path and Prefer header per call;
- PATCH body omits nil fields;
- upload chunking boundaries for 0, 1 B, exactly 320 KiB×n and 9 MB payloads; `Content-Range`
  values; no `Authorization` on upload PUTs;
- 429 retry on `SendDraft`.
**Done when:** all methods exist, are covered, and lint passes.

### P2.5 Outlook `Send`, `SetRead`, `Archive`, `WebLink`  *(PR 2a)*
**Files:** `server/mail/outlook/client.go`, new `server/mail/outlook/send.go`,
new `server/mail/outlook/send_test.go`, `server/testhelper/mock_graph.go`.
**Steps:**
1. `Send(ctx, out *mail.Outgoing) (*mail.SentInfo, error)`:
   1. Create the draft by mode.
   2. PATCH recipients (`To`/`Cc`/`Bcc` replace the draft's computed ones). Set body HTML to
      `out.HTML + "<br>" + draft.Body.Content` so the native quote is kept.
   3. Attachments: ≤ 3 MB → `AddFileAttachment`; larger → upload session; reject if the sum exceeds
      `MaxAttachmentSizeMB` or 150 MB (Graph limit), **before** creating the draft.
   4. `GetDraft` for `internetMessageId`, then `SendDraft`.
   5. On any error after draft creation, `DeleteMessage(draft)` (best effort) and return a wrapped
      error.
   6. Return `SentInfo{MessageID: draft.ID, InternetMessageID}`.
2. `SetRead` and `Archive` wrap the msgraph calls. `Archive` maps a 404 to `mail.ErrNotFound`.
3. `WebLink(msg)` returns Graph `webLink`. Add `webLink` to `$select` in the Phase 1 delta and
   `GetMessage` field lists if missing.
4. `mock_graph.go`:
   - handlers for createReply/All/Forward (draft with quoted body and generated
     `internetMessageId`), PATCH message, attachments, createUploadSession plus an upload URL
     handler, send (moves the draft to an in-memory "sent" list), move to `archive`, PATCH isRead;
   - recorders `SentMessages()`, `Drafts()`, `Moves()`;
   - a failure-injection hook `FailNext(method, pathPrefix, status)`.
**Tests:** `send_test.go` using `mock_graph`:
- each mode produces the right recipients and body prefix;
- small attachment, large attachment, oversize rejected before any Graph call;
- failure mid-way deletes the draft;
- `SentInfo.InternetMessageID` matches the draft.
**Done when:** `mail.Client` for Outlook implements `Send`, `SetRead`, `Archive` and `WebLink`, with
tests green. *PR 2a ends here; nothing user-visible changes yet.*

### P2.6 Drafts store and contact cache  *(PR 2b)*
**Files:** `server/store/kvstore/kvstore.go`, new `server/store/kvstore/compose_store.go`,
new `server/store/kvstore/compose_store_test.go`, `server/store/kvstore/types.go`.
**Steps:**
1. Add the `Draft` type as in the spec, plus `CreatedAt`, `Style` (`dialog` | `prompt`) and
   `IncludeMessage bool`.
2. `StoreDraft(d)` → `draft_{id}` with `pluginapi.SetExpiry(3600)`; `GetDraft(id)`; `DeleteDraft(id)`.
   `id = model.NewId()`.
3. `compose.LoadOwnedDraft(store, id, userID)` returns `ErrDraftNotFound` or `ErrForbidden` when
   `UserID` differs. Every handler goes through it.
4. Add the `ContactEntry{Address, Name string; LastSeen int64; Count int}` type.
   `contacts_{accountID}` is updated with `SetAtomicWithRetries`: `TouchContacts(accountID,
   []mail.Address)` upserts, sorts by `LastSeen`, and caps at 500.
5. Call `TouchContacts` from the sync engine's Created path (From/To/Cc/Reply-To minus self) and
   after a successful send (all recipients).
**Tests:** `compose_store_test.go` (plugintest API mock): TTL option passed; ownership mismatch;
contact upsert merges names, caps at 500 evicting the oldest, and is case-insensitive.
**Done when:** drafts and contacts persist with the expected keys and limits.

### P2.7 Dialog builders and lookup endpoint  *(PR 2b)*
**Files:** new `server/compose/dialog.go`, new `server/compose/dialog_test.go`,
new `server/compose/lookup.go`, new `server/compose/lookup_test.go`, `server/api.go`.
**Steps:**
1. `BuildComposeDialog(d *Draft, style RecipientFieldStyle, siteURL string) model.Dialog`:
   - `CallbackId: "compose"`, `State: d.ID`, `NotifyOnCancel: true`.
   - Title "Reply" / "Reply all" / "Forward" / "Edit recipients" (≤ 24).
   - `SubmitLabel: "Send"`, `URL` = `/plugins/com.mattermost.plugin-inbox/api/v1/compose/submit`.
   - Elements `to`, `cc`, `bcc` (`bcc` and `cc` optional), plus `message` (`textarea`, max 3000;
     only when `IncludeMessage`; required unless Forward).
   - Defaults are comma-joined bare addresses truncated to 3000 bytes. If they would overflow, keep
     what fits and add `HelpText` "+N more recipients kept" (≤ 150). The overflow stays in the draft
     and is merged on submit.
2. `ValidateDialog(dlg)` calls `dlg.IsValid()` and is used in tests and debug builds.
3. `POST /api/v1/compose/lookup` (authenticated subrouter):
   - parse per the spike result; load the draft from `state` with the ownership check;
   - query = typed text; items = the echo of the query if it parses as an address
     (`mail.ParseAddress`), then `contacts_` prefix matches on address or name (case-insensitive,
     max 20), then the draft's current values for that field;
   - respond with `LookupDialogResponse`.
   - `Text` is `"Name <addr>"` truncated to 150; `Value` is the bare address.
4. Textarea style: same builder, `type: "textarea"`, no `data_source`.
**Tests:**
- `dialog_test.go`: every mode × style passes `IsValid`; a 200-recipient default truncates within
  3000 with an overflow note; title lengths; `message` element presence by `IncludeMessage`.
- `lookup_test.go`: echo valid and invalid input, prefix matching, cap 20, foreign draft → 403.
**Done when:** a dialog opened from a test button validates on a real server (spike harness) and the
lookup returns contacts.

### P2.8 Email post actions and dialog submit  *(PR 2b)*
**Files:** `server/email/renderer.go`, new
`server/email/actions.go`, new `server/email/actions_test.go`, new `server/compose/service.go`,
new `server/compose/service_test.go`, `server/api.go`, `server/sync/sync.go`.
**Steps:**
1. `email.BuildEmailAttachment(acctID, msgID string, state mail.MessageState, actionURL string)
   *model.SlackAttachment`:
   - buttons `reply`, `reply_all`, `forward`;
   - a `select` named `actions` with options `mark_read` or `mark_unread` (by state) and `archive`
     (omitted when archived);
   - footer `Unread · Inbox` / `Read · Archived`;
   - every integration `Context` holds only `{"op", "account_id", "message_id"}`.
2. The sync engine attaches it on create and appends `[Open in Outlook](webLink)` to the body. Store
   the initial state in `MessageMapping.State`.
3. `POST /api/v1/action/email`:
   1. Decode `PostActionIntegrationRequest`.
   2. Load the account; require `account.MattermostUserID == req.UserId` and
      `!NeedsScopeUpgrade()`.
   3. Verify `req.PostId` is a bot post whose `inbox_message_id` prop equals `message_id`.
   4. Dispatch:
      - Reply / Reply all / Forward → fetch the original via `GetMessage`, compute recipients, store
        the draft (`IncludeMessage: true`), `OpenInteractiveDialog{TriggerId: req.TriggerId}`,
        respond `{}`;
      - `actions` → read `Context["selected_option"]` and hand off to P2.11.
4. `compose.Service.SubmitDialog(req SubmitDialogRequest) *model.SubmitDialogResponse`:
   1. On `Cancelled` → delete the draft.
   2. Load the owned draft; parse `to`/`cc`/`bcc` (multiselect `[]any` or a string) and merge the
      overflow.
   3. Validate: at least one To for Reply/Reply all/Forward; each address parses; field `Errors`
      otherwise.
   4. Render HTML with `email.MarkdownToHTML`; Text is the raw markdown.
   5. `client.Send`.
   6. `MarkMessageAsSent(accountID, internetMessageID)` (24h TTL, `sent_` key from Phase 1).
   7. `TouchContacts`; delete the draft.
   8. Create a post **as the user** (`UserId: draft.UserID`, `RootId: draft.RootPostID`) with the
      message, prop `inbox_outgoing: true`, and an attachment footer
      "📤 Sent to Alice, Bob · cc Carol".
   9. Return an empty response.
   - On send error → `SubmitDialogResponse{Error: "Couldn't send: …"}` and keep the draft so the
     user can retry.
5. Route `POST /api/v1/compose/submit` on the authenticated subrouter.
6. Leave the Phase 1 thread-reply flow untouched in this PR.
**Tests:**
- `actions_test.go`: attachment JSON passes `PostAction` validation; context has no extra fields;
  footer per state.
- `service_test.go` (fake `mail.Client` + plugintest):
  - foreign account → ephemeral error, no dialog;
  - non-bot post props are ignored;
  - submit with an invalid Cc → field error;
  - successful submit marks sent, posts as the user with `inbox_outgoing`, deletes the draft;
  - send failure keeps the draft;
  - cancel deletes the draft.
**Done when:** Reply all from a button sends a correctly addressed email via mock Graph and the
thread shows the user's message with the sent footer. *PR 2b ends here.*

### P2.9 Thread prompt replaces the confirm flow  *(PR 2c)*
**Files:** `server/hooks.go`, new `server/compose/prompt.go`, new `server/compose/prompt_test.go`,
`server/api.go`.
**Steps:**
1. `MessageHasBeenPosted` returns early when:
   - the post is by the bot, or has `inbox_outgoing` or `from_webhook`, or has no `RootId`;
   - the root post isn't a bot post with `inbox_account_id`;
   - the account owner isn't `post.UserId`, or the channel doesn't match.
2. Target = `ConversationMapping.LastInboundMessageID`. If empty, fall back to the root's
   `inbox_message_id`.
3. `AutoSend` `reply` / `reply_all` → `Service.SendFromPost(draftFor(mode), post)` in a goroutine,
   then post a bot reply "📤 Sent to …" (or an ephemeral error).
4. Otherwise store a `Draft` (`Style: prompt`, `SourcePostID`, recipients for Reply, and keep the
   Reply-all extras for the label). Create the bot prompt post in the thread:
   - text "Send as email? To: Alice · Reply all adds: Bob, Carol";
   - buttons `send_reply`, `send_reply_all`, `edit` ("Edit recipients…"), `discard`
     ("Don't send");
   - a select `always` with "Always send replies" / "Always send reply all";
   - contexts hold only `{"op", "draft_id"}`.
   Save `PromptPostID` in the draft.
5. `POST /api/v1/action/prompt`, ownership via `LoadOwnedDraft`:
   - `send_reply` / `send_reply_all` → `SendFromPost`;
   - `edit` → `OpenInteractiveDialog` with `IncludeMessage: false`; submit routes to `SendFromPost`
     because `SourcePostID` is set;
   - `discard` → delete the draft and update the prompt to "Not sent.";
   - `always` → `UpdateAccount(AutoSend=…)`, then send.
   - Responses update the prompt post in place: "📤 Sent to …", no actions.
6. `SendFromPost`:
   1. Re-read the source post (`GetPost`) to pick up edits; abort if it was deleted.
   2. Load its `FileIds` through `client.File.Get` and `GetInfo` into `OutgoingAttachment`s.
   3. Send, mark sent, touch contacts.
   4. Draft expiry (1h) leaves the prompt stale. Clicking it then responds
      "This prompt expired; reply again."
7. Delete the old confirm code, `ReplyToMessage` and the `outlook_message_id` context keys.
**Tests:** `prompt_test.go`:
- every early-return condition;
- AutoSend off → prompt created with the correct label;
- AutoSend reply_all → sends without a prompt;
- edit → dialog without the message element;
- `always` sets `AutoSend`;
- expired draft → message;
- source post edited before Send → the edited text is sent;
- attachments included.
**Done when:** typing in a thread produces the prompt, and each button behaves as specified.

### P2.10 `/inbox settings`  *(PR 2c)*
**Files:** `server/command/command.go`, `server/command/command_test.go`, `server/plugin.go` (Deps
wiring), regenerate `server/command/mocks/` with `make mock`.
**Steps:**
1. Add the subcommand `settings [account] autosend <off|reply|reply_all>`, with autocomplete static
   options for the value.
2. Add `Deps.SetAutoSend(userID, channelID, selector, mode string) error`, using the Phase 1 account
   resolver.
3. With no arguments, show current settings per account.
4. Mention it in `/inbox help`.
**Tests:** `command_test.go`: valid modes, invalid mode, selector inferred from channel, no args
shows settings.
**Done when:** the setting round-trips and the thread prompt honours it.

### P2.11 Read/unread and archive, both directions  *(PR 2c)*
**Files:** `server/compose/service.go` (or new `server/actions/state.go`; keep it in `compose` unless
it grows), `server/sync/sync.go`, `server/email/actions.go`.
**Steps:**
1. Actions select `mark_read` / `mark_unread` → `client.SetRead`; `archive` → `client.Archive`.
2. On success, compute the new state and update `MessageMapping.State` via a Phase 1
   `UpdateMessageMapping`. Respond with `PostActionIntegrationResponse{Update: post}` carrying the
   re-rendered attachment, so the change is optimistic and instant.
3. On `mail.ErrNotFound` → ephemeral "This email no longer exists in Outlook" and remove the actions.
4. Sync engine `StateChanged` / `Archived`: if the new state differs from `MessageMapping.State`,
   re-render the attachment via `UpdatePost` (bot post) and store the state. Same-state echoes from
   our own action are no-ops.
5. The archived footer reads "Read · Archived"; Reply buttons remain.
**Tests:** `service_test.go` / `sync_test.go`:
- mark read → Graph PATCH plus post updated;
- echo StateChanged with the same state → no `UpdatePost`;
- external unread → footer flips;
- archive → move call, footer updated, post kept;
- 404 → actions removed.
**Done when:** state changes from Mattermost reach Outlook, and changes in Outlook update the post
within one sync.

### P2.12 Loop prevention and cleanup  *(PR 2c)*
**Files:** `server/sync/sync.go`, `server/store/kvstore/email_store.go`, `CLAUDE.md` (flows section),
`README.md` (user docs: buttons, prompt, settings).
**Steps:**
1. In the Created path, skip messages whose `InternetMessageID` has a `sent_` entry. This covers
   reply-all to a list the user is on. Confirm the Phase 1 `GetMessage` and delta `$select` include
   `internetMessageId`.
2. Remove any remaining old `post_` / `outlook_*` references found with grep.
3. Update the docs.
**Tests:** `sync_test.go`: an inbound copy of a sent message is skipped; an unrelated message with
the same thread is processed.
**Done when:** no duplicate post appears after reply-all to a list containing self (mock + manual).

## Integration & manual test plan

Integration (in-repo `server/testhelper`, plugin container → `mock_graph` via host access,
`DeveloperEndpoints` set, `EnableDeveloper=true`). The OAuth flow is driven through the mock's
authorize/token endpoints.

1. Connect an account; inject a 3-message conversation through mock delta. Assert the root post has
   the attachment actions and footer.
2. POST `/api/v1/action/email` (reply_all) as the owner → 200, dialog opened (**Verify:** the
   container has no client to receive the dialog, so call `compose/submit` directly using the draft
   ID fetched through a test-only KV read or the returned draft).
   - Submit → the mock records the draft PATCH with the expected To/Cc and send.
   - The thread has the user post with `inbox_outgoing`.
3. The same action as another user → 403 / ephemeral; no draft created.
4. Thread reply with an attached file (AutoSend off) → prompt post; click `send_reply` → the mock
   records a file attachment; the prompt is updated.
5. AutoSend `reply_all` via `/inbox settings` → a thread reply sends immediately.
6. Mark read → mock PATCH; the mock then emits a delta update `isRead=true` → no extra `UpdatePost`
   (count edits).
7. Archive → mock move; delta `@removed` → the post is kept with the Archived footer.
8. Mock delta delivers an inbox copy of a sent message → no new post.
9. Account with old scopes → action returns the reconnect message; the channel gets one notice.

Manual (real tenant, 11.7 ESR; web, desktop, iOS, Android):
- Dialog chips, autocomplete from contacts, free-text address, Cc/Bcc optional, 3000-char limit
  message.
- Reply all removes self; Forward to a new address; a reply with a 5 MB attachment (upload session).
- The sent email in Outlook shows the native quote and correct threading.
- Read/unread and archive from Outlook Web reflect in Mattermost and vice versa.
- An expired prompt (wait over 1h, or shorten the TTL in a debug build) shows the expiry message.

## PR checklist

- [ ] Spike findings recorded in the PR 2a description; dialog style decided.
- [ ] `make check-style` and `make test` pass; `make mock` regenerated when `Deps` changed.
- [ ] Every action/dialog handler goes through an ownership check (account or `LoadOwnedDraft`);
      props are trusted only on bot-authored posts.
- [ ] Every dialog built in tests passes `Dialog.IsValid`.
- [ ] No Graph IDs or recipient lists in action contexts or dialog `state` — only opaque IDs.
- [ ] Draft cleanup on send failure (Graph) and on cancel (KV).
- [ ] `sent_` recorded before returning success.
- [ ] README/CLAUDE.md updated (PR 2c).
- [ ] Manual matrix run on mobile before merging PR 2b.
