package sync_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
	mockgraph "github.com/wiggin77/mattermost-plugin-inbox/server/testhelper"
)

// staticTokenSource provides a fixed OAuth2 token for tests.
type staticTokenSource struct {
	token *oauth2.Token
}

func (s *staticTokenSource) Token() (*oauth2.Token, error) {
	return s.token, nil
}

func newTestGraphClient(t *testing.T) (*msgraph.Client, *mockgraph.MockGraphServer) {
	t.Helper()
	mock := mockgraph.NewMockGraphServer(t)
	ts := &staticTokenSource{
		token: &oauth2.Token{
			AccessToken: "mock-token",
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(1 * time.Hour),
		},
	}
	client := msgraph.NewClientWithBaseURL(ts, mock.BaseURL())
	return client, mock
}

func TestMockGraphGetMe(t *testing.T) {
	client, _ := newTestGraphClient(t)

	user, err := client.GetMe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mock-graph-user-id", user.ID)
	assert.Equal(t, "testuser@contoso.com", user.Mail)
	assert.Equal(t, "Test User", user.DisplayName)
}

func TestMockGraphGetMeCustomProfile(t *testing.T) {
	client, mock := newTestGraphClient(t)

	mock.SetUserProfile(&msgraph.User{
		ID:                "custom-id",
		DisplayName:       "Custom User",
		Mail:              "custom@example.com",
		UserPrincipalName: "custom@example.com",
	})

	user, err := client.GetMe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "custom-id", user.ID)
	assert.Equal(t, "custom@example.com", user.Mail)
}

func TestMockGraphListInboxMessages(t *testing.T) {
	client, mock := newTestGraphClient(t)

	mock.AddMessage(&msgraph.Message{
		ID:             "msg-001",
		ConversationID: "conv-001",
		Subject:        "Hello from Outlook",
		From: msgraph.Recipient{
			EmailAddress: msgraph.EmailAddress{
				Name:    "Sender One",
				Address: "sender@contoso.com",
			},
		},
		ReceivedDateTime: time.Now(),
		Body: msgraph.ItemBody{
			ContentType: "html",
			Content:     "<p>Test email body</p>",
		},
	})

	resp, err := client.ListInboxMessages(context.Background(), time.Now().Add(-1*time.Hour), 50)
	require.NoError(t, err)
	require.Len(t, resp.Value, 1)
	assert.Equal(t, "msg-001", resp.Value[0].ID)
	assert.Equal(t, "Hello from Outlook", resp.Value[0].Subject)
}

func TestMockGraphListEmptyInbox(t *testing.T) {
	client, _ := newTestGraphClient(t)

	resp, err := client.ListInboxMessages(context.Background(), time.Now().Add(-1*time.Hour), 50)
	require.NoError(t, err)
	assert.Empty(t, resp.Value)
}

func TestMockGraphGetMessage(t *testing.T) {
	client, mock := newTestGraphClient(t)

	mock.AddMessage(&msgraph.Message{
		ID:             "msg-002",
		ConversationID: "conv-002",
		Subject:        "Specific Message",
		HasAttachments: true,
		Attachments: []msgraph.Attachment{
			{
				ID:           "att-001",
				Name:         "report.pdf",
				ContentType:  "application/pdf",
				Size:         1024,
				ContentBytes: base64.StdEncoding.EncodeToString([]byte("fake pdf content")),
			},
		},
		Body: msgraph.ItemBody{
			ContentType: "html",
			Content:     "<p>See attached report.</p>",
		},
	})

	msg, err := client.GetMessage(context.Background(), "msg-002")
	require.NoError(t, err)
	assert.Equal(t, "Specific Message", msg.Subject)
	assert.True(t, msg.HasAttachments)
	require.Len(t, msg.Attachments, 1)
	assert.Equal(t, "report.pdf", msg.Attachments[0].Name)
}

func TestMockGraphGetMessageNotFound(t *testing.T) {
	client, _ := newTestGraphClient(t)

	_, err := client.GetMessage(context.Background(), "nonexistent-id")
	require.Error(t, err)
}

func TestMockGraphReplyToMessage(t *testing.T) {
	client, mock := newTestGraphClient(t)

	mock.AddMessage(&msgraph.Message{
		ID:      "msg-003",
		Subject: "Thread to reply to",
	})

	err := client.ReplyToMessage(context.Background(), "msg-003", "<p>My reply</p>")
	require.NoError(t, err)

	replies := mock.RepliesSent()
	require.Len(t, replies, 1)
	assert.Equal(t, "msg-003", replies[0].MessageID)
	assert.Equal(t, "<p>My reply</p>", replies[0].HTMLBody)
}

func TestMockGraphMultipleReplies(t *testing.T) {
	client, mock := newTestGraphClient(t)

	err := client.ReplyToMessage(context.Background(), "msg-a", "<p>Reply 1</p>")
	require.NoError(t, err)
	err = client.ReplyToMessage(context.Background(), "msg-b", "<p>Reply 2</p>")
	require.NoError(t, err)

	replies := mock.RepliesSent()
	require.Len(t, replies, 2)
}

func TestMockGraphSubscriptionLifecycle(t *testing.T) {
	client, _ := newTestGraphClient(t)
	ctx := context.Background()

	// Create subscription.
	sub, err := client.CreateSubscription(ctx, &msgraph.Subscription{
		ChangeType:         "created,updated,deleted",
		NotificationURL:    "https://example.com/webhook",
		Resource:           "me/mailFolders('Inbox')/messages",
		ExpirationDateTime: time.Now().Add(6 * 24 * time.Hour),
		ClientState:        "test-secret",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, sub.ID)
	assert.Equal(t, "created,updated,deleted", sub.ChangeType)

	// Renew subscription.
	newExpiry := time.Now().Add(7 * 24 * time.Hour)
	err = client.RenewSubscription(ctx, sub.ID, newExpiry)
	require.NoError(t, err)

	// Delete subscription.
	err = client.DeleteSubscription(ctx, sub.ID)
	require.NoError(t, err)
}

func TestMockGraphMultipleMessagesInConversation(t *testing.T) {
	client, mock := newTestGraphClient(t)

	mock.AddMessage(&msgraph.Message{
		ID:             "msg-conv-1",
		ConversationID: "conv-shared",
		Subject:        "Thread Start",
		From: msgraph.Recipient{
			EmailAddress: msgraph.EmailAddress{
				Name:    "Alice",
				Address: "alice@contoso.com",
			},
		},
		ReceivedDateTime: time.Now().Add(-10 * time.Minute),
		Body:             msgraph.ItemBody{ContentType: "html", Content: "<p>Starting a thread</p>"},
	})
	mock.AddMessage(&msgraph.Message{
		ID:             "msg-conv-2",
		ConversationID: "conv-shared",
		Subject:        "Re: Thread Start",
		From: msgraph.Recipient{
			EmailAddress: msgraph.EmailAddress{
				Name:    "Bob",
				Address: "bob@contoso.com",
			},
		},
		ReceivedDateTime: time.Now().Add(-5 * time.Minute),
		Body:             msgraph.ItemBody{ContentType: "html", Content: "<p>Reply to thread</p>"},
	})

	resp, err := client.ListInboxMessages(context.Background(), time.Now().Add(-1*time.Hour), 50)
	require.NoError(t, err)
	require.Len(t, resp.Value, 2)
	assert.Equal(t, "conv-shared", resp.Value[0].ConversationID)
	assert.Equal(t, "conv-shared", resp.Value[1].ConversationID)
}

func TestMockGraphClearMessages(t *testing.T) {
	client, mock := newTestGraphClient(t)

	mock.AddMessage(&msgraph.Message{
		ID:      "msg-to-clear",
		Subject: "Will be cleared",
	})

	resp, err := client.ListInboxMessages(context.Background(), time.Now().Add(-1*time.Hour), 50)
	require.NoError(t, err)
	require.Len(t, resp.Value, 1)

	mock.ClearMessages()

	resp, err = client.ListInboxMessages(context.Background(), time.Now().Add(-1*time.Hour), 50)
	require.NoError(t, err)
	assert.Empty(t, resp.Value)
}
