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
	"sync"
	"time"
)

// ClickPesa Collection API (Tanzania: hosted checkout, USSD push, cards).
// Credentials: KeyClientID (client-id header), KeyAPIKey (api-key header, used
// once to mint a bearer token), KeyChecksumKey (optional): when set, requests
// and webhooks are signed/verified with it; when unset, requests are sent
// unsigned and webhooks are trusted only after a status re-check.

const clickpesaBaseURL = "https://api.clickpesa.com/third-parties"

func init() {
	RegisterFactory(ProviderClickPesa, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeyClientID, KeyAPIKey); err != nil {
			return nil, err
		}
		return &clickpesa{cfg: cfg, base: strings.TrimRight(firstNonEmpty(cfg.BaseURL, clickpesaBaseURL), "/")}, nil
	})
}

type clickpesa struct {
	cfg  Config
	base string

	mu        sync.Mutex
	token     string
	tokenExpr time.Time
}

func (c *clickpesa) Name() string { return ProviderClickPesa }

func (c *clickpesa) authToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpr) {
		return c.token, nil
	}
	var out struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	_, err := doJSON(ctx, c.cfg.httpClient(), c.Name(), http.MethodPost, c.base+"/generate-token", map[string]string{
		"client-id": c.cfg.cred(KeyClientID),
		"api-key":   c.cfg.cred(KeyAPIKey),
	}, nil, &out)
	if err != nil {
		return "", err
	}
	if !out.Success || out.Token == "" {
		return "", &ProviderError{Provider: c.Name(), Message: firstNonEmpty(out.Message, "no token returned")}
	}

	c.token = out.Token
	c.tokenExpr = time.Now().Add(55 * time.Minute) // tokens are valid for 1 hour
	return c.token, nil
}

func (c *clickpesa) call(ctx context.Context, method, path string, in any, out any) ([]byte, error) {
	tok, err := c.authToken(ctx)

	// console.Log("call.tok", tok)
	// console.Log("call.path", path)
	// console.Log("call.in", in)
	// console.Log("call.out", out)

	if err != nil {
		return nil, err
	}
	raw, err := doJSON(ctx, c.cfg.httpClient(), c.Name(), method, c.base+path, map[string]string{"Authorization": "Bearer " + tok}, in, out)
	if err != nil {
		var pe *ProviderError
		var e struct {
			Message string `json:"message"`
		}
		if asProviderError(err, &pe) && json.Unmarshal(raw, &e) == nil && e.Message != "" {
			pe.Message = e.Message
		}
	}
	return raw, err
}

// checksum signs body with HMAC-SHA256. Go's json.Marshal on a map[string]any
// already sorts keys alphabetically at every nesting level and produces
// compact output, which is exactly ClickPesa's canonicalization rule.
func (c *clickpesa) checksum(body map[string]any) (string, error) {
	clean := make(map[string]any, len(body))
	for k, v := range body {
		if k == "checksum" || k == "checksumMethod" {
			continue
		}
		clean[k] = v
	}
	canonical, err := json.Marshal(clean)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(c.cfg.cred(KeyChecksumKey)))
	mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// sign adds "checksum"/"checksumMethod" to body when a checksum key is
// configured; it is a no-op otherwise, since ClickPesa treats it as optional.
func (c *clickpesa) sign(body map[string]any) (map[string]any, error) {
	if c.cfg.cred(KeyChecksumKey) == "" {
		return body, nil
	}
	sum, err := c.checksum(body)
	if err != nil {
		return nil, err
	}
	body["checksum"] = sum
	body["checksumMethod"] = "HMAC-SHA256"
	return body, nil
}

func (c *clickpesa) PushUSSD(ctx context.Context, req PushRequest) (*PushResponse, error) {
	if strings.ToUpper(req.Currency) != "TZS" {
		return nil, fmt.Errorf("%w: clickpesa ussd push only supports TZS", ErrInvalidRequest)
	}
	body := map[string]any{
		"amount":         formatAmount(req.Amount, 0),
		"currency":       "TZS",
		"orderReference": req.Reference,
		"phoneNumber":    strings.NewReplacer("+", "", " ", "", "-", "").Replace(req.Phone),
	}
	body, err := c.sign(body)
	if err != nil {
		return nil, err
	}

	var out struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Channel string `json:"channel"`
		Message string `json:"message"`
	}
	raw, err := c.call(ctx, http.MethodPost, "/payments/initiate-ussd-push-request", body, &out)
	if err != nil {
		return nil, err
	}
	return &PushResponse{
		Provider:     c.Name(),
		Reference:    req.Reference,
		ProviderRef:  firstNonEmpty(out.ID, req.Reference),
		Status:       StatusPending,
		Instructions: firstNonEmpty(out.Message, "Enter your mobile money PIN on the "+firstNonEmpty(out.Channel, "USSD")+" prompt on your phone."),
		Raw:          rawMap(raw),
	}, nil
}

func (c *clickpesa) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	body := map[string]any{
		"totalPrice":     formatAmount(req.Amount, 2),
		"orderReference": req.Reference,
		"orderCurrency":  strings.ToUpper(req.Currency),
		"description":    firstNonEmpty(req.Description, "Payment "+req.Reference),
	}
	if req.Customer.Name != "" {
		body["customerName"] = req.Customer.Name
	}
	if req.Customer.Email != "" {
		body["customerEmail"] = req.Customer.Email
	}
	if req.Customer.Phone != "" {
		body["customerPhone"] = strings.NewReplacer("+", "", " ", "", "-", "").Replace(req.Customer.Phone)
	}
	if req.ReturnURL != "" {
		body["callbackUrl"] = req.ReturnURL
	}
	body, err := c.sign(body)
	if err != nil {
		return nil, err
	}

	var out struct {
		CheckoutLink string `json:"checkoutLink"`
	}
	raw, err := c.call(ctx, http.MethodPost, "/checkout-link/generate-checkout-url", body, &out)
	if err != nil {
		return nil, err
	}
	if out.CheckoutLink == "" {
		return nil, &ProviderError{Provider: c.Name(), Message: "checkout link was not returned"}
	}
	return &PaymentResponse{
		Provider:    c.Name(),
		Reference:   req.Reference,
		CheckoutURL: out.CheckoutLink,
		Status:      StatusPending,
		Raw:         rawMap(raw),
	}, nil
}

type clickpesaPaymentAttempt struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	OrderReference     string `json:"orderReference"`
	CollectedAmount    any    `json:"collectedAmount"`
	CollectedCurrency  string `json:"collectedCurrency"`
	PaymentPhoneNumber string `json:"paymentPhoneNumber"`
	Message            string `json:"message"`
}

// VerifyPayment looks up every payment attempt made against an order
// reference (ClickPesa allows more than one push attempt per order) and
// reports the most conclusive outcome: a success beats an in-progress
// attempt, which beats a failure.
func (c *clickpesa) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	if req.Reference == "" {
		return nil, fmt.Errorf("%w: clickpesa looks payments up by reference (order reference)", ErrInvalidRequest)
	}
	var attempts []clickpesaPaymentAttempt
	raw, err := c.call(ctx, http.MethodGet, "/payments/"+req.Reference, nil, &attempts)
	if err != nil {
		return nil, err
	}
	if len(attempts) == 0 {
		return &PaymentResult{Provider: c.Name(), Reference: req.Reference, Status: StatusPending, Raw: rawMap(raw)}, nil
	}

	best := attempts[0]
	bestRank := clickpesaStatusRank(best.Status)
	for _, a := range attempts[1:] {
		if r := clickpesaStatusRank(a.Status); r > bestRank {
			best, bestRank = a, r
		}
	}
	res := &PaymentResult{
		Provider:          c.Name(),
		Reference:         firstNonEmpty(best.OrderReference, req.Reference),
		ProviderPaymentID: best.ID,
		Currency:          firstNonEmpty(best.CollectedCurrency, "TZS"),
		Amount:            parseAmount(best.CollectedAmount),
		Method:            "mobile_money",
		Raw:               rawMap(raw),
	}
	res.Status, res.FailureReason = clickpesaStatus(best.Status, best.Message)
	return res, nil
}

// clickpesaStatusRank orders outcomes so VerifyPayment can pick the most
// conclusive attempt: success outranks in-progress, which outranks failure.
func clickpesaStatusRank(status string) int {
	switch strings.ToUpper(status) {
	case "SUCCESS", "SETTLED":
		return 2
	case "PROCESSING", "PENDING":
		return 1
	default: // FAILED and anything unrecognized
		return 0
	}
}

func clickpesaStatus(status, message string) (Status, string) {
	switch strings.ToUpper(status) {
	case "SUCCESS", "SETTLED":
		return StatusSucceeded, ""
	case "PROCESSING", "PENDING":
		return StatusPending, ""
	case "FAILED":
		return StatusFailed, firstNonEmpty(message, "failed")
	}
	return StatusPending, ""
}

// Refund is not offered by the Collection API; ClickPesa refunds go through
// its separate Payout API.
func (c *clickpesa) Refund(context.Context, RefundRequest) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: clickpesa refunds go through the payout api, not documented here", ErrNotSupported)
}

// GetRefund is not offered by the Collection API, for the same reason as Refund.
func (c *clickpesa) GetRefund(context.Context, RefundQuery) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: clickpesa refunds go through the payout api, not documented here", ErrNotSupported)
}

// ListRefunds is not offered by the Collection API, for the same reason as Refund.
func (c *clickpesa) ListRefunds(context.Context, RefundQuery) (*RefundList, error) {
	return nil, fmt.Errorf("%w: clickpesa refunds go through the payout api, not documented here", ErrNotSupported)
}

// CancelPayment is not offered: a USSD push in flight cannot be cancelled
// once initiated, and completed checkout-link orders aren't cancellable
// either.
func (c *clickpesa) CancelPayment(context.Context, CancelRequest) (*CancelResult, error) {
	return nil, fmt.Errorf("%w: clickpesa has no api to cancel a payment", ErrNotSupported)
}

// ListPayments wraps GET /payments/all.
func (c *clickpesa) ListPayments(ctx context.Context, req ListRequest) (*ListResult, error) {
	q := url.Values{}
	if !req.From.IsZero() {
		q.Set("startDate", req.From.Format("2006-01-02"))
	}
	if !req.To.IsZero() {
		q.Set("endDate", req.To.Format("2006-01-02"))
	}
	if req.Status != "" {
		q.Set("status", req.Status)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	q.Set("limit", strconv.Itoa(limit))
	skip := 0
	if req.Page > 1 {
		skip = (req.Page - 1) * limit
	}
	q.Set("skip", strconv.Itoa(skip))

	var out struct {
		Data       []clickpesaPaymentAttempt `json:"data"`
		TotalCount int                       `json:"totalCount"`
	}
	raw, err := c.call(ctx, http.MethodGet, "/payments/all?"+q.Encode(), nil, &out)
	if err != nil {
		return nil, err
	}

	items := make([]PaymentResult, 0, len(out.Data))
	for _, a := range out.Data {
		status, reason := clickpesaStatus(a.Status, a.Message)
		items = append(items, PaymentResult{
			Provider:          c.Name(),
			Reference:         a.OrderReference,
			ProviderPaymentID: a.ID,
			Currency:          firstNonEmpty(a.CollectedCurrency, "TZS"),
			Amount:            parseAmount(a.CollectedAmount),
			Method:            "mobile_money",
			Status:            status,
			FailureReason:     reason,
		})
	}
	return &ListResult{
		Provider: c.Name(),
		Items:    items,
		Total:    out.TotalCount,
		HasMore:  skip+len(items) < out.TotalCount,
		Raw:      rawMap(raw),
	}, nil
}

// ParseWebhook verifies the checksum when one is configured, then always
// re-confirms the outcome with VerifyPayment before trusting it — the
// checksum's field coverage on webhooks is not fully documented, so this is
// defense in depth rather than the sole authentication.
func (c *clickpesa) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Event    string         `json:"event"`
		Checksum string         `json:"checksum"`
		Data     map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: clickpesa body: %v", ErrInvalidWebhook, err)
	}
	if key := c.cfg.cred(KeyChecksumKey); key != "" && payload.Checksum != "" {
		var whole map[string]any
		_ = json.Unmarshal(body, &whole)
		want, err := c.checksum(whole)
		if err != nil {
			return nil, err
		}
		if !hmac.Equal([]byte(want), []byte(strings.ToLower(payload.Checksum))) {
			return nil, fmt.Errorf("%w: clickpesa checksum mismatch", ErrInvalidWebhook)
		}
	}

	ref := pickStr(payload.Data, "orderReference")
	if ref == "" {
		return (&WebhookEvent{Provider: c.Name(), Type: payload.Event}).withSuccess(), nil
	}
	res, err := c.VerifyPayment(ctx, VerifyRequest{Reference: ref})
	if err != nil {
		return nil, err
	}
	return (&WebhookEvent{Provider: c.Name(), Type: payload.Event, Result: res}).withSuccess(), nil
}
