package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// ensureBot creates or updates the bot account used by this plugin.
// Returns the bot user ID.
func (p *Plugin) ensureBot() (string, error) {
	botID, err := p.client.Bot.EnsureBot(&model.Bot{
		Username:    botUsername,
		DisplayName: botDisplayName,
		Description: botDescription,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to ensure bot account")
	}

	return botID, nil
}
