package msgraph

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
)

// OAuthConfig holds the Azure AD OAuth2 configuration.
type OAuthConfig struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// OAuth2Config returns a standard oauth2.Config for the Azure AD v2.0 endpoint.
func (c *OAuthConfig) OAuth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", c.TenantID),
			TokenURL: fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", c.TenantID),
		},
		RedirectURL: c.RedirectURL,
		Scopes:      []string{"Mail.Read", "Mail.Send", "offline_access"},
	}
}

// AuthCodeURL returns the URL to redirect the user to for OAuth2 authorization.
func (c *OAuthConfig) AuthCodeURL(state string) string {
	return c.OAuth2Config().AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// Exchange exchanges an authorization code for an OAuth2 token.
func (c *OAuthConfig) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return c.OAuth2Config().Exchange(ctx, code)
}

// TokenSource returns a token source that auto-refreshes the token.
func (c *OAuthConfig) TokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	return c.OAuth2Config().TokenSource(ctx, token)
}
