package helper

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
)

// EmailMessage represents an email message
type EmailMessage struct {
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	Body        string
	HTMLBody    string
	Attachments []Attachment
	ReplyTo     string
	From        string // Optional: override default from
}

// Attachment represents an email attachment
type Attachment struct {
	Filename string
	Content  []byte
	MimeType string
}

// EmailSender handles email sending operations
type EmailSender struct {
	Config *config.SMTPConfig
}

// NewEmailSender creates a new email sender instance
func NewEmailSender(config *config.SMTPConfig) *EmailSender {
	return &EmailSender{
		Config: config,
	}
}

// Send sends an email
func (e *EmailSender) Send(msg *EmailMessage, config *config.SMTPConfig) error {
	// Use default from if not specified
	from := msg.From
	if from == "" {
		from = e.Config.From
	}

	// Validate recipients
	if len(msg.To) == 0 {
		return fmt.Errorf("at least one recipient is required")
	}

	// Build email content
	var buf bytes.Buffer

	// Write headers
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(msg.To, ", ")))

	if len(msg.Cc) > 0 {
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(msg.Cc, ", ")))
	}

	if msg.ReplyTo != "" {
		buf.WriteString(fmt.Sprintf("Reply-To: %s\r\n", msg.ReplyTo))
	}

	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", msg.Subject))
	buf.WriteString("MIME-Version: 1.0\r\n")

	// Collect all recipients
	recipients := append([]string{}, msg.To...)
	recipients = append(recipients, msg.Cc...)
	recipients = append(recipients, msg.Bcc...)

	// Build message body based on content type
	if len(msg.Attachments) > 0 {
		// Email with attachments - use multipart
		if err := e.writeMultipartMessage(&buf, msg); err != nil {
			return err
		}
	} else if msg.HTMLBody != "" {
		// HTML email with optional plain text alternative
		if msg.Body != "" {
			// Both HTML and plain text
			if err := e.writeAlternativeMessage(&buf, msg); err != nil {
				return err
			}
		} else {
			// HTML only
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
			buf.WriteString(msg.HTMLBody)
		}
	} else {
		// Plain text only
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		buf.WriteString(msg.Body)
	}

	// Send email
	return e.sendSMTP(from, recipients, buf.Bytes())
}

// writeAlternativeMessage writes multipart/alternative message (plain + HTML)
func (e *EmailSender) writeAlternativeMessage(buf *bytes.Buffer, msg *EmailMessage) error {
	boundary := "boundary-alternative-" + generateBoundary()
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary))

	// Plain text part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	buf.WriteString(msg.Body)
	buf.WriteString("\r\n\r\n")

	// HTML part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	buf.WriteString(msg.HTMLBody)
	buf.WriteString("\r\n\r\n")

	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	return nil
}

// writeMultipartMessage writes multipart/mixed message with attachments
func (e *EmailSender) writeMultipartMessage(buf *bytes.Buffer, msg *EmailMessage) error {
	boundary := "boundary-mixed-" + generateBoundary()
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary))

	// Write message body part
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	if msg.HTMLBody != "" && msg.Body != "" {
		// Both HTML and plain text
		altBoundary := "boundary-alternative-" + generateBoundary()
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary))

		buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		buf.WriteString(msg.Body)
		buf.WriteString("\r\n\r\n")

		buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
		buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		buf.WriteString(msg.HTMLBody)
		buf.WriteString("\r\n\r\n")

		buf.WriteString(fmt.Sprintf("--%s--\r\n\r\n", altBoundary))
	} else if msg.HTMLBody != "" {
		buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		buf.WriteString(msg.HTMLBody)
		buf.WriteString("\r\n\r\n")
	} else {
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		buf.WriteString(msg.Body)
		buf.WriteString("\r\n\r\n")
	}

	// Write attachments
	for _, attachment := range msg.Attachments {
		buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))

		mimeType := attachment.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		buf.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", mimeType, attachment.Filename))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n\r\n", attachment.Filename))

		// Encode attachment in base64
		encoded := base64.StdEncoding.EncodeToString(attachment.Content)
		// Split into 76-character lines
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			buf.WriteString(encoded[i:end])
			buf.WriteString("\r\n")
		}
		buf.WriteString("\r\n")
	}

	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	return nil
}

// sendSMTP sends email via SMTP
func (e *EmailSender) sendSMTP(from string, to []string, msg []byte) error {
	addr := fmt.Sprintf("%s:%d", e.Config.Host, e.Config.Port)

	// Create auth
	auth := smtp.PlainAuth("", e.Config.Username, e.Config.Password, e.Config.Host)

	// Use TLS if secure
	if e.Config.Secure {
		return e.sendWithTLS(addr, auth, from, to, msg)
	}

	// Regular SMTP
	return smtp.SendMail(addr, auth, from, to, msg)
}

// sendWithTLS sends email using TLS
func (e *EmailSender) sendWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	// TLS config
	tlsConfig := &tls.Config{
		ServerName: e.Config.Host,
	}

	// Connect to server
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	// Create SMTP client
	client, err := smtp.NewClient(conn, e.Config.Host)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer client.Quit()

	// Authenticate
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Set sender
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	// Set recipients
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", recipient, err)
		}
	}

	// Send message
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	return nil
}

// SendSimple sends a simple text email
func (e *EmailSender) SendSimple(to []string, subject, body string, config *config.SMTPConfig) error {
	return e.Send(&EmailMessage{
		To:      to,
		Subject: subject,
		Body:    body,
	}, config)
}

// SendHTML sends an HTML email
func (e *EmailSender) SendHTML(to []string, subject, htmlBody string, config *config.SMTPConfig) error {
	return e.Send(&EmailMessage{
		To:       to,
		Subject:  subject,
		HTMLBody: htmlBody,
	}, config)
}

// SendWithAttachments sends an email with attachments
func (e *EmailSender) SendWithAttachments(to []string, subject, body string, attachments []Attachment, config *config.SMTPConfig) error {
	return e.Send(&EmailMessage{
		To:          to,
		Subject:     subject,
		Body:        body,
		Attachments: attachments,
	}, config)
}

// GetAttachmentFromFile reads a file and returns an Attachment
func (e *EmailMessage) GetAttachmentFromFile(filePath string) (*Attachment, error) {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read the file content
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	// Get the filename from the path
	filename := filepath.Base(filePath)

	// Detect MIME type from file extension
	mimeType := mime.TypeByExtension(filepath.Ext(filePath))
	if mimeType == "" {
		mimeType = "application/octet-stream" // default
	}

	return &Attachment{
		Filename: filename,
		Content:  content,
		MimeType: mimeType,
	}, nil
}

// generateBoundary generates a simple boundary string
func generateBoundary() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Helper function to get MIME type from filename
func getMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	mimeTypes := map[string]string{
		".txt":  "text/plain",
		".html": "text/html",
		".pdf":  "application/pdf",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".zip":  "application/zip",
		".json": "application/json",
		".xml":  "application/xml",
		".csv":  "text/csv",
	}

	if mimeType, ok := mimeTypes[ext]; ok {
		return mimeType
	}
	return "application/octet-stream"
}
