// Package payment_providers is a unified payment API over several gateways.
//
// main.go holds the provider-agnostic surface: the request/response types, the
// Provider interface, the factory registry and the Controller that routes a
// call to the right gateway by name. Each gateway lives in its own file
// (flutterwave.go, paypal.go, pesapal.go, selcom.go, clickpesa.go, azampay.go,
// stripe.go, twocheckout.go) and registers itself here.
//
//	ctrl, _ := payment_providers.NewController(
//		payment_providers.Config{Provider: "flutterwave", Credentials: map[string]string{
//			payment_providers.KeySecretKey: "...", payment_providers.KeyWebhookHash: "...",
//		}},
//	)
//	res, err := ctrl.CreatePayment(ctx, "flutterwave", payment_providers.PaymentRequest{
//		Reference: paymentId, Amount: 100, Currency: "USD", ReturnURL: "https://app/return",
//		Customer: payment_providers.Customer{Name: "Jane Doe", Email: "jane@example.com"},
//	})
//	// redirect the payer to res.CheckoutURL
//
// The package only depends on the standard library.
package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider names, as used in Config.Provider and Controller calls. They match
// the values stored in the `provider` field of the Payments collection.
const (
	ProviderFlutterwave = "flutterwave"
	ProviderPayPal      = "paypal"
	ProviderPesapal     = "pesapal"
	ProviderSelcom      = "selcom"
	ProviderClickPesa   = "clickpesa"
	ProviderAzamPay     = "azampay"
	ProviderStripe      = "stripe"
	Provider2Checkout   = "2checkout"
)

// Credential keys understood by the providers (Config.Credentials).
const (
	KeySecretKey      = "secret_key"      // flutterwave, stripe, 2checkout (REST API + IPN secret key)
	KeyWebhookHash    = "webhook_hash"    // flutterwave: the "secret hash" set in the dashboard
	KeyWebhookSecret  = "webhook_secret"  // stripe: the webhook endpoint's signing secret (whsec_...)
	KeyClientID       = "client_id"       // paypal, azampay
	KeyClientSecret   = "client_secret"   // paypal, azampay
	KeyWebhookID      = "webhook_id"      // paypal
	KeyConsumerKey    = "consumer_key"    // pesapal
	KeyConsumerSecret = "consumer_secret" // pesapal
	KeyIPNID          = "ipn_id"          // pesapal (optional, registered on demand when empty)
	KeyRefundUser     = "refund_username" // pesapal (optional, defaults to "system")
	KeyAPIKey         = "api_key"         // selcom, clickpesa, azampay (X-API-Key, sandbox only)
	KeyAPISecret      = "api_secret"      // selcom
	KeyVendor         = "vendor"          // selcom (vendor / till number)
	KeyChecksumKey    = "checksum_key"    // clickpesa (optional): HMAC secret signing requests/webhooks
	KeyAppName        = "app_name"        // azampay
	KeyPublicKey      = "public_key"      // azampay: PEM certificate used to verify callback signatures
	KeyMerchantCode   = "merchant_code"   // 2checkout
	KeyBuyLinkSecret  = "buy_link_secret" // 2checkout: the ConvertPlus "Secret Word"
)

// Status is the provider-neutral payment state. The values line up with the
// `status` options of the Payments collection; gateway states with no direct
// equivalent (cancelled, expired, declined) are reported as StatusFailed.
type Status string

const (
	StatusPending   Status = "PENDING"
	StatusSucceeded Status = "SUCCESS"
	StatusFailed    Status = "FAILED"
	StatusRefunded  Status = "REFUNDED"
)

var (
	ErrUnknownProvider = errors.New("payment_providers: unknown provider")
	ErrNotSupported    = errors.New("payment_providers: operation not supported by provider")
	ErrInvalidRequest  = errors.New("payment_providers: invalid request")
	ErrInvalidWebhook  = errors.New("payment_providers: invalid webhook")
)

// ProviderError is returned when a gateway rejects a call.
type ProviderError struct {
	Provider   string
	HTTPStatus int
	Code       string
	Message    string
}

func (e *ProviderError) Error() string {
	msg := e.Provider + ": "
	if e.HTTPStatus != 0 {
		msg += "http " + strconv.Itoa(e.HTTPStatus) + ": "
	}
	if e.Code != "" {
		msg += "[" + e.Code + "] "
	}
	return msg + e.Message
}

// Customer is the payer. Phone should be in international format.
type Customer struct {
	Name        string
	Email       string
	Phone       string
	CountryCode string // ISO 3166-1 alpha-2, optional
}

// PaymentRequest starts a hosted checkout.
type PaymentRequest struct {
	Reference     string // unique merchant reference, e.g. the Payments.id
	Amount        float64
	Currency      string // ISO 4217
	Description   string
	Method        string // any of "system", "selcom", "clickpesa", "azampay", "flutterwave", "pesapal", "stripe", "paypal", "2checkout", "manual", "cash"
	Channel       string // any of "vodacom", "tigo", "airtel", "halotel", "ttcl", "zantel", "mobile_money", "card", "bank_transfer", "paypal", "qr", "cash", "manual"
	PaymentMethod string // any of "manual", "card", "bank_transfer", "mobile_money", "paypal", "crypto"
	Customer      Customer
	ReturnURL     string            // where the payer lands after paying
	CancelURL     string            // where the payer lands after cancelling (falls back to ReturnURL)
	WebhookURL    string            // overrides Config.WebhookURL
	Metadata      map[string]string // echoed back by providers that support it
}

// PaymentResponse is the outcome of CreatePayment.
type PaymentResponse struct {
	Provider    string
	Reference   string // merchant reference
	ProviderRef string // gateway's id for the order/checkout, when it issues one
	CheckoutURL string // redirect the payer here
	Status      Status
	Raw         map[string]any
}

// VerifyRequest identifies a payment to check. Send both values when you have
// them: PayPal and Pesapal look up by ProviderRef, Flutterwave and Selcom by
// Reference.
type VerifyRequest struct {
	Reference   string
	ProviderRef string
}

// PaymentResult is the verified state of a payment. Always compare Amount and
// Currency with what you expected before marking an invoice as paid.
type PaymentResult struct {
	Provider          string
	Reference         string
	ProviderRef       string // gateway order/checkout id
	ProviderPaymentID string // id needed to refund (capture id, transaction id, confirmation code)
	Status            Status
	Amount            float64
	Currency          string
	Method            string // channel / payment method as reported by the gateway
	FailureReason     string
	Raw               map[string]any
}

// PushRequest triggers a direct mobile-money charge: the gateway sends the
// customer a prompt on their phone (a USSD/STK push) and they approve it with
// their mobile money PIN, with no redirect involved. Only Selcom and
// Flutterwave support this; PayPal and Pesapal return ErrNotSupported.
type PushRequest struct {
	Reference   string // unique merchant reference, e.g. the Payments.id
	Amount      float64
	Currency    string // ISO 4217
	Phone       string // MSISDN the push is sent to, required
	Network     string // mobile money operator (e.g. "MTN", "AIRTEL"); required by some gateways/countries
	CountryCode string // ISO 3166-1 alpha-2; Flutterwave uses it to pick the right mobile-money route
	Description string
	Customer    Customer
	WebhookURL  string // overrides Config.WebhookURL
	Metadata    map[string]string
}

// PushResponse is the outcome of a successful push: it only confirms the
// gateway delivered the prompt, not that the customer approved it. Poll
// VerifyPayment (or wait for the webhook) for the final state.
type PushResponse struct {
	Provider     string
	Reference    string // merchant reference
	ProviderRef  string // gateway id to verify/poll with later
	Status       Status // normally StatusPending right after a successful push
	Instructions string // human text from the gateway, e.g. "enter your PIN"
	Raw          map[string]any
}

// RefundRequest refunds a verified payment. Amount 0 means a full refund where
// the gateway supports it (PayPal, Flutterwave); Pesapal always needs an amount.
type RefundRequest struct {
	Reference         string
	ProviderPaymentID string // PaymentResult.ProviderPaymentID
	Amount            float64
	Currency          string // required by PayPal for partial refunds
	Reason            string
}

// RefundResult is the outcome of Refund, GetRefund or one item of ListRefunds.
// Refunds that settle later report StatusPending; the final state arrives
// through a webhook, VerifyPayment, or a later GetRefund. Amount/Currency/
// ProviderPaymentID are best-effort: Refund's own response often omits them
// (see each provider file), while GetRefund/ListRefunds populate them from
// the gateway whenever it reports them.
type RefundResult struct {
	Provider          string
	ProviderRefundID  string
	ProviderPaymentID string
	Status            Status
	Amount            float64
	Currency          string
	Reason            string
	Raw               map[string]any
}

// CancelRequest identifies a not-yet-settled payment to cancel. Send whichever
// of Reference/ProviderRef the gateway looks payments up by (see
// VerifyRequest's doc comment — the same split applies here).
type CancelRequest struct {
	Reference   string
	ProviderRef string
	Reason      string
}

// CancelResult is the outcome of a cancellation. A successful cancel reports
// StatusFailed with FailureReason "cancelled" — the package already folds
// gateway states with no Payments.status equivalent (voided, expired,
// declined) into StatusFailed this way; cancellation is another one.
type CancelResult struct {
	Provider      string
	Reference     string
	ProviderRef   string
	Status        Status
	FailureReason string
	Raw           map[string]any
}

// ListRequest filters and paginates ListPayments. Every field is optional and
// gateway-specific support varies; a gateway ignores filters it doesn't
// understand rather than rejecting the call. Status is the gateway's own
// filter value (e.g. Flutterwave's "successful", not this package's Status
// enum) since the two rarely line up one-to-one.
//
// Gateways paginate one of two ways: by page number (Page/Limit) or by an
// opaque cursor (Cursor, taken from a previous ListResult.NextCursor) — a
// gateway that doesn't support the style you pass simply ignores it.
type ListRequest struct {
	Status string
	From   time.Time
	To     time.Time
	Page   int
	Limit  int
	Cursor string
}

// ListResult is one page of payments. Fields a gateway doesn't report stay
// zero (Page, TotalPages, Total) — check HasMore/NextCursor instead of
// assuming Total is populated.
type ListResult struct {
	Provider   string
	Items      []PaymentResult
	Page       int
	TotalPages int
	Total      int
	HasMore    bool
	NextCursor string
	Raw        map[string]any
}

// CardRequest issues a Flutterwave virtual card. See flutterwave.go for an
// important caveat: Flutterwave's own SDKs mark this service unavailable, so
// every call should be treated as liable to fail until confirmed otherwise on
// your account.
type CardRequest struct {
	Currency          string // "USD" per Flutterwave's docs
	Amount            float64
	DebitCurrency     string // wallet currency the card is funded from, e.g. "NGN"
	Customer          Customer
	BillingAddress    string
	BillingCity       string
	BillingState      string
	BillingPostalCode string
	BillingCountry    string // ISO 3166-1 alpha-2
	DateOfBirth       string // YYYY-MM-DD
	Title             string // MR, MRS, MISS
	Gender            string // M, F
	CallbackURL       string // card OTPs are delivered here as a webhook
}

// Card is an issued virtual card. FullPAN and CVV are only ever populated by
// the create call's own response — no other call returns them again, so
// capture them immediately or not at all.
type Card struct {
	ID         string
	MaskedPAN  string
	FullPAN    string
	CVV        string
	Currency   string
	Amount     float64
	Expiration string // YYYY-MM
	CardType   string
	IsActive   bool
	Raw        map[string]any
}

// CardIssuer is an optional capability implemented by gateways that can issue
// virtual cards — currently only Flutterwave. Get it from a Controller with
// CardIssuer, which reports ErrNotSupported for any other provider.
type CardIssuer interface {
	CreateVirtualCard(ctx context.Context, req CardRequest) (*Card, error)
}

// RefundQuery identifies what GetRefund/ListRefunds should return: RefundID
// fetches one refund directly (GetRefund; ListRefunds ignores it),
// ProviderPaymentID scopes ListRefunds to every refund made against one
// payment, and leaving both empty asks ListRefunds for every refund the
// gateway will report (not every gateway supports that — see each file).
type RefundQuery struct {
	RefundID          string
	ProviderPaymentID string
	Page              int
	Limit             int
	Cursor            string
}

// RefundList is one page of refunds — see ListResult's doc comment for how
// Page/TotalPages/Total/HasMore/NextCursor are populated.
type RefundList struct {
	Provider   string
	Items      []RefundResult
	Page       int
	TotalPages int
	Total      int
	HasMore    bool
	NextCursor string
	Raw        map[string]any
}

// PaymentMethodRequest saves a payment method for reuse. Token is an opaque,
// already-tokenized reference your own client obtained directly from the
// gateway — Stripe.js's PaymentMethod id, or the id of a PayPal Vault setup
// token created by its JS SDK — never a raw card number. This package never
// collects card data itself, so a gateway whose vaulting flow requires the
// server to see raw card details isn't implemented here even where the
// gateway offers one (see paypal.go).
type PaymentMethodRequest struct {
	CustomerRef  string // the gateway's existing customer id; blank creates one from Customer
	Customer     Customer
	Token        string
	SetAsDefault bool
}

// PaymentMethod mirrors server_billing's PaymentMethods collection
// (providerCustomerId, providerPaymentMethodId, last4, brand, expiryMonth,
// expiryYear, isDefault) so a CreatePaymentMethod/ListPaymentMethods result
// can be written straight into it.
type PaymentMethod struct {
	Provider           string
	ProviderCustomerID string
	ID                 string
	Last4              string
	Brand              string
	ExpiryMonth        int
	ExpiryYear         int
	IsDefault          bool
	Raw                map[string]any
}

// PaymentMethodManager is an optional capability for gateways that can save a
// payment method for reuse — currently Stripe and PayPal. Get it from a
// Controller with PaymentMethodManager, which reports ErrNotSupported for
// any other provider.
type PaymentMethodManager interface {
	CreatePaymentMethod(ctx context.Context, req PaymentMethodRequest) (*PaymentMethod, error)
	ListPaymentMethods(ctx context.Context, customerRef string) ([]PaymentMethod, error)
	DeletePaymentMethod(ctx context.Context, customerRef, methodID string) error
}

// FeeRequest asks a gateway what it would charge in fees for a prospective
// payment, before you create it.
type FeeRequest struct {
	Amount      float64
	Currency    string
	PaymentType string // gateway-specific channel hint, e.g. Flutterwave's "card"/"mobilemoney"/"banktransfer"
}

// FeeQuote is the gateway's answer to a FeeRequest.
type FeeQuote struct {
	Provider string
	Fee      float64
	Currency string
	Raw      map[string]any
}

// FeeQuoter is an optional capability for gateways that can quote their fee
// ahead of a charge — currently only Flutterwave. Get it from a Controller
// with FeeQuoter, which reports ErrNotSupported for any other provider.
type FeeQuoter interface {
	QuoteFee(ctx context.Context, req FeeRequest) (*FeeQuote, error)
}

// WebhookEvent is a validated, provider-neutral notification. Providers whose
// callbacks are unsigned (Pesapal, Selcom) re-check the payment with the
// gateway, so Result can be trusted in both cases.
type WebhookEvent struct {
	Provider string
	Type     string         // gateway event name
	Result   *PaymentResult // nil for events that carry no payment state
	// Success is true only when Result reports a succeeded payment; pending,
	// failed and refunded payments, and events without a Result, are false.
	Success bool
	// Ack is the body the gateway expects in the HTTP response, JSON encoded.
	// nil means an empty 200.
	Ack any
}

// withSuccess sets Success from Result. Every ParseWebhook returns through it.
func (e *WebhookEvent) withSuccess() *WebhookEvent {
	e.Success = e.Result != nil && e.Result.Status == StatusSucceeded
	return e
}

// Provider is implemented once per gateway.
type Provider interface {
	Name() string
	// CreatePayment opens a hosted checkout and returns its URL.
	CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error)
	// VerifyPayment asks the gateway for the final state. For PayPal it also
	// captures an approved order.
	VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error)
	// PushUSSD sends a direct mobile-money charge request. Gateways without a
	// push flow return ErrNotSupported.
	PushUSSD(ctx context.Context, req PushRequest) (*PushResponse, error)
	// CancelPayment cancels a not-yet-settled payment. Most gateways have no
	// such call and return ErrNotSupported.
	CancelPayment(ctx context.Context, req CancelRequest) (*CancelResult, error)
	// ListPayments returns one page of payments matching req. Gateways
	// without a listing endpoint return ErrNotSupported.
	ListPayments(ctx context.Context, req ListRequest) (*ListResult, error)
	Refund(ctx context.Context, req RefundRequest) (*RefundResult, error)
	// GetRefund re-checks one refund by id — useful since several gateways
	// settle refunds asynchronously and Refund's own response only reports
	// StatusPending. Gateways without a lookup call return ErrNotSupported.
	GetRefund(ctx context.Context, req RefundQuery) (*RefundResult, error)
	// ListRefunds returns one page of refunds matching req. Gateways without
	// a listing endpoint return ErrNotSupported.
	ListRefunds(ctx context.Context, req RefundQuery) (*RefundList, error)
	// ParseWebhook authenticates and decodes an incoming notification. It
	// returns ErrInvalidWebhook (wrapped) when authentication fails.
	ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error)
}

// Config configures one gateway.
type Config struct {
	Provider    string
	Sandbox     bool              // use the gateway's test environment where it has one
	BaseURL     string            // overrides the built-in endpoint (Selcom UAT, proxies, tests)
	WebhookURL  string            // default notification URL
	Credentials map[string]string // see the Key* constants
	HTTPClient  *http.Client      // optional, defaults to a 30s timeout client
}

func (c Config) cred(key string) string { return strings.TrimSpace(c.Credentials[key]) }

func (c Config) require(keys ...string) error {
	var missing []string
	for _, k := range keys {
		if c.cred(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s config is missing %s", ErrInvalidRequest, c.Provider, strings.Join(missing, ", "))
	}
	return nil
}

func (c Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultHTTPClient
}

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

// Factory builds a provider from its config.
type Factory func(cfg Config) (Provider, error)

var (
	factoriesMu sync.RWMutex
	factories   = map[string]Factory{}
)

// RegisterFactory makes a gateway available to New and Controller.Add. The
// built-in providers register themselves; call this to plug in another one.
func RegisterFactory(name string, f Factory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	factories[normalize(name)] = f
}

// New builds a single provider from cfg.
func New(cfg Config) (Provider, error) {
	factoriesMu.RLock()
	f, ok := factories[normalize(cfg.Provider)]
	factoriesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, cfg.Provider)
	}
	cfg.Provider = normalize(cfg.Provider)
	return f(cfg)
}

// Controller is the single entry point: it holds the configured providers and
// routes every call by provider name. Safe for concurrent use.
type Controller struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewController builds a controller from the given gateway configs.
func NewController(cfgs ...Config) (*Controller, error) {
	c := &Controller{providers: map[string]Provider{}}
	for _, cfg := range cfgs {
		if err := c.Add(cfg); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Add configures a gateway, replacing any existing one with the same name.
func (c *Controller) Add(cfg Config) error {
	p, err := New(cfg)
	if err != nil {
		return err
	}
	c.Use(p)
	return nil
}

// Use installs an already built provider (custom or mock implementations).
func (c *Controller) Use(p Provider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.providers[normalize(p.Name())] = p
}

// Get returns a configured provider.
func (c *Controller) Get(name string) (Provider, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.providers[normalize(name)]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not configured", ErrUnknownProvider, name)
	}
	return p, nil
}

// Providers lists the configured provider names, sorted.
func (c *Controller) Providers() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.providers))
	for n := range c.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (c *Controller) CreatePayment(ctx context.Context, provider string, req PaymentRequest) (*PaymentResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.CreatePayment(ctx, req)
}

func (c *Controller) PushUSSD(ctx context.Context, provider string, req PushRequest) (*PushResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.PushUSSD(ctx, req)
}

func (c *Controller) CancelPayment(ctx context.Context, provider string, req CancelRequest) (*CancelResult, error) {
	if req.Reference == "" && req.ProviderRef == "" {
		return nil, fmt.Errorf("%w: reference or provider reference is required", ErrInvalidRequest)
	}
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.CancelPayment(ctx, req)
}

func (c *Controller) ListPayments(ctx context.Context, provider string, req ListRequest) (*ListResult, error) {
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.ListPayments(ctx, req)
}

// CardIssuer returns provider as a CardIssuer, for gateways that can issue
// virtual cards (currently only Flutterwave — see CardRequest's doc comment).
// It reports ErrNotSupported, wrapped, for any provider that isn't one.
func (c *Controller) CardIssuer(provider string) (CardIssuer, error) {
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	ci, ok := p.(CardIssuer)
	if !ok {
		return nil, fmt.Errorf("%w: %q does not support virtual card issuance", ErrNotSupported, provider)
	}
	return ci, nil
}

func (c *Controller) VerifyPayment(ctx context.Context, provider string, req VerifyRequest) (*PaymentResult, error) {
	if req.Reference == "" && req.ProviderRef == "" {
		return nil, fmt.Errorf("%w: reference or provider reference is required", ErrInvalidRequest)
	}
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.VerifyPayment(ctx, req)
}

func (c *Controller) Refund(ctx context.Context, provider string, req RefundRequest) (*RefundResult, error) {
	if req.ProviderPaymentID == "" {
		return nil, fmt.Errorf("%w: provider payment id is required", ErrInvalidRequest)
	}
	if req.Amount < 0 {
		return nil, fmt.Errorf("%w: amount cannot be negative", ErrInvalidRequest)
	}
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.Refund(ctx, req)
}

func (c *Controller) GetRefund(ctx context.Context, provider string, req RefundQuery) (*RefundResult, error) {
	if req.RefundID == "" {
		return nil, fmt.Errorf("%w: refund id is required", ErrInvalidRequest)
	}
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.GetRefund(ctx, req)
}

func (c *Controller) ListRefunds(ctx context.Context, provider string, req RefundQuery) (*RefundList, error) {
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.ListRefunds(ctx, req)
}

// PaymentMethodManager returns provider as a PaymentMethodManager, for
// gateways that can save a payment method for reuse (currently Stripe and
// PayPal). It reports ErrNotSupported, wrapped, for any other provider.
func (c *Controller) PaymentMethodManager(provider string) (PaymentMethodManager, error) {
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	pm, ok := p.(PaymentMethodManager)
	if !ok {
		return nil, fmt.Errorf("%w: %q does not support saved payment methods", ErrNotSupported, provider)
	}
	return pm, nil
}

// FeeQuoter returns provider as a FeeQuoter, for gateways that can quote
// their fee ahead of a charge (currently only Flutterwave). It reports
// ErrNotSupported, wrapped, for any other provider.
func (c *Controller) FeeQuoter(provider string) (FeeQuoter, error) {
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	fq, ok := p.(FeeQuoter)
	if !ok {
		return nil, fmt.Errorf("%w: %q does not support fee quotes", ErrNotSupported, provider)
	}
	return fq, nil
}

func (c *Controller) ParseWebhook(ctx context.Context, provider string, r *http.Request) (*WebhookEvent, error) {
	p, err := c.Get(provider)
	if err != nil {
		return nil, err
	}
	return p.ParseWebhook(ctx, r)
}

// WebhookHandler adapts ParseWebhook to net/http. onEvent runs only for
// authenticated events; return an error from it to answer 500 so the gateway
// retries. Handlers must be idempotent because gateways deliver more than once.
func (c *Controller) WebhookHandler(provider string, onEvent func(ctx context.Context, ev *WebhookEvent) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ev, err := c.ParseWebhook(r.Context(), provider, r)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, ErrInvalidWebhook):
				status = http.StatusUnauthorized
			case errors.Is(err, ErrUnknownProvider):
				status = http.StatusNotFound
			case errors.Is(err, ErrInvalidRequest):
				status = http.StatusBadRequest
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		if onEvent != nil {
			if err := onEvent(r.Context(), ev); err != nil {
				http.Error(w, "webhook handler failed", http.StatusInternalServerError)
				return
			}
		}
		if ev.Ack == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ev.Ack)
	}
}

func (r PaymentRequest) validate() error {
	switch {
	case strings.TrimSpace(r.Reference) == "":
		return fmt.Errorf("%w: reference is required", ErrInvalidRequest)
	case r.Amount <= 0:
		return fmt.Errorf("%w: amount must be greater than zero", ErrInvalidRequest)
	case strings.TrimSpace(r.Currency) == "":
		return fmt.Errorf("%w: currency is required", ErrInvalidRequest)
	}
	return nil
}

func (r PushRequest) validate() error {
	switch {
	case strings.TrimSpace(r.Reference) == "":
		return fmt.Errorf("%w: reference is required", ErrInvalidRequest)
	case r.Amount <= 0:
		return fmt.Errorf("%w: amount must be greater than zero", ErrInvalidRequest)
	case strings.TrimSpace(r.Currency) == "":
		return fmt.Errorf("%w: currency is required", ErrInvalidRequest)
	case strings.TrimSpace(r.Phone) == "":
		return fmt.Errorf("%w: phone is required for a push request", ErrInvalidRequest)
	}
	return nil
}

// ---------------------------------------------------------------------------
// shared helpers used by the provider files
// ---------------------------------------------------------------------------

// asProviderError is errors.As for *ProviderError.
func asProviderError(err error, target **ProviderError) bool { return errors.As(err, target) }

func normalize(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// doJSON sends an optional JSON body and decodes a JSON response into out (may
// be nil). A non-2xx answer becomes a *ProviderError carrying the response
// body; the raw body is returned as well so callers can inspect error codes.
func doJSON(ctx context.Context, client *http.Client, provider, method, url string, headers map[string]string, in any, out any) ([]byte, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	return doRaw(ctx, client, provider, method, url, headers, body, out)
}

func doRaw(ctx context.Context, client *http.Client, provider, method, url string, headers map[string]string, body io.Reader, out any) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", provider, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: reading response: %w", provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return raw, &ProviderError{Provider: provider, HTTPStatus: resp.StatusCode, Message: truncate(string(raw), 500)}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, fmt.Errorf("%s: decoding response: %w", provider, err)
		}
	}
	return raw, nil
}

// readBody reads a webhook body (capped at 1MB) and restores nothing: callers
// own the request.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r.Body, 1<<20))
}

func rawMap(raw []byte) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// formatAmount renders an amount with a fixed number of decimals.
func formatAmount(amount float64, decimals int) string {
	return strconv.FormatFloat(amount, 'f', decimals, 64)
}

// parseAmount reads amounts that gateways send as number or string.
func parseAmount(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	}
	return 0
}

// splitName breaks a full name into first and last name.
func splitName(full string) (first, last string) {
	parts := strings.Fields(full)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// pick reads the first present key out of a generic decoded-JSON map. Used
// where a gateway's exact response field name is uncertain and a provider
// wants to try a few candidates instead of silently reading a zero value.
func pick(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func pickStr(m map[string]any, keys ...string) string { return str(pick(m, keys...)) }

// str reads a string out of a decoded JSON value, formatting numbers too.
func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return fmt.Sprint(v)
}
