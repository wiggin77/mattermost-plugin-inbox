package sync

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

const (
	inboxChannelPrefix      = "inbox-"
	inboxChannelDisplayName = "Inbox"
	inboxChannelPurpose     = "Your Outlook email inbox synced by the Inbox plugin."
)

// EnsureInboxChannel creates or retrieves the private inbox channel for a user.
// Returns the channel ID.
func EnsureInboxChannel(client *pluginapi.Client, teamID, userID, botUserID, username string) (string, error) {
	channelName := inboxChannelPrefix + username

	// Try to get existing channel first.
	channel, err := client.Channel.GetByName(teamID, channelName, false)
	if err == nil && channel != nil {
		// Ensure user and bot are members.
		ensureChannelMember(client, channel.Id, userID)
		ensureChannelMember(client, channel.Id, botUserID)
		return channel.Id, nil
	}

	// Create the private channel. Channel.Create sets the ID in-place.
	channel = &model.Channel{
		TeamId:      teamID,
		Name:        channelName,
		DisplayName: inboxChannelDisplayName,
		Purpose:     inboxChannelPurpose,
		Header:      "Outlook emails synced to Mattermost. Reply to any thread to send an email reply.",
		Type:        model.ChannelTypePrivate,
	}
	if err := client.Channel.Create(channel); err != nil {
		return "", errors.Wrap(err, "failed to create inbox channel")
	}

	// Add user and bot as members.
	ensureChannelMember(client, channel.Id, userID)
	ensureChannelMember(client, channel.Id, botUserID)

	return channel.Id, nil
}

func ensureChannelMember(client *pluginapi.Client, channelID, userID string) {
	_, _ = client.Channel.AddMember(channelID, userID)
}
