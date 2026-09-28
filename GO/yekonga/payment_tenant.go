package yekonga

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/gateway"
	"github.com/robertkonga/yekonga-server-go/gateway/payment"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

const (
	tenantConfigModelName     = "TenantConfig"
	tenantConfigPaymentField  = "payment"
	paymentConfigDefault      = "default"
	paymentConfigTenant       = "tenant"
	paymentWebhookTenantParam = "tenantId"
)

// paymentGateways is the set of providers one payment call or webhook uses:
// the server defaults, with any provider the tenant configures itself
// replacing the default of the same name.
type paymentGateways struct {
	ctrl            *payment.Controller
	tenantId        string
	tenantProviders []config.PaymentProviderConfig
	overridden      map[string]bool // provider names served by the tenant's own config
}

type tenantPaymentEntry struct {
	fingerprint string
	gateways    *paymentGateways
}

// isTenant reports whether provider runs on the tenant's own credentials.
func (g *paymentGateways) isTenant(provider string) bool {
	if g.overridden == nil {
		return false
	}

	p, err := g.ctrl.Get(provider)
	if err != nil {
		return false
	}

	return g.overridden[strings.ToLower(p.Name())]
}

// TenantPaymentController returns the gateways for tenantId. Providers the
// tenant configures in TenantConfig.payment.providers override the server
// defaults (config.apiGateway.payment.providers) of the same name; the rest
// fall back to the defaults. An empty tenantId, or a tenant without payment
// config, gets the default controller.
func (y *YekongaData) TenantPaymentController(tenantId string) (*payment.Controller, error) {
	gateways, err := y.paymentGatewaysFor(tenantId)
	if err != nil {
		return nil, err
	}

	return gateways.ctrl, nil
}

func (y *YekongaData) paymentGatewaysFor(tenantId string) (*paymentGateways, error) {
	base, err := y.PaymentController()
	if err != nil {
		return nil, err
	}

	defaults := &paymentGateways{ctrl: base}
	if tenantId == "" {
		return defaults, nil
	}

	providers, fingerprint := y.tenantPaymentProviders(tenantId)
	if len(providers) == 0 {
		return defaults, nil
	}

	y.tenantPaymentsMu.Lock()
	defer y.tenantPaymentsMu.Unlock()

	// Rebuild only when the tenant's config changed, so providers keep their
	// cached access tokens between calls.
	if entry, ok := y.tenantPayments[tenantId]; ok && entry.fingerprint == fingerprint {
		return entry.gateways, nil
	}

	ctrl, _ := payment.NewController()
	for _, name := range base.Providers() {
		if p, err := base.Get(name); err == nil {
			ctrl.Use(p)
		}
	}

	overridden := map[string]bool{}
	for _, p := range providers {
		provider, err := gateway.NewPaymentProvider(paymentConfigFrom(p))
		if err != nil {
			return nil, fmt.Errorf("tenant %s payment provider %q: %w", tenantId, p.Provider, err)
		}

		ctrl.Use(provider)
		overridden[strings.ToLower(provider.Name())] = true
	}

	gateways := &paymentGateways{ctrl: ctrl, tenantId: tenantId, tenantProviders: providers, overridden: overridden}

	if y.tenantPayments == nil {
		y.tenantPayments = map[string]*tenantPaymentEntry{}
	}
	y.tenantPayments[tenantId] = &tenantPaymentEntry{fingerprint: fingerprint, gateways: gateways}

	return gateways, nil
}

// tenantPaymentProviders reads TenantConfig.payment for tenantId, shaped like
// config.apiGateway.payment ({"providers": [...]}). It's read straight from
// the collection, never through GetTenantConfig, because that is served to
// clients by /tenant-config and these are credentials.
func (y *YekongaData) tenantPaymentProviders(tenantId string) ([]config.PaymentProviderConfig, string) {
	if !y.Config.HasTenant {
		return nil, ""
	}

	if _, ok := y.models[tenantConfigModelName]; !ok {
		return nil, ""
	}

	record := y.ModelQuery(tenantConfigModelName).SkipTenant().SkipBeforeCommit().Where("tenantId", tenantId).FindOne(nil)
	if helper.IsEmpty(record) {
		return nil, ""
	}

	var raw []byte
	switch v := (*record)[tenantConfigPaymentField].(type) {
	case nil:
		return nil, ""
	case string:
		raw = []byte(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, ""
		}
		raw = b
	}

	var cfg config.PaymentGatewayConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, ""
	}

	return cfg.Providers, string(raw)
}

// providerConfig returns the config provider runs on: the tenant's when it
// overrides it, otherwise the server default.
func (y *YekongaData) providerConfig(gateways *paymentGateways, provider string) *config.PaymentProviderConfig {
	list := y.Config.ApiGateway.Payment.Providers
	if gateways.isTenant(provider) {
		list = gateways.tenantProviders
	}

	for i := range list {
		if strings.EqualFold(strings.TrimSpace(list[i].Provider), strings.TrimSpace(provider)) {
			return &list[i]
		}
	}

	return nil
}

func paymentConfigFrom(p config.PaymentProviderConfig) *payment.Config {
	return &payment.Config{
		Provider:    p.Provider,
		Sandbox:     p.Sandbox,
		BaseURL:     p.BaseURL,
		WebhookURL:  p.WebhookURL,
		Credentials: p.Credentials,
	}
}

// requestTenantID is the tenant a request acts for, resolved the same way
// model queries scope their data: the token's tenant first, then the one
// matched from the request's domain.
func (y *YekongaData) requestTenantID(req *Request) string {
	if req == nil || !y.Config.HasTenant {
		return ""
	}

	if payload := req.TokenPayload(); payload != nil && helper.IsNotEmpty(payload.TenantId) {
		return idString(payload.TenantId)
	}

	return idString(req.TenantId())
}

func idString(v interface{}) string {
	switch id := v.(type) {
	case nil:
		return ""
	case string:
		return id
	case bson.ObjectID:
		if id.IsZero() {
			return ""
		}
		return id.Hex()
	}

	if helper.IsEmpty(v) {
		return ""
	}

	return fmt.Sprint(v)
}
