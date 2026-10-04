# Phase 6 — Setup Polish and Docs
> Spec: [inbox-v2.md](inbox-v2.md) · Depends on: Phases 1–5 · Estimate: 1–2 days · PRs: 6a (UseSSOApp + plugin.json polish), 6b (README, CLAUDE.md, release prep)

## Outcome

Admins can set up either provider from the README alone, optionally reusing Mattermost's existing
Office 365 / Google sign-in app instead of registering a new one. `/inbox setup` reports exactly what
is missing. The release is measured (binary size), manually verified across providers, clients and
push modes, and documented with release notes ready to tag.

## Out of scope

- Organization-wide consent (decided against).
- A custom System Console component (the webapp was removed in Phase 1). All guidance lives in
  `plugin.json` help text, the README and `/inbox setup`.
- Automating Azure or GCP resource creation.

## Verification spikes (do first)

1. **SSO settings fields.** Confirmed in `github.com/mattermost/mattermost/server/public/model/config.go`
   (v0.1.21):
   - `Config.Office365Settings` is `Office365Settings{Enable, Secret, Id, Scope, AuthEndpoint, TokenEndpoint, UserAPIEndpoint, DiscoveryEndpoint, DirectoryId}`;
   - `Config.GoogleSettings` is `SSOSettings{Enable, Secret, Id, Scope, AuthEndpoint, TokenEndpoint, UserAPIEndpoint, DiscoveryEndpoint, ButtonText, ButtonColor}`.
   All are `*string` / `*bool`. **Verify** the same fields exist in the `server/public` version
   pinned at the time of this phase.
2. **Unsanitized read.** Confirm `p.API.GetUnsanitizedConfig()` returns `Office365Settings.Secret` and
   `GoogleSettings.Secret` unmasked on 11.7. **Verify** whether Office 365 SSO settings are present
   (and usable) on servers without an Enterprise license. Treat empty values as "not available".
3. **Shared app viability.**
   - Azure: add the plugin redirect URI and mail permissions to an existing Mattermost O365 SSO app,
     then confirm both Mattermost login and `/inbox connect outlook` work.
   - Google: confirm that adding `gmail.modify`/`gmail.send` to a Google SSO app that is **External
     and verified for login scopes** sends it back into verification. If so, the docs must warn
     loudly that this toggle is mainly useful for Internal (Workspace) apps.
4. **Upload limit.** **Verify** the server's plugin upload limit (`FileSettings.MaxFileSize`, default
   believed to be 100 MB) against the final tarball size from P6.5.

## Tasks

### P6.1 `OutlookUseSSOApp` / `GmailUseSSOApp`
**Files:** `plugin.json`, `server/configuration.go`, `server/configuration_test.go`,
`server/credentials.go` (new), `server/mail/outlook/provider.go`, `server/mail/gmail/provider.go`,
`server/setup.go`

**Steps:**
1. Add bool settings `OutlookUseSSOApp` and `GmailUseSSOApp`. Help text: "Use the client ID and secret
   from System Console > Authentication > Office 365 (Google). You must still add this plugin's
   redirect URI and mail permissions to that app."
2. `credentials.go`: `func (p *Plugin) outlookCredentials() (tenant, clientID, secret string, source string, err error)`
   and `gmailCredentials()`.
   - When the toggle is on, read `p.API.GetUnsanitizedConfig()`:
     - Outlook: `Office365Settings.Id`, `.Secret`, `.DirectoryId` → tenant;
     - Gmail: `GoogleSettings.Id`, `.Secret`.
   - Nil-safe dereference. Empty values → error "Mattermost Office 365 sign-in is not configured".
   - When the toggle is off, use the plugin's own fields.
   - `source` is `"plugin"` or `"mattermost-sso"`.
3. Every place that builds an `oauth2.Config` (Phase 1 `Provider.OAuth2Config`, and token sources for
   existing accounts) goes through these functions. Read at use time; server config changes don't
   fire plugin `OnConfigurationChange`.
4. `IsValid`: when a toggle is on, skip the plugin's own client-field checks for that provider.
   Server-side emptiness is reported by `/inbox setup` and at connect time, not by `IsValid`, since
   the server config may be filled in later.
5. Ignore the SSO app's `Scope`, `AuthEndpoint` and `TokenEndpoint`; the plugin always uses its own
   scopes and the public endpoints. **Verify:** sovereign clouds (GCC High) need different endpoints;
   if so, document them as unsupported.
6. `/inbox setup` per provider:
   - "Credentials: plugin settings" or "Credentials: Mattermost Office 365 sign-in app (client ID
     …last 4)";
   - result of the Phase 1 dummy-code credential check;
   - a checklist of what must be added to that app: redirect URI, permissions/scopes, and for Google
     the consent-screen verification warning.
7. Switching the toggle doesn't invalidate existing tokens if the client ID is the same app. If it's a
   different app, refresh fails with `invalid_client`/`invalid_grant`, which Phase 1 turns into
   `NeedsReauth`. Document that switching apps requires users to reconnect.

**Tests:** table tests for credential resolution (toggle on/off, nil pointers, empty values),
`IsValid` behaviour, and `/inbox setup` golden text for both sources.

**Done when:** a server with O365 SSO configured can connect Outlook accounts with only the toggle
enabled plus the app-side changes, and `/inbox setup` explains any gaps.

### P6.2 `plugin.json` polish
**Files:** `plugin.json`, `assets/` (icon)

**Steps:**
1. Final name "Mattermost Inbox", description covering Outlook and Gmail, `min_server_version`
   `11.7.0` (set in Phase 1; re-check).
2. Group settings by provider using `section`/`header` help text (**Verify** plugin `settings_schema`
   section support on 11.7). Each block says which fields are ignored when "Use SSO app" is on.
3. Header and footer link to the README admin-guide anchors. No inline setup essays.
4. Replace `assets/starter-template-icon.svg` with an inbox icon and update `icon_path`.

**Tests:** `make check-style` (manifest check); `configuration_test.go` still passes.

**Done when:** the System Console page reads cleanly top to bottom for an admin setting up either
provider.

### P6.3 README admin guide
**Files:** `README.md`

**Steps:** restructure as:
1. **Overview.** What it does; one channel per account; reply UX; mobile support.
2. **Requirements.**
   - Mattermost 11.7+.
   - Outbound HTTPS to `login.microsoftonline.com`, `graph.microsoft.com`, `oauth2.googleapis.com`,
     `gmail.googleapis.com`, `www.googleapis.com` (certs for push verification).
   - Inbound public HTTPS only for real-time push (optional).
3. **Outlook setup (Microsoft Entra ID).**
   - App registrations → New registration.
   - Supported account types:
     - *single tenant* → Tenant ID = Directory (tenant) ID;
     - *multitenant* → Tenant ID `organizations`;
     - *+ personal accounts* → `common`. **Verify** that personal Outlook.com mailboxes work with
       delta and subscriptions before documenting them as supported.
   - Redirect URI (Web): `{SiteURL}/plugins/com.mattermost.plugin-inbox/api/v1/oauth2/outlook/complete`.
   - API permissions (Delegated): `User.Read`, `Mail.ReadWrite`, `Mail.Send`, `offline_access`. Admin
     consent is only needed if tenant policy blocks user consent.
   - Client secret: note its expiry and that an expired secret disconnects everyone.
   - Enter the values, or enable "Use Mattermost's Office 365 sign-in app" (P6.1 checklist).
   - Real-time: automatic when Microsoft can reach the Site URL; check with `/inbox setup`.
4. **Gmail setup (Google Cloud).**
   - Create or select a project; enable the Gmail API.
   - OAuth consent screen:
     - **Internal** (Workspace only, no verification);
     - **External**: `gmail.modify` is a *restricted* scope, requiring Google verification plus an
       annual third-party security assessment (CASA);
     - **Testing** mode: max 100 test users, refresh tokens expire after 7 days.
   - Scopes: `gmail.modify`, `gmail.send`.
   - OAuth client (Web application). Redirect URI
     `{SiteURL}/plugins/com.mattermost.plugin-inbox/api/v1/oauth2/gmail/complete`.
   - Enter the ID and secret, or enable "Use Mattermost's Google sign-in app" (with the verification
     warning from spike 3).
5. **Gmail real-time (optional).** The `gcloud` commands validated in P5.8:
   1. Create topic.
   2. `add-iam-policy-binding` granting `gmail-api-push@system.gserviceaccount.com`
      `roles/pubsub.publisher`.
   3. Create the push-auth service account.
   4. Grant Token Creator to the Pub/Sub service agent if still required.
   5. Create the push subscription with endpoint
      `{SiteURL}/plugins/com.mattermost.plugin-inbox/api/v1/webhook/gmail`,
      `--push-auth-service-account`, `--push-auth-token-audience`.
   6. Fill in `GmailPubSubTopic`, `GmailPushServiceAccount`, optional `GmailPushAudience`.
   Also explain the "degraded" status.
6. **Verifying setup.** Sample `/inbox setup` output and what each line means.
7. **Troubleshooting table.**

   | Symptom | Likely cause |
   |---|---|
   | `AADSTS50011` | redirect URI mismatch |
   | `AADSTS7000215` | bad secret |
   | `invalid_client` | wrong ID/secret |
   | `access_denied` (External/Testing user not added) | test user list |
   | Gmail tokens expire weekly | Testing mode |
   | Push `unavailable` (publisher grant) | missing Publisher grant |
   | Push `degraded` | missing or unauthenticated subscription |
   | "Needs reconnect" | revoked consent or expired secret |

8. **Security notes.**
   - Tokens are AES-256-GCM encrypted with the auto-generated key; regenerating it disconnects
     everyone.
   - Only the bot posts email content; inbox channels are private.
   - Reply actions verify ownership.

**Tests:** n/a. Every URL and command copy-pasted from the README is used during the P6.6 manual
pass.

**Done when:** a colleague who didn't build the plugin sets up both providers from the README alone
(record who and any fixes).

### P6.4 README user guide and developer section
**Files:** `README.md`

**Steps:**
1. **User guide.**
   - `/inbox connect outlook|gmail`.
   - Where mail appears (`Inbox: you@…` channel).
   - Replying: buttons → dialog (3000-character limit) vs typing in the thread (no limit,
     attachments, prompt).
   - Recipient chips.
   - Actions menu: read/unread, archive.
   - `/inbox settings autosend`, `/inbox status`, `/inbox sync`, `/inbox disconnect`.
   - Archive/delete semantics; what "Real-time: off" means.
2. **Developer section.**
   - Remove the webapp/npm build and watch instructions.
   - `make`, `make server`, `make test`.
   - Integration tests need Docker (`MM_TEST_IMAGE` to pin 11.7, `SKIP_DOCKER_TESTS`).
   - `DeveloperEndpoints` and `EnableDeveloper` for local mocks.
   - Golden-file `-update` flag.
   - e2e tests (Playwright) still need Node via `.nvmrc`.

**Done when:** no stale Outlook-only or webapp references remain (`grep -n -i "webapp\|npm run" README.md`
returns only the e2e section).

### P6.5 Binary and bundle size check
**Files:** none (results go in the PR description). Possibly `Makefile` if flags change.

**Steps:**
1. Build `make dist` on the commit before Phase 3 (pre-Google deps) and on the release candidate.
   Record the sizes of the five `server/dist/plugin-*` binaries and the tarball.
2. Confirm release builds strip symbols (check `GO_BUILD_FLAGS`/`-ldflags` in `Makefile`;
   `MM_DEBUG` builds excluded).
3. Find the largest contributors:
   `go tool nm -size -sort size server/dist/plugin-linux-amd64 | head -50` and
   `go version -m server/dist/plugin-linux-amd64 | grep google`. Only `gmail/v1`, `idtoken`,
   `option` and `googleapi` should appear, not other API packages.
4. Threshold: if growth is above ~20 MB per binary, or the tarball nears the server upload limit
   (spike 4), file a follow-up to replace the Google client with a hand-rolled one (the spec's
   fallback). Don't block the release on it unless it's over the limit.

**Done when:** sizes are recorded, and the tarball uploads through System Console on a default 11.7
server.

### P6.6 Full manual pass
**Files:** `implementation-plans/phase-6-manual-results.md` (new, the filled-in matrix)

**Steps:** run on 11.7 ESR with a real O365 tenant and a Workspace Internal app. Matrix:

| Provider | Push | Client | Scenarios |
|---|---|---|---|
| Outlook | on (public URL) | web, desktop, mobile | A–H |
| Outlook | off (unreachable URL) | web, mobile | A–D, G |
| Gmail | on (Pub/Sub) | web, desktop, mobile | A–H |
| Gmail | off (no topic) | web, mobile | A–D, G |
| Both on one user | per above | web | I |

Scenarios:
- **A.** Connect.
- **B.** Inbound new thread + follow-up threading (`uniqueBody`/quote stripping).
- **C.** Reply / Reply all / Forward via dialog, with recipient chip editing and autocomplete.
- **D.** Thread reply with attachment via prompt; AutoSend.
- **E.** Read/unread both directions.
- **F.** Archive both directions; trash removes the post.
- **G.** Disconnect/reconnect reuses the channel, and old threads remain repliable.
- **H.** Revoke consent at the provider → "needs reconnect" message.
- **I.** Two accounts land in separate channels; `/inbox status` lists both; the `[account]`
  selector works.

Also: a long email is truncated with a link; a large inbound attachment shows the note;
`/inbox setup` is clean for both providers, including the SSO-app toggle on one of them.

**Done when:** every cell passes or has a linked issue, triaged as blocker or not.

### P6.7 `CLAUDE.md` update
**Files:** `CLAUDE.md`

**Steps:**
1. Rewrite "What This Is", "High-Level Flow" and "Server Packages" for:
   - `mail`, `mail/outlook`, `mail/gmail`, `msgraph`, `sync`, `compose`, `email`, `store/kvstore`,
     `command`, `testhelper`;
   - delta sync with push as a hint; compose paths.
2. Replace "KVStore Key Patterns" with the inbox-v2 key table (`acct_`, `tok_`, `uaccts_`,
   `accounts`, `remote_`, `sub_`, `c_`, `m_`, `sent_`, `draft_`, `contacts_`, `oauth_`).
3. Conventions:
   - minimum server 11.7.0; no webapp;
   - bot-author check before trusting post props;
   - dialog element length limits;
   - golden-file updates;
   - Docker needed for integration tests.
4. Remove webapp build and test commands; keep the e2e notes.

**Done when:** `CLAUDE.md` matches the code. Spot-check three claims against the source.

### P6.8 Release notes and version
**Files:** `README.md` (Releasing section if it changes), `implementation-plans/release-notes-v1.0.0.md`
(draft text for the GitHub release)

**Steps:**
1. Release notes:
   - **highlights:** Gmail, interactive replies, reliable sync, simpler setup;
   - **breaking:** min server 11.7, config keys renamed, users must reconnect, new scopes
     (`Mail.ReadWrite`, `gmail.modify`);
   - **admin actions required**;
   - **known limitations:** dialog 3000-character limit, one account per provider, Gmail External
     app verification.
2. Proposed version `v1.0.0`; the user creates and pushes the tag (versioning comes from git tags).
3. Confirm `ci.yml` (`plugin-ci`) passes without a webapp, and that the e2e workflow's server image
   is ≥ 11.7.

**Done when:** release notes are reviewed and CI is green on the release commit. Tag left to the user.

## Integration & manual test plan

- **Integration:**
  - credential-resolution tests (P6.1) run in unit tests;
  - one integration test enables `OutlookUseSSOApp`, with the container's server config holding
    `Office365Settings` pointing at the mock (via `DeveloperEndpoints`), and completes the mock OAuth
    flow.
  - **Verify** that the testhelper can set server config (`th.AdminClient.PatchConfig`).
- **Manual:** P6.6 matrix, plus a clean-room README walkthrough (P6.3 done-when) and a tarball upload
  via System Console (P6.5).

## PR checklist

- [ ] SSO-app toggles resolved at use time; no secrets logged or shown (`/inbox setup` shows only the
      last 4 characters of client IDs).
- [ ] `plugin.json` help text and README anchors match.
- [ ] README admin guide validated by someone else; troubleshooting table complete.
- [ ] `CLAUDE.md` updated; no stale webapp or Outlook-only references in docs.
- [ ] Binary and tarball sizes recorded; follow-up filed if over threshold.
- [ ] Manual matrix results committed; blockers resolved.
- [ ] Release notes drafted; CI green; tag handed to the user.
