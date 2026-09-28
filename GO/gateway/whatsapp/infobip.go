package whatsapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/gateway/setting"
	"github.com/robertkonga/yekonga-server-go/helper"
)

// InfobipProvider handles InfoBip WhatsApp operations
type InfobipProvider struct {
	DefaultConfig *config.WhatsappGatewayConfig
	HTTPClient    *http.Client
}

// Send sends a WhatsApp message via InfoBip
func (i *InfobipProvider) Send(params setting.SendParams, config *config.WhatsappGatewayConfig) (*setting.SendResponse, error) {
	status := "FAILED"
	messageID := helper.UUID()
	baseURL := i.DefaultConfig.BaseURL
	apiKey := i.DefaultConfig.APIKey
	senderID := i.DefaultConfig.Sender
	domain := params.Domain

	// Apply config overrides
	if params.Config != nil {
		if helper.IsNotEmpty(params.Config.BaseURL) {
			baseURL = params.Config.BaseURL
		}
		if helper.IsNotEmpty(params.Config.APIKey) {
			apiKey = params.Config.APIKey
		}
		if helper.IsNotEmpty(params.Config.Sender) {
			senderID = params.Config.Sender
		}
	}

	// Determine message type and content
	var contentBody interface{}
	msgType := "text"

	if params.Content != nil {
		if helper.IsNotEmpty(params.Content.Type) {
			msgType = params.Content.Type
		} else if helper.IsNotEmpty(params.Content.Content) {
			msgType = "template"
		}

		// Build content body
		if helper.IsNotEmpty(params.Content.TemplateName) {
			contentBody = params.Content
		} else if helper.IsNotEmpty(params.Content.Content) {
			contentBody = params.Content.Content
		} else if msgType == "template" {
			placeholders := params.Content.Placeholders
			if placeholders == nil {
				placeholders = []string{}
			}
			template := params.Content.Template
			if template == "" {
				template = params.Content.TemplateName
			}

			contentBody = setting.Content{
				TemplateName: template,
				TemplateData: &setting.TemplateData{
					Body: setting.TemplateBody{
						Placeholders: placeholders,
					},
				},
				Language: "en",
			}
		} else if msgType == "text" {
			text := params.Content.Text
			if helper.IsEmpty(text) && params.Content.Content != nil {
				if textVal, ok := params.Content.Content["text"].(string); ok {
					text = textVal
				}
			}

			if helper.IsEmpty(text) {
				text = "..."
			}
			contentBody = map[string]string{"text": text}
		} else if params.Content.MediaURL != "" {
			contentBody = map[string]string{
				"mediaUrl": params.Content.MediaURL,
				"caption":  params.Content.Caption,
			}
		}
	}

	url := fmt.Sprintf("https://%s/whatsapp/1/message/%s", baseURL, msgType)

	// Build message
	singleMessage := map[string]interface{}{
		"from":         senderID,
		"to":           params.Phone,
		"messageId":    messageID,
		"content":      contentBody,
		"callbackData": fmt.Sprintf(`{"id": "%s"}`, messageID),
		"notifyUrl":    fmt.Sprintf("https://%s/infobip/whatsapp/notification", domain),
	}

	var body interface{}
	if msgType == "text" {
		body = singleMessage
	} else {
		body = map[string]interface{}{
			"messages": []interface{}{singleMessage},
		}
	}

	// Make request
	apiResponse, err := i.postJSON(url, body, apiKey)

	if err != nil {
		return &setting.SendResponse{
			Status:  "FAILED",
			Message: err.Error(),
		}, err
	}

	// Parse response
	var responseData setting.APIResponse
	if err := json.Unmarshal(apiResponse, &responseData); err != nil {
		return &setting.SendResponse{
			Status:  "FAILED",
			Message: "Failed to parse response",
		}, err
	}

	// Process response
	var message string
	if len(responseData.Messages) > 0 {
		status = "SUCCESS"
		message = responseData.Messages[0].Status.Description
		messageID = responseData.Messages[0].MessageID
	} else if responseData.RequestError != nil {
		status = "FAILED"
		message = responseData.RequestError.ServiceException.Text
		messageID = responseData.RequestError.ServiceException.MessageID
	}

	return &setting.SendResponse{
		Status:    status,
		Message:   message,
		MessageID: messageID,
		Response:  responseData,
	}, nil
}

// Notification processes notification callbacks
func (i *InfobipProvider) Notification(providerParams map[string]string, body *setting.NotificationBody) error {
	if body == nil || len(body.Results) == 0 {
		return nil
	}

	for _, result := range body.Results {
		if err := i.DeliveryStatus(providerParams, result); err != nil {
			fmt.Printf("Error processing delivery status: %v\n", err)
		}
	}

	return nil
}

// DeliveryStatus updates delivery status
func (i *InfobipProvider) DeliveryStatus(providerParams map[string]string, result setting.NotificationResult) error {
	var status string

	if result.Status.GroupName != "" {
		groupName := result.Status.GroupName

		switch groupName {
		case "DELIVERED":
			status = "delivered"
		case "UNDELIVERED":
			status = "undelivered"
		default:
			status = groupName
		}
	}

	// Build update body
	updateBody := make(map[string]interface{})
	if helper.IsNotEmpty(status) {
		updateBody["status"] = status
	}
	if helper.IsNotEmpty(result.SeenAt) {
		updateBody["isSeen"] = true
		updateBody["opened"] = true
	}

	// Update notification in database
	if result.MessageID != "" {
		fmt.Printf("Updating notification %s with status: %s\n", result.MessageID, status)
	}

	return nil
}

// Balance checks account balance
func (i *InfobipProvider) Balance() (*setting.BalanceResponse, error) {
	url := fmt.Sprintf("https://%s/account/1/balance", i.DefaultConfig.BaseURL)

	apiResponse, err := i.getJSON(url, i.DefaultConfig.APIKey)

	if err != nil {
		return &setting.BalanceResponse{
			Status:  "FAILED",
			Balance: 0,
		}, err
	}

	var responseData setting.InfoBipBalanceResponse
	if err := json.Unmarshal(apiResponse, &responseData); err != nil {
		return &setting.BalanceResponse{
			Status:  "FAILED",
			Balance: 0,
		}, err
	}

	if responseData.Balance > 0 {
		return &setting.BalanceResponse{
			Status:   "SUCCESS",
			Balance:  responseData.Balance,
			Response: responseData,
		}, nil
	}

	return &setting.BalanceResponse{
		Status:   "FAILED",
		Balance:  0,
		Response: responseData,
	}, nil
}

// postJSON makes a POST request with JSON body
func (i *InfobipProvider) postJSON(url string, body interface{}, apiKey string) ([]byte, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("App %s", apiKey))

	resp, err := i.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// getJSON makes a GET request
func (i *InfobipProvider) getJSON(url, apiKey string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("App %s", apiKey))

	resp, err := i.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
