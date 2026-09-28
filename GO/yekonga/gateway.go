package yekonga

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
)

// SmsRecipient represents an SMS recipient
type SmsRecipient struct {
	RecipientID int    `json:"recipient_id"`
	DestAddr    string `json:"dest_addr"`
}

// SendRequest represents the SMS send request body
type SendRequest struct {
	SourceAddr   string         `json:"source_addr"`
	ScheduleTime string         `json:"schedule_time"`
	Message      string         `json:"message"`
	Encoding     int            `json:"encoding"`
	Recipients   []SmsRecipient `json:"recipients"`
}

// SendResponse represents the response from send API
type SendResponse struct {
	Status    string      `json:"status"`
	Message   string      `json:"message"`
	MessageID string      `json:"messageId"`
	Code      int         `json:"code"`
	RequestID string      `json:"request_id"`
	Response  interface{} `json:"response"`
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
	Status  string      `json:"status"`
	Balance float64     `json:"balance"`
	Data    BalanceData `json:"data,omitempty"`
}

// BalanceData holds the balance data from API
type BalanceData struct {
	CreditBalance float64 `json:"credit_balance"`
}

// SmsControl handles SMS operations
type SmsControl struct {
	DefaultConfig *config.SMSGatewayConfig
	HTTPClient    *http.Client
}

// NewSmsControl creates a new SMS control instance
func NewSmsControl(defaultConfig *config.SMSGatewayConfig) *SmsControl {
	return &SmsControl{
		DefaultConfig: defaultConfig,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Send sends an SMS message
func (s *SmsControl) Send(phone, text, senderID string, config *config.SMSGatewayConfig) (*SendResponse, error) {
	url := "https://apisms.beem.africa/v1/send"

	// Use default config if not provided
	apiKey := s.DefaultConfig.APIKey
	secretKey := s.DefaultConfig.SecretKey
	if senderID == "" {
		senderID = s.DefaultConfig.Sender
	}

	// Override with custom config if provided
	if config != nil {
		if config.Sender != "" {
			senderID = config.Sender
		}
		if config.APIKey != "" {
			apiKey = config.APIKey
		}
		if config.SecretKey != "" {
			secretKey = config.SecretKey
		}
	}

	// Build recipients
	recipients := []SmsRecipient{
		{
			RecipientID: getRandomInt(),
			DestAddr:    phone,
		},
	}

	// Build request body
	body := SendRequest{
		SourceAddr:   senderID,
		ScheduleTime: "",
		Message:      text,
		Encoding:     0,
		Recipients:   recipients,
	}

	// Make HTTP request
	apiResponse, err := s.postJSON(url, body, apiKey, secretKey)
	if err != nil {
		return &SendResponse{
			Status:  "FAILED",
			Message: err.Error(),
		}, err
	}

	// Parse response
	var responseData map[string]interface{}
	if err := json.Unmarshal(apiResponse, &responseData); err != nil {
		return &SendResponse{
			Status:  "FAILED",
			Message: "Failed to parse response",
		}, err
	}

	// Extract response fields
	code, _ := responseData["code"].(float64)
	message, _ := responseData["message"].(string)
	requestID, _ := responseData["request_id"].(string)

	status := "FAILED"
	if int(code) == 100 {
		status = "SUCCESS"
	}

	return &SendResponse{
		Status:    status,
		Message:   message,
		MessageID: requestID,
		Code:      int(code),
		RequestID: requestID,
		Response:  responseData,
	}, nil
}

// CheckStatus checks the delivery status of an SMS
func (s *SmsControl) CheckStatus(phone, requestID string, config *config.SMSGatewayConfig) (*StatusResponse, error) {
	// Use default config if not provided
	apiKey := s.DefaultConfig.APIKey
	secretKey := s.DefaultConfig.SecretKey

	// Override with custom config if provided
	if config != nil {
		if config.APIKey != "" {
			apiKey = config.APIKey
		}
		if config.SecretKey != "" {
			secretKey = config.SecretKey
		}
	}

	url := fmt.Sprintf("https://dlrapi.beem.africa/public/v1/delivery-reports?dest_addr=%s&request_id=%s",
		phone, requestID)

	// Make HTTP request
	apiResponse, err := s.getJSON(url, apiKey, secretKey)
	if err != nil {
		return &StatusResponse{
			Status:  "FAILED",
			Message: "no response",
		}, err
	}

	// Parse response
	var responseData map[string]interface{}
	if err := json.Unmarshal(apiResponse, &responseData); err != nil {
		return &StatusResponse{
			Status:  "FAILED",
			Message: "Failed to parse response",
		}, err
	}

	status, _ := responseData["status"].(string)
	reqID, _ := responseData["request_id"].(string)

	return &StatusResponse{
		Status:    status,
		Message:   reqID,
		RequestID: reqID,
		Response:  responseData,
	}, nil
}

// Balance checks the account balance
func (s *SmsControl) Balance() (*BalanceResponse, error) {
	url := "https://apisms.beem.africa/public/v1/vendors/balance"

	apiResponse, err := s.getJSON(url, s.DefaultConfig.APIKey, s.DefaultConfig.SecretKey)
	if err != nil {
		return &BalanceResponse{
			Status:  "FAILED",
			Balance: 0,
		}, err
	}

	// Parse response
	var responseData struct {
		Data BalanceData `json:"data"`
	}

	if err := json.Unmarshal(apiResponse, &responseData); err != nil {
		return &BalanceResponse{
			Status:  "FAILED",
			Balance: 0,
		}, err
	}

	return &BalanceResponse{
		Status:  "SUCCESS",
		Balance: responseData.Data.CreditBalance,
		Data:    responseData.Data,
	}, nil
}

// postJSON makes a POST request with JSON body
func (s *SmsControl) postJSON(url string, body interface{}, apiKey, secretKey string) ([]byte, error) {
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
	req.Header.Set("Authorization", "Basic "+getBasicAuth(apiKey, secretKey))

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// getJSON makes a GET request
func (s *SmsControl) getJSON(url, apiKey, secretKey string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+getBasicAuth(apiKey, secretKey))

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// getBasicAuth creates Basic Auth header value
func getBasicAuth(apiKey, secretKey string) string {
	auth := apiKey + ":" + secretKey
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// getRandomInt generates a random integer
func getRandomInt() int {
	return rand.Intn(100000000)
}
