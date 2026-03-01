package msgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

const (
	graphBaseURL = "https://graph.microsoft.com/v1.0"
	maxRetries   = 5
)

// Client is a Microsoft Graph API client with automatic token management and retry.
type Client struct {
	httpClient  *http.Client
	tokenSource oauth2.TokenSource
	baseURL     string
}

// NewClient creates a new Graph API client using the provided token source.
func NewClient(tokenSource oauth2.TokenSource) *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		tokenSource: tokenSource,
		baseURL:     graphBaseURL,
	}
}

// NewClientWithBaseURL creates a Graph API client pointed at a custom base URL (for testing).
func NewClientWithBaseURL(tokenSource oauth2.TokenSource, baseURL string) *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		tokenSource: tokenSource,
		baseURL:     baseURL,
	}
}

// GetMe fetches the authenticated user's profile.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var user User
	if err := c.get(ctx, "/me?$select=id,displayName,mail,userPrincipalName", &user); err != nil {
		return nil, errors.Wrap(err, "failed to get user profile")
	}
	return &user, nil
}

// ListInboxMessages fetches inbox messages newer than the given timestamp.
func (c *Client) ListInboxMessages(ctx context.Context, since time.Time, top int) (*MessagesResponse, error) {
	filter := fmt.Sprintf("receivedDateTime ge %s", since.UTC().Format(time.RFC3339))

	params := url.Values{}
	params.Set("$filter", filter)
	params.Set("$orderby", "receivedDateTime asc")
	params.Set("$top", fmt.Sprintf("%d", top))
	params.Set("$select", "id,conversationId,subject,bodyPreview,body,from,toRecipients,ccRecipients,receivedDateTime,hasAttachments,isRead")

	path := "/me/mailFolders('Inbox')/messages?" + params.Encode()

	var resp MessagesResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, errors.Wrap(err, "failed to list inbox messages")
	}
	return &resp, nil
}

// GetMessage fetches a single message by ID with attachments expanded.
func (c *Client) GetMessage(ctx context.Context, messageID string) (*Message, error) {
	url := fmt.Sprintf("/me/messages/%s?$select=id,conversationId,subject,bodyPreview,body,from,toRecipients,ccRecipients,receivedDateTime,hasAttachments,isRead&$expand=attachments", messageID)

	var msg Message
	if err := c.get(ctx, url, &msg); err != nil {
		return nil, errors.Wrapf(err, "failed to get message %s", messageID)
	}
	return &msg, nil
}

// ReplyToMessage sends a reply to the specified message.
func (c *Client) ReplyToMessage(ctx context.Context, messageID string, htmlBody string) error {
	url := fmt.Sprintf("/me/messages/%s/reply", messageID)
	body := ReplyBody{
		Message: ReplyMessage{
			Body: ItemBody{
				ContentType: "html",
				Content:     htmlBody,
			},
		},
	}
	return c.post(ctx, url, body, nil)
}

// CreateSubscription creates a Graph webhook subscription.
func (c *Client) CreateSubscription(ctx context.Context, sub *Subscription) (*Subscription, error) {
	var result Subscription
	if err := c.post(ctx, "/subscriptions", sub, &result); err != nil {
		return nil, errors.Wrap(err, "failed to create subscription")
	}
	return &result, nil
}

// RenewSubscription updates the expiration time of a subscription.
func (c *Client) RenewSubscription(ctx context.Context, subscriptionID string, expiry time.Time) error {
	url := fmt.Sprintf("/subscriptions/%s", subscriptionID)
	body := map[string]string{
		"expirationDateTime": expiry.UTC().Format(time.RFC3339),
	}
	return c.patch(ctx, url, body)
}

// DeleteSubscription removes a Graph webhook subscription.
func (c *Client) DeleteSubscription(ctx context.Context, subscriptionID string) error {
	url := fmt.Sprintf("/subscriptions/%s", subscriptionID)
	return c.delete(ctx, url)
}

// get performs a GET request and decodes the JSON response into result.
func (c *Client) get(ctx context.Context, path string, result any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, result)
}

// post performs a POST request with a JSON body.
func (c *Client) post(ctx context.Context, path string, body any, result any) error {
	return c.doJSON(ctx, http.MethodPost, path, body, result)
}

// patch performs a PATCH request with a JSON body.
func (c *Client) patch(ctx context.Context, path string, body any) error {
	return c.doJSON(ctx, http.MethodPatch, path, body, nil)
}

// delete performs a DELETE request.
func (c *Client) delete(ctx context.Context, path string) error {
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// doJSON executes an HTTP request with retry and rate-limit handling.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return errors.Wrap(err, "failed to marshal request body")
		}
		bodyReader = bytes.NewReader(jsonBytes)
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Re-create reader for retries.
		if body != nil && attempt > 0 {
			jsonBytes, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(jsonBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
		if err != nil {
			return errors.Wrap(err, "failed to create request")
		}

		token, err := c.tokenSource.Token()
		if err != nil {
			return errors.Wrap(err, "failed to get access token")
		}

		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return errors.Wrap(err, "failed to execute request")
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return errors.Wrap(err, "failed to read response body")
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			delay := parseRetryAfter(resp.Header.Get("Retry-After"), attempt)
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if resp.StatusCode >= 400 {
			var graphErr GraphError
			if err := json.Unmarshal(respBody, &graphErr); err == nil {
				return &APIError{
					StatusCode: resp.StatusCode,
					Code:       graphErr.Error.Code,
					Message:    graphErr.Error.Message,
				}
			}
			return &APIError{
				StatusCode: resp.StatusCode,
				Message:    string(respBody),
			}
		}

		// 204 No Content or no result expected.
		if result == nil || resp.StatusCode == http.StatusNoContent {
			return nil
		}

		if err := json.Unmarshal(respBody, result); err != nil {
			return errors.Wrap(err, "failed to decode response")
		}

		return nil
	}

	return errors.New("max retries exceeded for rate-limited request")
}
