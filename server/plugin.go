package main

import (
	"context"
	"encoding/json"
	"net/http"
	gosync "sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"

	"github.com/wiggin77/mattermost-plugin-inbox/server/command"
	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
	"github.com/wiggin77/mattermost-plugin-inbox/server/store/kvstore"
	"github.com/wiggin77/mattermost-plugin-inbox/server/sync"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// store is the KVStore client used for all plugin persistence.
	store kvstore.KVStore

	// client is the Mattermost server API client.
	client *pluginapi.Client

	// commandClient is the client used to register and execute slash commands.
	commandClient command.Command

	// router is the HTTP router for handling API requests.
	router *mux.Router

	// syncEngine orchestrates email sync from Outlook to Mattermost.
	syncEngine *sync.Engine

	// botUserID is the user ID of the plugin bot account.
	botUserID string

	// pollingJob is the background job for polling email inboxes.
	pollingJob *cluster.Job

	// subscriptionRenewalJob renews Graph webhook subscriptions.
	subscriptionRenewalJob *cluster.Job

	// configurationLock synchronizes access to the configuration.
	configurationLock gosync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration
}

// OnActivate is invoked when the plugin is activated.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	p.store = kvstore.NewKVStore(p.client)

	// Ensure the bot account exists.
	botID, err := p.ensureBot()
	if err != nil {
		return errors.Wrap(err, "failed to ensure bot")
	}
	p.botUserID = botID

	// Initialize the sync engine.
	config := p.getConfiguration()
	p.syncEngine = sync.NewEngine(
		p.client,
		p.store,
		p.botUserID,
		config.GetMaxAttachmentSize(),
		p.API.LogError,
		p.API.LogInfo,
	)

	// Register slash commands with dependency injection for plugin operations.
	p.commandClient = command.NewCommandHandler(p.client, command.Deps{
		GetOAuthConnectURL: p.generateOAuthConnectURL,
		DisconnectUser:     p.disconnectUser,
		GetUserStatus:      p.getUserStatusText,
		TriggerSync:        p.triggerUserSync,
	})

	p.router = p.initRouter()

	// Schedule polling background job.
	pollingInterval := config.GetPollingInterval()
	if pollingInterval > 0 {
		pollingJob, schedErr := cluster.Schedule(
			p.API,
			"EmailPollJob",
			cluster.MakeWaitForRoundedInterval(time.Duration(pollingInterval)*time.Minute),
			p.runEmailPollJob,
		)
		if schedErr != nil {
			return errors.Wrap(schedErr, "failed to schedule email polling job")
		}
		p.pollingJob = pollingJob
	}

	// Schedule subscription renewal job (every 4 hours).
	renewalJob, schedErr := cluster.Schedule(
		p.API,
		"SubscriptionRenewalJob",
		cluster.MakeWaitForRoundedInterval(4*time.Hour),
		p.runSubscriptionRenewalJob,
	)
	if schedErr != nil {
		return errors.Wrap(schedErr, "failed to schedule subscription renewal job")
	}
	p.subscriptionRenewalJob = renewalJob

	return nil
}

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	if p.pollingJob != nil {
		if err := p.pollingJob.Close(); err != nil {
			p.API.LogError("Failed to close polling job", "err", err)
		}
	}
	if p.subscriptionRenewalJob != nil {
		if err := p.subscriptionRenewalJob.Close(); err != nil {
			p.API.LogError("Failed to close subscription renewal job", "err", err)
		}
	}
	return nil
}

// ExecuteCommand dispatches slash commands to the command handler.
func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response, err := p.commandClient.Handle(args)
	if err != nil {
		return nil, model.NewAppError("ExecuteCommand", "plugin.command.execute_command.app_error", nil, err.Error(), http.StatusInternalServerError)
	}
	return response, nil
}

// getGraphClientForUser creates a Graph API client for a connected user.
func (p *Plugin) getGraphClientForUser(conn *kvstore.UserConnection) (*msgraph.Client, error) {
	config := p.getConfiguration()

	tokenJSON, err := kvstore.Decrypt(conn.EncryptedToken, config.EncryptionKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decrypt token")
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenJSON, &token); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal token")
	}

	oauthConfig := p.getOAuthConfig()
	tokenSource := oauthConfig.TokenSource(context.Background(), &token)

	return msgraph.NewClient(tokenSource), nil
}

// triggerUserSync triggers an immediate inbox sync for the given user.
func (p *Plugin) triggerUserSync(userID string) error {
	conn, err := p.store.GetUserConnection(userID)
	if err != nil {
		return errors.Wrap(err, "failed to get user connection")
	}
	if conn == nil {
		return errors.New("you are not connected to Outlook. Run `/inbox connect` first")
	}

	graphClient, err := p.getGraphClientForUser(conn)
	if err != nil {
		return errors.Wrap(err, "failed to create Graph client")
	}

	go func() {
		if err := p.syncEngine.PollUserInbox(context.Background(), graphClient, conn); err != nil {
			p.API.LogError("Manual sync failed", "error", err, "user_id", userID)
		}
	}()

	return nil
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
