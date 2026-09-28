package gateway

import (
	"net/http"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/gateway/setting"
	"github.com/robertkonga/yekonga-server-go/gateway/sms"
	"github.com/robertkonga/yekonga-server-go/helper"
)

// NewSMSProvider creates a new SMS control instance
func NewSMSProvider(defaultConfig *config.SMSGatewayConfig) setting.SMSProvider {
	if helper.IsEmpty(defaultConfig.Sender) {
		defaultConfig.Sender = "INFO"
	}

	if defaultConfig.Provider == config.ProviderInfobip {
		if helper.IsEmpty(defaultConfig.BaseURL) {
			defaultConfig.BaseURL = "43vk1n.api.infobip.com"
		}

		return &sms.InfobipProvider{
			DefaultConfig: defaultConfig,
			HTTPClient: &http.Client{
				Timeout: 30 * time.Second,
			},
		}
	}

	return &sms.BeemProvider{
		DefaultConfig: defaultConfig,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}
