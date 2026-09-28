package gateway

import (
	"errors"
	"net/http"
	"time"

	"github.com/robertkonga/yekonga-server-go/gateway/payment"
)

// NewPaymentProvider creates a payment provider for defaultConfig.Provider
// (flutterwave, paypal, pesapal, selcom, clickpesa, azampay, stripe,
// twocheckout, or any gateway added via payment.RegisterFactory). It fails
// when the provider is unknown or its required credentials are missing.
func NewPaymentProvider(defaultConfig *payment.Config) (payment.Provider, error) {
	if defaultConfig == nil {
		return nil, errors.New("payment config is required")
	}

	if defaultConfig.HTTPClient == nil {
		defaultConfig.HTTPClient = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	return payment.New(*defaultConfig)
}
