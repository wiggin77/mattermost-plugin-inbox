package msgraph

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

// APIError represents an error from the Microsoft Graph API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("graph API error %d (%s): %s", e.StatusCode, e.Code, e.Message)
}

// IsRateLimited returns true if the error is a 429 Too Many Requests.
func (e *APIError) IsRateLimited() bool {
	return e.StatusCode == http.StatusTooManyRequests
}

// IsUnauthorized returns true if the error is a 401 Unauthorized.
func (e *APIError) IsUnauthorized() bool {
	return e.StatusCode == http.StatusUnauthorized
}

// IsNotFound returns true if the error is a 404 Not Found.
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

// parseRetryAfter parses the Retry-After header value.
// Falls back to exponential backoff if the header is missing or unparseable.
func parseRetryAfter(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	// Exponential backoff: 1s, 2s, 4s, 8s, 16s
	return time.Duration(math.Pow(2, float64(attempt))) * time.Second
}
