package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PayPal Orders v2 (hosted approval flow).
// Credentials: KeyClientID, KeyClientSecret, KeyWebhookID (needed for webhooks).
//
// Flow: CreatePayment -> payer approves on PayPal -> VerifyPayment captures the
// approved order and returns the capture id as ProviderPaymentID (used to refund).

const (
	paypalLiveURL    = "https://api-m.paypal.com"
	paypalSandboxURL = "https://api-m.sandbox.paypal.com"
)

// currencies PayPal accepts without decimals
var paypalZeroDecimal = map[string]bool{"JPY": true, "HUF": true, "TWD": true}

func init() {
	RegisterFactory(ProviderPayPal, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeyClientID, KeyClientSecret); err != nil {
			return nil, err
		}
		base := cfg.BaseURL
		if base == "" {
			base = paypalLiveURL
			if cfg.Sandbox {
				base = paypalSandboxURL
			}
		}
		return &paypal{cfg: cfg, base: strings.TrimRight(base, "/")}, nil
	})
}

type paypal struct {
	cfg  Config
	base string

	mu        sync.Mutex
	token     string
	tokenExpr time.Time
}

func (p *paypal) Name() string { return ProviderPayPal }

// accessToken returns a cached OAuth token, refreshing it a minute before expiry.
func (p *paypal) accessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && time.Now().Before(p.tokenExpr) {
		return p.token, nil
	}

	basic := base64.StdEncoding.EncodeToString([]byte(p.cfg.cred(KeyClientID) + ":" + p.cfg.cred(KeyClientSecret)))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	_, err := doRaw(ctx, p.cfg.httpClient(), p.Name(), http.MethodPost, p.base+"/v1/oauth2/token",
		map[string]string{"Authorization": "Basic " + basic, "Content-Type": "application/x-www-form-urlencoded"},
		strings.NewReader("grant_type=client_credentials"), &out)
	if err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", &ProviderError{Provider: p.Name(), Message: "no access token returned"}
	}
	p.token = out.AccessToken
	p.tokenExpr = time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second)
	return p.token, nil
}

func (p *paypal) call(ctx context.Context, method, path string, extra map[string]string, in any, out any) ([]byte, error) {
	tok, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "Bearer " + tok}
	for k, v := range extra {
		headers[k] = v
	}
	raw, err := doJSON(ctx, p.cfg.httpClient(), p.Name(), method, p.base+path, headers, in, out)
	if err != nil {
		var pe *ProviderError
		var e struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		if asProviderError(err, &pe) && json.Unmarshal(raw, &e) == nil && e.Name != "" {
			pe.Code, pe.Message = e.Name, firstNonEmpty(e.Message, pe.Message)
		}
	}
	return raw, err
}

func (p *paypal) amount(v float64, currency string) map[string]any {
	c := strings.ToUpper(currency)
	dec := 2
	if paypalZeroDecimal[c] {
		dec = 0
	}
	return map[string]any{"currency_code": c, "value": formatAmount(v, dec)}
}

func (p *paypal) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	if req.ReturnURL == "" {
		return nil, fmt.Errorf("%w: paypal needs a return url", ErrInvalidRequest)
	}
	unit := map[string]any{
		"custom_id": req.Reference,
		"amount":    p.amount(req.Amount, req.Currency),
	}
	if req.Description != "" {
		unit["description"] = truncate(req.Description, 127)
	}
	ctxt := map[string]any{
		"return_url":  req.ReturnURL,
		"cancel_url":  firstNonEmpty(req.CancelURL, req.ReturnURL),
		"user_action": "PAY_NOW",
	}
	paypalSource := map[string]any{"experience_context": ctxt}
	if req.Customer.Email != "" {
		paypalSource["email_address"] = req.Customer.Email
	}
	body := map[string]any{
		"intent":         "CAPTURE",
		"purchase_units": []any{unit},
		"payment_source": map[string]any{"paypal": paypalSource},
	}

	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Links  []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	// the request id makes a retried create return the same order
	raw, err := p.call(ctx, http.MethodPost, "/v2/checkout/orders", map[string]string{"PayPal-Request-Id": req.Reference}, body, &out)
	if err != nil {
		return nil, err
	}
	var link string
	for _, l := range out.Links {
		if l.Rel == "payer-action" || l.Rel == "approve" {
			link = l.Href
			break
		}
	}
	if out.ID == "" || link == "" {
		return nil, &ProviderError{Provider: p.Name(), Message: "approval link was not returned"}
	}
	return &PaymentResponse{
		Provider:    p.Name(),
		Reference:   req.Reference,
		ProviderRef: out.ID,
		CheckoutURL: link,
		Status:      StatusPending,
		Raw:         rawMap(raw),
	}, nil
}

type paypalOrder struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	PurchaseUnits []struct {
		CustomID string `json:"custom_id"`
		Amount   struct {
			CurrencyCode string `json:"currency_code"`
			Value        string `json:"value"`
		} `json:"amount"`
		Payments struct {
			Captures []paypalCapture `json:"captures"`
		} `json:"payments"`
	} `json:"purchase_units"`
}

type paypalCapture struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount struct {
		CurrencyCode string `json:"currency_code"`
		Value        string `json:"value"`
	} `json:"amount"`
	StatusDetails struct {
		Reason string `json:"reason"`
	} `json:"status_details"`
}

// PushUSSD is not offered by PayPal: it has no mobile-money/USSD push flow.
func (p *paypal) PushUSSD(context.Context, PushRequest) (*PushResponse, error) {
	return nil, fmt.Errorf("%w: paypal has no mobile money push flow, use CreatePayment", ErrNotSupported)
}

// VerifyPayment reads the order and captures it when the payer has approved it.
func (p *paypal) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	if req.ProviderRef == "" {
		return nil, fmt.Errorf("%w: paypal looks payments up by order id (provider reference)", ErrInvalidRequest)
	}
	path := "/v2/checkout/orders/" + url.PathEscape(req.ProviderRef)

	var order paypalOrder
	raw, err := p.call(ctx, http.MethodGet, path, nil, nil, &order)
	if err != nil {
		return nil, err
	}

	if order.Status == "APPROVED" {
		var captured paypalOrder
		craw, cerr := p.call(ctx, http.MethodPost, path+"/capture", map[string]string{"PayPal-Request-Id": "capture-" + req.ProviderRef}, map[string]any{}, &captured)
		switch {
		case cerr == nil:
			order, raw = captured, craw
		case strings.Contains(string(craw), "ORDER_ALREADY_CAPTURED"):
			// a webhook or a second callback captured it first
			if raw, err = p.call(ctx, http.MethodGet, path, nil, nil, &order); err != nil {
				return nil, err
			}
		default:
			return nil, cerr
		}
	}
	return p.orderResult(&order, raw), nil
}

func (p *paypal) orderResult(o *paypalOrder, raw []byte) *PaymentResult {
	res := &PaymentResult{Provider: p.Name(), ProviderRef: o.ID, Status: StatusPending, Raw: rawMap(raw)}
	if len(o.PurchaseUnits) == 0 {
		return res
	}
	u := o.PurchaseUnits[0]
	res.Reference = u.CustomID
	res.Amount, res.Currency = parseAmount(u.Amount.Value), u.Amount.CurrencyCode
	res.Method = "paypal"

	if len(u.Payments.Captures) > 0 {
		c := u.Payments.Captures[0]
		res.ProviderPaymentID = c.ID
		res.Amount, res.Currency = parseAmount(c.Amount.Value), c.Amount.CurrencyCode
		res.Status, res.FailureReason = paypalCaptureStatus(c.Status, c.StatusDetails.Reason)
		return res
	}
	if o.Status == "VOIDED" {
		res.Status, res.FailureReason = StatusFailed, "voided"
	}
	return res
}

func paypalCaptureStatus(status, reason string) (Status, string) {
	switch strings.ToUpper(status) {
	case "COMPLETED":
		return StatusSucceeded, ""
	case "REFUNDED", "PARTIALLY_REFUNDED":
		return StatusRefunded, ""
	case "DECLINED", "FAILED":
		return StatusFailed, firstNonEmpty(reason, strings.ToLower(status))
	}
	return StatusPending, "" // PENDING
}

// CancelPayment is not offered: this package always creates orders with
// intent=CAPTURE (auto-capture on approval), and PayPal's order-cancel/void
// endpoint only applies to intent=AUTHORIZE orders. An order simply expires
// on its own (PayPal's default is 3 hours) if the payer never approves it.
func (p *paypal) CancelPayment(context.Context, CancelRequest) (*CancelResult, error) {
	return nil, fmt.Errorf("%w: paypal orders cannot be cancelled via api, they expire on their own", ErrNotSupported)
}

// ListPayments is not implemented: PayPal has no "list orders" endpoint on
// the Orders v2 API this package uses — that requires the separate
// Transaction Search API (different reporting scope), which isn't wired up.
func (p *paypal) ListPayments(context.Context, ListRequest) (*ListResult, error) {
	return nil, fmt.Errorf("%w: paypal order listing needs the separate transaction search api, not implemented here", ErrNotSupported)
}

func (p *paypal) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	body := map[string]any{}
	if req.Amount > 0 {
		if req.Currency == "" {
			return nil, fmt.Errorf("%w: paypal partial refunds need a currency", ErrInvalidRequest)
		}
		body["amount"] = p.amount(req.Amount, req.Currency)
	}
	if req.Reason != "" {
		body["note_to_payer"] = truncate(req.Reason, 255)
	}

	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	// keyed on capture + amount so a retry cannot refund twice
	rid := "refund-" + req.ProviderPaymentID + "-" + formatAmount(req.Amount, 2)
	raw, err := p.call(ctx, http.MethodPost, "/v2/payments/captures/"+url.PathEscape(req.ProviderPaymentID)+"/refund",
		map[string]string{"PayPal-Request-Id": rid}, body, &out)
	if err != nil {
		return nil, err
	}
	status := StatusPending
	switch strings.ToUpper(out.Status) {
	case "COMPLETED":
		status = StatusRefunded
	case "FAILED", "CANCELLED":
		status = StatusFailed
	}
	return &RefundResult{Provider: p.Name(), ProviderRefundID: out.ID, Status: status, Raw: rawMap(raw)}, nil
}

// GetRefund wraps GET /v2/payments/refunds/{id}.
func (p *paypal) GetRefund(ctx context.Context, req RefundQuery) (*RefundResult, error) {
	if req.RefundID == "" {
		return nil, fmt.Errorf("%w: refund id is required", ErrInvalidRequest)
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Amount struct {
			CurrencyCode string `json:"currency_code"`
			Value        string `json:"value"`
		} `json:"amount"`
	}
	raw, err := p.call(ctx, http.MethodGet, "/v2/payments/refunds/"+url.PathEscape(req.RefundID), nil, nil, &out)
	if err != nil {
		return nil, err
	}
	status := StatusPending
	switch strings.ToUpper(out.Status) {
	case "COMPLETED":
		status = StatusRefunded
	case "FAILED", "CANCELLED":
		status = StatusFailed
	}
	return &RefundResult{
		Provider:         p.Name(),
		ProviderRefundID: out.ID,
		Status:           status,
		Amount:           parseAmount(out.Amount.Value),
		Currency:         out.Amount.CurrencyCode,
		Raw:              rawMap(raw),
	}, nil
}

// ListRefunds is not offered: PayPal's Payments v2 API has no "list refunds"
// call — only lookup by id (GetRefund) or discovery through the capture's
// own resource, which this package doesn't separately track.
func (p *paypal) ListRefunds(context.Context, RefundQuery) (*RefundList, error) {
	return nil, fmt.Errorf("%w: paypal has no refund listing api, use GetRefund by id", ErrNotSupported)
}

// CreatePaymentMethod exchanges a Vault setup token for a permanent payment
// token (POST /v3/vault/payment-tokens). req.Token is the id of a setup
// token your client created with PayPal's JS SDK — this package never
// collects a raw card number itself, so the setup-token step (which does)
// happens client-side, not here. req.CustomerRef, if given, is passed
// through as the vault customer id; PayPal assigns one automatically
// otherwise. The Payment Method Tokens API is US-only per PayPal's docs.
func (p *paypal) CreatePaymentMethod(ctx context.Context, req PaymentMethodRequest) (*PaymentMethod, error) {
	if req.Token == "" {
		return nil, fmt.Errorf("%w: paypal needs a vault setup token id", ErrInvalidRequest)
	}
	body := map[string]any{
		"payment_source": map[string]any{
			"token": map[string]any{"id": req.Token, "type": "SETUP_TOKEN"},
		},
	}
	if req.CustomerRef != "" {
		body["customer"] = map[string]any{"id": req.CustomerRef}
	}

	var out paypalVaultToken
	raw, err := p.call(ctx, http.MethodPost, "/v3/vault/payment-tokens", map[string]string{"PayPal-Request-Id": "vault-" + req.Token}, body, &out)
	if err != nil {
		return nil, err
	}
	return out.toPaymentMethod(p.Name(), raw), nil
}

// ListPaymentMethods wraps GET /v3/vault/payment-tokens?customer_id=...
// PayPal caps page_size at 5; this fetches only the first page since
// PaymentMethodManager's interface has no pagination of its own.
func (p *paypal) ListPaymentMethods(ctx context.Context, customerRef string) ([]PaymentMethod, error) {
	if customerRef == "" {
		return nil, fmt.Errorf("%w: paypal needs a customer id to list payment methods", ErrInvalidRequest)
	}
	var out map[string]any
	if _, err := p.call(ctx, http.MethodGet, "/v3/vault/payment-tokens?"+url.Values{"customer_id": {customerRef}, "page_size": {"5"}}.Encode(), nil, nil, &out); err != nil {
		return nil, err
	}
	// the wrapper key isn't confirmed in PayPal's published docs; try the
	// documented resource name first, then a couple of plausible fallbacks.
	items, _ := pick(out, "payment_tokens", "items", "payment_methods").([]any)
	methods := make([]PaymentMethod, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		var tok paypalVaultToken
		b, _ := json.Marshal(m)
		if json.Unmarshal(b, &tok) == nil {
			methods = append(methods, *tok.toPaymentMethod(p.Name(), b))
		}
	}
	return methods, nil
}

// DeletePaymentMethod wraps DELETE /v3/vault/payment-tokens/{id}.
func (p *paypal) DeletePaymentMethod(ctx context.Context, customerRef, methodID string) error {
	if methodID == "" {
		return fmt.Errorf("%w: payment method id is required", ErrInvalidRequest)
	}
	_, err := p.call(ctx, http.MethodDelete, "/v3/vault/payment-tokens/"+url.PathEscape(methodID), nil, nil, nil)
	return err
}

// paypalVaultToken is a PayPal Vault payment token (POST/GET .../payment-tokens).
type paypalVaultToken struct {
	ID       string `json:"id"`
	Customer struct {
		ID string `json:"id"`
	} `json:"customer"`
	PaymentSource struct {
		Card struct {
			LastDigits string `json:"last_digits"`
			Brand      string `json:"brand"`
			Expiry     string `json:"expiry"` // "YYYY-MM"
		} `json:"card"`
	} `json:"payment_source"`
}

func (t paypalVaultToken) toPaymentMethod(provider string, raw []byte) *PaymentMethod {
	pm := &PaymentMethod{
		Provider:           provider,
		ProviderCustomerID: t.Customer.ID,
		ID:                 t.ID,
		Last4:              t.PaymentSource.Card.LastDigits,
		Brand:              t.PaymentSource.Card.Brand,
		Raw:                rawMap(raw),
	}
	if y, m, ok := strings.Cut(t.PaymentSource.Card.Expiry, "-"); ok {
		if yi, err := strconv.Atoi(y); err == nil {
			pm.ExpiryYear = yi
		}
		if mi, err := strconv.Atoi(m); err == nil {
			pm.ExpiryMonth = mi
		}
	}
	return pm
}

// ParseWebhook verifies the notification signature with PayPal
// (/v1/notifications/verify-webhook-signature) before trusting it.
func (p *paypal) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	webhookID := p.cfg.cred(KeyWebhookID)
	if webhookID == "" {
		return nil, fmt.Errorf("%w: paypal webhook_id is not configured", ErrInvalidRequest)
	}
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("%w: paypal body is not json", ErrInvalidWebhook)
	}

	verify := map[string]any{
		"auth_algo":         r.Header.Get("PAYPAL-AUTH-ALGO"),
		"cert_url":          r.Header.Get("PAYPAL-CERT-URL"),
		"transmission_id":   r.Header.Get("PAYPAL-TRANSMISSION-ID"),
		"transmission_sig":  r.Header.Get("PAYPAL-TRANSMISSION-SIG"),
		"transmission_time": r.Header.Get("PAYPAL-TRANSMISSION-TIME"),
		"webhook_id":        webhookID,
		"webhook_event":     json.RawMessage(body), // must be sent byte for byte
	}
	var vr struct {
		Status string `json:"verification_status"`
	}
	if _, err := p.call(ctx, http.MethodPost, "/v1/notifications/verify-webhook-signature", nil, verify, &vr); err != nil {
		return nil, err
	}
	if vr.Status != "SUCCESS" {
		return nil, fmt.Errorf("%w: paypal signature verification failed", ErrInvalidWebhook)
	}

	var ev struct {
		EventType string `json:"event_type"`
		Resource  struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			CustomID string `json:"custom_id"`
			Amount   struct {
				CurrencyCode string `json:"currency_code"`
				Value        string `json:"value"`
			} `json:"amount"`
			StatusDetails struct {
				Reason string `json:"reason"`
			} `json:"status_details"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"links"`
			Supplementary struct {
				RelatedIDs struct {
					OrderID string `json:"order_id"`
				} `json:"related_ids"`
			} `json:"supplementary_data"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("%w: paypal body: %v", ErrInvalidWebhook, err)
	}

	out := &WebhookEvent{Provider: p.Name(), Type: ev.EventType}
	res := ev.Resource
	switch ev.EventType {
	case "CHECKOUT.ORDER.APPROVED":
		// approval alone does not move money; capture it now
		result, err := p.VerifyPayment(ctx, VerifyRequest{ProviderRef: res.ID})
		if err != nil {
			return nil, err
		}
		out.Result = result
	case "PAYMENT.CAPTURE.COMPLETED", "PAYMENT.CAPTURE.PENDING", "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.DECLINED":
		status, reason := paypalCaptureStatus(res.Status, res.StatusDetails.Reason)
		out.Result = &PaymentResult{
			Provider:          p.Name(),
			Reference:         res.CustomID,
			ProviderRef:       res.Supplementary.RelatedIDs.OrderID,
			ProviderPaymentID: res.ID,
			Status:            status,
			Amount:            parseAmount(res.Amount.Value),
			Currency:          res.Amount.CurrencyCode,
			Method:            "paypal",
			FailureReason:     reason,
		}
	case "PAYMENT.CAPTURE.REFUNDED":
		// the resource is the refund; its "up" link points at the capture
		var captureID string
		for _, l := range res.Links {
			if l.Rel == "up" {
				captureID = l.Href[strings.LastIndex(l.Href, "/")+1:]
			}
		}
		out.Result = &PaymentResult{
			Provider:          p.Name(),
			ProviderPaymentID: captureID,
			Status:            StatusRefunded,
			Amount:            parseAmount(res.Amount.Value),
			Currency:          res.Amount.CurrencyCode,
			Method:            "paypal",
		}
	}
	return out.withSuccess(), nil
}
