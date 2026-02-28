package kvstore

// KVStore defines all storage operations for the plugin.
type KVStore interface {
	// User connection
	StoreUserConnection(userID string, conn *UserConnection) error
	GetUserConnection(userID string) (*UserConnection, error)
	DeleteUserConnection(userID string) error

	// Connected users index
	AddConnectedUser(userID string) error
	RemoveConnectedUser(userID string) error
	GetConnectedUsers() ([]string, error)

	// OAuth state
	StoreOAuthState(state string, oauthState *OAuthState) error
	GetAndDeleteOAuthState(state string) (*OAuthState, error)

	// Conversation mapping (email thread → MM thread)
	StoreConversationMapping(mapping *ConversationMapping) error
	GetConversationMapping(conversationID string) (*ConversationMapping, error)

	// Message mapping (individual email → MM post, dedup)
	StoreMessageMapping(mapping *MessageMapping) error
	GetMessageMapping(outlookMessageID string) (*MessageMapping, error)
	DeleteMessageMapping(outlookMessageID string) error

	// Post mapping (reverse lookup: MM post → email)
	StorePostMapping(mapping *PostMapping) error
	GetPostMapping(postID string) (*PostMapping, error)
	DeletePostMapping(postID string) error

	// Sent message tracking (reply loop prevention)
	MarkMessageAsSent(outlookMessageID string) error
	IsMessageSent(outlookMessageID string) (bool, error)
}
