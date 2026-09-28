package setting

import (
	"github.com/robertkonga/yekonga-server-go/config"
)

// SmsRecipient represents an SMS recipient
type SmsRecipient struct {
	RecipientID int    `json:"recipient_id"`
	DestAddr    string `json:"dest_addr"`
}

// SendRequest represents the SMS send request body
type SendRequest struct {
	SourceAddr   string    `json:"source_addr"`
	ScheduleTime string    `json:"schedule_time"`
	Message      string    `json:"message"`
	Messages     []Message `json:"messages"`

	Encoding   int            `json:"encoding"`
	Recipients []SmsRecipient `json:"recipients"`
}

// SendResponse represents the response from send API
type SendResponse struct {
	Status    string      `json:"status"`
	Message   string      `json:"message"`
	MessageID string      `json:"messageId"`
	Code      int         `json:"code"`
	RequestID string      `json:"request_id"`
	Response  interface{} `json:"response"`
	Error     string      `json:"error,omitempty"`
}

// StatusResponse represents delivery status response
type StatusResponse struct {
	Status    string      `json:"status"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id"`
	Response  interface{} `json:"response"`
}

// BalanceResponse represents balance check response
type BalanceResponse struct {
	Status   string      `json:"status"`
	Balance  float64     `json:"balance"`
	Response interface{} `json:"response"`
	Data     BalanceData `json:"data,omitempty"`
}

// BalanceData holds the balance data from API
type BalanceData struct {
	CreditBalance float64 `json:"credit_balance"`
}

// Destination represents an SMS destination
type Destination struct {
	To string `json:"to"`
}

// Message represents a single SMS message
type Message struct {
	From         string        `json:"from"`
	Text         string        `json:"text"`
	Destinations []Destination `json:"destinations"`
}

// MessageStatus represents the status of a message
type MessageStatus struct {
	GroupID     int    `json:"groupId"`
	GroupName   string `json:"groupName"`
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ServiceException represents an error from the API
type ServiceException struct {
	MessageID string `json:"messageId"`
	Text      string `json:"text"`
}

// MessageResponse represents a message response
type MessageResponse struct {
	MessageID string        `json:"messageId"`
	Status    MessageStatus `json:"status"`
	To        string        `json:"to"`
}

// RequestError represents request error
type RequestError struct {
	ServiceException ServiceException `json:"serviceException"`
}

// DeliveryResult represents a single delivery status result
type DeliveryResult struct {
	MessageID string        `json:"messageId"`
	Status    MessageStatus `json:"status"`
}

// DeliveryStatusBody represents the delivery status callback body
type DeliveryStatusBody struct {
	Results []DeliveryResult `json:"results"`
}

// Config holds WhatsApp configuration
type Config struct {
	BaseURL string
	APIKey  string
	Sender  string
}

// Content represents message content
type Content struct {
	Type         string                 `json:"type,omitempty"`
	Template     string                 `json:"template,omitempty"`
	TemplateName string                 `json:"templateName,omitempty"`
	Placeholders []string               `json:"placeholders,omitempty"`
	Text         string                 `json:"text,omitempty"`
	MediaURL     string                 `json:"mediaUrl,omitempty"`
	Caption      string                 `json:"caption,omitempty"`
	Content      map[string]interface{} `json:"content,omitempty"`
	TemplateData *TemplateData          `json:"templateData,omitempty"`
	Language     string                 `json:"language,omitempty"`
}

// TemplateData represents template data structure
type TemplateData struct {
	Body TemplateBody `json:"body"`
}

// TemplateBody represents template body with placeholders
type TemplateBody struct {
	Placeholders []string `json:"placeholders"`
}

// SendParams holds parameters for sending messages
type SendParams struct {
	Phone        string
	Text         string
	Content      *Content
	Domain       string
	SenderID     string
	Config       *Config
	Template     string
	Placeholders []string
}

// APIResponse represents API response
type APIResponse struct {
	Messages     []MessageResponse `json:"messages,omitempty"`
	RequestError *RequestError     `json:"requestError,omitempty"`
}

// NotificationResult represents delivery notification result
type NotificationResult struct {
	MessageID string        `json:"messageId"`
	Status    MessageStatus `json:"status"`
	SeenAt    string        `json:"seenAt,omitempty"`
}

// NotificationBody represents notification callback body
type NotificationBody struct {
	Results []NotificationResult `json:"results"`
}

// InfoBipBalanceResponse represents InfoBip balance API response
type InfoBipBalanceResponse struct {
	Balance  float64 `json:"balance"`
	Currency string  `json:"currency"`
}

type SMSProvider interface {
	Send(params SendParams, config *config.SMSGatewayConfig) (*SendResponse, error)
	CheckStatus(phone, requestID string, config *config.SMSGatewayConfig) (*StatusResponse, error)
	Balance() (*BalanceResponse, error)
}

// WhatsappProvider interface for different providers
type WhatsappProvider interface {
	Send(params SendParams, config *config.WhatsappGatewayConfig) (*SendResponse, error)
	Notification(providerParams map[string]string, body *NotificationBody) error
	DeliveryStatus(providerParams map[string]string, result NotificationResult) error
	Balance() (*BalanceResponse, error)
}
