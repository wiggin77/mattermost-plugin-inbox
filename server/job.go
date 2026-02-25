package main

import (
	"context"
	"time"

	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
	"github.com/wiggin77/mattermost-plugin-inbox/server/store/kvstore"
)

// runEmailPollJob polls each connected user's inbox for new emails.
// This runs on a configurable interval as a fallback when webhooks are unavailable.
func (p *Plugin) runEmailPollJob() {
	users, err := p.store.GetConnectedUsers()
	if err != nil {
		p.API.LogError("Failed to get connected users for polling", "error", err)
		return
	}

	if len(users) == 0 {
		return
	}

	config := p.getConfiguration()
	if config.EnableDiagnostics {
		p.API.LogInfo("Running email poll job", "connected_users", len(users))
	}

	for _, userID := range users {
		conn, err := p.store.GetUserConnection(userID)
		if err != nil || conn == nil {
			continue
		}

		// Skip users with active webhook subscriptions.
		if conn.SubscriptionID != "" && time.Now().Unix() < conn.SubscriptionExpiry-300 {
			continue
		}

		graphClient, err := p.getGraphClientForUser(conn)
		if err != nil {
			p.API.LogError("Failed to create Graph client for polling", "error", err, "user_id", userID)
			continue
		}

		if err := p.syncEngine.PollUserInbox(context.Background(), graphClient, conn); err != nil {
			p.API.LogError("Failed to poll inbox", "error", err, "user_id", userID)
		}
	}
}

// runSubscriptionRenewalJob renews Graph webhook subscriptions that are expiring soon.
func (p *Plugin) runSubscriptionRenewalJob() {
	users, err := p.store.GetConnectedUsers()
	if err != nil {
		p.API.LogError("Failed to get connected users for subscription renewal", "error", err)
		return
	}

	config := p.getConfiguration()

	for _, userID := range users {
		conn, err := p.store.GetUserConnection(userID)
		if err != nil || conn == nil {
			continue
		}

		graphClient, err := p.getGraphClientForUser(conn)
		if err != nil {
			p.API.LogError("Failed to create Graph client for subscription renewal", "error", err, "user_id", userID)
			continue
		}

		ctx := context.Background()

		// Renew if subscription expires within 24 hours.
		if conn.SubscriptionID != "" && time.Now().Unix() > conn.SubscriptionExpiry-(24*3600) {
			newExpiry := time.Now().Add(6 * 24 * time.Hour) // 6 days (max is ~7 days for mail)
			err := graphClient.RenewSubscription(ctx, conn.SubscriptionID, newExpiry)
			if err != nil {
				p.API.LogWarn("Subscription renewal failed, recreating", "error", err, "user_id", userID)
				p.createSubscriptionForUser(ctx, graphClient, conn, config)
				continue
			}
			conn.SubscriptionExpiry = newExpiry.Unix()
			if err := p.store.StoreUserConnection(userID, conn); err != nil {
				p.API.LogError("Failed to update subscription expiry", "error", err, "user_id", userID)
			}
			continue
		}

		// Create subscription if none exists.
		if conn.SubscriptionID == "" {
			p.createSubscriptionForUser(ctx, graphClient, conn, config)
		}
	}
}

// createSubscriptionForUser creates a Graph webhook subscription for a user.
func (p *Plugin) createSubscriptionForUser(ctx context.Context, graphClient *msgraph.Client, conn *kvstore.UserConnection, config *configuration) {
	siteURL := p.API.GetConfig().ServiceSettings.SiteURL
	if siteURL == nil || *siteURL == "" {
		p.API.LogError("Site URL is not configured, cannot create webhook subscription")
		return
	}

	notificationURL := *siteURL + "/plugins/com.mattermost.plugin-inbox/api/v1/webhook/graph"
	expiry := time.Now().Add(6 * 24 * time.Hour)

	sub, err := graphClient.CreateSubscription(ctx, &msgraph.Subscription{
		ChangeType:         "created",
		NotificationURL:    notificationURL,
		Resource:           "me/mailFolders('Inbox')/messages",
		ExpirationDateTime: expiry,
		ClientState:        config.WebhookSecret,
	})
	if err != nil {
		p.API.LogError("Failed to create webhook subscription", "error", err, "user_id", conn.MattermostUserID)
		return
	}

	conn.SubscriptionID = sub.ID
	conn.SubscriptionExpiry = expiry.Unix()
	if err := p.store.StoreUserConnection(conn.MattermostUserID, conn); err != nil {
		p.API.LogError("Failed to store subscription info", "error", err, "user_id", conn.MattermostUserID)
	}
}
