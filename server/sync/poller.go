package sync

import (
	"context"
	"time"

	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
	"github.com/wiggin77/mattermost-plugin-inbox/server/store/kvstore"
)

// PollUserInbox fetches new emails for a user and processes them through the sync engine.
func (e *Engine) PollUserInbox(ctx context.Context, graphClient *msgraph.Client, conn *kvstore.UserConnection) error {
	since := time.Unix(conn.LastSyncTimestamp, 0)
	if conn.LastSyncTimestamp == 0 {
		// First sync — only get messages from the connection time.
		since = time.Unix(conn.CreatedAt, 0)
	}

	resp, err := graphClient.ListInboxMessages(ctx, since, 50)
	if err != nil {
		return err
	}

	for i := range resp.Value {
		msg := &resp.Value[i]
		if err := e.ProcessMessage(ctx, graphClient, conn, msg); err != nil {
			e.logError("Failed to process message during poll",
				"error", err,
				"message_id", msg.ID,
				"user_id", conn.MattermostUserID,
			)
			continue
		}
	}

	// Update last sync timestamp.
	conn.LastSyncTimestamp = time.Now().Unix()
	if err := e.store.StoreUserConnection(conn.MattermostUserID, conn); err != nil {
		e.logError("Failed to update last sync timestamp", "error", err)
	}

	return nil
}
