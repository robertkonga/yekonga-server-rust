package gateway

import (
	"net/http"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/gateway/setting"
	"github.com/robertkonga/yekonga-server-go/gateway/whatsapp"
	"github.com/robertkonga/yekonga-server-go/helper"
)

// NewWhatsappProvider creates a new WhatsApp control instance
func NewWhatsappProvider(defaultConfig *config.WhatsappGatewayConfig) setting.WhatsappProvider {
	if helper.IsEmpty(defaultConfig.Sender) {
		defaultConfig.Sender = "441134960000"
	}

	if defaultConfig.Provider == config.ProviderInfobip {
		if helper.IsEmpty(defaultConfig.BaseURL) {
			defaultConfig.BaseURL = "43vk1n.api.infobip.com"
		}
	}

	return &whatsapp.InfobipProvider{
		DefaultConfig: defaultConfig,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}
