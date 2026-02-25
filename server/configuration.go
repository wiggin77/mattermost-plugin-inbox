package main

import (
	"encoding/base64"
	"reflect"
	"strconv"

	"github.com/pkg/errors"
)

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
type configuration struct {
	// TenantID is the Azure AD tenant ID. Use "common" for multi-tenant apps.
	TenantID string `json:"TenantID"`

	// ClientID is the Azure AD application (client) ID.
	ClientID string `json:"ClientID"`

	// ClientSecret is the Azure AD client secret.
	ClientSecret string `json:"ClientSecret"`

	// EncryptionKey is the AES-256 key used to encrypt OAuth tokens at rest.
	EncryptionKey string `json:"EncryptionKey"`

	// WebhookSecret is the clientState for validating Graph webhook notifications.
	WebhookSecret string `json:"WebhookSecret"`

	// PollingIntervalMinutes is how often to poll for new emails (0 to disable).
	PollingIntervalMinutes string `json:"PollingIntervalMinutes"`

	// MaxAttachmentSizeMB is the max attachment size to sync.
	MaxAttachmentSizeMB string `json:"MaxAttachmentSizeMB"`

	// EnableDiagnostics enables verbose logging.
	EnableDiagnostics bool `json:"EnableDiagnostics"`
}

// Clone shallow copies the configuration.
func (c *configuration) Clone() *configuration {
	clone := *c
	return &clone
}

// IsValid validates that all required configuration fields are set.
func (c *configuration) IsValid() error {
	if c.TenantID == "" {
		return errors.New("Azure tenant ID is required")
	}
	if c.ClientID == "" {
		return errors.New("Azure client ID is required")
	}
	if c.ClientSecret == "" {
		return errors.New("Azure client secret is required")
	}
	if c.EncryptionKey == "" {
		return errors.New("encryption key is required; generate one in System Console")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(c.EncryptionKey)
	if err != nil {
		return errors.New("encryption key is not valid base64")
	}
	if len(keyBytes) != 32 {
		return errors.Errorf("encryption key must be exactly 32 bytes (got %d); regenerate in System Console", len(keyBytes))
	}
	if c.WebhookSecret == "" {
		return errors.New("webhook secret is required; generate one in System Console")
	}
	return nil
}

// GetPollingInterval returns the polling interval in minutes, defaulting to 5.
func (c *configuration) GetPollingInterval() int {
	if c.PollingIntervalMinutes == "" {
		return 5
	}
	val, err := strconv.Atoi(c.PollingIntervalMinutes)
	if err != nil || val < 0 {
		return 5
	}
	return val
}

// GetMaxAttachmentSize returns the max attachment size in bytes.
func (c *configuration) GetMaxAttachmentSize() int64 {
	if c.MaxAttachmentSizeMB == "" {
		return 50 * 1024 * 1024
	}
	val, err := strconv.Atoi(c.MaxAttachmentSizeMB)
	if err != nil || val <= 0 {
		return 50 * 1024 * 1024
	}
	return int64(val) * 1024 * 1024
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
//
// Do not call setConfiguration while holding the configurationLock, as sync.Mutex is not
// reentrant. In particular, avoid using the plugin API entirely, as this may in turn trigger a
// hook back into the plugin. If that hook attempts to acquire this lock, a deadlock may occur.
//
// This method panics if setConfiguration is called with the existing configuration. This almost
// certainly means that the configuration was modified without being cloned and may result in
// an unsafe access.
func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	configuration := new(configuration)

	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	p.setConfiguration(configuration)

	return nil
}
