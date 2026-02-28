package kvstore

import (
	"crypto/sha256"
	"fmt"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

const (
	kvPrefixConversation  = "conv_"
	kvPrefixMessage       = "msg_"
	kvPrefixPost          = "post_"
	kvPrefixSentMessage   = "sent_"
	sentMessageTTLSeconds = 60
)

// hashKey produces a truncated SHA-256 hex hash suitable for KV key suffixes.
// KVStore keys have a 50-byte limit; the prefix + 16 hex chars fits well.
func hashKey(input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}

func (kv Client) StoreConversationMapping(mapping *ConversationMapping) error {
	key := kvPrefixConversation + hashKey(mapping.ConversationID)
	_, err := kv.client.KV.Set(key, mapping)
	if err != nil {
		return errors.Wrap(err, "failed to store conversation mapping")
	}
	return nil
}

func (kv Client) GetConversationMapping(conversationID string) (*ConversationMapping, error) {
	key := kvPrefixConversation + hashKey(conversationID)
	var mapping ConversationMapping
	err := kv.client.KV.Get(key, &mapping)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get conversation mapping")
	}
	if mapping.ConversationID == "" {
		return nil, nil
	}
	return &mapping, nil
}

func (kv Client) StoreMessageMapping(mapping *MessageMapping) error {
	key := kvPrefixMessage + hashKey(mapping.OutlookMessageID)
	_, err := kv.client.KV.Set(key, mapping)
	if err != nil {
		return errors.Wrap(err, "failed to store message mapping")
	}
	return nil
}

func (kv Client) GetMessageMapping(outlookMessageID string) (*MessageMapping, error) {
	key := kvPrefixMessage + hashKey(outlookMessageID)
	var mapping MessageMapping
	err := kv.client.KV.Get(key, &mapping)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get message mapping")
	}
	if mapping.OutlookMessageID == "" {
		return nil, nil
	}
	return &mapping, nil
}

func (kv Client) DeleteMessageMapping(outlookMessageID string) error {
	key := kvPrefixMessage + hashKey(outlookMessageID)
	if err := kv.client.KV.Delete(key); err != nil {
		return errors.Wrap(err, "failed to delete message mapping")
	}
	return nil
}

func (kv Client) StorePostMapping(mapping *PostMapping) error {
	key := kvPrefixPost + mapping.MattermostPostID
	_, err := kv.client.KV.Set(key, mapping)
	if err != nil {
		return errors.Wrap(err, "failed to store post mapping")
	}
	return nil
}

func (kv Client) GetPostMapping(postID string) (*PostMapping, error) {
	key := kvPrefixPost + postID
	var mapping PostMapping
	err := kv.client.KV.Get(key, &mapping)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get post mapping")
	}
	if mapping.MattermostPostID == "" {
		return nil, nil
	}
	return &mapping, nil
}

func (kv Client) DeletePostMapping(postID string) error {
	key := kvPrefixPost + postID
	if err := kv.client.KV.Delete(key); err != nil {
		return errors.Wrap(err, "failed to delete post mapping")
	}
	return nil
}

func (kv Client) MarkMessageAsSent(outlookMessageID string) error {
	key := kvPrefixSentMessage + hashKey(outlookMessageID)
	_, err := kv.client.KV.Set(key, true, pluginapi.SetExpiry(sentMessageTTLSeconds))
	if err != nil {
		return errors.Wrap(err, "failed to mark message as sent")
	}
	return nil
}

func (kv Client) IsMessageSent(outlookMessageID string) (bool, error) {
	key := kvPrefixSentMessage + hashKey(outlookMessageID)
	var sent bool
	err := kv.client.KV.Get(key, &sent)
	if err != nil {
		return false, errors.Wrap(err, "failed to check sent message")
	}
	return sent, nil
}
