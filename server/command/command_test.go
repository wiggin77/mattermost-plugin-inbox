package command

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type env struct {
	client *pluginapi.Client
	api    *plugintest.API
}

func setupTest() *env {
	api := &plugintest.API{}
	driver := &plugintest.Driver{}
	client := pluginapi.NewClient(api, driver)

	return &env{
		client: client,
		api:    api,
	}
}

func TestInboxHelpCommand(t *testing.T) {
	assert := assert.New(t)
	env := setupTest()

	env.api.On("RegisterCommand", mock.AnythingOfType("*model.Command")).Return(nil)

	deps := Deps{
		GetOAuthConnectURL: func(userID string) (string, error) { return "", nil },
		DisconnectUser:     func(userID string) error { return nil },
		GetUserStatus:      func(userID string) (string, error) { return "", nil },
		TriggerSync:        func(userID string) error { return nil },
	}
	cmdHandler := NewCommandHandler(env.client, deps)

	args := &model.CommandArgs{
		Command: "/inbox help",
		UserId:  "test-user-id",
	}
	response, err := cmdHandler.Handle(args)
	assert.Nil(err)
	assert.Equal(model.CommandResponseTypeEphemeral, response.ResponseType)
	assert.Contains(response.Text, "Mattermost Inbox")
}

func TestInboxConnectCommand(t *testing.T) {
	assert := assert.New(t)
	env := setupTest()

	env.api.On("RegisterCommand", mock.AnythingOfType("*model.Command")).Return(nil)

	deps := Deps{
		GetOAuthConnectURL: func(userID string) (string, error) {
			return "https://login.microsoftonline.com/test/oauth2/v2.0/authorize?state=abc", nil
		},
		DisconnectUser: func(userID string) error { return nil },
		GetUserStatus:  func(userID string) (string, error) { return "", nil },
		TriggerSync:    func(userID string) error { return nil },
	}
	cmdHandler := NewCommandHandler(env.client, deps)

	args := &model.CommandArgs{
		Command: "/inbox connect",
		UserId:  "test-user-id",
	}
	response, err := cmdHandler.Handle(args)
	assert.Nil(err)
	assert.Contains(response.Text, "Click here to connect")
}

func TestInboxNoSubcommand(t *testing.T) {
	assert := assert.New(t)
	env := setupTest()

	env.api.On("RegisterCommand", mock.AnythingOfType("*model.Command")).Return(nil)

	deps := Deps{
		GetOAuthConnectURL: func(userID string) (string, error) { return "", nil },
		DisconnectUser:     func(userID string) error { return nil },
		GetUserStatus:      func(userID string) (string, error) { return "", nil },
		TriggerSync:        func(userID string) error { return nil },
	}
	cmdHandler := NewCommandHandler(env.client, deps)

	args := &model.CommandArgs{
		Command: "/inbox",
		UserId:  "test-user-id",
	}
	response, err := cmdHandler.Handle(args)
	assert.Nil(err)
	// Should return help when no subcommand given.
	assert.Contains(response.Text, "Mattermost Inbox")
}
