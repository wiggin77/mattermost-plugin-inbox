package command

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const inboxCommandTrigger = "inbox"

// Deps holds the dependencies needed by the command handler beyond the plugin API client.
type Deps struct {
	// GetOAuthConnectURL returns the OAuth2 authorization URL for the given user.
	GetOAuthConnectURL func(userID string) (string, error)

	// DisconnectUser disconnects a user from Outlook.
	DisconnectUser func(userID string) error

	// GetUserStatus returns a formatted status string for the user.
	GetUserStatus func(userID string) (string, error)

	// TriggerSync triggers an immediate sync for the user.
	TriggerSync func(userID string) error
}

// Handler implements the Command interface for /inbox.
type Handler struct {
	client *pluginapi.Client
	deps   Deps
}

// Command is the interface for handling slash commands.
type Command interface {
	Handle(args *model.CommandArgs) (*model.CommandResponse, error)
}

// NewCommandHandler registers the /inbox slash command and returns the handler.
func NewCommandHandler(client *pluginapi.Client, deps Deps) Command {
	err := client.SlashCommand.Register(&model.Command{
		Trigger:          inboxCommandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Manage your Outlook email inbox",
		AutoCompleteHint: "[connect|disconnect|status|sync|help]",
		AutocompleteData: getAutocompleteData(),
	})
	if err != nil {
		client.Log.Error("Failed to register command", "error", err)
	}
	return &Handler{
		client: client,
		deps:   deps,
	}
}

func getAutocompleteData() *model.AutocompleteData {
	inbox := model.NewAutocompleteData(inboxCommandTrigger, "[command]", "Manage your Outlook inbox")

	connect := model.NewAutocompleteData("connect", "", "Connect your Outlook account")
	inbox.AddCommand(connect)

	disconnect := model.NewAutocompleteData("disconnect", "", "Disconnect your Outlook account")
	inbox.AddCommand(disconnect)

	status := model.NewAutocompleteData("status", "", "Show connection status")
	inbox.AddCommand(status)

	sync := model.NewAutocompleteData("sync", "", "Manually trigger a sync of your inbox")
	inbox.AddCommand(sync)

	help := model.NewAutocompleteData("help", "", "Show help text")
	inbox.AddCommand(help)

	return inbox
}

// Handle dispatches the /inbox subcommand.
func (c *Handler) Handle(args *model.CommandArgs) (*model.CommandResponse, error) {
	fields := strings.Fields(args.Command)
	if len(fields) < 2 {
		return c.executeHelpCommand(), nil
	}

	subcommand := fields[1]
	switch subcommand {
	case "connect":
		return c.executeConnectCommand(args)
	case "disconnect":
		return c.executeDisconnectCommand(args)
	case "status":
		return c.executeStatusCommand(args)
	case "sync":
		return c.executeSyncCommand(args)
	case "help":
		return c.executeHelpCommand(), nil
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Unknown subcommand: `%s`. Run `/inbox help` for usage.", subcommand),
		}, nil
	}
}

func (c *Handler) executeConnectCommand(args *model.CommandArgs) (*model.CommandResponse, error) {
	url, err := c.deps.GetOAuthConnectURL(args.UserId)
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Failed to generate connect URL: %s", err.Error()),
		}, nil
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("[Click here to connect your Outlook account](%s)", url),
	}, nil
}

func (c *Handler) executeDisconnectCommand(args *model.CommandArgs) (*model.CommandResponse, error) {
	if err := c.deps.DisconnectUser(args.UserId); err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Failed to disconnect: %s", err.Error()),
		}, nil
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         "Your Outlook account has been disconnected. Your inbox channel has been preserved.",
	}, nil
}

func (c *Handler) executeStatusCommand(args *model.CommandArgs) (*model.CommandResponse, error) {
	status, err := c.deps.GetUserStatus(args.UserId)
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Failed to get status: %s", err.Error()),
		}, nil
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         status,
	}, nil
}

func (c *Handler) executeSyncCommand(args *model.CommandArgs) (*model.CommandResponse, error) {
	if err := c.deps.TriggerSync(args.UserId); err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Failed to trigger sync: %s", err.Error()),
		}, nil
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         "Inbox sync triggered. New emails will appear shortly.",
	}, nil
}

func (c *Handler) executeHelpCommand() *model.CommandResponse {
	text := `**Mattermost Inbox - Outlook Email Sync**

| Command | Description |
|---------|-------------|
| ` + "`/inbox connect`" + ` | Connect your Outlook account |
| ` + "`/inbox disconnect`" + ` | Disconnect your Outlook account |
| ` + "`/inbox status`" + ` | Show connection status |
| ` + "`/inbox sync`" + ` | Manually trigger an inbox sync |
| ` + "`/inbox help`" + ` | Show this help text |

After connecting, your emails will appear in a private channel called **inbox-{your-username}**. Reply to any email thread in that channel to send a reply from Outlook.`

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
