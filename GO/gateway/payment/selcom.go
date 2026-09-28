package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Selcom Checkout API (Tanzania, TZS).
// Credentials: KeyAPIKey, KeyAPISecret, KeyVendor. Selcom has no public sandbox
// host; set Config.BaseURL to the UAT gateway they issue for testing.
//
// Every request is signed with HMAC-SHA256 over
// "timestamp=<ts>&<field>=<value>..." in the order given by the Signed-Fields
// header. For GET requests the query parameters are the signed fields.

const selcomBaseURL = "https://apigw.selcommobile.com"

// Selcom quotes timestamps in East Africa Time with a colon offset (+03:00).
var selcomZone = time.FixedZone("EAT", 3*60*60)

func init() {
	RegisterFactory(ProviderSelcom, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeyAPIKey, KeyAPISecret, KeyVendor); err != nil {
			return nil, err
		}
		return &selcom{cfg: cfg, base: strings.TrimRight(firstNonEmpty(cfg.BaseURL, selcomBaseURL), "/"), now: time.Now}, nil
	})
}

type selcom struct {
	cfg  Config
	base string
	now  func() time.Time
}

func (s *selcom) Name() string { return ProviderSelcom }

// selcomField keeps request fields in order, which the signature depends on.
type selcomField struct {
	Key   string
	Value any
}

// signedString is how a field value enters the signature.
func (f selcomField) signedString() string {
	switch v := f.Value.(type) {
	case json.Number:
		return v.String()
	case string:
		return v
	}
	return fmt.Sprint(f.Value)
}

func (s *selcom) sign(fields []selcomField) map[string]string {
	ts := s.now().In(selcomZone).Format(time.RFC3339)
	data := "timestamp=" + ts
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		data += "&" + f.Key + "=" + f.signedString()
		keys = append(keys, f.Key)
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.cred(KeyAPISecret)))
	mac.Write([]byte(data))

	return map[string]string{
		"Authorization": "SELCOM " + base64.StdEncoding.EncodeToString([]byte(s.cfg.cred(KeyAPIKey))),
		"Digest-Method": "HS256",
		"Digest":        base64.StdEncoding.EncodeToString(mac.Sum(nil)),
		"Timestamp":     ts,
		"Signed-Fields": strings.Join(keys, ","),
	}
}

type selcomEnvelope struct {
	Reference  string           `json:"reference"`
	ResultCode string           `json:"resultcode"`
	Result     string           `json:"result"`
	Message    string           `json:"message"`
	Data       []map[string]any `json:"data"`
}

func (e *selcomEnvelope) ok() bool { return e.ResultCode == "000" }

func (e *selcomEnvelope) err(provider string) error {
	return &ProviderError{Provider: provider, Code: e.ResultCode, Message: firstNonEmpty(e.Message, e.Result, "request rejected")}
}

func (s *selcom) post(ctx context.Context, path string, fields []selcomField) (*selcomEnvelope, []byte, error) {
	// Marshal in field order so the body matches Signed-Fields.
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(f.Key)
		v, err := json.Marshal(f.Value)
		if err != nil {
			return nil, nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')

	var env selcomEnvelope
	raw, err := doRaw(ctx, s.cfg.httpClient(), s.Name(), http.MethodPost, s.base+path, s.sign(fields), strings.NewReader(b.String()), &env)
	if err != nil {
		return nil, raw, s.enrich(err, raw)
	}
	return &env, raw, nil
}

func (s *selcom) get(ctx context.Context, path string, fields []selcomField) (*selcomEnvelope, []byte, error) {
	q := make([]string, 0, len(fields))
	for _, f := range fields {
		q = append(q, url.QueryEscape(f.Key)+"="+url.QueryEscape(f.signedString()))
	}
	var env selcomEnvelope
	raw, err := doRaw(ctx, s.cfg.httpClient(), s.Name(), http.MethodGet, s.base+path+"?"+strings.Join(q, "&"), s.sign(fields), nil, &env)
	if err != nil {
		return nil, raw, s.enrich(err, raw)
	}
	return &env, raw, nil
}

// enrich replaces the raw HTTP error body with Selcom's own message when present.
func (s *selcom) enrich(err error, raw []byte) error {
	var pe *ProviderError
	var env selcomEnvelope
	if asProviderError(err, &pe) && json.Unmarshal(raw, &env) == nil && (env.Message != "" || env.ResultCode != "") {
		pe.Code = env.ResultCode
		pe.Message = firstNonEmpty(env.Message, env.Result, pe.Message)
	}
	return err
}

func (s *selcom) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	c := req.Customer
	if c.Name == "" || c.Email == "" || c.Phone == "" {
		return nil, fmt.Errorf("%w: selcom needs customer name, email and phone", ErrInvalidRequest)
	}
	fields := []selcomField{
		{"vendor", s.cfg.cred(KeyVendor)},
		{"order_id", req.Reference},
		{"buyer_email", c.Email},
		{"buyer_name", c.Name},
		{"buyer_phone", strings.NewReplacer("+", "", " ", "", "-", "").Replace(c.Phone)},
		{"amount", json.Number(formatAmount(req.Amount, 0))}, // TZS has no minor unit
		{"currency", strings.ToUpper(req.Currency)},
	}
	// callback urls travel base64 encoded
	if req.ReturnURL != "" {
		fields = append(fields, selcomField{"redirect_url", b64(req.ReturnURL)})
	}
	if cancel := firstNonEmpty(req.CancelURL, req.ReturnURL); cancel != "" {
		fields = append(fields, selcomField{"cancel_url", b64(cancel)})
	}
	if hook := firstNonEmpty(req.WebhookURL, s.cfg.WebhookURL); hook != "" {
		fields = append(fields, selcomField{"webhook", b64(hook)})
	}
	fields = append(fields,
		selcomField{"buyer_remarks", firstNonEmpty(req.Description, "None")},
		selcomField{"merchant_remarks", firstNonEmpty(req.Description, "None")},
		selcomField{"no_of_items", json.Number("1")},
	)

	env, raw, err := s.post(ctx, "/v1/checkout/create-order-minimal", fields)
	if err != nil {
		return nil, err
	}
	if !env.ok() || len(env.Data) == 0 {
		return nil, env.err(s.Name())
	}
	link := decodeB64OrRaw(str(env.Data[0]["payment_gateway_url"]))
	if link == "" {
		return nil, &ProviderError{Provider: s.Name(), Code: env.ResultCode, Message: "checkout link was not returned"}
	}
	return &PaymentResponse{
		Provider:    s.Name(),
		Reference:   req.Reference,
		ProviderRef: env.Reference,
		CheckoutURL: link,
		Status:      StatusPending,
		Raw:         rawMap(raw),
	}, nil
}

// PushUSSD triggers a wallet debit prompt on the customer's phone via
// Selcom's Wallet API (/v1/wallet/pushussd) — a different API family from the
// hosted Checkout used by CreatePayment. A successful response only means the
// customer's wallet provider accepted the push, not that they approved it
// yet; poll VerifyPayment (which falls back to the wallet status query for
// push-originated references) or wait for the webhook.
func (s *selcom) PushUSSD(ctx context.Context, req PushRequest) (*PushResponse, error) {
	fields := []selcomField{
		{"transid", req.Reference},
		{"utilityref", firstNonEmpty(req.Description, req.Reference)},
		{"amount", json.Number(formatAmount(req.Amount, 0))}, // TZS has no minor unit
		{"vendor", s.cfg.cred(KeyVendor)},
		{"msisdn", strings.NewReplacer("+", "", " ", "", "-", "").Replace(req.Phone)},
	}
	env, raw, err := s.post(ctx, "/v1/wallet/pushussd", fields)
	if err != nil {
		return nil, err
	}
	if !env.ok() {
		return nil, env.err(s.Name())
	}
	return &PushResponse{
		Provider:     s.Name(),
		Reference:    req.Reference,
		ProviderRef:  firstNonEmpty(env.Reference, req.Reference),
		Status:       StatusPending,
		Instructions: firstNonEmpty(env.Message, "Enter your mobile money PIN on your phone to complete the payment."),
		Raw:          rawMap(raw),
	}, nil
}

// VerifyPayment checks the checkout order-status first (payments started with
// CreatePayment); a push started with PushUSSD lives in a different Selcom API
// (Wallet C2B) so a not-found order id falls back to its status query.
func (s *selcom) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	if req.Reference == "" {
		return nil, fmt.Errorf("%w: selcom looks payments up by reference (order id / transaction id)", ErrInvalidRequest)
	}
	env, raw, err := s.get(ctx, "/v1/checkout/order-status", []selcomField{{"order_id", req.Reference}})
	if err == nil && env.ok() && len(env.Data) > 0 {
		d := env.Data[0]
		res := &PaymentResult{
			Provider:          s.Name(),
			Reference:         firstNonEmpty(str(d["order_id"]), req.Reference),
			ProviderRef:       firstNonEmpty(str(d["reference"]), env.Reference),
			ProviderPaymentID: str(d["transid"]),
			Amount:            parseAmount(d["amount"]),
			Currency:          "TZS",
			Method:            str(d["channel"]),
			Raw:               rawMap(raw),
		}
		res.Status, res.FailureReason = selcomStatus(str(d["payment_status"]))
		return res, nil
	}
	return s.queryC2BStatus(ctx, req.Reference)
}

// queryC2BStatus checks a wallet push (PushUSSD) via /v1/c2b/query-status.
func (s *selcom) queryC2BStatus(ctx context.Context, transID string) (*PaymentResult, error) {
	env, raw, err := s.get(ctx, "/v1/c2b/query-status", []selcomField{{"transid", transID}})
	if err != nil {
		return nil, err
	}
	// resultcode "000" means the status query itself succeeded; the actual
	// payment outcome is in result/message (SUCCESS/FAILED, or in-progress
	// wording when the customer has not approved the push yet).
	if !env.ok() {
		return nil, env.err(s.Name())
	}
	res := &PaymentResult{
		Provider:          s.Name(),
		Reference:         firstNonEmpty(env.Reference, transID),
		ProviderPaymentID: transID,
		Currency:          "TZS",
		Raw:               rawMap(raw),
	}
	msg := strings.ToUpper(env.Message)
	switch {
	case strings.EqualFold(env.Result, "SUCCESS") || strings.Contains(msg, "COMPLETE") || strings.Contains(msg, "CONFIRMED"):
		res.Status = StatusSucceeded
	case strings.EqualFold(env.Result, "FAILED"):
		res.Status, res.FailureReason = StatusFailed, firstNonEmpty(env.Message, "failed")
	default:
		res.Status = StatusPending
	}
	return res, nil
}

func selcomStatus(s string) (Status, string) {
	switch strings.ToUpper(s) {
	case "COMPLETED":
		return StatusSucceeded, ""
	case "CANCELLED", "USERCANCELLED", "REJECTED", "EXPIRED", "FAILED":
		return StatusFailed, strings.ToLower(s)
	}
	return StatusPending, "" // PENDING, INPROGRESS
}

// CancelPayment is not offered by either of Selcom's Checkout or Wallet APIs.
func (s *selcom) CancelPayment(context.Context, CancelRequest) (*CancelResult, error) {
	return nil, fmt.Errorf("%w: selcom has no api to cancel an order or push", ErrNotSupported)
}

// ListPayments is not offered: neither the Checkout nor the Wallet C2B API
// exposes a "list all orders/transactions" endpoint.
func (s *selcom) ListPayments(context.Context, ListRequest) (*ListResult, error) {
	return nil, fmt.Errorf("%w: selcom has no transaction listing api", ErrNotSupported)
}

// GetRefund is not offered: Selcom has no refund api to look one up from.
func (s *selcom) GetRefund(context.Context, RefundQuery) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: selcom has no refund api", ErrNotSupported)
}

// ListRefunds is not offered: Selcom has no refund api to list from.
func (s *selcom) ListRefunds(context.Context, RefundQuery) (*RefundList, error) {
	return nil, fmt.Errorf("%w: selcom has no refund api", ErrNotSupported)
}

// Refund is not offered by the Selcom checkout API.
func (s *selcom) Refund(context.Context, RefundRequest) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: selcom refunds are handled outside the API", ErrNotSupported)
}

// ParseWebhook decodes Selcom's JSON callback. The callback carries no
// signature, so the order status is fetched from Selcom before it is trusted.
func (s *selcom) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: selcom body: %v", ErrInvalidWebhook, err)
	}
	orderID := str(payload["order_id"])
	if orderID == "" {
		return nil, fmt.Errorf("%w: selcom callback without order_id", ErrInvalidWebhook)
	}
	res, err := s.VerifyPayment(ctx, VerifyRequest{Reference: orderID})
	if err != nil {
		return nil, err
	}
	return (&WebhookEvent{Provider: s.Name(), Type: "order.status", Result: res}).withSuccess(), nil
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// decodeB64OrRaw returns v decoded from base64, or v itself when it already is a URL.
func decodeB64OrRaw(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil {
		return string(b)
	}
	return ""
}
