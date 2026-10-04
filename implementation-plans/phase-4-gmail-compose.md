# Phase 4 — Gmail Compose and Actions
> Spec: [inbox-v2.md](inbox-v2.md) · Depends on: Phases 1, 2, 3 · Estimate: 2–3 days · PRs: 4a (MIME composer), 4b (Send/actions wiring + UI enablement)

## Outcome

Gmail accounts get the full Phase 2 experience: **Reply**, **Reply all**, **Forward** (dialog and
thread paths), **Mark read/unread** and **Archive**, with the same behaviour as Outlook. Outgoing
mail threads correctly in Gmail and in third-party clients, quotes the original, carries Mattermost
file attachments, and never echoes back into the channel.

## Out of scope

- Gmail push (Phase 5).
- Sending as a Gmail alias (`settings.sendAs`), which would need an extra scope. Mail is always sent
  as the account's default identity.
- Attachments in the compose dialog (file-upload element, server 11.10+; listed under "Later").
- Drafts saved to the Gmail Drafts folder. Sends are one-shot.

## Verification spikes (do first)

Run these against a real Google Workspace account with an Internal OAuth app (`gmail.modify` +
`gmail.send`), using a throwaway Go program under `server/mail/gmail/internal/spike/` (deleted
before merge) or `go test -run Spike -tags spike`.

1. **Threading.** Send a reply built by the prototype composer with `threadId` + `In-Reply-To` +
   `References` + `Re:` subject. Confirm:
   - it joins the thread in Gmail web (sender and recipient side);
   - it threads in Outlook (desktop or OWA) as the recipient, which relies only on the
     References/In-Reply-To headers;
   - Forward (`Fwd:` subject, same `threadId`) also stays in the thread in Gmail web.
2. **Message-ID preservation.** Send with a generated `Message-ID`, then
   `messages.get(format=metadata, metadataHeaders=Message-ID)`. Record whether Gmail keeps or
   rewrites it. The implementation reads it back either way (P4.3 step 5); the spike decides whether
   the read-back is required or only a safety net.
3. **From omission.** Omit the `From` header and confirm Gmail fills in the default send-as identity
   *with* its display name. If it doesn't, set `From` from the account's sendAs display name (that
   needs `gmail.settings.basic`; prefer to avoid it).
4. **Bcc.** Confirm a `Bcc` header in `raw` is delivered and stripped from recipients' copies.
5. **Media upload.** **Verify:** `UsersMessagesSendCall.Media(r io.Reader, ...)` exists in
   `google.golang.org/api/gmail/v1` and honours `threadId` in the metadata part. Send a ~20 MB
   message through it. If it works and threading is intact, use media upload for *all* sends (one
   code path). Otherwise use JSON `raw` up to 4 MB and media above that.
6. **Self-recipient echo.** Reply-all where the user adds their own address. Inspect the history
   record: one message with `SENT`+`INBOX` labels, or two messages? This drives the loop-prevention
   test in P4.5.

Write the findings at the top of the PR 4a description. Any "no" answer changes the task below that
depends on it.

## Tasks

### P4.1 MIME composer
**Files:** `server/mail/gmail/compose.go`, `server/mail/gmail/compose_test.go`,
`server/mail/gmail/testdata/compose/*.eml`, `server/mail/gmail/quote.go`

**Steps:**
1. `type composer struct { now func() time.Time; boundary func() string; messageID func() string }`
   with a production constructor (`crypto/rand` boundaries, Message-ID
   `<inbox.{model.NewId()}@{account domain}>`). Tests inject fixed values for golden output.
2. `func (c *composer) Build(in composeInput) ([]byte, error)`. `composeInput` holds:
   - mode;
   - the original `*mail.Message` (needs `InternetMessageID`, `References`, `Subject`, `From`, `To`,
     `Cc`, `Date`, `HTML`, `Text`, `Attachments`);
   - To/Cc/Bcc;
   - user HTML and text;
   - outgoing attachments;
   - original attachment bytes (Forward) and inline parts.
3. MIME tree (omit any layer that has no children):
   ```
   multipart/mixed
   ├─ multipart/related            (only if quoted HTML references cid: parts)
   │  ├─ multipart/alternative
   │  │  ├─ text/plain; charset=UTF-8   (quoted-printable)
   │  │  └─ text/html;  charset=UTF-8   (quoted-printable)
   │  └─ inline parts (Content-ID preserved, Content-Disposition: inline)
   └─ attachments (base64, 76-col lines, Content-Disposition: attachment)
   ```
   Use `mime/multipart.Writer` with injected boundaries, `mime/quotedprintable`, `encoding/base64`.
4. Headers, in a fixed order:
   - `Date` (RFC 1123Z), `Message-ID`, `Subject`, `To`, `Cc`, `Bcc`, `MIME-Version: 1.0`;
   - for Reply/Reply all/Forward: `In-Reply-To: <orig>` and
     `References: <orig References…> <orig Message-ID>`. Keep the last 20 IDs and fold at 78 columns
     with CRLF + space;
   - `From` omitted, pending spike 3.
5. Subject: `Re: ` / `Fwd: ` added unless already present (case-insensitive, also matching
   `RE:`, `FW:`, `Fw:`). Encode non-ASCII with `mime.QEncoding.Encode("utf-8", s)`.
6. Addresses: format with `(&mail.Address{Name, Address}).String()`, which RFC 2047-encodes names.
   Re-validate every address with `mail.ParseAddress` and fail on error.
7. **Header injection.** Before any header is written, strip `\r`, `\n` and other control characters
   from every value (subject, names, filenames). One `sanitizeHeader` helper with a dedicated test.
8. Attachment filenames: `mime.FormatMediaType("attachment", map[string]string{"filename": name})`
   (RFC 2231 for non-ASCII). Content-Type is the original's or `mime.TypeByExtension`, falling back
   to `application/octet-stream`.
9. Quoting (`quote.go`):
   - **Reply/Reply all, HTML:** user HTML +
     `<div class="gmail_quote"><div class="gmail_attr">On {date}, {name} &lt;{addr}&gt; wrote:<br></div>`
     `<blockquote class="gmail_quote" style="margin:0 0 0 .8ex;border-left:1px #ccc solid;padding-left:1ex">`
     `{original body inner HTML}</blockquote></div>`. Extract the `<body>` content with
     `golang.org/x/net/html`, and drop `<script>` and `<style>` from the quoted copy.
   - **Reply/Reply all, text:** user text + `\n\nOn {date}, {from} wrote:\n` + the original text with
     each line prefixed `> `. When the original has only HTML, use the Phase 3 HTML→text helper.
   - **Forward:** user text, then a `---------- Forwarded message ---------` block (From, Date,
     Subject, To, Cc), then the original body unquoted (Gmail style).
10. Output: raw bytes with CRLF line endings throughout. Base64url encoding happens in the client,
    not here.

**Tests:**
- Golden files with an `-update` flag: `reply_plain.eml`, `reply_all_cc_bcc.eml`,
  `reply_with_attachments.eml`, `forward_with_attachments.eml`, `forward_inline_images.eml`,
  `non_ascii_headers.eml`, `long_references.eml`.
- Structural assertions (independent of goldens): parse with `net/mail.ReadMessage` +
  `mime/multipart`, check part tree, content types, `In-Reply-To`/`References`, decoded subject.
- Table tests for subject prefixing and `sanitizeHeader`.
- `FuzzBuildHeaders`: arbitrary subject/name/filename strings never produce a bare CR or LF inside a
  header value, and output always parses.
- HTML quote extraction: full documents, fragments, malformed HTML.

**Done when:** all goldens are stable, `make check-style` passes, and the composer has no network or
Mattermost dependencies.

### P4.2 Original-message data needed for replies
**Files:** `server/mail/types.go`, `server/mail/gmail/mime.go` (Phase 3 walker),
`server/mail/gmail/client.go`

**Steps:**
1. Confirm `mail.Message` exposes `InternetMessageID`, `References []string`, `ReplyTo`, `HTML`,
   `Text`, and attachments including inline ones (`Inline`, `ContentID`). Add whatever Phase 3
   didn't populate. The Phase 3 walker skips inline parts for *rendering*; it must still list them
   with their attachment IDs.
2. Parse `References` by splitting on whitespace and keeping `<…>` tokens.

**Tests:** extend the Phase 3 MIME walker fixtures with assertions for the new fields.

**Done when:** `GetMessage` returns everything P4.1 needs, using the existing walker fixtures.

### P4.3 `Client.Send` for Gmail
**Files:** `server/mail/gmail/client.go`, `server/mail/gmail/send.go`,
`server/mail/gmail/send_test.go`

**Steps:**
1. Replace the Phase 3 stub. Load the original with `GetMessage(out.TargetMessageID)`.
2. Fetch bytes with `GetAttachment`:
   - **Forward:** every non-inline original attachment;
   - **all modes:** inline parts whose `cid:` is referenced in the quoted HTML.
3. **Size budget.** Estimate the encoded size (bodies + Σ attachment bytes × 4/3 + 10% headroom) and
   reject over 35 MB with `mail.ErrTooLarge`. The compose service turns that into "Attachments are
   too large for Gmail (about 25 MB of files max)." Each outgoing file is also capped by
   `MaxAttachmentSizeMB` (Phase 2 check, reused).
4. Build with the composer. Send per spike 5:
   - **media path:** `Users.Messages.Send("me", &gmail.Message{ThreadId: orig.ThreadID}).Media(bytes.NewReader(raw), googleapi.ContentType("message/rfc822"))`;
   - **raw path:** `&gmail.Message{Raw: base64.URLEncoding.EncodeToString(raw), ThreadId: …}`.
   Forward also sets `ThreadId` (per spike 1).
5. Read back `Message-ID` via `messages.get(id, format=metadata, metadataHeaders=Message-ID)`. Return
   `SentInfo{MessageID: resp.Id, InternetMessageID: readBack}`. Skip the read-back if spike 2 shows
   Gmail always preserves the header.
6. Map errors:
   - `400 invalidArgument` → `mail.ErrInvalidRecipient` (user-facing message);
   - `413` / size errors → `mail.ErrTooLarge`;
   - `401` / `invalid_grant` → `ErrReauthRequired`;
   - rate limits → the Phase 3 backoff wrapper.

**Tests:** `send_test.go` against `mock_gmail.go`:
- Reply, Reply all and Forward produce the right `threadId` and headers;
- Forward includes original attachments;
- over-size input returns `ErrTooLarge` without calling the API;
- recipient error mapping;
- the returned `InternetMessageID` equals what the mock recorded.

**Done when:** `Send` passes for all three modes against the mock, and spike 1 behaviour is confirmed
manually.

### P4.4 `SetRead` and `Archive`
**Files:** `server/mail/gmail/client.go`, `server/mail/gmail/actions_test.go`

**Steps:**
1. `SetRead(id, true)` → `messages.modify{RemoveLabelIds:["UNREAD"]}`; `false` →
   `AddLabelIds:["UNREAD"]`.
2. `Archive(id)` → `messages.modify{RemoveLabelIds:["INBOX"]}`.
3. `404` → `mail.ErrNotFound`. The action handler (Phase 2) then refreshes the post state from the
   next sync.
4. Confirm Phase 3 requested `gmail.modify` (spec scopes). If Phase 3 shipped `gmail.readonly`
   instead, fix the scope here and note in the PR that existing Gmail test accounts must reconnect.

**Tests:** mock records label changes and appends `labelAdded`/`labelRemoved` history. After the
action plus a `SyncAccount`, the post footer state matches and there's no duplicate update (the
`MessageMapping.State` comparison).

**Done when:** both actions round-trip through the mock and the footer reflects them.

### P4.5 Loop prevention for Gmail sends
**Files:** `server/sync/apply.go` (Phase 1/2 change application), `server/mail/gmail/history.go`

**Steps:**
1. The Phase 2 compose service records `sent_{hash(accountID|InternetMessageID)}` from `SentInfo`.
   Verify Gmail returns a non-empty `InternetMessageID` in every mode.
2. Ordinary sends produce a `messageAdded` with `SENT` and no `INBOX`, which Phase 3's history
   mapping already drops. Keep that rule.
3. Self-recipient case (from spike 6): a `SENT`+`INBOX` message must be skipped by the `sent_` check
   in the Created path, not posted. If Gmail produces a *separate* inbox copy, its Message-ID matches
   too, so the same check covers it.

**Tests:** engine tests:
- send then sync: no new post;
- send to self then sync: no new post;
- an unrelated inbound message in the same thread still posts as a thread reply.

**Done when:** no echo appears in any of the three scenarios.

### P4.6 Enable Gmail actions in the UI
**Files:** wherever Phase 3 gated Gmail out. Expected: the email-post attachment builder in
`server/email/` (buttons + Actions select) and the thread-prompt path in `server/compose/` / the
`MessageHasBeenPosted` hook in `server/hooks.go`. Also `server/command/command.go`
(`/inbox settings … autosend`).

**Steps:**
1. Flip the Gmail provider's `Capabilities()` (Phase 1, P1.5) to `{Compose: true, StateActions: true}`.
2. "Open in Gmail" link: confirm `WebLink` (Phase 3) produces
   `https://mail.google.com/mail/u/{email}/#all/{messageId}`. **Verify:** this deep link opens the
   message when several Google accounts are signed in.
3. Allow `autosend` for Gmail accounts.

**Tests:** renderer tests assert Gmail email posts carry the same buttons and select options as
Outlook. Hook test: a thread reply in a Gmail channel produces the prompt.

**Done when:** Gmail and Outlook email posts and prompts are identical apart from provider names and
links.

### P4.7 Mock Gmail additions
**Files:** `server/testhelper/mock_gmail.go`

**Steps:**
1. `POST /gmail/v1/users/me/messages/send` (JSON `raw`) and
   `POST /upload/gmail/v1/users/me/messages/send` (multipart upload: JSON metadata part +
   `message/rfc822` part). Decode, store as a mailbox message with a new ID, the given `threadId` and
   labels `SENT` (+ `INBOX` if any recipient equals the profile address), and append a
   `messageAdded` history record.
2. `GET …/messages/{id}?format=metadata&metadataHeaders=Message-ID` returns the stored header.
3. `POST …/messages/{id}/modify` applies label changes and appends history.
4. Accessors: `SentMessages() []SentRaw{ID, ThreadID string; Raw []byte}` and
   `Labels(id) []string`.
5. Optional failure injection: `FailNextSend(status int, reason string)`.

**Tests:** covered by P4.3–P4.5.

**Done when:** all Phase 4 unit and integration tests run against the mock with no network.

## Integration & manual test plan

**Integration** (`server/integration_test.go` or `server/integration_gmail_test.go`, testcontainers,
in-repo `server/testhelper`, mock Gmail reachable via host access, endpoints overridden with
`DeveloperEndpoints`):
1. Connect a Gmail account through the mock OAuth flow and seed an inbound message; sync → root post.
2. Dialog path (using the Phase 2 dialog test helper): Reply all with an edited Cc. Assert the mock's
   recorded MIME To/Cc, `threadId`, and the user's post with the "Sent to" footer in the thread.
3. Thread path: post a reply with a file attachment → prompt → *Send reply*. Assert an attachment
   part in the recorded MIME.
4. Forward of a message with two attachments: both are re-attached.
5. Mark read and Archive from the Actions select: mock labels change; after sync the post remains
   with the footer "Read · Archived".
6. Send to self → sync → no echo post.

**Manual** (real Workspace account, server 11.7 ESR):
- Web, desktop, mobile: Reply / Reply all / Forward from buttons; thread prompt; AutoSend `reply_all`.
- Recipient sees correct threading in Gmail web and in Outlook.
- Non-ASCII subject and filename render correctly for recipients.
- A ~20 MB attachment sends; a ~40 MB one fails with the friendly message.
- Archive in Mattermost → gone from the Gmail inbox; un-archive in Gmail → footer updates.

## PR checklist

- [ ] Spike findings recorded in PR 4a; spike code removed.
- [ ] Golden files committed; `-update` documented in the test file header comment.
- [ ] No header value can contain CR/LF (fuzz test in CI).
- [ ] Every new error path has a user-facing message (no raw Google errors in posts).
- [ ] `make check-style` and `make test` green; integration tests green with Docker.
- [ ] Scopes confirmed as `gmail.modify` + `gmail.send`.
- [ ] Manual matrix above completed, including mobile.
