package yekonga

import (
	"fmt"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/gateway"
	"github.com/robertkonga/yekonga-server-go/gateway/payment"
	"github.com/robertkonga/yekonga-server-go/gateway/setting"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
)

// AddCloudFunction registers a new cloud function
func (y *YekongaData) SetSendSMS(fn BackendCloudFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.primaryFunctions[SendSMSCloudFunctionKey]; exists {
		return fmt.Errorf("cloud function %s already exists", SendSMSCloudFunctionKey)
	}

	y.primaryFunctions[SendSMSCloudFunctionKey] = fn
	logger.Error("Registered cloud function", SendSMSCloudFunctionKey)
	return nil
}

func (y *YekongaData) SendSMS(data setting.SendParams, config *config.SMSGatewayConfig) (*setting.SendResponse, error) {
	y.mut.RLock()
	fun, exists := y.primaryFunctions[SendSMSCloudFunctionKey]
	y.mut.RUnlock()

	if exists {
		result, err := fun(data)

		if r, ok := result.(*setting.SendResponse); ok {
			return r, err
		}

		return nil, err
	}

	provider := gateway.NewSMSProvider(&y.Config.ApiGateway.SMS)
	result, err := provider.Send(data, config)

	return result, err
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) SetSendEmail(fn BackendCloudFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.primaryFunctions[SendEmailCloudFunctionKey]; exists {
		return fmt.Errorf("cloud function %s already exists", SendEmailCloudFunctionKey)
	}

	y.primaryFunctions[SendEmailCloudFunctionKey] = fn
	logger.Error("Registered cloud function", SendEmailCloudFunctionKey)
	return nil
}

func (y *YekongaData) SendEmail(data helper.EmailMessage, config *config.SMTPConfig) (*setting.SendResponse, error) {
	y.mut.RLock()
	fun, exists := y.primaryFunctions[SendSMSCloudFunctionKey]
	y.mut.RUnlock()

	if exists {
		result, err := fun(data)

		if r, ok := result.(*setting.SendResponse); ok {
			return r, err
		}

		return nil, err
	}

	// Initialize with your config
	sender := helper.NewEmailSender(&y.Config.Mail.Smtp)

	// Example 4: Send complex email with all features
	err := sender.Send(&data, config)

	var result *setting.SendResponse

	if err != nil {
		console.Error("Error sending complex email: %v\n", err)
	} else {
		result = &setting.SendResponse{
			Status:  "SUCCESS",
			Message: "Complex email sent successfully!",
		}
		fmt.Println("Complex email sent successfully!")
	}

	return result, err
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) SetSendWhatsapp(fn BackendCloudFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.primaryFunctions[SendWhatsappCloudFunctionKey]; exists {
		return fmt.Errorf("cloud function %s already exists", SendWhatsappCloudFunctionKey)
	}

	y.primaryFunctions[SendWhatsappCloudFunctionKey] = fn
	logger.Error("Registered cloud function", SendWhatsappCloudFunctionKey)
	return nil
}

func (y *YekongaData) SendWhatsapp(data setting.SendParams, config *config.WhatsappGatewayConfig) (*setting.SendResponse, error) {
	y.mut.RLock()
	fun, exists := y.primaryFunctions[SendWhatsappCloudFunctionKey]
	y.mut.RUnlock()

	if exists {
		result, err := fun(data)

		if r, ok := result.(*setting.SendResponse); ok {
			return r, err
		}

		return nil, err
	}

	provider := gateway.NewWhatsappProvider(&y.Config.ApiGateway.Whatsapp)
	result, err := provider.Send(data, config)

	return result, err
}

// PaymentWebhookData is what a SetPaymentWebhook function receives.
type PaymentWebhookData struct {
	Event   *payment.WebhookEvent // authenticated notification; Event.Success reports a settled payment
	Payment *datatype.DataMap     // Payment record after the webhook was applied; nil when none matches or the event carries no payment state
	Request *Request
}

// SetPaymentWebhook registers a function that runs for every authenticated
// payment webhook, after the Payment record has been updated. It receives a
// *PaymentWebhookData. Returning an error answers the gateway with 500 so it
// retries; the retry calls the function again, so it must be idempotent.
func (y *YekongaData) SetPaymentWebhook(fn BackendCloudFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.primaryFunctions[PaymentCloudFunctionKey]; exists {
		return fmt.Errorf("cloud function %s already exists", PaymentCloudFunctionKey)
	}

	y.primaryFunctions[PaymentCloudFunctionKey] = fn
	logger.Error("Registered cloud function", PaymentCloudFunctionKey)
	return nil
}

// PaymentWebhook runs the function registered with SetPaymentWebhook, if any.
func (y *YekongaData) PaymentWebhook(data *PaymentWebhookData) error {
	y.mut.RLock()
	fun, exists := y.primaryFunctions[PaymentCloudFunctionKey]
	y.mut.RUnlock()

	if !exists {
		return nil
	}

	_, err := fun(data)

	return err
}
