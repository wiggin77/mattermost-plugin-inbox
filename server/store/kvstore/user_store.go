package kvstore

import (
	"slices"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

const (
	kvPrefixUser        = "user_"
	kvKeyConnectedUsers = "connected_users"
)

func (kv Client) StoreUserConnection(userID string, conn *UserConnection) error {
	_, err := kv.client.KV.Set(kvPrefixUser+userID, conn)
	if err != nil {
		return errors.Wrap(err, "failed to store user connection")
	}
	return nil
}

func (kv Client) GetUserConnection(userID string) (*UserConnection, error) {
	var conn UserConnection
	err := kv.client.KV.Get(kvPrefixUser+userID, &conn)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user connection")
	}
	if conn.MattermostUserID == "" {
		return nil, nil
	}
	return &conn, nil
}

func (kv Client) DeleteUserConnection(userID string) error {
	err := kv.client.KV.Delete(kvPrefixUser + userID)
	if err != nil {
		return errors.Wrap(err, "failed to delete user connection")
	}
	return nil
}

func (kv Client) AddConnectedUser(userID string) error {
	users, err := kv.GetConnectedUsers()
	if err != nil {
		return err
	}

	// Check if already present.
	if slices.Contains(users, userID) {
		return nil
	}

	users = append(users, userID)

	_, err = kv.client.KV.Set(kvKeyConnectedUsers, users)
	if err != nil {
		return errors.Wrap(err, "failed to store connected users")
	}
	return nil
}

func (kv Client) RemoveConnectedUser(userID string) error {
	users, err := kv.GetConnectedUsers()
	if err != nil {
		return err
	}

	filtered := make([]string, 0, len(users))
	for _, u := range users {
		if u != userID {
			filtered = append(filtered, u)
		}
	}

	if _, err := kv.client.KV.Set(kvKeyConnectedUsers, filtered); err != nil {
		return errors.Wrap(err, "failed to store connected users")
	}
	return nil
}

func (kv Client) GetConnectedUsers() ([]string, error) {
	var users []string
	err := kv.client.KV.Get(kvKeyConnectedUsers, &users)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get connected users")
	}
	if users == nil {
		return []string{}, nil
	}
	return users, nil
}

// Client wraps pluginapi.Client for KVStore operations.
type Client struct {
	client *pluginapi.Client
}

// NewKVStore creates a new KVStore backed by the plugin API.
func NewKVStore(client *pluginapi.Client) KVStore {
	return Client{
		client: client,
	}
}
