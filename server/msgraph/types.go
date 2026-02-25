package msgraph

import "time"

// User represents a Microsoft Graph user profile.
type User struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

// Message represents an Outlook email message from Graph API.
type Message struct {
	ID               string       `json:"id"`
	ConversationID   string       `json:"conversationId"`
	Subject          string       `json:"subject"`
	BodyPreview      string       `json:"bodyPreview"`
	Body             ItemBody     `json:"body"`
	From             Recipient    `json:"from"`
	ToRecipients     []Recipient  `json:"toRecipients"`
	CcRecipients     []Recipient  `json:"ccRecipients"`
	ReceivedDateTime time.Time    `json:"receivedDateTime"`
	HasAttachments   bool         `json:"hasAttachments"`
	IsRead           bool         `json:"isRead"`
	Attachments      []Attachment `json:"attachments"`
}

// ItemBody represents the body of an email.
type ItemBody struct {
	ContentType string `json:"contentType"` // "text" or "html"
	Content     string `json:"content"`
}

// Recipient represents an email recipient.
type Recipient struct {
	EmailAddress EmailAddress `json:"emailAddress"`
}

// EmailAddress contains a display name and email address.
type EmailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// Attachment represents an email attachment.
type Attachment struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ContentType  string `json:"contentType"`
	Size         int64  `json:"size"`
	IsInline     bool   `json:"isInline"`
	ContentBytes string `json:"contentBytes"` // Base64-encoded for file attachments
	ODataType    string `json:"@odata.type"`
}

// MessagesResponse is the response from listing messages.
type MessagesResponse struct {
	Value    []Message `json:"value"`
	NextLink string    `json:"@odata.nextLink"`
}

// Subscription represents a Microsoft Graph webhook subscription.
type Subscription struct {
	ID                        string    `json:"id,omitempty"`
	ChangeType                string    `json:"changeType"`
	NotificationURL           string    `json:"notificationUrl"`
	Resource                  string    `json:"resource"`
	ExpirationDateTime        time.Time `json:"expirationDateTime"`
	ClientState               string    `json:"clientState,omitempty"`
	LatestSupportedTLSVersion string    `json:"latestSupportedTlsVersion,omitempty"`
}

// ChangeNotificationCollection is the payload sent by Graph webhooks.
type ChangeNotificationCollection struct {
	Value []ChangeNotification `json:"value"`
}

// ChangeNotification is a single notification from Graph.
type ChangeNotification struct {
	SubscriptionID                 string       `json:"subscriptionId"`
	ClientState                    string       `json:"clientState"`
	ChangeType                     string       `json:"changeType"`
	Resource                       string       `json:"resource"`
	SubscriptionExpirationDateTime string       `json:"subscriptionExpirationDateTime"`
	ResourceData                   ResourceData `json:"resourceData"`
}

// ResourceData contains the ID of the changed resource.
type ResourceData struct {
	ODataType string `json:"@odata.type"`
	ODataID   string `json:"@odata.id"`
	ID        string `json:"id"`
}

// ReplyBody is the request body for replying to a message.
type ReplyBody struct {
	Message ReplyMessage `json:"message"`
}

// ReplyMessage is the message portion of a reply request.
type ReplyMessage struct {
	Body ItemBody `json:"body"`
}

// GraphError represents an error response from the Graph API.
type GraphError struct {
	Error GraphErrorDetail `json:"error"`
}

// GraphErrorDetail contains the error code and message.
type GraphErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
