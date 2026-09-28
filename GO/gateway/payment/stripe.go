package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stripe Checkout Sessions (hosted checkout) + PaymentIntents.
// Credentials: KeySecretKey (sk_...), KeyWebhookSecret (whsec_..., needed for webhooks).
//
// Stripe has no mobile-money/USSD push flow, so PushUSSD is not supported.

const stripeBaseURL = "https://api.stripe.com/v1"

// stripeZeroDecimalCurrencies are the currencies Stripe expects/returns without
// a minor unit (amounts are already in the currency's base unit).
var stripeZeroDecimalCurrencies = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true, "KMF": true,
	"KRW": true, "MGA": true, "PYG": true, "RWF": true, "UGX": true, "VND": true,
	"VUV": true, "XAF": true, "XOF": true, "XPF": true,
}

func stripeDecimals(currency string) int {
	if stripeZeroDecimalCurrencies[strings.ToUpper(currency)] {
		return 0
	}
	return 2
}

func stripeToMinorUnits(amount float64, currency string) int64 {
	dec := stripeDecimals(currency)
	mul := 1.0
	for i := 0; i < dec; i++ {
		mul *= 10
	}
	return int64(amount*mul + 0.5)
}

func stripeFromMinorUnits(amount int64, currency string) float64 {
	dec := stripeDecimals(currency)
	div := 1.0
	for i := 0; i < dec; i++ {
		div *= 10
	}
	return float64(amount) / div
}

func init() {
	RegisterFactory(ProviderStripe, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeySecretKey); err != nil {
			return nil, err
		}
		return &stripeProvider{cfg: cfg, base: firstNonEmpty(cfg.BaseURL, stripeBaseURL)}, nil
	})
}

type stripeProvider struct {
	cfg  Config
	base string
}

func (s *stripeProvider) Name() string { return ProviderStripe }

func (s *stripeProvider) headers() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + s.cfg.cred(KeySecretKey),
		"Content-Type":  "application/x-www-form-urlencoded",
	}
}

func (s *stripeProvider) call(ctx context.Context, method, path string, form url.Values, out any) ([]byte, error) {
	target := s.base + path
	if method == http.MethodGet {
		if len(form) > 0 {
			target += "?" + form.Encode()
		}
		raw, err := doRaw(ctx, s.cfg.httpClient(), s.Name(), method, target, s.headers(), nil, out)
		return raw, s.enrich(err, raw)
	}
	raw, err := doRaw(ctx, s.cfg.httpClient(), s.Name(), method, target, s.headers(), strings.NewReader(form.Encode()), out)
	return raw, s.enrich(err, raw)
}

func (s *stripeProvider) enrich(err error, raw []byte) error {
	var pe *ProviderError
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if asProviderError(err, &pe) && json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		pe.Code = firstNonEmpty(e.Error.Code, e.Error.Type)
		pe.Message = e.Error.Message
	}
	return err
}

func (s *stripeProvider) PushUSSD(context.Context, PushRequest) (*PushResponse, error) {
	return nil, fmt.Errorf("%w: stripe has no mobile money push flow, use CreatePayment", ErrNotSupported)
}

func (s *stripeProvider) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	if req.ReturnURL == "" {
		return nil, fmt.Errorf("%w: stripe needs a return url", ErrInvalidRequest)
	}
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", req.ReturnURL)
	form.Set("cancel_url", firstNonEmpty(req.CancelURL, req.ReturnURL))
	form.Set("client_reference_id", req.Reference)
	if req.Customer.Email != "" {
		form.Set("customer_email", req.Customer.Email)
	}
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", strings.ToLower(req.Currency))
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(stripeToMinorUnits(req.Amount, req.Currency), 10))
	form.Set("line_items[0][price_data][product_data][name]", truncate(firstNonEmpty(req.Description, "Payment "+req.Reference), 250))
	form.Set("payment_intent_data[metadata][reference]", req.Reference)
	for k, v := range req.Metadata {
		form.Set("metadata["+k+"]", v)
	}

	var out struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Status string `json:"status"`
	}
	raw, err := s.call(ctx, http.MethodPost, "/checkout/sessions", form, &out)
	if err != nil {
		return nil, err
	}
	if out.URL == "" {
		return nil, &ProviderError{Provider: s.Name(), Message: "checkout url was not returned"}
	}
	return &PaymentResponse{
		Provider:    s.Name(),
		Reference:   req.Reference,
		ProviderRef: out.ID,
		CheckoutURL: out.URL,
		Status:      StatusPending,
		Raw:         rawMap(raw),
	}, nil
}

type stripeSession struct {
	ID                string `json:"id"`
	ClientReferenceID string `json:"client_reference_id"`
	Status            string `json:"status"` // open, complete, expired
	PaymentStatus     string `json:"payment_status"`
	AmountTotal       int64  `json:"amount_total"`
	Currency          string `json:"currency"`
	PaymentIntent     any    `json:"payment_intent"` // string id, or an expanded object
}

func (s stripeSession) paymentIntentID() string {
	switch v := s.PaymentIntent.(type) {
	case string:
		return v
	case map[string]any:
		return str(v["id"])
	}
	return ""
}

// VerifyPayment reads the checkout session by its id (VerifyRequest.ProviderRef).
func (s *stripeProvider) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	if req.ProviderRef == "" {
		return nil, fmt.Errorf("%w: stripe looks payments up by checkout session id (provider reference)", ErrInvalidRequest)
	}
	var session stripeSession
	raw, err := s.call(ctx, http.MethodGet, "/checkout/sessions/"+url.PathEscape(req.ProviderRef), url.Values{"expand[]": {"payment_intent"}}, &session)
	if err != nil {
		return nil, err
	}
	return s.sessionResult(session, raw), nil
}

func (s *stripeProvider) sessionResult(session stripeSession, raw []byte) *PaymentResult {
	res := &PaymentResult{
		Provider:          s.Name(),
		Reference:         session.ClientReferenceID,
		ProviderRef:       session.ID,
		ProviderPaymentID: session.paymentIntentID(),
		Currency:          strings.ToUpper(session.Currency),
		Amount:            stripeFromMinorUnits(session.AmountTotal, session.Currency),
		Method:            "card",
		Raw:               rawMap(raw),
	}
	switch {
	case session.PaymentStatus == "paid" || session.PaymentStatus == "no_payment_required":
		res.Status = StatusSucceeded
	case session.Status == "expired":
		res.Status, res.FailureReason = StatusFailed, "expired"
	default:
		res.Status = StatusPending
	}
	return res
}

func (s *stripeProvider) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	form := url.Values{"payment_intent": {req.ProviderPaymentID}}
	if req.Amount > 0 {
		form.Set("amount", strconv.FormatInt(stripeToMinorUnits(req.Amount, req.Currency), 10))
	}
	if req.Reason != "" {
		form.Set("metadata[reason]", truncate(req.Reason, 500))
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	raw, err := s.call(ctx, http.MethodPost, "/refunds", form, &out)
	if err != nil {
		return nil, err
	}
	status := StatusPending
	switch out.Status {
	case "succeeded":
		status = StatusRefunded
	case "failed", "canceled":
		status = StatusFailed
	}
	return &RefundResult{Provider: s.Name(), ProviderRefundID: out.ID, Status: status, Raw: rawMap(raw)}, nil
}

type stripeRefund struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	PaymentIntent string `json:"payment_intent"`
	Reason        string `json:"reason"`
}

func (r stripeRefund) toResult(provider string, raw []byte) *RefundResult {
	status := StatusPending
	switch r.Status {
	case "succeeded":
		status = StatusRefunded
	case "failed", "canceled":
		status = StatusFailed
	}
	return &RefundResult{
		Provider:          provider,
		ProviderRefundID:  r.ID,
		ProviderPaymentID: r.PaymentIntent,
		Status:            status,
		Amount:            stripeFromMinorUnits(r.Amount, r.Currency),
		Currency:          strings.ToUpper(r.Currency),
		Reason:            r.Reason,
		Raw:               rawMap(raw),
	}
}

// GetRefund wraps GET /v1/refunds/{id}.
func (s *stripeProvider) GetRefund(ctx context.Context, req RefundQuery) (*RefundResult, error) {
	if req.RefundID == "" {
		return nil, fmt.Errorf("%w: refund id is required", ErrInvalidRequest)
	}
	var out stripeRefund
	raw, err := s.call(ctx, http.MethodGet, "/refunds/"+url.PathEscape(req.RefundID), nil, &out)
	if err != nil {
		return nil, err
	}
	return out.toResult(s.Name(), raw), nil
}

// ListRefunds wraps GET /v1/refunds, optionally scoped to one payment intent
// via req.ProviderPaymentID. Paginates by cursor, like ListPayments.
func (s *stripeProvider) ListRefunds(ctx context.Context, req RefundQuery) (*RefundList, error) {
	form := url.Values{}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	form.Set("limit", strconv.Itoa(limit))
	if req.ProviderPaymentID != "" {
		form.Set("payment_intent", req.ProviderPaymentID)
	}
	if req.Cursor != "" {
		form.Set("starting_after", req.Cursor)
	}

	var out struct {
		Data    []stripeRefund `json:"data"`
		HasMore bool           `json:"has_more"`
	}
	raw, err := s.call(ctx, http.MethodGet, "/refunds", form, &out)
	if err != nil {
		return nil, err
	}
	items := make([]RefundResult, 0, len(out.Data))
	var cursor string
	for _, r := range out.Data {
		items = append(items, *r.toResult(s.Name(), nil))
		cursor = r.ID
	}
	res := &RefundList{Provider: s.Name(), Items: items, HasMore: out.HasMore, Raw: rawMap(raw)}
	if out.HasMore {
		res.NextCursor = cursor
	}
	return res, nil
}

// CreatePaymentMethod attaches a PaymentMethod your client already created
// (Stripe.js Elements/SetupIntent — this package never collects a raw card
// number itself) to a customer, creating the customer first if CustomerRef
// is empty.
func (s *stripeProvider) CreatePaymentMethod(ctx context.Context, req PaymentMethodRequest) (*PaymentMethod, error) {
	if req.Token == "" {
		return nil, fmt.Errorf("%w: stripe needs a payment method id", ErrInvalidRequest)
	}
	customerID := req.CustomerRef
	if customerID == "" {
		form := url.Values{}
		if req.Customer.Email != "" {
			form.Set("email", req.Customer.Email)
		}
		if req.Customer.Name != "" {
			form.Set("name", req.Customer.Name)
		}
		if req.Customer.Phone != "" {
			form.Set("phone", req.Customer.Phone)
		}
		var cust struct {
			ID string `json:"id"`
		}
		if _, err := s.call(ctx, http.MethodPost, "/customers", form, &cust); err != nil {
			return nil, err
		}
		customerID = cust.ID
	}

	var pm stripePaymentMethod
	raw, err := s.call(ctx, http.MethodPost, "/payment_methods/"+url.PathEscape(req.Token)+"/attach", url.Values{"customer": {customerID}}, &pm)
	if err != nil {
		return nil, err
	}
	if req.SetAsDefault {
		if _, err := s.call(ctx, http.MethodPost, "/customers/"+url.PathEscape(customerID),
			url.Values{"invoice_settings[default_payment_method]": {pm.ID}}, nil); err != nil {
			return nil, err
		}
	}
	pm.Customer = customerID
	return pm.toPaymentMethod(s.Name(), raw, req.SetAsDefault), nil
}

// ListPaymentMethods wraps GET /v1/payment_methods?customer=...&type=card.
func (s *stripeProvider) ListPaymentMethods(ctx context.Context, customerRef string) ([]PaymentMethod, error) {
	if customerRef == "" {
		return nil, fmt.Errorf("%w: stripe needs a customer id to list payment methods", ErrInvalidRequest)
	}
	var out struct {
		Data []stripePaymentMethod `json:"data"`
	}
	if _, err := s.call(ctx, http.MethodGet, "/payment_methods", url.Values{"customer": {customerRef}, "type": {"card"}}, &out); err != nil {
		return nil, err
	}
	methods := make([]PaymentMethod, 0, len(out.Data))
	for _, pm := range out.Data {
		pm.Customer = customerRef
		methods = append(methods, *pm.toPaymentMethod(s.Name(), nil, false))
	}
	return methods, nil
}

// DeletePaymentMethod wraps POST /v1/payment_methods/{id}/detach.
func (s *stripeProvider) DeletePaymentMethod(ctx context.Context, customerRef, methodID string) error {
	if methodID == "" {
		return fmt.Errorf("%w: payment method id is required", ErrInvalidRequest)
	}
	_, err := s.call(ctx, http.MethodPost, "/payment_methods/"+url.PathEscape(methodID)+"/detach", url.Values{}, nil)
	return err
}

// stripePaymentMethod is a Stripe PaymentMethod object (card details only —
// this package doesn't handle other payment method types).
type stripePaymentMethod struct {
	ID       string `json:"id"`
	Customer string `json:"customer"`
	Card     struct {
		Brand    string `json:"brand"`
		Last4    string `json:"last4"`
		ExpMonth int    `json:"exp_month"`
		ExpYear  int    `json:"exp_year"`
	} `json:"card"`
}

func (pm stripePaymentMethod) toPaymentMethod(provider string, raw []byte, isDefault bool) *PaymentMethod {
	return &PaymentMethod{
		Provider:           provider,
		ProviderCustomerID: pm.Customer,
		ID:                 pm.ID,
		Last4:              pm.Card.Last4,
		Brand:              pm.Card.Brand,
		ExpiryMonth:        pm.Card.ExpMonth,
		ExpiryYear:         pm.Card.ExpYear,
		IsDefault:          isDefault,
		Raw:                rawMap(raw),
	}
}

// CancelPayment expires the checkout session (POST .../expire), which only
// succeeds while the session is still "open" — once the payer has completed
// or it has already expired, Stripe rejects the call.
func (s *stripeProvider) CancelPayment(ctx context.Context, req CancelRequest) (*CancelResult, error) {
	if req.ProviderRef == "" {
		return nil, fmt.Errorf("%w: stripe cancels by checkout session id (provider reference)", ErrInvalidRequest)
	}
	var session stripeSession
	raw, err := s.call(ctx, http.MethodPost, "/checkout/sessions/"+url.PathEscape(req.ProviderRef)+"/expire", url.Values{}, &session)
	if err != nil {
		return nil, err
	}
	return &CancelResult{
		Provider:      s.Name(),
		Reference:     session.ClientReferenceID,
		ProviderRef:   session.ID,
		Status:        StatusFailed,
		FailureReason: "cancelled",
		Raw:           rawMap(raw),
	}, nil
}

// ListPayments wraps GET /checkout/sessions, which paginates by cursor
// (ListRequest.Cursor / ListResult.NextCursor) rather than by page number.
func (s *stripeProvider) ListPayments(ctx context.Context, req ListRequest) (*ListResult, error) {
	form := url.Values{}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	form.Set("limit", strconv.Itoa(limit))
	if req.Cursor != "" {
		form.Set("starting_after", req.Cursor)
	}
	if req.Status != "" {
		form.Set("status", req.Status)
	}
	if !req.From.IsZero() {
		form.Set("created[gte]", strconv.FormatInt(req.From.Unix(), 10))
	}
	if !req.To.IsZero() {
		form.Set("created[lte]", strconv.FormatInt(req.To.Unix(), 10))
	}

	var out struct {
		Data    []stripeSession `json:"data"`
		HasMore bool            `json:"has_more"`
	}
	raw, err := s.call(ctx, http.MethodGet, "/checkout/sessions", form, &out)
	if err != nil {
		return nil, err
	}

	items := make([]PaymentResult, 0, len(out.Data))
	var cursor string
	for _, session := range out.Data {
		items = append(items, *s.sessionResult(session, nil))
		cursor = session.ID
	}
	res := &ListResult{Provider: s.Name(), Items: items, HasMore: out.HasMore, Raw: rawMap(raw)}
	if out.HasMore {
		res.NextCursor = cursor
	}
	return res, nil
}

// ParseWebhook verifies the Stripe-Signature header (HMAC-SHA256 over
// "{timestamp}.{body}" with the endpoint's signing secret) before trusting
// the event, and rejects timestamps older than 5 minutes.
func (s *stripeProvider) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	secret := s.cfg.cred(KeyWebhookSecret)
	if secret == "" {
		return nil, fmt.Errorf("%w: stripe webhook_secret is not configured", ErrInvalidRequest)
	}
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	if err := verifyStripeSignature(r.Header.Get("Stripe-Signature"), body, secret, 5*time.Minute); err != nil {
		return nil, err
	}

	var ev struct {
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("%w: stripe body: %v", ErrInvalidWebhook, err)
	}
	out := &WebhookEvent{Provider: s.Name(), Type: ev.Type}

	switch ev.Type {
	case "checkout.session.completed", "checkout.session.expired":
		var session stripeSession
		if err := json.Unmarshal(ev.Data.Object, &session); err != nil {
			return nil, fmt.Errorf("%w: stripe session: %v", ErrInvalidWebhook, err)
		}
		out.Result = s.sessionResult(session, ev.Data.Object)
	case "charge.refunded":
		var charge struct {
			PaymentIntent  string `json:"payment_intent"`
			AmountRefunded int64  `json:"amount_refunded"`
			Currency       string `json:"currency"`
			Refunded       bool   `json:"refunded"`
		}
		if err := json.Unmarshal(ev.Data.Object, &charge); err != nil {
			return nil, fmt.Errorf("%w: stripe charge: %v", ErrInvalidWebhook, err)
		}
		if charge.Refunded {
			out.Result = &PaymentResult{
				Provider:          s.Name(),
				ProviderPaymentID: charge.PaymentIntent,
				Status:            StatusRefunded,
				Amount:            stripeFromMinorUnits(charge.AmountRefunded, charge.Currency),
				Currency:          strings.ToUpper(charge.Currency),
				Raw:               rawMap(ev.Data.Object),
			}
		}
	}
	return out.withSuccess(), nil
}

// verifyStripeSignature checks a "t=...,v1=...[,v1=...]" header.
func verifyStripeSignature(header string, body []byte, secret string, tolerance time.Duration) error {
	if header == "" {
		return fmt.Errorf("%w: missing Stripe-Signature header", ErrInvalidWebhook)
	}
	var timestamp string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if timestamp == "" || len(sigs) == 0 {
		return fmt.Errorf("%w: malformed Stripe-Signature header", ErrInvalidWebhook)
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad timestamp in Stripe-Signature header", ErrInvalidWebhook)
	}
	if tolerance > 0 {
		age := time.Since(time.Unix(ts, 0))
		if age < 0 {
			age = -age
		}
		if age > tolerance {
			return fmt.Errorf("%w: Stripe-Signature timestamp outside tolerance", ErrInvalidWebhook)
		}
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + string(body)))
	want := hex.EncodeToString(mac.Sum(nil))
	for _, got := range sigs {
		if hmac.Equal([]byte(got), []byte(want)) {
			return nil
		}
	}
	return fmt.Errorf("%w: Stripe-Signature mismatch", ErrInvalidWebhook)
}
