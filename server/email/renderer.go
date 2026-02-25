package email

import (
	"fmt"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"

	"github.com/wiggin77/mattermost-plugin-inbox/server/msgraph"
)

const maxBodyLength = 60000

// RenderRootPost renders a new email conversation as a Mattermost root post.
func RenderRootPost(msg *msgraph.Message) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("#### %s\n\n", msg.Subject))
	b.WriteString(fmt.Sprintf("**From:** %s\n", formatRecipient(msg.From)))

	if len(msg.ToRecipients) > 0 {
		b.WriteString(fmt.Sprintf("**To:** %s\n", formatRecipients(msg.ToRecipients)))
	}
	if len(msg.CcRecipients) > 0 {
		b.WriteString(fmt.Sprintf("**CC:** %s\n", formatRecipients(msg.CcRecipients)))
	}

	b.WriteString(fmt.Sprintf("**Date:** %s\n", msg.ReceivedDateTime.Local().Format("Jan 2, 2006 at 3:04 PM")))
	b.WriteString("\n---\n\n")

	body := renderBody(msg)
	b.WriteString(body)

	return truncate(b.String(), maxBodyLength)
}

// RenderReplyPost renders a follow-up email in an existing conversation thread.
func RenderReplyPost(msg *msgraph.Message) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("**From:** %s\n", formatRecipient(msg.From)))
	b.WriteString(fmt.Sprintf("**Date:** %s\n", msg.ReceivedDateTime.Local().Format("Jan 2, 2006 at 3:04 PM")))
	b.WriteString("\n---\n\n")

	body := renderBody(msg)
	b.WriteString(body)

	return truncate(b.String(), maxBodyLength)
}

// RenderAttachmentNote renders a note for an oversized attachment.
func RenderAttachmentNote(name string, sizeMB int64) string {
	return fmt.Sprintf("> Attachment: **%s** (%d MB) -- *Too large to sync. View in Outlook.*", name, sizeMB)
}

// MarkdownToHTML converts Mattermost markdown to HTML for outbound email replies.
func MarkdownToHTML(md string) string {
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse([]byte(md))

	renderer := html.NewRenderer(html.RendererOptions{})
	return string(markdown.Render(doc, renderer))
}

func renderBody(msg *msgraph.Message) string {
	if msg.Body.ContentType == "html" && msg.Body.Content != "" {
		converted, err := htmltomarkdown.ConvertString(msg.Body.Content)
		if err == nil && converted != "" {
			return converted
		}
	}

	// Fall back to plain text preview.
	if msg.BodyPreview != "" {
		return msg.BodyPreview
	}
	return msg.Body.Content
}

func formatRecipient(r msgraph.Recipient) string {
	if r.EmailAddress.Name != "" {
		return fmt.Sprintf("%s <%s>", r.EmailAddress.Name, r.EmailAddress.Address)
	}
	return r.EmailAddress.Address
}

func formatRecipients(recipients []msgraph.Recipient) string {
	parts := make([]string, len(recipients))
	for i, r := range recipients {
		parts[i] = formatRecipient(r)
	}
	return strings.Join(parts, ", ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n\n*[Message truncated]*"
}

// FormatTimestamp formats a time for display.
func FormatTimestamp(t time.Time) string {
	return t.Local().Format("Jan 2, 2006 at 3:04 PM")
}
