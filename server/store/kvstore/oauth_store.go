package kvstore

import (
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

const (
	kvPrefixOAuthState   = "oauth_"
	oauthStateTTLSeconds = 300 // 5 minutes
)

func (kv Client) StoreOAuthState(state string, oauthState *OAuthState) error {
	_, err := kv.client.KV.Set(kvPrefixOAuthState+state, oauthState, pluginapi.SetExpiry(oauthStateTTLSeconds))
	if err != nil {
		return errors.Wrap(err, "failed to store oauth state")
	}
	return nil
}

func (kv Client) GetAndDeleteOAuthState(state string) (*OAuthState, error) {
	var oauthState OAuthState
	err := kv.client.KV.Get(kvPrefixOAuthState+state, &oauthState)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get oauth state")
	}
	if oauthState.MattermostUserID == "" {
		return nil, nil
	}

	// Delete after retrieval (one-time use).
	if err := kv.client.KV.Delete(kvPrefixOAuthState + state); err != nil {
		return nil, errors.Wrap(err, "failed to delete oauth state")
	}

	return &oauthState, nil
}
