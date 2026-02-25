package kvstore

// UserConnection stores a connected user's metadata and encrypted OAuth token.
type UserConnection struct {
	MattermostUserID      string `json:"mm_user_id"`
	RemoteEmail           string `json:"remote_email"`
	RemoteUserID          string `json:"remote_user_id"`
	EncryptedToken        []byte `json:"encrypted_token"`
	InboxChannelID        string `json:"inbox_channel_id"`
	LastSyncTimestamp     int64  `json:"last_sync_ts"`
	SubscriptionID        string `json:"subscription_id"`
	SubscriptionExpiry    int64  `json:"subscription_expiry"`
	CreatedAt             int64  `json:"created_at"`
	SkipReplyConfirmation bool   `json:"skip_reply_confirmation"`
}

// OAuthState is temporary storage for OAuth2 CSRF validation during the auth flow.
type OAuthState struct {
	MattermostUserID string `json:"mm_user_id"`
	CreateAt         int64  `json:"create_at"`
}

// ConversationMapping maps an Outlook conversationId to a Mattermost root post (thread).
type ConversationMapping struct {
	ConversationID   string `json:"conversation_id"`
	MattermostPostID string `json:"mm_post_id"`
	MattermostUserID string `json:"mm_user_id"`
	ChannelID        string `json:"channel_id"`
	Subject          string `json:"subject"`
	LastMessageID    string `json:"last_message_id"`
}

// MessageMapping maps an individual Outlook message to a Mattermost post (for deduplication).
type MessageMapping struct {
	OutlookMessageID string `json:"outlook_msg_id"`
	MattermostPostID string `json:"mm_post_id"`
	ConversationID   string `json:"conversation_id"`
	MattermostUserID string `json:"mm_user_id"`
}

// PostMapping provides a reverse lookup from a Mattermost post to an Outlook message.
type PostMapping struct {
	MattermostPostID string `json:"mm_post_id"`
	OutlookMessageID string `json:"outlook_msg_id"`
	ConversationID   string `json:"conversation_id"`
	MattermostUserID string `json:"mm_user_id"`
	IsRootPost       bool   `json:"is_root_post"`
}
