# Mattermost Inbox (Outlook Sync)

[![Build Status](https://github.com/wiggin77/mattermost-plugin-inbox/actions/workflows/ci.yml/badge.svg)](https://github.com/wiggin77/mattermost-plugin-inbox/actions/workflows/ci.yml)
[![E2E Status](https://github.com/wiggin77/mattermost-plugin-inbox/actions/workflows/e2e.yml/badge.svg)](https://github.com/wiggin77/mattermost-plugin-inbox/actions/workflows/e2e.yml)

A Mattermost plugin that syncs Microsoft 365 Outlook emails into Mattermost. Each connected user gets a private channel where incoming emails appear as threaded posts. Users can reply to emails directly from Mattermost.

## Features

- **Inbound email sync** — New Outlook emails appear as threaded posts in a private `inbox-{username}` channel
- **Reply from Mattermost** — Reply to any email thread in the inbox channel to send an email reply via Outlook
- **Real-time delivery** — Microsoft Graph webhooks push new emails instantly, with polling as a fallback
- **Attachment support** — Email attachments are uploaded to Mattermost (configurable size limit)
- **Reply confirmation** — Optional confirmation prompt before sending email replies, with a "Don't Ask Again" option
- **Encrypted token storage** — OAuth tokens are encrypted at rest with AES-256-GCM

## Prerequisites

### Azure AD App Registration

Register an application in the [Azure Portal](https://portal.azure.com):

1. Go to **Azure Active Directory** > **App registrations** > **New registration**
2. Set the **Redirect URI** to:
   ```
   https://<your-mattermost-url>/plugins/com.mattermost.plugin-inbox/api/v1/oauth2/complete
   ```
3. Under **API permissions**, add the following **delegated** Microsoft Graph permissions:
   - `Mail.Read`
   - `Mail.Send`
   - `offline_access`
4. Under **Certificates & secrets**, create a new client secret
5. Note the **Application (client) ID**, **Directory (tenant) ID**, and the **client secret value**

### Mattermost Server

- Mattermost Server **9.0.0** or later
- Plugin uploads enabled in the Mattermost System Console

## Installation

1. Download the latest release from the [Releases](https://github.com/wiggin77/mattermost-plugin-inbox/releases) page
2. Upload the `.tar.gz` file in **System Console** > **Plugins** > **Plugin Management**
3. Configure the plugin settings (see below)
4. Enable the plugin

## Configuration

In **System Console** > **Plugins** > **Mattermost Inbox (Outlook Sync)**:

| Setting | Description |
|---------|-------------|
| **Azure Directory (Tenant) ID** | Your Azure AD tenant ID. Use `common` for multi-tenant apps. |
| **Azure Application (Client) ID** | The application (client) ID from your Azure app registration. |
| **Client Secret** | The client secret from your Azure app registration. |
| **Encryption Key** | Auto-generated. Used to encrypt OAuth tokens at rest. Regenerating disconnects all users. |
| **Webhook Validation Secret** | Auto-generated. Used to validate incoming Graph webhook notifications. |
| **Polling Interval (minutes)** | How often to poll for emails when webhooks are unavailable. Default: `5`. Set to `0` to disable. |
| **Maximum Attachment Size (MB)** | Attachments larger than this are shown as a link only. Default: `50`. |
| **Enable Diagnostics** | Enable verbose logging for troubleshooting. |

## Usage

### Connecting Your Outlook Account

Run `/inbox connect` in any Mattermost channel. You'll be redirected to Microsoft to authorize the plugin. After authorization, a private channel named `inbox-{your-username}` is created where your emails will appear.

### Slash Commands

| Command | Description |
|---------|-------------|
| `/inbox connect` | Connect your Outlook account |
| `/inbox disconnect` | Disconnect your Outlook account |
| `/inbox status` | Show connection status (email, subscription, last sync) |
| `/inbox sync` | Manually trigger an inbox sync |
| `/inbox help` | Show help text |

### Replying to Emails

Reply to any email thread in your inbox channel. The plugin will send your reply as an email from your connected Outlook account. On first reply, you'll see a confirmation prompt with options to **Send**, **Send & Don't Ask Again**, or **Cancel**.

## Development

### Requirements

- Go 1.25+
- Node.js 24.13.1 (see `.nvmrc` — run `nvm i` to install)
- npm 8+

### Building

```bash
make                  # Full pipeline: check-style + test + dist
make server           # Build Go server binaries
make webapp           # Build webapp bundle
make dist             # Build plugin distribution tarball
make check-style      # Run all linters
make test             # Run all tests
MM_DEBUG=1 make dist  # Debug build (unminified JS, Go debug symbols)
```

### Deploying Locally

Enable [local mode](https://docs.mattermost.com/administration/mmctl-cli-tool.html#local-mode) on your Mattermost server:

```json
{
    "ServiceSettings": {
        "EnableLocalMode": true,
        "LocalModeSocketLocation": "/var/tmp/mattermost_local.socket"
    }
}
```

Then deploy:

```bash
make deploy
```

Or deploy with credentials:

```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_TOKEN=<your-token>
make deploy
```

### Watch Mode

Auto-rebuild and deploy webapp changes:

```bash
make watch
```

### Running Tests

```bash
# All tests
make test

# Single Go test
go test -run TestFunctionName ./server/...

# Webapp tests
cd webapp && npm run test
cd webapp && npm run test:watch
```

## Releasing

Version is determined automatically from git tags (`v1.2.3` → version `1.2.3`).

```bash
make patch       # Bump patch version (e.g. 1.0.0 → 1.0.1)
make minor       # Bump minor version (e.g. 1.0.1 → 1.1.0)
make major       # Bump major version (e.g. 1.1.0 → 2.0.0)
make patch-rc    # Bump patch release candidate
make minor-rc    # Bump minor release candidate
make major-rc    # Bump major release candidate
```

Releases can only be created from the `master` or `release` branches.
