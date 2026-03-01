package main

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validTestConfig(t *testing.T) *configuration {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return &configuration{
		TenantID:      "test-tenant",
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		EncryptionKey: base64.StdEncoding.EncodeToString(key),
		WebhookSecret: "test-webhook-secret",
	}
}

func TestConfigurationIsValid(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		config := validTestConfig(t)
		assert.NoError(t, config.IsValid())
	})

	t.Run("missing tenant ID", func(t *testing.T) {
		config := validTestConfig(t)
		config.TenantID = ""
		assert.ErrorContains(t, config.IsValid(), "tenant ID")
	})

	t.Run("missing client ID", func(t *testing.T) {
		config := validTestConfig(t)
		config.ClientID = ""
		assert.ErrorContains(t, config.IsValid(), "client ID")
	})

	t.Run("missing client secret", func(t *testing.T) {
		config := validTestConfig(t)
		config.ClientSecret = ""
		assert.ErrorContains(t, config.IsValid(), "client secret")
	})

	t.Run("missing encryption key", func(t *testing.T) {
		config := validTestConfig(t)
		config.EncryptionKey = ""
		assert.ErrorContains(t, config.IsValid(), "encryption key")
	})

	t.Run("invalid base64 encryption key", func(t *testing.T) {
		config := validTestConfig(t)
		config.EncryptionKey = "not-valid-base64!"
		assert.ErrorContains(t, config.IsValid(), "base64")
	})

	t.Run("wrong length encryption key", func(t *testing.T) {
		config := validTestConfig(t)
		shortKey := make([]byte, 16)
		_, _ = rand.Read(shortKey)
		config.EncryptionKey = base64.StdEncoding.EncodeToString(shortKey)
		assert.ErrorContains(t, config.IsValid(), "32 bytes")
	})

	t.Run("missing webhook secret", func(t *testing.T) {
		config := validTestConfig(t)
		config.WebhookSecret = ""
		assert.ErrorContains(t, config.IsValid(), "webhook secret")
	})
}

func TestGetPollingInterval(t *testing.T) {
	t.Run("default when empty", func(t *testing.T) {
		config := &configuration{}
		assert.Equal(t, 5, config.GetPollingInterval())
	})

	t.Run("custom value", func(t *testing.T) {
		config := &configuration{PollingIntervalMinutes: "10"}
		assert.Equal(t, 10, config.GetPollingInterval())
	})

	t.Run("zero disables polling", func(t *testing.T) {
		config := &configuration{PollingIntervalMinutes: "0"}
		assert.Equal(t, 0, config.GetPollingInterval())
	})

	t.Run("invalid falls back to default", func(t *testing.T) {
		config := &configuration{PollingIntervalMinutes: "abc"}
		assert.Equal(t, 5, config.GetPollingInterval())
	})

	t.Run("negative falls back to default", func(t *testing.T) {
		config := &configuration{PollingIntervalMinutes: "-1"}
		assert.Equal(t, 5, config.GetPollingInterval())
	})
}

func TestGetMaxAttachmentSize(t *testing.T) {
	t.Run("default when empty", func(t *testing.T) {
		config := &configuration{}
		assert.Equal(t, int64(50*1024*1024), config.GetMaxAttachmentSize())
	})

	t.Run("custom value", func(t *testing.T) {
		config := &configuration{MaxAttachmentSizeMB: "25"}
		assert.Equal(t, int64(25*1024*1024), config.GetMaxAttachmentSize())
	})

	t.Run("invalid falls back to default", func(t *testing.T) {
		config := &configuration{MaxAttachmentSizeMB: "abc"}
		assert.Equal(t, int64(50*1024*1024), config.GetMaxAttachmentSize())
	})

	t.Run("zero falls back to default", func(t *testing.T) {
		config := &configuration{MaxAttachmentSizeMB: "0"}
		assert.Equal(t, int64(50*1024*1024), config.GetMaxAttachmentSize())
	})

	t.Run("negative falls back to default", func(t *testing.T) {
		config := &configuration{MaxAttachmentSizeMB: "-5"}
		assert.Equal(t, int64(50*1024*1024), config.GetMaxAttachmentSize())
	})
}
