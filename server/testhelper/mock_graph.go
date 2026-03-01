package testhelper

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
)

// MockGraphServer provides a fake Microsoft Graph API for integration tests.
// It serves configurable responses for email, user profile, and subscription endpoints.
type MockGraphServer struct {
	Server *httptest.Server

	mu            sync.Mutex
	messages      map[string]*msgraph.Message
	inboxMessages []msgraph.Message
	repliesSent   []SentReply
	subscriptions map[string]*msgraph.Subscription
	userProfile   *msgraph.User
}

// SentReply records a reply sent via the mock.
type SentReply struct {
	MessageID string
	HTMLBody  string
}

// NewMockGraphServer creates and starts a mock Graph API server.
func NewMockGraphServer(t *testing.T) *MockGraphServer {
	t.Helper()

	m := &MockGraphServer{
		messages:      make(map[string]*msgraph.Message),
		subscriptions: make(map[string]*msgraph.Subscription),
		userProfile: &msgraph.User{
			ID:                "mock-graph-user-id",
			DisplayName:       "Test User",
			Mail:              "testuser@contoso.com",
			UserPrincipalName: "testuser@contoso.com",
		},
	}

	m.Server = httptest.NewServer(http.HandlerFunc(m.route))
	t.Cleanup(m.Server.Close)

	return m
}

// BaseURL returns the base URL of the mock server including the /v1.0 prefix.
func (m *MockGraphServer) BaseURL() string {
	return m.Server.URL + "/v1.0"
}

// SetUserProfile configures the user profile returned by GET /me.
func (m *MockGraphServer) SetUserProfile(user *msgraph.User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userProfile = user
}

// AddMessage adds a message to the mock server. It will be returned by
// both GetMessage (by ID) and ListInboxMessages.
func (m *MockGraphServer) AddMessage(msg *msgraph.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.ID] = msg
	m.inboxMessages = append(m.inboxMessages, *msg)
}

// ClearMessages removes all messages from the mock.
func (m *MockGraphServer) ClearMessages() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = make(map[string]*msgraph.Message)
	m.inboxMessages = nil
}

// RepliesSent returns the list of replies sent through the mock.
func (m *MockGraphServer) RepliesSent() []SentReply {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]SentReply, len(m.repliesSent))
	copy(result, m.repliesSent)
	return result
}

// route dispatches requests based on method and path.
func (m *MockGraphServer) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case r.Method == http.MethodGet && path == "/v1.0/me":
		m.handleGetMe(w, r)

	case r.Method == http.MethodGet && strings.Contains(path, "/mailFolders"):
		m.handleListInboxMessages(w, r)

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1.0/me/messages/"):
		m.handleGetMessage(w, r)

	case r.Method == http.MethodPost && strings.HasSuffix(path, "/reply"):
		m.handleReplyToMessage(w, r)

	case r.Method == http.MethodPost && path == "/v1.0/subscriptions":
		m.handleCreateSubscription(w, r)

	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/v1.0/subscriptions/"):
		m.handleRenewSubscription(w, r)

	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/v1.0/subscriptions/"):
		m.handleDeleteSubscription(w, r)

	default:
		http.Error(w, "not found: "+r.Method+" "+path, http.StatusNotFound)
	}
}

// extractPathSegment extracts the last path segment (e.g., ID) from a URL path.
func extractPathSegment(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/reply")
	return s
}

func (m *MockGraphServer) handleGetMe(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	writeJSON(w, m.userProfile)
}

func (m *MockGraphServer) handleListInboxMessages(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	resp := msgraph.MessagesResponse{
		Value: m.inboxMessages,
	}
	writeJSON(w, resp)
}

func (m *MockGraphServer) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := extractPathSegment(r.URL.Path, "/v1.0/me/messages/")
	msg, ok := m.messages[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, msgraph.GraphError{
			Error: msgraph.GraphErrorDetail{
				Code:    "ErrorItemNotFound",
				Message: "The specified object was not found in the store.",
			},
		})
		return
	}
	writeJSON(w, msg)
}

func (m *MockGraphServer) handleReplyToMessage(w http.ResponseWriter, r *http.Request) {
	// Path is /v1.0/me/messages/{id}/reply
	id := extractPathSegment(r.URL.Path, "/v1.0/me/messages/")

	var body msgraph.ReplyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.repliesSent = append(m.repliesSent, SentReply{
		MessageID: id,
		HTMLBody:  body.Message.Body.Content,
	})
	m.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
}

func (m *MockGraphServer) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var sub msgraph.Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	sub.ID = "mock-subscription-" + strings.ReplaceAll(time.Now().Format("20060102150405.000"), ".", "")

	m.mu.Lock()
	m.subscriptions[sub.ID] = &sub
	m.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, sub)
}

func (m *MockGraphServer) handleRenewSubscription(w http.ResponseWriter, r *http.Request) {
	id := extractPathSegment(r.URL.Path, "/v1.0/subscriptions/")

	m.mu.Lock()
	sub, ok := m.subscriptions[id]
	m.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if expiry, exists := body["expirationDateTime"]; exists {
		t, _ := time.Parse(time.RFC3339, expiry)
		m.mu.Lock()
		sub.ExpirationDateTime = t
		m.mu.Unlock()
	}

	writeJSON(w, sub)
}

func (m *MockGraphServer) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := extractPathSegment(r.URL.Path, "/v1.0/subscriptions/")

	m.mu.Lock()
	delete(m.subscriptions, id)
	m.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
