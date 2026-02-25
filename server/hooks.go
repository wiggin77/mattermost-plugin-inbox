package main

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/wiggin77/mattermost-plugin-inbox/server/email"
	"github.com/wiggin77/mattermost-plugin-inbox/server/store/kvstore"
)

const (
	actionSend       = "send"
	actionSendAlways = "send_always"
	actionCancel     = "cancel"
)

// MessageHasBeenPosted is invoked after a message has been posted.
// We use this to detect replies in inbox channels and either send them as
// email replies immediately (if the user opted out of confirmation) or prompt
// with interactive buttons.
func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	// Ignore bot posts to prevent infinite loops.
	if post.UserId == p.botUserID {
		return
	}
	if post.GetProp("from_webhook") == "true" {
		return
	}

	// Only process threaded replies (must have a RootId).
	if post.RootId == "" {
		return
	}

	// Check if the author is a connected user with this channel as their inbox.
	conn, err := p.store.GetUserConnection(post.UserId)
	if err != nil || conn == nil {
		return
	}
	if conn.InboxChannelID != post.ChannelId {
		return
	}

	// Look up the email conversation from the root post.
	postMapping, err := p.store.GetPostMapping(post.RootId)
	if err != nil || postMapping == nil {
		return // Not a reply to an email thread.
	}

	// If the user opted out of confirmation, send immediately.
	if conn.SkipReplyConfirmation {
		go p.sendEmailReply(conn, postMapping, post)
		return
	}

	// Otherwise, post an interactive confirmation prompt.
	p.postReplyConfirmation(conn, postMapping, post)
}

// postReplyConfirmation posts a bot message with action buttons asking the user
// to confirm sending the reply as an email.
func (p *Plugin) postReplyConfirmation(conn *kvstore.UserConnection, mapping *kvstore.PostMapping, post *model.Post) {
	siteURL := ""
	if s := p.API.GetConfig().ServiceSettings.SiteURL; s != nil {
		siteURL = *s
	}
	actionURL := fmt.Sprintf("%s/plugins/com.mattermost.plugin-inbox/api/v1/action/send-reply", siteURL)

	attachment := &model.SlackAttachment{
		Text: "Send this as an email reply?",
		Actions: []*model.PostAction{
			{
				Id:    "send",
				Name:  "Send",
				Type:  "button",
				Style: "primary",
				Integration: &model.PostActionIntegration{
					URL: actionURL,
					Context: map[string]any{
						"action":             actionSend,
						"post_id":            post.Id,
						"root_post_id":       post.RootId,
						"outlook_message_id": mapping.OutlookMessageID,
						"conversation_id":    mapping.ConversationID,
					},
				},
			},
			{
				Id:    "send_always",
				Name:  "Send & Don't Ask Again",
				Type:  "button",
				Style: "success",
				Integration: &model.PostActionIntegration{
					URL: actionURL,
					Context: map[string]any{
						"action":             actionSendAlways,
						"post_id":            post.Id,
						"root_post_id":       post.RootId,
						"outlook_message_id": mapping.OutlookMessageID,
						"conversation_id":    mapping.ConversationID,
					},
				},
			},
			{
				Id:    "cancel",
				Name:  "Cancel",
				Type:  "button",
				Style: "danger",
				Integration: &model.PostActionIntegration{
					URL: actionURL,
					Context: map[string]any{
						"action": actionCancel,
					},
				},
			},
		},
	}

	confirmPost := &model.Post{
		UserId:    p.botUserID,
		ChannelId: conn.InboxChannelID,
		RootId:    post.RootId,
	}
	model.ParseSlackAttachment(confirmPost, []*model.SlackAttachment{attachment})

	if err := p.client.Post.CreatePost(confirmPost); err != nil {
		p.API.LogError("Failed to post reply confirmation", "error", err)
	}
}

// sendEmailReply sends the user's Mattermost reply as an email via Graph API.
func (p *Plugin) sendEmailReply(conn *kvstore.UserConnection, mapping *kvstore.PostMapping, post *model.Post) {
	graphClient, err := p.getGraphClientForUser(conn)
	if err != nil {
		p.API.LogError("Failed to create Graph client for reply", "error", err, "user_id", conn.MattermostUserID)
		p.sendEphemeralError(conn, "Failed to send email reply: could not connect to Outlook.")
		return
	}

	// Convert Mattermost markdown to HTML for the email body.
	htmlBody := email.MarkdownToHTML(post.Message)

	// Send reply via Graph API.
	if err := graphClient.ReplyToMessage(context.Background(), mapping.OutlookMessageID, htmlBody); err != nil {
		p.API.LogError("Failed to send email reply", "error", err, "user_id", conn.MattermostUserID)
		p.sendEphemeralError(conn, "Failed to send email reply: "+err.Error())
		return
	}

	// Mark as sent to prevent reply loop when the sent message comes back via webhook.
	if err := p.store.MarkMessageAsSent(mapping.OutlookMessageID); err != nil {
		p.API.LogError("Failed to mark message as sent", "error", err)
	}

	// Add a reaction to indicate success.
	_ = p.client.Post.AddReaction(&model.Reaction{
		UserId:    p.botUserID,
		PostId:    post.Id,
		EmojiName: "outbox_tray",
	})
}

func (p *Plugin) sendEphemeralError(conn *kvstore.UserConnection, message string) {
	p.API.SendEphemeralPost(conn.MattermostUserID, &model.Post{
		ChannelId: conn.InboxChannelID,
		Message:   message,
	})
}
