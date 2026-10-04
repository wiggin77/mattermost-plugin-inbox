# Phase 1 — Foundation (Outlook)

> Spec: [inbox-v2.md](inbox-v2.md) · Depends on: — · Estimate: 5–6 days · PRs: 1a Groundwork, 1b Accounts & provider abstraction, 1c Delta sync & push hints, 1d Commands & setup

## Outcome

Outlook-only plugin on the v2 foundation:
- An account-scoped store.
- A provider-neutral `mail` layer.
- Delta sync that doesn't need a reachable Site URL, with push used only as a hint and auto-detected.
- Persisted tokens; admin secrets generated automatically.
- Fixes for: posts that are too long, duplicate posts, and mutable IDs.
- `/inbox` with account selection and `/inbox setup`.

Users still reply by typing in a thread and confirming; that flow now targets the latest inbound email.

## Out of scope

Compose dialogs, Reply all/Forward, read/archive actions and the `Mail.ReadWrite` scope (Phase 2). Gmail
(Phases 3–5). SSO-app reuse and the README admin guide (Phase 6).

## Verification spikes (do first)

1. **Graph delta initial filter.** Against a real mailbox, call
   `GET /me/mailFolders('inbox')/messages/delta?$filter=receivedDateTime+ge+{ts}&$select=id`. Record
   whether the filter is accepted and whether the returned deltaLink then only yields newer changes.
   If it's rejected, P1.12 uses the fallback: page an unfiltered `$select=id` delta to its deltaLink,
   processing nothing.
2. **Immutable IDs across moves.** With `Prefer: IdType="ImmutableId"`, move a message
   Inbox → Archive → Deleted Items. Confirm the ID is stable and `parentFolderId` reflects each move.
   Record what the delta `@removed` entry looks like for each move.
3. **Subscription validation failure shape.** Create a subscription with an unreachable
   `notificationUrl`. Record the status code and error code/message so P1.14 can classify it as
   "unreachable" rather than "transient".
4. **Plugin config keys outside `settings_schema`.** Confirm a key such as `DeveloperEndpoints`, set via
   `PatchConfig`, round-trips to `LoadPluginConfiguration` and isn't stripped by the System Console
   on save.

## Tasks

### PR 1a — Groundwork

#### P1.1 Raise minimum server to 11.7
**Files:** `plugin.json`, `go.mod`, `go.sum`, `CLAUDE.md`
**Steps:**
1. `min_server_version` → `11.7.0`.
2. Bump `github.com/mattermost/mattermost/server/public` to the latest version compatible with
   11.7. It must include `DialogElement.MultiSelect`, `DataSourceURL` and `LookupDialogResponse`
   (present in v0.1.21).
3. `CLAUDE.md`: "Minimum Mattermost Server 11.7.0".
**Tests:** existing suites pass.
**Done when:** `make dist` succeeds; the plugin refuses to install on < 11.7.

#### P1.2 Remove the webapp
**Files:** delete `webapp/`; edit `plugin.json` (drop the `webapp` block), `.gitignore` (webapp
entries), `.nvmrc` (keep only if `e2e-tests` still needs it), `CLAUDE.md` (build/test commands,
conventions).
**Steps:**
1. Remove the `webapp` section from the manifest. `build/setup.mk` derives `HAS_WEBAPP` from the
   manifest, so the Makefile skips all webapp targets.
2. Check `.github/workflows/e2e.yml` still works: it uses Node only for `e2e-tests/`.
**Tests:** `make check-style test dist` passes; the bundle contains no `webapp/`.
**Done when:** the plugin installs and runs with no webapp bundle.

#### P1.3 In-repo test helper with mock-server access
**Files:** new `server/testhelper/helper.go`, `mmcontainer.go`, `mockhub.go`, `memkv.go`, `doc.go`
(forked from `github.com/mattermost/mattermost-plugin-starter-template/server/testhelper`); edit
`server/integration_test.go` (import path); `go.mod` (drop the starter-template dependency if
nothing else uses it).
**Steps:**
1. Copy the starter `testhelper` (`helper.go`, `mmcontainer.go`, `doc.go`) into `server/testhelper`,
   alongside the existing `mock_graph.go`.
2. `mockhub.go`: start one `httptest.Server` (listening on `0.0.0.0`) **before** the containers.
   - Register its port in `ContainerRequest.HostAccessPorts`.
   - It routes `/t/{testID}/{service}/...` to handlers each test registers via
     `th.MockHub.Register(service, http.Handler) (baseURL string)`.
   - `baseURL` uses `http://host.testcontainers.internal:{port}/t/{testID}/{service}` for the plugin
     and the loopback form for the test process.
3. `TestHelper.SetPluginConfig(map[string]any)`: patch `PluginSettings.Plugins[pluginID]` through
   `AdminClient.PatchConfig` and wait for `OnConfigurationChange` to apply. Poll a cheap endpoint,
   or use a short retry loop on the effect.
4. Default image: `MM_TEST_IMAGE`, else `mattermost/mattermost-enterprise-edition:release-11.7`.
   **Verify:** the exact tag name.
5. `memkv.go`: `NewMemKVAPI(t) *plugintest.API`, with `KVSetWithOptions` (including
   `Atomic`/`OldValue` and `ExpireInSeconds`), `KVGet`, `KVDelete` and `KVList` backed by a map.
   Store unit tests use it via `pluginapi.NewClient(api, nil)`.
**Tests:** existing integration tests pass with the forked helper. A new
`TestMockHubReachableFromPlugin` registers a handler and asserts the plugin reaches it, through a
`DeveloperEndpoints`-routed `/inbox setup` check once P1.4 lands.
**Done when:** integration tests can point the running plugin at per-test mock servers.

#### P1.4 Configuration rework and auto-generated secrets
**Files:** `server/configuration.go`, `server/configuration_test.go`, `plugin.json`, `server/plugin.go`
**Steps:**
1. Rename the fields:
   - `TenantID` → `OutlookTenantID`, `ClientID` → `OutlookClientID`,
     `ClientSecret` → `OutlookClientSecret`.
   - `WebhookSecret` → `OutlookWebhookSecret`.
   - Add `OutlookEnabled bool`.
2. Add hidden `DeveloperEndpoints string` (JSON:
   `{"graph":"","microsoftLogin":"","gmail":"","googleOAuth":""}`). Parse it in
   `getDeveloperEndpoints()`, which returns empty unless `*GetConfig().ServiceSettings.EnableDeveloper`
   is true. Log a warning on activation when overrides are active.
3. Replace `IsValid` with:
   - `validateCommon()`: only checks the encryption key format.
   - `outlookConfigError() error`: nil when the provider is enabled and complete.
   The plugin activates even with no provider configured.
4. `ensureGeneratedSecrets()` in `OnActivate`:
   - Under `cluster.NewMutex(p.API, "inbox_config_init")`, re-read `p.API.GetPluginConfig()`.
   - If `EncryptionKey` or `OutlookWebhookSecret` is empty, generate it (32 random bytes, base64)
     and `SavePluginConfig`.
   - The mutex stops two nodes generating different keys.
5. `PollingIntervalMinutes` default 2 (used from P1.13). Update `plugin.json` settings, help text and
   footer: provider-neutral header, both settings grouped under an "Outlook" section header.
**Tests:** `configuration_test.go`:
- Per-provider validation.
- Developer endpoints ignored when `EnableDeveloper` is false.
- `ensureGeneratedSecrets` fills only empty values and is idempotent, using `plugintest.API` for
  `GetPluginConfig`/`SavePluginConfig` and a fake mutex via `memkv`.
**Done when:** a fresh install activates with no admin input besides the Outlook credentials, and
secrets appear in the System Console.

### PR 1b — Accounts & provider abstraction

#### P1.5 `server/mail` package
**Files:** new `server/mail/types.go`, `provider.go`, `errors.go`, `address.go`, `address_test.go`
**Steps:**
1. Types per inbox-v2.md:
   - `ProviderType` (`ProviderOutlook = "outlook"`, `ProviderGmail = "gmail"`).
   - `Address{Name, Email}`.
   - `Message{ID, ThreadID, InternetMessageID, Subject, From, ReplyTo, To, Cc, Date, HTMLBody,
     TextBody, UniqueHTMLBody, Preview, Attachments, State, WebLink, FromSelf bool}`.
   - `Attachment{ID, Name, ContentType, Size, Inline, Data}`.
   - `MessageState{Read, Archived bool}`.
   - `Change{Type, MessageID}` with `ChangeCreated|Updated|StateChanged|Archived|Deleted`.
   - `Profile{RemoteID, Email}`.
   - `PushState{ID, Expiry int64, Status, Reason string}` with
     `PushActive|PushUnavailable|PushExpired`.
   - `ComposeMode`, `Outgoing`, `SentInfo{InternetMessageID}`, `WatchRequest{NotificationURL,
     LifecycleURL, Secret}`.
2. `Provider` and `Client` interfaces as in the spec, plus two additions:
   - `Provider.Capabilities() Capabilities{Compose, StateActions bool}`. Email-post buttons and
     actions are shown only when they're supported. Outlook reports `false/false` until Phase 2;
     Gmail until Phase 4.
   - **Contract:** `InitialCursor(ctx, since)` returns a cursor whose `ListChanges` yields every
     Inbox message received after `since`. That one method covers both cases:
     - Connect: `since = now`.
     - `ErrCursorExpired` recovery: `since = LastSyncAt`.
     Outlook uses a filtered delta; Gmail encodes a `messages.list` catch-up followed by
     `historyId`. No separate `Resync` method.

   Clients return `ErrNotSupported` for methods not yet implemented (`SetRead`, `Archive`,
   non-Reply `Send`).
3. Errors: `ErrNotFound`, `ErrCursorExpired`, `ErrReauthRequired`, `ErrNotSupported`, and
   `IsRateLimited(err)`.
4. `address.go`: `ParseAddressList(string) ([]Address, error)` (wraps `net/mail`, tolerant of
   `;`), `Address.String()`, `NormalizeEmail` (lowercase, trim), `DedupeAddresses`.
**Tests:** `address_test.go` (parsing, separators, display names with commas, dedupe
case-insensitivity).
**Done when:** the package compiles with zero imports of `msgraph` or any provider.

#### P1.6 Graph client hardening
**Files:** `server/msgraph/client.go`, `types.go`, `errors.go`
**Steps:**
1. Send `Prefer: IdType="ImmutableId"` on every request. Merge it with any other `Prefer` values
   using `, `.
2. Extend `$select` everywhere to `id, conversationId, internetMessageId, subject, bodyPreview, body,
   uniqueBody, from, replyTo, toRecipients, ccRecipients, receivedDateTime, hasAttachments, isRead,
   parentFolderId, webLink`. Add the fields to `Message`.
3. `ListInboxMessages`: follow `@odata.nextLink` until exhausted or `maxPages`. Return the messages
   plus the max `receivedDateTime` seen (no more "cursor = now").
4. `NewClientWithBaseURL` is already there; add an optional `loginBaseURL` to `OAuthConfig` for
   `DeveloperEndpoints`.
5. `APIError.IsGone()` (410) for delta use in PR 1c.
**Tests:** new `server/msgraph/client_test.go` against `testhelper.MockGraphServer`:
- `Prefer` header present.
- Paging followed.
- 429 retry honours `Retry-After`.
**Done when:** every Graph call uses immutable IDs.

#### P1.7 Outlook adapter (interim)
**Files:** new `server/mail/outlook/provider.go`, `client.go`, `convert.go`, `convert_test.go`
**Steps:**
1. `Provider`:
   - `OAuth2Config` builds from tenant/client/secret plus the login base URL.
   - Scopes: `User.Read Mail.Read Mail.Send offline_access` (`Mail.ReadWrite` arrives in Phase 2).
   - `AuthCodeOptions` = `AccessTypeOffline`.
2. `Client` wraps `*msgraph.Client`:
   - `Profile`: `GetMe`, using `Mail` or else `UserPrincipalName`; `RemoteID` = Graph `id`.
   - `GetMessage`, `GetAttachment` (returns `Data` already decoded from `contentBytes`),
     `WebLink`.
   - `Send` supports only `ComposeReply` with no recipient override (`/reply`); anything else
     returns `ErrNotSupported`.
   - Subscription methods map to `Watch`/`RenewWatch`/`StopWatch`.
3. Interim `ListChanges`: the cursor is an RFC 3339 timestamp. Use the paged `ListInboxMessages`
   and emit `ChangeCreated` for each message. The new cursor is the max `receivedDateTime` seen;
   dedup makes the `ge` overlap harmless. Replaced in P1.12.
4. `convert.go`: `msgraph.Message` → `mail.Message`, including `UniqueHTMLBody`, `State.Read`, and
   `FromSelf` (from-address equals the account email, passed in at client construction).
**Tests:** `convert_test.go`: field mapping, HTML vs text bodies, nil `replyTo`, attachments,
`FromSelf`.
**Done when:** nothing outside `mail/outlook` and `testhelper` imports `msgraph`.

#### P1.8 Account-scoped KV store
**Files:** rewrite `server/store/kvstore/kvstore.go` (interface), `types.go`; new
`account_store.go`, `token_store.go`, `routing_store.go`, `mapping_store.go`, `sent_store.go`,
`lists.go`, `store_test.go`; keep `oauth_store.go` (new fields) and `encryption.go`; delete
`user_store.go`, `email_store.go`.
**Steps:**
1. Types from inbox-v2.md "Storage": `Account`, `RemoteBinding`, `ConversationMapping`,
   `MessageMapping`, `OAuthState{MattermostUserID, Provider, TeamID, CreateAt}`. `Draft` and
   contacts come in Phase 2. No `post_` mapping.
2. Keys:
   - `acct_`, `tok_`, `uaccts_`, `accounts`, `remote_`, `sub_`.
   - `c_{accountID}_{hash}`, `m_{accountID}_{hash}`.
   - `sent_{hash(accountID|internetMessageID)}`.
   - Constants live in one block with a test asserting every key is ≤ 50 bytes for 26-char IDs.
3. `lists.go`: `appendUnique(key, id)` and `removeValue(key, id)` via `KV.SetAtomicWithRetries`.
4. API:
   - `CreateAccount(acct, encryptedToken)`: writes `tok_`, `acct_`, both indexes and the binding,
     in that order, so a partial failure leaves no orphan index entry pointing at a missing account.
   - `GetAccount`, `UpdateAccount(id, mutate)` (`SetAtomicWithRetries`).
   - `DeleteAccount(id)`: removes `acct_`, `tok_`, index entries and `sub_`; keeps `remote_`.
   - `ListAccountsForUser`, `ListAllAccountIDs`, binding, subscription index, mapping and sent
     methods.
5. Mapping methods all take `accountID`. `DeleteConversationMapping` is new.
**Tests:** `store_test.go` with `testhelper.NewMemKVAPI`:
- CRUD, index atomicity under concurrent `appendUnique`.
- `DeleteAccount` keeps the binding.
- Key-length test.
- Mappings isolated across accounts with the same message ID.
**Done when:** the interface has no user-keyed methods left.

#### P1.9 Persisting token source and re-auth handling
**Files:** new `server/accounts.go` (client factory); `server/plugin.go`
**Steps:**
1. `p.clientFor(acct) (mail.Client, error)`:
   - Decrypt `tok_`.
   - Build `oauth2.ReuseTokenSource(tok, provider.OAuth2Config(...).TokenSource(ctx, tok))` and
     wrap it in `persistingTokenSource`.
   - The wrapper encrypts and writes `tok_` when the access token changes.
   - It maps `*oauth2.RetrieveError` with `ErrorCode == "invalid_grant"` to
     `mail.ErrReauthRequired`.
2. Per-node client cache keyed by account ID, invalidated on disconnect, reconnect and
   `OnConfigurationChange`.
3. `p.markNeedsReauth(acct)`: `UpdateAccount` sets `NeedsReauth` only if it's false, then posts
   once in the account channel: "Your connection expired. Run `/inbox connect outlook` to
   reconnect."
**Tests:** `accounts_test.go`:
- Token refresh persists exactly once per new token.
- `invalid_grant` maps to `ErrReauthRequired`.
- Reauth message posted once across repeated failures.
**Done when:** restarting the plugin after a refresh uses the refreshed token.

#### P1.10 Provider registry, OAuth flow and channels
**Files:** new `server/providers.go`, `server/oauth.go` (moved out of `api.go`); edit `server/api.go`
(routes), `server/sync/channel.go`, `server/plugin.go`
**Steps:**
1. `p.providers()` returns the enabled and configured providers, built from config with
   `DeveloperEndpoints` applied. It is rebuilt in `OnConfigurationChange`.
2. Routes become `GET /api/v1/oauth2/{provider}/connect` and
   `GET /api/v1/oauth2/{provider}/complete`. Unknown or disabled provider → 404.
3. `generateOAuthConnectURL(userID, teamID, provider)` stores `OAuthState` with `TeamID`.
4. Completion handler, in this order:
   1. Exchange the code.
   2. `Profile`.
   3. Resolve `RemoteBinding` (same user → reuse `AccountID`/`ChannelID`; other user with an
      active account → error page; stale → replace).
   4. Enforce `maxAccountsPerProvider = 1` against `ListAccountsForUser`.
   5. Ensure the channel.
   6. `CreateAccount`.
   7. `InitialCursor(since=now)`.
   8. Start push asynchronously (P1.14; until then a no-op).
   9. Welcome post.
   10. Completion page naming the provider.
5. `EnsureAccountChannel(teamID, userID, username, provider, email, preferredChannelID)`:
   - Reuse `preferredChannelID` if it exists and isn't deleted.
   - Otherwise create `inbox-{username}-{provider}`, with suffix `-2…` on name conflicts and the
     username truncated so the name is ≤ 64 characters.
   - Display name `Inbox: {email}` (≤ 64 runes); provider-specific header and purpose.
   - Team: `OAuthState.TeamID`, falling back to the first team.
6. Disconnect: stop the watch (best effort), `DeleteAccount`, evict the client cache. The channel
   is preserved.
**Tests:** `channel_test.go` (naming, suffixing, truncation, reuse). Integration (P1.17).
**Done when:** connect → disconnect → reconnect reuses the same account ID and channel.

#### P1.11 Port engine, renderer, hooks and jobs to accounts
**Files:** `server/sync/sync.go`, `server/sync/poller.go` (delete; replaced by `SyncAccount`),
`server/email/renderer.go`, `server/hooks.go`, `server/api.go`, `server/job.go`, tests in
`server/sync/sync_test.go` and `server/email/renderer_test.go`
**Steps:**
1. Engine:
   - `ProcessMessage(ctx, acct, client, *mail.Message)`, using account-scoped mappings.
   - Skip when `m_` exists or `sent_` matches the `InternetMessageID`.
   - Post props: `inbox_account_id`, `inbox_message_id`, `inbox_thread_id`, `inbox_provider`.
   - Maintain `ConversationMapping.LastInboundMessageID` for messages where `!FromSelf`.
2. `SyncAccount(ctx, acct, client)` (unlocked version for now): `ListChanges` from `SyncCursor`,
   apply, then `UpdateAccount` the cursor and `LastSyncAt`. Locking arrives in P1.13.
3. `ApplyChanges` handles Created and Deleted. Deleting a root also deletes `c_`.
4. Renderer takes `*mail.Message` and a provider display name. Unchanged otherwise until P1.15.
5. Hook:
   - Load the root post; continue only if `root.UserId == p.botUserID` and it has
     `inbox_account_id`.
   - Load the account; require `acct.MattermostUserID == post.UserId` and
     `acct.InboxChannelID == post.ChannelId`.
   - The confirmation context carries only `account_id` and `post_id`.
6. Action handler:
   - Reload the account and verify the owner equals `request.UserId`.
   - Verify the referenced post's author equals the owner and its root is a bot email post of
     that account.
   - Target = `LastInboundMessageID`.
   - `client.Send(ComposeReply)`.
7. Graph webhook handler routes via the `sub_` index (O(1)) and calls `SyncAccount`. Payloads are
   only hints from now on.
8. Poll job iterates `ListAllAccountIDs`; renewal job iterates accounts.
**Tests:** port `sync_test.go` to the engine:
- Dedup.
- Threading via `c_`.
- Root delete clears `c_` and the next message starts a new thread.
- `LastInboundMessageID` ignores `FromSelf`.
- Hook ignores props on non-bot posts.
- Action handler rejects a mismatched user.
**Done when:** the PR 1b plugin works end to end for Outlook, with the same user-visible behaviour
as v0.5.0 plus the bug fixes.

### PR 1c — Delta sync & push hints

#### P1.12 Graph delta in the Outlook adapter
**Files:** `server/msgraph/client.go` (`Delta`, `GetWellKnownFolderID`), `types.go`
(`DeltaPage`, `Removed`); `server/mail/outlook/client.go`, `changes.go`, `changes_test.go`;
`server/testhelper/mock_graph.go`
**Steps:**
1. `msgraph.Delta(ctx, link string)` sends `Prefer: odata.maxpagesize=50` (plus `ImmutableId`).
   Returns `Value` (messages, some with `@removed`), `NextLink` and `DeltaLink`.
2. Cursor = `deltaLink`. `InitialCursor(since)` follows spike 1 (filtered delta, or a silent
   `$select=id` walk to the deltaLink).
3. `ListChanges(cursor)` fetches one page and returns `more=true` while `NextLink` is set; the
   cursor carries the nextLink in between.
   - New ID → Created.
   - Known ID with a changed `isRead` → StateChanged. Other modifications → Updated.
   - `@removed` → `GetMessage`:
     - 404 → Deleted.
     - `parentFolderId` equal to the cached `deleteditems` ID → Deleted.
     - Otherwise → Archived.
4. HTTP 410 → `ErrCursorExpired`. Engine recovery (P1.13): `cursor = InitialCursor(LastSyncAt)`,
   which for Outlook is a new filtered delta. Dedup absorbs any overlap.
5. Mock: `/me/mailFolders('inbox')/messages/delta` with server-side change log and tokens,
   `@removed` entries, move/delete helpers that keep IDs stable, and `/me/mailFolders/{wellKnown}`.
**Tests:** `changes_test.go`: classification table, paging with cursor carry-over, 410 handling.
**Done when:** bursts over 50 messages, messages moved into the Inbox, and deletes are all handled
without webhooks.

#### P1.13 Locked, coalescing `SyncAccount` and the scheduler
**Files:** `server/sync/sync.go`, new `server/sync/trigger.go`, `server/scheduler.go` (replaces
`server/job.go`), `server/plugin.go`
**Steps:**
1. `TriggerSync(accountID)`:
   1. Set `resync_{accountID}` (10 min TTL).
   2. Try `cluster.NewMutex(api, "sync_"+id).LockWithContext` with a 200 ms timeout. If that fails,
      return; the holder will see the flag.
   3. If acquired: loop { delete flag; `SyncAccount` } while the flag is set again.
   4. Unlock. After unlocking, re-check the flag once and retry the lock to close the race.
2. Persist the cursor after **each** `ListChanges` page.
3. On `ErrCursorExpired`: `cursor = InitialCursor(LastSyncAt)`, persist it, and continue the loop
   (once per sync, to avoid spinning).
4. On `ErrReauthRequired`, call `markNeedsReauth` and stop.
4. Scheduler:
   - A single `cluster.Schedule(p.API, "InboxScheduler", cluster.MakeWaitForInterval(time.Minute),
     p.runScheduler)` replaces `EmailPollJob`.
   - An account is due when (no active push and `now-LastSyncAt ≥ PollingIntervalMinutes`) or
     (active push and ≥ 15 min).
   - `NeedsReauth` accounts are skipped.
   - Run with `errgroup.Group` and `SetLimit(10)`, calling `TriggerSync`.
5. `/inbox sync` and webhooks call `TriggerSync` asynchronously.
**Tests:** `trigger_test.go`:
- Concurrent triggers produce one sync plus at most one rerun.
- A trigger during a sync isn't lost.
- Cursor persisted per page (fail on page 2 → page 1 cursor kept).
Scheduler due-selection table test.
**Done when:** concurrent webhook and scheduler runs never double-post (asserted in integration).

#### P1.14 Push as a hint with auto-detection
**Files:** `server/push_outlook.go` (subscription lifecycle; replaces logic in `job.go`),
`server/api.go` (`/webhook/outlook`, `/webhook/outlook/lifecycle`), `server/scheduler.go`
(`PushMaintenance`, hourly)
**Steps:**
1. `ensureOutlookPush(acct)`:
   - Site URL missing or not `https` → `PushUnavailable` with reason "Site URL must be public
     HTTPS".
   - Otherwise create a subscription (`created,updated,deleted` on the Inbox, `clientState` =
     `OutlookWebhookSecret`, `lifecycleNotificationUrl`).
   - On a validation failure (spike 3) → `PushUnavailable` with reason "Microsoft could not reach
     {url}".
   - Store `sub_` and `Account.Push`.
2. Hourly maintenance:
   - Renew subscriptions within 24h of expiry.
   - Recreate them on 404.
   - Retry `PushUnavailable` once per day.
3. Handlers:
   - Echo `validationToken` (both URLs).
   - Check `clientState`.
   - Map `sub_` → account; `202` → `TriggerSync`.
   - Lifecycle events: `reauthorizationRequired` → renew; `subscriptionRemoved` → recreate;
     `missed` → `TriggerSync`.
4. Remove the now-unused `processCreateNotification`/`Update`/`Delete` paths from `api.go`.
**Tests:** `push_outlook_test.go`:
- Unreachable classification.
- Renewal window.
- Lifecycle dispatch.
- Bad `clientState` → 401.
**Done when:** with an unreachable Site URL, accounts show "polling" and still receive mail. With a
reachable one, notifications trigger a sync within seconds.

#### P1.15 Renderer fixes
**Files:** `server/email/renderer.go`, `renderer_test.go`
**Steps:**
1. Truncate by runes at `maxPostRunes = 16000` (headroom under 16,383 for headers). Cut at a rune
   boundary and append `*[Truncated — Open in {Provider}]({WebLink})*`.
2. Follow-up posts use `UniqueHTMLBody` when it's non-empty; roots use the full body.
3. Append an `[Open in {Provider}]({WebLink})` line to every email post.
4. Header line includes Cc on follow-ups too.
5. `RenderAttachmentNote(name, sizeMB, provider)`.
**Tests:**
- 70,000-character body → ≤ 16,383 runes and valid UTF-8.
- Multi-byte boundary.
- `uniqueBody` preference.
- Link presence.
**Done when:** a 1 MB HTML email posts successfully.

### PR 1d — Commands & setup

#### P1.16 `/inbox` commands
**Files:** `server/command/command.go`, `command_test.go`, `server/command/mocks/` (`make mock`),
`server/commands.go` (new; plugin-side `Deps` implementations), `server/plugin.go`
**Steps:**
1. `Deps`:
   - `Connect(userID, teamID, provider)`
   - `Disconnect(userID, channelID, selector)`
   - `Status(userID)`
   - `Sync(userID, channelID, selector)`
   - `Setup(userID)`
   - `EnabledProviders()`
2. `resolveAccount(userID, channelID, selector)`, in order:
   1. Selector matches an email or provider.
   2. The current channel is an account channel owned by the user.
   3. The user's only account.
   4. Otherwise an error listing the user's accounts.
3. `connect` with no argument works when exactly one provider is enabled. Autocomplete lists enabled
   providers; re-register the command in `OnConfigurationChange`.
4. `status` renders a table per account: provider, email, channel (`~name`), real-time
   (Active until … / Polling every N min — reason), last sync, reconnect needed.
5. Help text is provider-neutral.
**Tests:** `command_test.go`: selector resolution table, connect argument handling, autocomplete
reflects enabled providers.
**Done when:** every subcommand works with zero, one or two accounts (the second via a test-only
provider stub).

#### P1.17 `/inbox setup`
**Files:** `server/setup.go`, `setup_test.go`
**Steps:**
1. System admins only (`p.API.HasPermissionTo(userID, model.PermissionManageSystem)`).
2. Print:
   - Site URL check (set, `https`).
   - Per provider: enabled, configured, redirect URI, webhook URL(s).
   - Credential check.
   - Counts of accounts by push status and of `NeedsReauth` accounts.
3. Credential check: POST to the provider token endpoint with `grant_type=authorization_code`, a
   dummy code and the configured client credentials.
   - `invalid_client`/`unauthorized_client` → ✗.
   - `invalid_grant` → ✓.
   - Network error → ⚠.
   - **Verify:** the Azure AADSTS codes for a bad secret vs a bad code.
**Tests:** `setup_test.go` with an httptest token endpoint returning each error type.
**Done when:** a misconfigured secret is reported precisely without any user connecting.

## Integration & manual test plan

Integration (`server/integration_test.go` and new `server/integration_outlook_test.go`, using
P1.3):
1. Connect via the mock OAuth (mock authorize endpoint redirects back with code and state) → account,
   binding and channel created; welcome post.
2. Mock delta returns 120 messages across 3 conversations → 120 posts, correctly threaded.
3. Push unavailable (mock subscription create returns a validation failure) → `status` shows polling;
   a new mock message appears after the scheduler tick (interval lowered via config).
4. Push active → POST a notification to `/webhook/outlook` → message appears without waiting for
   the scheduler.
5. Concurrent: fire 5 notifications plus a manual `/inbox sync` → no duplicate posts.
6. Move to Archive in the mock → post kept. Move to Deleted Items → post removed; root delete starts
   a new thread on the next message.
7. Thread reply → confirm → mock records `/reply` against `LastInboundMessageID`.
8. Token refresh: mock issues a new access token → `tok_` updated. Mock returns `invalid_grant` →
   reauth post once.
9. Disconnect → reconnect reuses the account and channel; a second MM user connecting the same
   mailbox is rejected.

Manual (real tenant, 11.7):
- Fresh install: only enter Outlook credentials, run `/inbox setup`, connect.
- Site URL unreachable vs reachable (tunnel).
- File a message into a folder in Outlook, then reply from Mattermost → succeeds.

## PR checklist

- [ ] `make check-style test dist` green; integration tests green locally with Docker.
- [ ] No imports of `msgraph` outside `server/mail/outlook` and `server/testhelper` (after PR 1b).
- [ ] KV key-length test covers every prefix.
- [ ] `CLAUDE.md` updated in the same PR for any changed commands, packages, KV keys or minimum
      version.
- [ ] Manual spike results recorded in the PR description (spikes 1–4).
- [ ] No webapp references left in `Makefile` targets' output or the manifest (PR 1a).
