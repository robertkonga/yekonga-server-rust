package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Pesapal API 3.0 (hosted checkout, East Africa).
// Credentials: KeyConsumerKey, KeyConsumerSecret, optional KeyIPNID and KeyRefundUser.
//
// Pesapal notifies through an IPN url that must be registered first; when
// KeyIPNID is empty the notification url (Config.WebhookURL or
// PaymentRequest.WebhookURL) is registered on demand and the id cached.

const (
	pesapalLiveURL    = "https://pay.pesapal.com/v3"
	pesapalSandboxURL = "https://cybqa.pesapal.com/pesapalv3"
)

func init() {
	RegisterFactory(ProviderPesapal, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeyConsumerKey, KeyConsumerSecret); err != nil {
			return nil, err
		}
		base := cfg.BaseURL
		if base == "" {
			base = pesapalLiveURL
			if cfg.Sandbox {
				base = pesapalSandboxURL
			}
		}
		return &pesapal{cfg: cfg, base: strings.TrimRight(base, "/"), ipnIDs: map[string]string{}}, nil
	})
}

type pesapal struct {
	cfg  Config
	base string

	mu        sync.Mutex
	token     string
	tokenExpr time.Time
	ipnIDs    map[string]string // notification url -> ipn id
}

func (p *pesapal) Name() string { return ProviderPesapal }

// pesapalEnvelope holds the fields every Pesapal response can carry.
type pesapalEnvelope struct {
	Status  any             `json:"status"`
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
}

// failure reports the error object Pesapal embeds in otherwise 200 answers.
func (e *pesapalEnvelope) failure(provider string) error {
	if len(e.Error) == 0 || string(e.Error) == "null" {
		return nil
	}
	var d struct {
		Type    string `json:"error_type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(e.Error, &d) != nil {
		return &ProviderError{Provider: provider, Message: string(e.Error)}
	}
	return &ProviderError{Provider: provider, Code: firstNonEmpty(d.Code, d.Type), Message: firstNonEmpty(d.Message, e.Message)}
}

func (p *pesapal) authToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && time.Now().Before(p.tokenExpr) {
		return p.token, nil
	}
	var out struct {
		pesapalEnvelope
		Token      string `json:"token"`
		ExpiryDate string `json:"expiryDate"`
	}
	_, err := doJSON(ctx, p.cfg.httpClient(), p.Name(), http.MethodPost, p.base+"/api/Auth/RequestToken", nil,
		map[string]string{"consumer_key": p.cfg.cred(KeyConsumerKey), "consumer_secret": p.cfg.cred(KeyConsumerSecret)}, &out)
	if err != nil {
		return "", err
	}
	if ferr := out.failure(p.Name()); ferr != nil {
		return "", ferr
	}
	if out.Token == "" {
		return "", &ProviderError{Provider: p.Name(), Message: firstNonEmpty(out.Message, "no token returned")}
	}
	p.token = out.Token
	// tokens live five minutes; stay well inside that whatever the clock says
	p.tokenExpr = time.Now().Add(4 * time.Minute)
	if exp, err := time.Parse(time.RFC3339, out.ExpiryDate); err == nil && time.Until(exp) < 4*time.Minute {
		p.tokenExpr = exp.Add(-30 * time.Second)
	}
	return p.token, nil
}

func (p *pesapal) call(ctx context.Context, method, path string, in any, out any) ([]byte, error) {
	tok, err := p.authToken(ctx)
	if err != nil {
		return nil, err
	}
	return doJSON(ctx, p.cfg.httpClient(), p.Name(), method, p.base+path, map[string]string{"Authorization": "Bearer " + tok}, in, out)
}

// ipnID returns the registered IPN id for the notification url.
func (p *pesapal) ipnID(ctx context.Context, notifyURL string) (string, error) {
	if id := p.cfg.cred(KeyIPNID); id != "" {
		return id, nil
	}
	if notifyURL == "" {
		return "", fmt.Errorf("%w: pesapal needs an ipn_id or a webhook url", ErrInvalidRequest)
	}
	p.mu.Lock()
	id := p.ipnIDs[notifyURL]
	p.mu.Unlock()
	if id != "" {
		return id, nil
	}

	var out struct {
		pesapalEnvelope
		IPNID string `json:"ipn_id"`
	}
	if _, err := p.call(ctx, http.MethodPost, "/api/URLSetup/RegisterIPN",
		map[string]string{"url": notifyURL, "ipn_notification_type": "GET"}, &out); err != nil {
		return "", err
	}
	if ferr := out.failure(p.Name()); ferr != nil {
		return "", ferr
	}
	if out.IPNID == "" {
		return "", &ProviderError{Provider: p.Name(), Message: "ipn registration returned no id"}
	}
	p.mu.Lock()
	p.ipnIDs[notifyURL] = out.IPNID
	p.mu.Unlock()
	return out.IPNID, nil
}

func (p *pesapal) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	if req.ReturnURL == "" {
		return nil, fmt.Errorf("%w: pesapal needs a return url", ErrInvalidRequest)
	}
	if len(req.Reference) > 50 {
		return nil, fmt.Errorf("%w: pesapal references are limited to 50 characters", ErrInvalidRequest)
	}
	ipn, err := p.ipnID(ctx, firstNonEmpty(req.WebhookURL, p.cfg.WebhookURL))
	if err != nil {
		return nil, err
	}

	first, last := splitName(req.Customer.Name)
	body := map[string]any{
		"id":               req.Reference,
		"currency":         strings.ToUpper(req.Currency),
		"amount":           req.Amount,
		"description":      truncate(firstNonEmpty(req.Description, "Payment "+req.Reference), 100),
		"callback_url":     req.ReturnURL,
		"cancellation_url": firstNonEmpty(req.CancelURL, req.ReturnURL),
		"notification_id":  ipn,
		"billing_address": map[string]any{
			"email_address": req.Customer.Email,
			"phone_number":  req.Customer.Phone,
			"country_code":  strings.ToUpper(req.Customer.CountryCode),
			"first_name":    first,
			"last_name":     last,
		},
	}

	var out struct {
		pesapalEnvelope
		OrderTrackingID   string `json:"order_tracking_id"`
		MerchantReference string `json:"merchant_reference"`
		RedirectURL       string `json:"redirect_url"`
	}
	raw, err := p.call(ctx, http.MethodPost, "/api/Transactions/SubmitOrderRequest", body, &out)
	if err != nil {
		return nil, err
	}
	if ferr := out.failure(p.Name()); ferr != nil {
		return nil, ferr
	}
	if out.RedirectURL == "" {
		return nil, &ProviderError{Provider: p.Name(), Message: firstNonEmpty(out.Message, "redirect url was not returned")}
	}
	return &PaymentResponse{
		Provider:    p.Name(),
		Reference:   firstNonEmpty(out.MerchantReference, req.Reference),
		ProviderRef: out.OrderTrackingID,
		CheckoutURL: out.RedirectURL,
		Status:      StatusPending,
		Raw:         rawMap(raw),
	}, nil
}

// PushUSSD is not offered by the Pesapal API: mobile money is selected by the
// payer on the hosted checkout page, which then triggers its own STK/USSD
// prompt outside Pesapal's control. Use CreatePayment instead.
func (p *pesapal) PushUSSD(context.Context, PushRequest) (*PushResponse, error) {
	return nil, fmt.Errorf("%w: pesapal has no direct push api, use CreatePayment", ErrNotSupported)
}

func (p *pesapal) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	if req.ProviderRef == "" {
		return nil, fmt.Errorf("%w: pesapal looks payments up by order tracking id (provider reference)", ErrInvalidRequest)
	}
	var out struct {
		pesapalEnvelope
		StatusCode        int     `json:"status_code"`
		PaymentStatus     string  `json:"payment_status_description"`
		ConfirmationCode  string  `json:"confirmation_code"`
		PaymentMethod     string  `json:"payment_method"`
		Amount            float64 `json:"amount"`
		Currency          string  `json:"currency"`
		MerchantReference string  `json:"merchant_reference"`
		Description       string  `json:"description"`
	}
	raw, err := p.call(ctx, http.MethodGet, "/api/Transactions/GetTransactionStatus?orderTrackingId="+url.QueryEscape(req.ProviderRef), nil, &out)
	if err != nil {
		return nil, err
	}
	if ferr := out.failure(p.Name()); ferr != nil {
		return nil, ferr
	}

	res := &PaymentResult{
		Provider:          p.Name(),
		Reference:         firstNonEmpty(out.MerchantReference, req.Reference),
		ProviderRef:       req.ProviderRef,
		ProviderPaymentID: out.ConfirmationCode,
		Amount:            out.Amount,
		Currency:          out.Currency,
		Method:            out.PaymentMethod,
		Raw:               rawMap(raw),
	}
	switch out.StatusCode {
	case 1:
		res.Status = StatusSucceeded
	case 2:
		res.Status, res.FailureReason = StatusFailed, firstNonEmpty(out.Description, "failed")
	case 3:
		res.Status = StatusRefunded
	default: // 0 = invalid: not paid yet
		res.Status = StatusPending
	}
	return res, nil
}

// Refund asks Pesapal to reverse a payment. The refund settles later; watch
// for status_code 3 through VerifyPayment or the IPN.
// CancelPayment is not offered by the Pesapal API.
func (p *pesapal) CancelPayment(context.Context, CancelRequest) (*CancelResult, error) {
	return nil, fmt.Errorf("%w: pesapal has no api to cancel an order", ErrNotSupported)
}

// ListPayments is not offered by the Pesapal API.
func (p *pesapal) ListPayments(context.Context, ListRequest) (*ListResult, error) {
	return nil, fmt.Errorf("%w: pesapal has no transaction listing api", ErrNotSupported)
}

// GetRefund is not offered: Pesapal's RefundRequest response carries no
// separate refund id to look up later — re-check the payment's own status
// with VerifyPayment (status_code 3 means refunded) instead.
func (p *pesapal) GetRefund(context.Context, RefundQuery) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: pesapal has no refund lookup api, use VerifyPayment on the payment instead", ErrNotSupported)
}

// ListRefunds is not offered by the Pesapal API.
func (p *pesapal) ListRefunds(context.Context, RefundQuery) (*RefundList, error) {
	return nil, fmt.Errorf("%w: pesapal has no refund listing api", ErrNotSupported)
}

func (p *pesapal) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if req.Amount <= 0 {
		return nil, fmt.Errorf("%w: pesapal refunds need an amount", ErrInvalidRequest)
	}
	body := map[string]any{
		"confirmation_code": req.ProviderPaymentID,
		"amount":            formatAmount(req.Amount, 2),
		"username":          firstNonEmpty(p.cfg.cred(KeyRefundUser), "system"),
		"remarks":           firstNonEmpty(req.Reason, "Refund"),
	}
	var out pesapalEnvelope
	raw, err := p.call(ctx, http.MethodPost, "/api/Transactions/RefundRequest", body, &out)
	if err != nil {
		return nil, err
	}
	if ferr := out.failure(p.Name()); ferr != nil {
		return nil, ferr
	}
	if s := str(out.Status); s != "" && s != "200" {
		return nil, &ProviderError{Provider: p.Name(), Code: s, Message: firstNonEmpty(out.Message, "refund rejected")}
	}
	return &RefundResult{Provider: p.Name(), ProviderRefundID: req.ProviderPaymentID, Status: StatusPending, Raw: rawMap(raw)}, nil
}

// ParseWebhook reads the IPN (query string on GET, JSON on POST). IPNs are not
// signed, so the transaction status is fetched from Pesapal; only the tracking
// id from the request is used. The reply must echo the ids back with status 200.
func (p *pesapal) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	q := r.URL.Query()
	trackingID, merchantRef, kind := q.Get("OrderTrackingId"), q.Get("OrderMerchantReference"), q.Get("OrderNotificationType")

	if trackingID == "" && r.Method == http.MethodPost {
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		var b struct {
			OrderTrackingID        string `json:"OrderTrackingId"`
			OrderMerchantReference string `json:"OrderMerchantReference"`
			OrderNotificationType  string `json:"OrderNotificationType"`
		}
		if err := json.Unmarshal(body, &b); err != nil {
			return nil, fmt.Errorf("%w: pesapal body: %v", ErrInvalidWebhook, err)
		}
		trackingID, merchantRef, kind = b.OrderTrackingID, b.OrderMerchantReference, b.OrderNotificationType
	}
	if trackingID == "" {
		return nil, fmt.Errorf("%w: pesapal ipn without OrderTrackingId", ErrInvalidWebhook)
	}

	res, err := p.VerifyPayment(ctx, VerifyRequest{Reference: merchantRef, ProviderRef: trackingID})
	if err != nil {
		return nil, err
	}
	return (&WebhookEvent{
		Provider: p.Name(),
		Type:     firstNonEmpty(kind, "IPNCHANGE"),
		Result:   res,
		Ack: map[string]any{
			"orderNotificationType":  firstNonEmpty(kind, "IPNCHANGE"),
			"orderTrackingId":        trackingID,
			"orderMerchantReference": merchantRef,
			"status":                 200,
		},
	}).withSuccess(), nil
}
