# Phase 5 — Gmail Push (Optional Real-Time)
> Spec: [inbox-v2.md](inbox-v2.md) · Depends on: Phases 1, 3 (Phase 4 not required) · Estimate: 2 days · PRs: 5a (config, watch lifecycle, status), 5b (webhook + verifier + tests)

## Outcome

When an admin configures a Cloud Pub/Sub topic, Gmail accounts get near-real-time delivery: Gmail
publishes to the topic, Pub/Sub pushes to the plugin, and the plugin triggers `SyncAccount`.
Without the config, nothing changes: accounts keep polling every `PollingIntervalMinutes`.
Misconfiguration is visible in `/inbox setup` and `/inbox status` rather than silently degrading.

## Out of scope

- Pub/Sub **pull** subscriptions (rejected in the design review).
- Creating topics or subscriptions automatically. The plugin has no GCP admin credentials; the admin
  creates them (steps in Phase 6 docs).
- Treating notification payloads as data. They are hints only; history sync stays the source of
  truth.

## Verification spikes (do first)

Use a real Workspace account, a test GCP project, and a push subscription pointed at a tunnel (e.g.
`ngrok`) to a dev server.

1. **Label filter coverage.** `users.watch{labelIds:["INBOX"], labelFilterBehavior:"include"}`. Check
   which actions publish a notification:
   - new inbox mail;
   - archive (INBOX removed);
   - move to Trash;
   - mark read/unread;
   - permanent delete.
   If archive, trash or UNREAD changes don't publish, drop the label filter (watch the whole mailbox;
   history sync filters anyway) and note the higher notification volume.
2. **Go field name.** **Verify** whether `gmail.WatchRequest` exposes `LabelFilterBehavior`
   (current) or only the deprecated `LabelFilterAction`, in the `google.golang.org/api` version pinned
   in Phase 3.
3. **OIDC token claims.** Capture a push request's `Authorization: Bearer` token and decode it.
   Confirm:
   - `aud` equals the subscription's configured audience;
   - `iss` is `https://accounts.google.com` (or `accounts.google.com`);
   - `email` is the push service account;
   - `email_verified` is `true`.
   **Verify** whether `idtoken.Validate` checks `iss` itself; if not, check it explicitly.
4. **Watch error shapes.** Record the exact error status and message for:
   - a topic that doesn't exist;
   - a topic without the `gmail-api-push@system.gserviceaccount.com` Publisher grant;
   - a malformed topic name.
   These become the `Push.Reason` strings in P5.3.
5. **Watch idempotency.** Calling `users.watch` again with the same topic replaces the existing watch
   and extends expiry. Calling it with a different topic moves the watch. Confirm both.

## Tasks

### P5.1 Configuration
**Files:** `plugin.json`, `server/configuration.go`, `server/configuration_test.go`

**Steps:**
1. Add to `settings_schema` under a "Gmail real-time (optional)" header-style help text:
   - `GmailPubSubTopic` (text, placeholder `projects/my-project/topics/inbox-gmail`);
   - `GmailPushServiceAccount` (text, placeholder `inbox-push@my-project.iam.gserviceaccount.com`);
   - `GmailPushAudience` (text, optional, help: "Defaults to the webhook URL").
2. Add the fields to `configuration` with `GmailPushEnabled() bool` (topic non-empty) and
   `GmailPushAudienceOrDefault(siteURL string) string`
   (`{SiteURL}/plugins/com.mattermost.plugin-inbox/api/v1/webhook/gmail`).
3. `IsValid` rules, only when `GmailEnabled` and the topic is set:
   - topic matches `^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/topics/[A-Za-z][A-Za-z0-9._~+%-]{2,254}$`;
   - service account is non-empty and ends in `.iam.gserviceaccount.com`;
   - audience, when set, is an absolute HTTPS URL.
   Errors name the offending field.
4. Changes to these fields don't touch accounts directly; P5.3 reconciles.

**Tests:** table tests in `configuration_test.go` for each rule and the audience default.

**Done when:** invalid push config is rejected with a field-specific message, and an empty topic
validates as "polling only".

### P5.2 Watch / renew / stop in the Gmail client
**Files:** `server/mail/gmail/watch.go`, `server/mail/gmail/watch_test.go`, `server/mail/types.go`

**Steps:**
1. Extend `mail.PushState` (if Phase 1 didn't) with `Resource string` (the topic for Gmail, the
   subscription ID for Outlook), `LastAttemptAt`, `LastNotificationAt int64` and `Reason string`.
   `Status` values are `active`, `unavailable`, `expired` and `degraded`.
2. `Watch(ctx, WatchRequest{Topic})` → `users.watch` with the INBOX filter (per spike 1). Returns
   `PushState{Status: active, Resource: topic, Expiry: resp.Expiration/1000}`. The response
   `historyId` is **not** written to `SyncCursor`, which only history sync advances.
3. `RenewWatch` = `Watch` with the same topic (spike 5).
4. `StopWatch` → `users.stop`. A `404` or "no watch" response counts as success.
5. Map spike-4 errors to `PushState{Status: unavailable, Reason: …}` with actionable text, e.g.
   "Gmail cannot publish to the topic: grant gmail-api-push@system.gserviceaccount.com the Pub/Sub
   Publisher role" and "Topic not found". Unknown errors keep the API message, truncated to 200
   characters.

**Tests:** against `mock_gmail.go` (add `/watch` and `/stop` recording, plus injectable errors):
success, each mapped error, stop idempotency.

**Done when:** the client returns a correct `PushState` for success and every spike-4 error.

### P5.3 Watch lifecycle and reconciliation
**Files:** `server/scheduler.go` (Phase 1 hourly `PushMaintenance` job, P1.14), new
`server/push_gmail.go` (alongside Phase 1's `server/push_outlook.go`), `server/oauth.go` (OAuth
completion, P1.10), and the disconnect path in `server/oauth.go`/`server/accounts.go` (P1.9–P1.10)

**Steps:**
1. `reconcileGmailPush(acct)`, under the account's sync mutex:
   - topic configured, and `Push` is nil, has a different `Resource`, expires within 48h, or is
     `unavailable` with `LastAttemptAt` over 24h ago → `Watch`, then
     `UpdateAccount(acct.ID, set Push)`;
   - topic **not** configured and `Push` is non-nil → `StopWatch` (best effort) and set `Push = nil`.
2. Call sites:
   - **OAuth completion:** async after `CreateAccount` and `InitialCursor`;
   - **`PushMaintenance` job:** every run, for every Gmail account not marked `NeedsReauth`;
   - **`OnConfigurationChange`:** when any `GmailPush*` field changed, start a goroutine that takes
     the cluster mutex `gmail_push_reconcile` and reconciles all Gmail accounts. Every node runs the
     hook; the mutex plus the idempotent rules make the extra runs harmless.
3. Disconnect: `StopWatch` before deleting the account; failures are logged, never block.
4. **Verify:** whether `users.stop` affects watches created by *other* GCP projects for the same
   mailbox (e.g. a second Mattermost install). Document it if so.

**Tests:**
- Unit tests for each `reconcileGmailPush` decision, with a fake client and clock.
- Integration: set the topic in config → account watch is recorded by the mock; clear it → stop is
  recorded.

**Done when:** watches are created, renewed, moved and stopped purely by config and time, with no
manual steps.

### P5.4 Push token verifier
**Files:** `server/mail/gmail/push.go`, `server/mail/gmail/push_test.go`

**Steps:**
1. `type PushVerifier interface { Verify(ctx context.Context, bearer string) error }`.
2. `oidcVerifier{audience, serviceAccount string}`:
   - `idtoken.Validate(ctx, token, audience)`;
   - check `payload.Claims["email"] == serviceAccount`, `payload.Claims["email_verified"] == true`
     and the issuer (per spike 3);
   - return a typed `ErrPushUnauthorized` with a reason for logs.
3. `idtoken` fetches and caches Google's certs. Use a shared `*http.Client` with a 10s timeout via
   `idtoken.NewValidator(ctx, option.WithHTTPClient(…))` and build the validator once, not per
   request.
4. `staticVerifier{token string}` for tests. It is selectable only through Phase 1's
   `DeveloperEndpoints` config (`GmailPushVerifier: "static:<token>"`), honoured only when
   `ServiceSettings.EnableDeveloper` is true.
5. `DecodeEnvelope(body []byte) (email string, historyID uint64, err error)`:
   - parse `{"message":{"data":…,"messageId":…,"publishTime":…},"subscription":…}`;
   - `base64.StdEncoding` (fall back to URL encoding) → `{"emailAddress":…,"historyId":…}`;
   - lowercase the email; `historyId` may be a JSON number or string.

**Tests:**
- Envelope: valid, base64url variant, numeric vs string `historyId`, missing fields, garbage.
- Verifier: wrong email, `email_verified` false, wrong audience. Run against a locally signed JWT
  using a test validator (`idtoken` accepts a custom HTTP client pointed at a test JWKS server;
  **Verify** this; otherwise unit-test only the claim checks on a `*idtoken.Payload`).

**Done when:** every rejection path is covered, and production code can't use the static verifier
without developer mode.

### P5.5 Gmail webhook handler
**Files:** `server/webhook_gmail.go` (main package), `server/api.go` (route registration),
`server/webhook_gmail_test.go`

**Steps:**
1. Route `POST /api/v1/webhook/gmail` on the public router.
2. Handler order:
   1. Push not configured → `404` (shows up in Pub/Sub delivery metrics).
   2. `http.MaxBytesReader` at 64 KB.
   3. Missing or invalid bearer → `401`; log at debug level, rate-limited.
   4. `DecodeEnvelope` error → `400`.
   5. Look up `remote_{hash("gmail"|email)}`. No binding, account deleted, or `NeedsReauth` → `204`
      (avoids endless redelivery).
   6. Account found: set `Push.LastNotificationAt` (cheap atomic update, skipped if updated in the
      last 30s). If `historyId` ≤ the parsed `SyncCursor`, stop here → `204`.
   7. Otherwise call the Phase 1 trigger (`p.triggerSync(accountID)`, coalescing via the `resync`
      flag) in a goroutine → `204`.
3. Never block on Gmail API calls inside the handler. Pub/Sub's ack deadline (10s default) must
   always be met.

**Tests:** handler unit tests with a fake verifier and store:
- each status code above;
- `historyId` short-circuit;
- trigger called exactly once;
- 204 for unknown addresses.

**Done when:** a valid push for a known account triggers exactly one sync and all other inputs return
the documented status quickly.

### P5.6 Degraded-push detection and scheduler behaviour
**Files:** `server/sync/scheduler.go` (Phase 1), `server/sync/engine.go`

**Steps:**
1. Phase 1 scheduler rule: `Push.Status == active` → safety-net sync when `LastSyncAt` is older than
   15 minutes; otherwise every `PollingIntervalMinutes`. Confirm it applies to Gmail unchanged.
2. When a **safety-net** sync (not one triggered by a notification) applies at least one Created
   change for a message whose `internalDate` is over 5 minutes old, and `Push.LastNotificationAt` is
   older than that message, set `Push.Status = degraded` with the reason "Notifications are not
   arriving; check the Pub/Sub push subscription and its authentication". This catches the common
   silent failure: the watch succeeds but the push subscription is missing or misconfigured.
3. `degraded` accounts are scheduled like `unavailable` (normal poll interval). The next received
   notification restores `active`.

**Tests:** scheduler table test over status and timestamps. Engine test: safety-net sync with stale
`LastNotificationAt` → degraded; then a notification → active.

**Done when:** a missing push subscription leads to `degraded` within one safety-net cycle, and mail
still arrives via polling.

### P5.7 Status and setup reporting
**Files:** `server/status.go` (or Phase 1's status text builder), `server/setup.go` (Phase 1
`/inbox setup`), `server/command/command.go`

**Steps:**
1. `/inbox status`, Gmail rows:
   - "Real-time: on (renews {date})";
   - "Real-time: off (polling every N min)", plus the reason when `unavailable` or `degraded`;
   - "Real-time: not configured by admin".
2. `/inbox setup`, Gmail push section:
   - topic, service account and audience;
   - the webhook URL to paste into the push subscription;
   - counts of accounts by push status, with the top three distinct reasons;
   - a note that the plugin can't verify the subscription exists — rely on "degraded" detection and
     Pub/Sub metrics.
3. Site URL check: warn if it isn't `https://` (Pub/Sub push requires HTTPS).

**Tests:** golden-text tests for both outputs across statuses.

**Done when:** an admin can diagnose every spike-4 failure and the degraded case from `/inbox setup`
alone.

### P5.8 Admin documentation handoff
**Files:** `implementation-plans/phase-6-setup-docs.md` (reference only). The text itself is written
in Phase 6.

**Steps:** record in the PR description the exact GCP steps and `gcloud` commands validated during
the spikes:
1. Topic creation.
2. Publisher grant to `gmail-api-push@system.gserviceaccount.com`.
3. Push service account creation.
4. Whether the Pub/Sub service agent needs Token Creator on it (**Verify** for current projects).
5. Push subscription creation with `--push-endpoint`, `--push-auth-service-account` and
   `--push-auth-token-audience`.
6. The three plugin settings.

**Done when:** the commands have been run end to end once against a clean GCP project.

## Integration & manual test plan

**Integration** (testcontainers; mock Gmail via host access; `DeveloperEndpoints` with
`GmailPushVerifier: "static:test-token"`):
1. Configure the topic → the connected Gmail account's watch is recorded; `/inbox status` shows
   "Real-time: on".
2. Seed a message in the mock and POST a valid envelope with the static bearer → the post appears
   without waiting for the scheduler (assert within 5s).
3. POST the same envelope twice concurrently → one post (coalescing + dedup).
4. Wrong bearer → 401; unknown email → 204; not configured → 404.
5. Mock watch returns the "not authorized to publish" error → status `unavailable` with the mapped
   reason, and polling continues.
6. Clear the topic → stop recorded, `Push` nil.

**Manual** (real GCP project + Workspace, Site URL on public HTTPS):
- Follow the Phase 6 draft steps from scratch. New mail appears in under 10s on web and mobile.
- Archive, trash, and read/unread in Gmail reflect within seconds (or within the safety-net window if
  the label filter was kept and doesn't cover them; record which).
- Delete the push subscription → within ~15 min the account shows `degraded`; mail still arrives.
- Remove the Publisher grant and wait for renewal (or force it by clearing and resetting the topic)
  → `unavailable` with the grant hint.

## PR checklist

- [ ] Spike results (label filter, claims, error strings, Go field name) recorded in PR 5a.
- [ ] Static verifier unreachable without `EnableDeveloper` (test proves it).
- [ ] Webhook never calls Gmail synchronously; handler p99 < 50 ms in tests.
- [ ] No secrets or tokens logged.
- [ ] `plugin.json` help text links to the README Gmail real-time section (filled in Phase 6).
- [ ] `make check-style`, `make test`, integration tests green.
- [ ] Manual checks above done, including the degraded and unavailable paths.
