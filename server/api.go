package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"

	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
	"github.com/wiggin77/mattermost-plugin-inbox/server/store/kvstore"
	syncpkg "github.com/wiggin77/mattermost-plugin-inbox/server/sync"
)

func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()

	// Public routes (no Mattermost auth — validated differently).
	publicRouter := router.PathPrefix("/api/v1").Subrouter()
	publicRouter.HandleFunc("/oauth2/complete", p.handleOAuthComplete).Methods(http.MethodGet)
	publicRouter.HandleFunc("/webhook/graph", p.handleGraphWebhook).Methods(http.MethodPost)

	// Authenticated routes (require logged-in Mattermost user).
	authRouter := router.PathPrefix("/api/v1").Subrouter()
	authRouter.Use(p.MattermostAuthorizationRequired)
	authRouter.HandleFunc("/oauth2/connect", p.handleOAuthConnect).Methods(http.MethodGet)
	authRouter.HandleFunc("/user/status", p.handleUserStatus).Methods(http.MethodGet)
	authRouter.HandleFunc("/user/disconnect", p.handleUserDisconnect).Methods(http.MethodPost)
	authRouter.HandleFunc("/action/send-reply", p.handleSendReplyAction).Methods(http.MethodPost)

	return router
}

// ServeHTTP routes all plugin HTTP requests.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.router.ServeHTTP(w, r)
}

func (p *Plugin) MattermostAuthorizationRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("Mattermost-User-ID")
		if userID == "" {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleOAuthConnect starts the OAuth2 flow for the requesting user.
func (p *Plugin) handleOAuthConnect(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")

	url, err := p.generateOAuthConnectURL(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// handleOAuthComplete is the callback from Azure AD after the user authorizes.
func (p *Plugin) handleOAuthComplete(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state parameter", http.StatusBadRequest)
		return
	}

	// Validate and consume the OAuth state.
	oauthState, err := p.store.GetAndDeleteOAuthState(state)
	if err != nil || oauthState == nil {
		http.Error(w, "Invalid or expired OAuth state. Please try /inbox connect again.", http.StatusBadRequest)
		return
	}

	userID := oauthState.MattermostUserID
	config := p.getConfiguration()

	// Exchange code for token.
	oauthConfig := p.getOAuthConfig()
	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		p.API.LogError("OAuth token exchange failed", "error", err, "user_id", userID)
		http.Error(w, "Failed to exchange authorization code", http.StatusInternalServerError)
		return
	}

	// Get user profile from Graph.
	tokenSource := oauthConfig.TokenSource(context.Background(), token)
	graphClient := msgraph.NewClient(tokenSource)
	me, err := graphClient.GetMe(context.Background())
	if err != nil {
		p.API.LogError("Failed to get Graph user profile", "error", err, "user_id", userID)
		http.Error(w, "Failed to get your Outlook profile", http.StatusInternalServerError)
		return
	}

	email := me.Mail
	if email == "" {
		email = me.UserPrincipalName
	}

	// Get user to determine team.
	mmUser, err := p.client.User.Get(userID)
	if err != nil {
		p.API.LogError("Failed to get Mattermost user", "error", err, "user_id", userID)
		http.Error(w, "Failed to get your Mattermost profile", http.StatusInternalServerError)
		return
	}

	// Get teams for user and use the first one.
	teams, appErr := p.API.GetTeamsForUser(userID)
	if appErr != nil || len(teams) == 0 {
		p.API.LogError("Failed to get teams for user", "error", appErr, "user_id", userID)
		http.Error(w, "Failed to find a team for your inbox channel", http.StatusInternalServerError)
		return
	}

	// Create inbox channel.
	channelID, err := syncpkg.EnsureInboxChannel(p.client, teams[0].Id, userID, p.botUserID, mmUser.Username)
	if err != nil {
		p.API.LogError("Failed to create inbox channel", "error", err, "user_id", userID)
		http.Error(w, "Failed to create your inbox channel", http.StatusInternalServerError)
		return
	}

	// Encrypt token.
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		http.Error(w, "Failed to serialize token", http.StatusInternalServerError)
		return
	}

	encryptedToken, err := kvstore.Encrypt(tokenJSON, config.EncryptionKey)
	if err != nil {
		p.API.LogError("Failed to encrypt token", "error", err, "user_id", userID)
		http.Error(w, "Failed to encrypt token", http.StatusInternalServerError)
		return
	}

	// Store user connection.
	conn := &kvstore.UserConnection{
		MattermostUserID: userID,
		RemoteEmail:      email,
		RemoteUserID:     me.ID,
		EncryptedToken:   encryptedToken,
		InboxChannelID:   channelID,
		CreatedAt:        time.Now().Unix(),
	}

	if err := p.store.StoreUserConnection(userID, conn); err != nil {
		p.API.LogError("Failed to store user connection", "error", err, "user_id", userID)
		http.Error(w, "Failed to save connection", http.StatusInternalServerError)
		return
	}

	if err := p.store.AddConnectedUser(userID); err != nil {
		p.API.LogError("Failed to add to connected users", "error", err, "user_id", userID)
	}

	// Create a Graph webhook subscription for real-time notifications.
	go func() {
		graphClient := msgraph.NewClient(oauthConfig.TokenSource(context.Background(), token))
		p.createSubscriptionForUser(context.Background(), graphClient, conn, config)
	}()

	// Post a welcome message in the inbox channel.
	p.postBotMessage(channelID, fmt.Sprintf(
		"Connected to Outlook as **%s**. New emails will appear in this channel as threaded posts. Reply to any thread to send an email reply.",
		email,
	))

	// Serve a simple completion page.
	w.Header().Set("Content-Type", "text/html")
	_, _ = fmt.Fprint(w, `<!DOCTYPE html><html><body><h2>Connected!</h2><p>Your Outlook account has been connected. You can close this window.</p><script>setTimeout(function(){window.close()},3000)</script></body></html>`)
}

// handleGraphWebhook receives Microsoft Graph change notifications.
func (p *Plugin) handleGraphWebhook(w http.ResponseWriter, r *http.Request) {
	// Handle subscription validation request.
	if validationToken := r.URL.Query().Get("validationToken"); validationToken != "" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, validationToken)
		return
	}

	// Parse notification payload.
	var payload msgraph.ChangeNotificationCollection
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Validate clientState.
	config := p.getConfiguration()
	for _, notification := range payload.Value {
		if notification.ClientState != config.WebhookSecret {
			http.Error(w, "invalid client state", http.StatusUnauthorized)
			return
		}
	}

	// Respond 202 immediately (Graph requires response within 3 seconds).
	w.WriteHeader(http.StatusAccepted)

	// Process notifications asynchronously.
	go func() {
		for _, notification := range payload.Value {
			if err := p.processChangeNotification(notification); err != nil {
				p.API.LogError("Failed to process change notification",
					"error", err,
					"subscription_id", notification.SubscriptionID,
					"resource", notification.Resource,
				)
			}
		}
	}()
}

// handleUserStatus returns the connection status for the requesting user.
func (p *Plugin) handleUserStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")
	status, err := p.getUserStatusText(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

// handleUserDisconnect disconnects the requesting user from Outlook.
func (p *Plugin) handleUserDisconnect(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("Mattermost-User-ID")
	if err := p.disconnectUser(userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "disconnected"})
}

// generateOAuthConnectURL creates a new OAuth2 URL for the user.
func (p *Plugin) generateOAuthConnectURL(userID string) (string, error) {
	config := p.getConfiguration()
	if err := config.IsValid(); err != nil {
		return "", errors.Wrap(err, "plugin is not configured")
	}

	state := model.NewId()
	oauthState := &kvstore.OAuthState{
		MattermostUserID: userID,
		CreateAt:         time.Now().Unix(),
	}

	if err := p.store.StoreOAuthState(state, oauthState); err != nil {
		return "", errors.Wrap(err, "failed to store OAuth state")
	}

	oauthConfig := p.getOAuthConfig()
	return oauthConfig.AuthCodeURL(state), nil
}

func (p *Plugin) getOAuthConfig() *msgraph.OAuthConfig {
	config := p.getConfiguration()
	siteURL := p.API.GetConfig().ServiceSettings.SiteURL
	redirectURL := ""
	if siteURL != nil {
		redirectURL = fmt.Sprintf("%s/plugins/com.mattermost.plugin-inbox/api/v1/oauth2/complete", *siteURL)
	}

	return &msgraph.OAuthConfig{
		TenantID:     config.TenantID,
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RedirectURL:  redirectURL,
	}
}

// disconnectUser removes a user's Outlook connection.
func (p *Plugin) disconnectUser(userID string) error {
	conn, err := p.store.GetUserConnection(userID)
	if err != nil {
		return errors.Wrap(err, "failed to get user connection")
	}
	if conn == nil {
		return errors.New("you are not connected to Outlook")
	}

	// Delete Graph subscription if active.
	if conn.SubscriptionID != "" {
		graphClient, clientErr := p.getGraphClientForUser(conn)
		if clientErr == nil {
			_ = graphClient.DeleteSubscription(context.Background(), conn.SubscriptionID)
		}
	}

	if err := p.store.DeleteUserConnection(userID); err != nil {
		return errors.Wrap(err, "failed to delete user connection")
	}

	if err := p.store.RemoveConnectedUser(userID); err != nil {
		p.API.LogError("Failed to remove from connected users", "error", err, "user_id", userID)
	}

	return nil
}

// getUserStatusText returns a formatted status string for the user.
func (p *Plugin) getUserStatusText(userID string) (string, error) {
	conn, err := p.store.GetUserConnection(userID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get user connection")
	}
	if conn == nil {
		return "Not connected. Run `/inbox connect` to connect your Outlook account.", nil
	}

	subStatus := "None"
	if conn.SubscriptionID != "" {
		if time.Now().Unix() < conn.SubscriptionExpiry {
			subStatus = fmt.Sprintf("Active (expires %s)", time.Unix(conn.SubscriptionExpiry, 0).Format(time.RFC822))
		} else {
			subStatus = "Expired"
		}
	}

	lastSync := "Never"
	if conn.LastSyncTimestamp > 0 {
		lastSync = time.Unix(conn.LastSyncTimestamp, 0).Format(time.RFC822)
	}

	return fmt.Sprintf("**Outlook Connection Status**\n\n"+
		"| Field | Value |\n"+
		"|-------|-------|\n"+
		"| Email | %s |\n"+
		"| Webhook Subscription | %s |\n"+
		"| Last Sync | %s |\n"+
		"| Connected Since | %s |",
		conn.RemoteEmail,
		subStatus,
		lastSync,
		time.Unix(conn.CreatedAt, 0).Format(time.RFC822),
	), nil
}

// postBotMessage posts a message as the bot in the specified channel.
func (p *Plugin) postBotMessage(channelID, message string) {
	post := &model.Post{
		UserId:    p.botUserID,
		ChannelId: channelID,
		Message:   message,
	}
	if err := p.client.Post.CreatePost(post); err != nil {
		p.API.LogError("Failed to post bot message", "error", err, "channel_id", channelID)
	}
}

// processChangeNotification handles a single Graph webhook change notification.
func (p *Plugin) processChangeNotification(notification msgraph.ChangeNotification) error {
	if notification.ChangeType != "created" {
		return nil // Only process new messages.
	}

	messageID := notification.ResourceData.ID
	if messageID == "" {
		return errors.New("notification missing resource data ID")
	}

	// Find the user associated with this subscription.
	users, err := p.store.GetConnectedUsers()
	if err != nil {
		return errors.Wrap(err, "failed to get connected users")
	}

	for _, userID := range users {
		conn, err := p.store.GetUserConnection(userID)
		if err != nil || conn == nil {
			continue
		}
		if conn.SubscriptionID != notification.SubscriptionID {
			continue
		}

		// Found the user. Fetch the full message and process it.
		graphClient, err := p.getGraphClientForUser(conn)
		if err != nil {
			return errors.Wrap(err, "failed to create Graph client")
		}

		msg, err := graphClient.GetMessage(context.Background(), messageID)
		if err != nil {
			return errors.Wrapf(err, "failed to get message %s", messageID)
		}

		if err := p.syncEngine.ProcessMessage(context.Background(), graphClient, conn, msg); err != nil {
			return errors.Wrap(err, "failed to process message")
		}

		// Update last sync timestamp.
		conn.LastSyncTimestamp = msg.ReceivedDateTime.Unix()
		if err := p.store.StoreUserConnection(userID, conn); err != nil {
			p.API.LogError("Failed to update last sync timestamp", "error", err)
		}

		return nil
	}

	p.API.LogWarn("No user found for subscription", "subscription_id", notification.SubscriptionID)
	return nil
}

// handleSendReplyAction handles interactive button clicks for email reply confirmation.
func (p *Plugin) handleSendReplyAction(w http.ResponseWriter, r *http.Request) {
	var request model.PostActionIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	userID := request.UserId
	action, _ := request.Context["action"].(string)

	// Handle cancel — just update the confirmation post.
	if action == actionCancel {
		p.writeActionResponse(w, "Reply not sent.")
		return
	}

	// Get the original post to send as email.
	postID, _ := request.Context["post_id"].(string)
	outlookMessageID, _ := request.Context["outlook_message_id"].(string)
	conversationID, _ := request.Context["conversation_id"].(string)
	rootPostID, _ := request.Context["root_post_id"].(string)

	if postID == "" || outlookMessageID == "" {
		p.writeActionResponse(w, "Error: missing context data.")
		return
	}

	// Verify user connection.
	conn, err := p.store.GetUserConnection(userID)
	if err != nil || conn == nil {
		p.writeActionResponse(w, "Error: you are not connected to Outlook.")
		return
	}

	// Get the original reply post.
	post, err := p.client.Post.GetPost(postID)
	if err != nil {
		p.writeActionResponse(w, "Error: could not find the original reply.")
		return
	}

	// Build the post mapping for sendEmailReply.
	mapping := &kvstore.PostMapping{
		MattermostPostID: rootPostID,
		OutlookMessageID: outlookMessageID,
		ConversationID:   conversationID,
		MattermostUserID: userID,
	}

	// Send the email.
	p.sendEmailReply(conn, mapping, post)

	// If "Don't Ask Again", save the preference.
	if action == actionSendAlways {
		conn.SkipReplyConfirmation = true
		if storeErr := p.store.StoreUserConnection(userID, conn); storeErr != nil {
			p.API.LogError("Failed to save skip confirmation preference", "error", storeErr, "user_id", userID)
		}
		p.writeActionResponse(w, "Email sent. Future replies will be sent automatically.")
		return
	}

	p.writeActionResponse(w, "Email sent.")
}

// writeActionResponse writes a PostActionIntegrationResponse that replaces
// the confirmation post's attachment with a result message.
func (p *Plugin) writeActionResponse(w http.ResponseWriter, message string) {
	resp := &model.PostActionIntegrationResponse{
		Update: &model.Post{
			Message: message,
			Props:   model.StringInterface{},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
