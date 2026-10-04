package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-starter-template/server/testhelper"
)

// pluginURL returns the full URL for a plugin API endpoint.
func pluginURL(th *testhelper.TestHelper, path string) string {
	return th.ServerURL + "/plugins/" + testhelper.PluginID() + path
}

// doPluginRequest makes an authenticated HTTP request to a plugin endpoint.
func doPluginRequest(t *testing.T, method, url, authToken string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+authToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// --- Plugin Lifecycle Tests ---

func TestPluginActivation(t *testing.T) {
	th := testhelper.Setup(t)

	ctx := context.Background()
	statuses, _, err := th.AdminClient.GetPluginStatuses(ctx)
	require.NoError(t, err)

	found := false
	for _, s := range statuses {
		if s.PluginId == testhelper.PluginID() && s.State == model.PluginStateRunning {
			found = true
			break
		}
	}
	require.True(t, found, "plugin %s should be running", testhelper.PluginID())
}

func TestSlashCommandRegistered(t *testing.T) {
	th := testhelper.Setup(t)

	ctx := context.Background()
	commands, _, err := th.AdminClient.ListAutocompleteCommands(ctx, th.Team.Id)
	require.NoError(t, err)

	found := false
	for _, cmd := range commands {
		if cmd.Trigger == "inbox" {
			found = true
			assert.True(t, cmd.AutoComplete)
			assert.Contains(t, cmd.AutoCompleteHint, "connect")
			break
		}
	}
	require.True(t, found, "/inbox command should be registered")
}

// --- API Endpoint Tests ---

func TestAuthenticatedEndpointRequiresAuth(t *testing.T) {
	th := testhelper.Setup(t)

	// Request without auth token should return 401.
	resp, err := http.Get(pluginURL(th, "/api/v1/user/status"))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestUserStatusNotConnected(t *testing.T) {
	th := testhelper.Setup(t)

	resp := doPluginRequest(t, http.MethodGet, pluginURL(th, "/api/v1/user/status"), th.Client.AuthToken, nil)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Contains(t, result["status"], "Not connected")
}

func TestUserDisconnectNotConnected(t *testing.T) {
	th := testhelper.Setup(t)

	resp := doPluginRequest(t, http.MethodPost, pluginURL(th, "/api/v1/user/disconnect"), th.Client.AuthToken, nil)
	defer func() { _ = resp.Body.Close() }()

	// Should return 500 since user is not connected.
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// --- Webhook Handler Tests ---

func TestGraphWebhookValidation(t *testing.T) {
	th := testhelper.Setup(t)

	// Microsoft Graph sends a validation request with a validationToken query parameter.
	// The plugin must echo it back with 200 OK.
	validationToken := "test-validation-token-12345"
	resp, err := http.Post(pluginURL(th, "/api/v1/webhook/graph")+"?validationToken="+validationToken, "application/json", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, validationToken, string(body))
}

func TestGraphWebhookInvalidClientState(t *testing.T) {
	th := testhelper.Setup(t)

	// Send a notification with wrong clientState — should be rejected.
	notification := map[string]any{
		"value": []map[string]any{
			{
				"subscriptionId": "fake-sub-id",
				"clientState":    "wrong-secret",
				"changeType":     "created",
				"resource":       "me/messages/msg-123",
				"resourceData": map[string]any{
					"id": "msg-123",
				},
			},
		},
	}

	payload, err := json.Marshal(notification)
	require.NoError(t, err)

	resp, err := http.Post(
		pluginURL(th, "/api/v1/webhook/graph"),
		"application/json",
		bytes.NewReader(payload),
	)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- OAuth Flow Tests ---

func TestOAuthConnectRequiresAuth(t *testing.T) {
	th := testhelper.Setup(t)

	// OAuth connect without auth should fail.
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects.
		},
	}

	resp, err := client.Get(pluginURL(th, "/api/v1/oauth2/connect"))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestOAuthCompleteRequiresParams(t *testing.T) {
	th := testhelper.Setup(t)

	// Missing code and state should return 400.
	resp, err := http.Get(pluginURL(th, "/api/v1/oauth2/complete"))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestOAuthCompleteInvalidState(t *testing.T) {
	th := testhelper.Setup(t)

	// Valid params but invalid state should return 400.
	resp, err := http.Get(pluginURL(th, "/api/v1/oauth2/complete") + "?code=fake-code&state=invalid-state")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
