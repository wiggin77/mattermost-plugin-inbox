package kvstore

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecrypt(t *testing.T) {
	key := generateTestKey(t)
	plaintext := []byte(`{"access_token":"abc123","refresh_token":"def456"}`)

	ciphertext, err := Encrypt(plaintext, key)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, ciphertext)

	decrypted, err := Decrypt(ciphertext, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestDecryptWithWrongKey(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)

	plaintext := []byte("secret data")
	ciphertext, err := Encrypt(plaintext, key1)
	require.NoError(t, err)

	_, err = Decrypt(ciphertext, key2)
	assert.Error(t, err)
}

func TestDecryptTooShort(t *testing.T) {
	key := generateTestKey(t)
	_, err := Decrypt([]byte("short"), key)
	assert.Error(t, err)
}

func TestEncryptInvalidKey(t *testing.T) {
	_, err := Encrypt([]byte("data"), "not-base64!")
	assert.Error(t, err)
}
