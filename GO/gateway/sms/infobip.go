package sms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/gateway/setting"
)

// InfobipProvider handles SMS operations for InfoBip
type InfobipProvider struct {
	DefaultConfig *config.SMSGatewayConfig
	HTTPClient    *http.Client
}

// Send sends an SMS message
func (s *InfobipProvider) Send(params setting.SendParams, config *config.SMSGatewayConfig) (*setting.SendResponse, error) {

	// Use default config if not provided
	apiKey := s.DefaultConfig.APIKey
	baseURL := s.DefaultConfig.BaseURL
	senderID := params.SenderID
	phone := params.Phone
	text := params.Text

	if senderID == "" {
		senderID = s.DefaultConfig.Sender
	}

	// Override with custom config if provided
	if config != nil {
		if isNotEmpty(config.BaseURL) {
			baseURL = config.BaseURL
		}
		if isNotEmpty(config.APIKey) {
			apiKey = config.APIKey
		}
		if isNotEmpty(config.Sender) {
			senderID = config.Sender
		}
	}

	url := fmt.Sprintf("https://%s/sms/2/text/advanced", baseURL)

	// Build request body
	recipients := []setting.Destination{
		{To: phone},
	}

	body := setting.SendRequest{
		Messages: []setting.Message{
			{
				From:         senderID,
				Text:         text,
				Destinations: recipients,
			},
		},
	}

	// Make HTTP request
	apiResponse, err := s.postJSON(url, body, apiKey)
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
	status := "FAILED"
	var message string
	var messageID string

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

// CheckStatus checks the delivery status of an SMS
func (s *InfobipProvider) CheckStatus(phone, messageID string, config *config.SMSGatewayConfig) (*setting.StatusResponse, error) {
	apiKey := s.DefaultConfig.APIKey
	baseURL := s.DefaultConfig.BaseURL

	// Override with custom config if provided
	if config != nil {
		if isNotEmpty(config.BaseURL) {
			baseURL = config.BaseURL
		}
		if isNotEmpty(config.APIKey) {
			apiKey = config.APIKey
		}
	}

	url := fmt.Sprintf("https://%s/public/v1/delivery-reports?dest_addr=%s&request_id=%s",
		baseURL, phone, messageID)

	// Make HTTP request
	apiResponse, err := s.getJSON(url, apiKey)
	if err != nil {
		return &setting.StatusResponse{
			Status:  "FAILED",
			Message: "no response",
		}, err
	}

	// Parse response
	var responseData setting.APIResponse
	if err := json.Unmarshal(apiResponse, &responseData); err != nil {
		return &setting.StatusResponse{
			Status:  "FAILED",
			Message: "Failed to parse response",
		}, err
	}

	// Process response
	status := "FAILED"
	var message string
	var msgID string

	if len(responseData.Messages) > 0 {
		status = "SUCCESS"
		message = responseData.Messages[0].Status.Description
		msgID = responseData.Messages[0].MessageID
	} else if responseData.RequestError != nil {
		status = "FAILED"
		message = responseData.RequestError.ServiceException.Text
		msgID = responseData.RequestError.ServiceException.MessageID
	}

	return &setting.StatusResponse{
		Status:    status,
		Message:   message,
		RequestID: msgID,
		Response:  responseData,
	}, nil
}

// Balance checks the account balance
func (s *InfobipProvider) Balance() (*setting.BalanceResponse, error) {
	url := fmt.Sprintf("https://%s/account/1/balance", s.DefaultConfig.BaseURL)

	apiResponse, err := s.getJSON(url, s.DefaultConfig.APIKey)
	if err != nil {
		return &setting.BalanceResponse{
			Status:  "FAILED",
			Balance: 0,
		}, err
	}

	// Parse response
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
func (s *InfobipProvider) postJSON(url string, body interface{}, apiKey string) ([]byte, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("App %s", apiKey))

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// getJSON makes a GET request
func (s *InfobipProvider) getJSON(url, apiKey string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("App %s", apiKey))

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// Helper functions for checking empty/non-empty strings
func isEmpty(s string) bool {
	return s == ""
}

func isNotEmpty(s string) bool {
	return s != ""
}
