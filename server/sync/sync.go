package sync

import (
	"bytes"
	"context"
	"encoding/base64"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"

	"github.com/wiggin77/mattermost-plugin-inbox/server/email"
	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
	"github.com/wiggin77/mattermost-plugin-inbox/server/store/kvstore"
)

// Engine orchestrates email synchronization from Outlook to Mattermost.
type Engine struct {
	client            *pluginapi.Client
	store             kvstore.KVStore
	botUserID         string
	maxAttachmentSize int64
	logError          func(msg string, keyValuePairs ...any)
	logInfo           func(msg string, keyValuePairs ...any)
}

// NewEngine creates a new sync engine.
func NewEngine(client *pluginapi.Client, store kvstore.KVStore, botUserID string, maxAttachmentSize int64, logError, logInfo func(string, ...any)) *Engine {
	return &Engine{
		client:            client,
		store:             store,
		botUserID:         botUserID,
		maxAttachmentSize: maxAttachmentSize,
		logError:          logError,
		logInfo:           logInfo,
	}
}

// ProcessMessage takes a Graph message and creates/updates the appropriate Mattermost post.
// It handles deduplication, threading, and attachments.
func (e *Engine) ProcessMessage(ctx context.Context, graphClient *msgraph.Client, conn *kvstore.UserConnection, msg *msgraph.Message) error {
	// Check if we've already processed this message (dedup).
	existing, err := e.store.GetMessageMapping(msg.ID)
	if err != nil {
		return errors.Wrap(err, "failed to check message dedup")
	}
	if existing != nil {
		return nil // Already processed.
	}

	// Check if this was sent by us (reply loop prevention).
	sent, err := e.store.IsMessageSent(msg.ID)
	if err != nil {
		e.logError("Failed to check sent message", "error", err)
	}
	if sent {
		return nil // Skip messages we sent.
	}

	// Upload attachments if any.
	var fileIDs []string
	if msg.HasAttachments {
		fileIDs, err = e.uploadAttachments(ctx, graphClient, conn, msg)
		if err != nil {
			e.logError("Failed to upload some attachments", "error", err, "message_id", msg.ID)
		}
	}

	// Check if we have an existing conversation thread.
	convMapping, err := e.store.GetConversationMapping(msg.ConversationID)
	if err != nil {
		return errors.Wrap(err, "failed to get conversation mapping")
	}

	if convMapping == nil {
		// New conversation — create a root post.
		return e.createRootPost(conn, msg, fileIDs)
	}

	// Existing conversation — create a reply post in the thread.
	return e.createReplyPost(conn, convMapping, msg, fileIDs)
}

func (e *Engine) createRootPost(conn *kvstore.UserConnection, msg *msgraph.Message, fileIDs []string) error {
	postBody := email.RenderRootPost(msg)
	postBody = e.appendAttachmentNotes(postBody, msg)

	post := &model.Post{
		UserId:    e.botUserID,
		ChannelId: conn.InboxChannelID,
		Message:   postBody,
		FileIds:   fileIDs,
	}

	if err := e.client.Post.CreatePost(post); err != nil {
		return errors.Wrap(err, "failed to create root post")
	}

	// Store conversation mapping.
	if err := e.store.StoreConversationMapping(&kvstore.ConversationMapping{
		ConversationID:   msg.ConversationID,
		MattermostPostID: post.Id,
		MattermostUserID: conn.MattermostUserID,
		ChannelID:        conn.InboxChannelID,
		Subject:          msg.Subject,
		LastMessageID:    msg.ID,
	}); err != nil {
		return errors.Wrap(err, "failed to store conversation mapping")
	}

	// Store message and post mappings.
	return e.storeMessageMappings(conn, msg, post.Id, true)
}

func (e *Engine) createReplyPost(conn *kvstore.UserConnection, convMapping *kvstore.ConversationMapping, msg *msgraph.Message, fileIDs []string) error {
	postBody := email.RenderReplyPost(msg)
	postBody = e.appendAttachmentNotes(postBody, msg)

	post := &model.Post{
		UserId:    e.botUserID,
		ChannelId: conn.InboxChannelID,
		RootId:    convMapping.MattermostPostID,
		Message:   postBody,
		FileIds:   fileIDs,
	}

	if err := e.client.Post.CreatePost(post); err != nil {
		return errors.Wrap(err, "failed to create reply post")
	}

	// Update conversation mapping with latest message.
	convMapping.LastMessageID = msg.ID
	if err := e.store.StoreConversationMapping(convMapping); err != nil {
		e.logError("Failed to update conversation mapping", "error", err)
	}

	return e.storeMessageMappings(conn, msg, post.Id, false)
}

func (e *Engine) storeMessageMappings(conn *kvstore.UserConnection, msg *msgraph.Message, postID string, isRoot bool) error {
	if err := e.store.StoreMessageMapping(&kvstore.MessageMapping{
		OutlookMessageID: msg.ID,
		MattermostPostID: postID,
		ConversationID:   msg.ConversationID,
		MattermostUserID: conn.MattermostUserID,
	}); err != nil {
		return errors.Wrap(err, "failed to store message mapping")
	}

	if err := e.store.StorePostMapping(&kvstore.PostMapping{
		MattermostPostID: postID,
		OutlookMessageID: msg.ID,
		ConversationID:   msg.ConversationID,
		MattermostUserID: conn.MattermostUserID,
		IsRootPost:       isRoot,
	}); err != nil {
		return errors.Wrap(err, "failed to store post mapping")
	}

	return nil
}

func (e *Engine) uploadAttachments(ctx context.Context, graphClient *msgraph.Client, conn *kvstore.UserConnection, msg *msgraph.Message) ([]string, error) {
	var fileIDs []string

	for _, att := range msg.Attachments {
		if att.IsInline {
			continue // Skip inline images for now.
		}

		if att.Size > e.maxAttachmentSize {
			continue // Will be noted in the post body.
		}

		// Decode base64 content (Graph includes it in expanded attachments).
		if att.ContentBytes == "" {
			continue
		}

		data, err := base64.StdEncoding.DecodeString(att.ContentBytes)
		if err != nil {
			e.logError("Failed to decode attachment", "error", err, "name", att.Name)
			continue
		}

		fileInfo, err := e.client.File.Upload(bytes.NewReader(data), att.Name, conn.InboxChannelID)
		if err != nil {
			e.logError("Failed to upload attachment", "error", err, "name", att.Name)
			continue
		}

		fileIDs = append(fileIDs, fileInfo.Id)
	}

	return fileIDs, nil
}

func (e *Engine) appendAttachmentNotes(body string, msg *msgraph.Message) string {
	for _, att := range msg.Attachments {
		if att.IsInline {
			continue
		}
		if att.Size > e.maxAttachmentSize {
			sizeMB := att.Size / (1024 * 1024)
			body += "\n\n" + email.RenderAttachmentNote(att.Name, sizeMB)
		}
	}
	return body
}
