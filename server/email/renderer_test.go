package email

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
)

func TestRenderRootPost(t *testing.T) {
	msg := &msgraph.Message{
		Subject: "Meeting Tomorrow",
		From: msgraph.Recipient{
			EmailAddress: msgraph.EmailAddress{
				Name:    "John Doe",
				Address: "john@contoso.com",
			},
		},
		ToRecipients: []msgraph.Recipient{
			{EmailAddress: msgraph.EmailAddress{Name: "Jane", Address: "jane@contoso.com"}},
		},
		CcRecipients: []msgraph.Recipient{
			{EmailAddress: msgraph.EmailAddress{Address: "bob@contoso.com"}},
		},
		ReceivedDateTime: time.Date(2026, 1, 15, 15, 45, 0, 0, time.UTC),
		Body: msgraph.ItemBody{
			ContentType: "html",
			Content:     "<p>Hello, let's meet tomorrow.</p>",
		},
	}

	result := RenderRootPost(msg)
	assert.Contains(t, result, "#### Meeting Tomorrow")
	assert.Contains(t, result, "**From:** John Doe <john@contoso.com>")
	assert.Contains(t, result, "**To:** Jane <jane@contoso.com>")
	assert.Contains(t, result, "**CC:** bob@contoso.com")
	assert.Contains(t, result, "meet tomorrow")
}

func TestRenderReplyPost(t *testing.T) {
	msg := &msgraph.Message{
		From: msgraph.Recipient{
			EmailAddress: msgraph.EmailAddress{
				Name:    "Jane Smith",
				Address: "jane@contoso.com",
			},
		},
		ReceivedDateTime: time.Date(2026, 1, 15, 16, 12, 0, 0, time.UTC),
		Body: msgraph.ItemBody{
			ContentType: "text",
			Content:     "Sounds good!",
		},
		BodyPreview: "Sounds good!",
	}

	result := RenderReplyPost(msg)
	assert.Contains(t, result, "**From:** Jane Smith <jane@contoso.com>")
	assert.Contains(t, result, "Sounds good!")
	assert.NotContains(t, result, "####") // No subject line for replies.
}

func TestRenderAttachmentNote(t *testing.T) {
	result := RenderAttachmentNote("large-report.pdf", 125)
	assert.Contains(t, result, "large-report.pdf")
	assert.Contains(t, result, "125 MB")
	assert.Contains(t, result, "Too large to sync")
}

func TestMarkdownToHTML(t *testing.T) {
	md := "**Hello** world\n\n- item 1\n- item 2"
	html := MarkdownToHTML(md)
	assert.Contains(t, html, "<strong>Hello</strong>")
	assert.Contains(t, html, "<li>item 1</li>")
}

func TestTruncate(t *testing.T) {
	short := "hello"
	assert.Equal(t, short, truncate(short, 100))

	long := strings.Repeat("a", 70000)
	result := truncate(long, 60000)
	assert.Contains(t, result, "[Message truncated]")
	assert.Less(t, len(result), 61000)
}

func TestRenderBodyPlaintextFallback(t *testing.T) {
	msg := &msgraph.Message{
		Body: msgraph.ItemBody{
			ContentType: "text",
			Content:     "Plain text email body",
		},
		BodyPreview: "Plain text email body",
	}

	result := RenderRootPost(msg)
	assert.Contains(t, result, "Plain text email body")
}
