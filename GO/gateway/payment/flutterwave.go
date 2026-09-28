package payment

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Flutterwave v3 standard (hosted) checkout.
// Credentials: KeySecretKey, KeyWebhookHash (the "secret hash" from the dashboard).

const flutterwaveBaseURL = "https://api.flutterwave.com/v3"

func init() {
	RegisterFactory(ProviderFlutterwave, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeySecretKey); err != nil {
			return nil, err
		}
		return &flutterwave{cfg: cfg, base: firstNonEmpty(cfg.BaseURL, flutterwaveBaseURL)}, nil
	})
}

type flutterwave struct {
	cfg  Config
	base string
}

func (f *flutterwave) Name() string { return ProviderFlutterwave }

func (f *flutterwave) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + f.cfg.cred(KeySecretKey)}
}

type flwEnvelope struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

func (f *flutterwave) call(ctx context.Context, method, path string, in any) (*flwEnvelope, []byte, error) {
	var env flwEnvelope
	raw, err := doJSON(ctx, f.cfg.httpClient(), f.Name(), method, f.base+path, f.headers(), in, &env)
	if err != nil {
		var pe *ProviderError
		if asProviderError(err, &pe) {
			var e flwEnvelope
			if json.Unmarshal(raw, &e) == nil && e.Message != "" {
				pe.Message = e.Message
			}
		}
		return nil, raw, err
	}
	return &env, raw, nil
}

func (f *flutterwave) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	if req.ReturnURL == "" {
		return nil, fmt.Errorf("%w: flutterwave needs a return url", ErrInvalidRequest)
	}
	body := map[string]any{
		"tx_ref":       req.Reference,
		"amount":       req.Amount,
		"currency":     strings.ToUpper(req.Currency),
		"redirect_url": req.ReturnURL, // cancelled payers are sent here too with status=cancelled
		"customer": map[string]any{
			"email":       req.Customer.Email,
			"name":        req.Customer.Name,
			"phonenumber": req.Customer.Phone,
		},
		"customizations": map[string]any{"description": req.Description},
	}
	if len(req.Metadata) > 0 {
		body["meta"] = req.Metadata
	}

	env, raw, err := f.call(ctx, http.MethodPost, "/payments", body)
	if err != nil {
		return nil, err
	}
	link := str(env.Data["link"])
	if env.Status != "success" || link == "" {
		return nil, &ProviderError{Provider: f.Name(), Message: firstNonEmpty(env.Message, "checkout link was not returned")}
	}
	return &PaymentResponse{
		Provider:    f.Name(),
		Reference:   req.Reference,
		CheckoutURL: link,
		Status:      StatusPending,
		Raw:         rawMap(raw),
	}, nil
}

// flwMobileMoneyType maps a country to the "type" query param of
// POST /v3/charges. Francophone countries share one type and additionally
// need "country" in the body.
var flwMobileMoneyType = map[string]string{
	"TZ": "mobile_money_tanzania",
	"KE": "mpesa",
	"UG": "mobile_money_uganda",
	"GH": "mobile_money_ghana",
	"RW": "mobile_money_rwanda",
	"ZM": "mobile_money_zambia",
	"CM": "mobile_money_franco",
	"SN": "mobile_money_franco",
	"BF": "mobile_money_franco",
	"CI": "mobile_money_franco",
}

// flwCurrencyCountry falls back from currency to country when CountryCode is
// not given.
var flwCurrencyCountry = map[string]string{
	"TZS": "TZ", "KES": "KE", "UGX": "UG", "GHS": "GH", "RWF": "RW", "ZMW": "ZM",
	"XOF": "SN", "XAF": "CM",
}

// networkRequired lists countries whose charge requires an explicit "network".
var flwNetworkRequired = map[string]bool{"UG": true, "GH": true, "RW": true, "ZM": true, "CM": true, "SN": true, "BF": true, "CI": true}

// PushUSSD charges the customer's mobile money wallet directly: Flutterwave
// sends them a prompt on their phone (a push notification or USSD code) and
// they approve it with their PIN. The country/currency picks the country
// specific charge endpoint Flutterwave requires for mobile money.
func (f *flutterwave) PushUSSD(ctx context.Context, req PushRequest) (*PushResponse, error) {
	country := strings.ToUpper(req.CountryCode)
	if country == "" {
		country = flwCurrencyCountry[strings.ToUpper(req.Currency)]
	}
	chargeType, ok := flwMobileMoneyType[country]
	if !ok {
		return nil, fmt.Errorf("%w: flutterwave has no mobile money route for country %q / currency %q", ErrInvalidRequest, req.CountryCode, req.Currency)
	}
	if flwNetworkRequired[country] && req.Network == "" {
		return nil, fmt.Errorf("%w: flutterwave needs a network for %s mobile money", ErrInvalidRequest, country)
	}

	body := map[string]any{
		"tx_ref":       req.Reference,
		"amount":       req.Amount,
		"currency":     strings.ToUpper(req.Currency),
		"phone_number": req.Phone,
		"email":        firstNonEmpty(req.Customer.Email, "no-reply@dephics.com"),
	}
	if req.Network != "" {
		body["network"] = strings.ToUpper(req.Network)
	}
	if req.Customer.Name != "" {
		body["fullname"] = req.Customer.Name
	}
	if chargeType == "mobile_money_franco" {
		body["country"] = country
	}
	if len(req.Metadata) > 0 {
		body["meta"] = req.Metadata
	}

	env, raw, err := f.call(ctx, http.MethodPost, "/charges?type="+chargeType, body)
	if err != nil {
		return nil, err
	}
	if env.Status != "success" && env.Status != "pending" {
		return nil, &ProviderError{Provider: f.Name(), Message: firstNonEmpty(env.Message, "push request rejected")}
	}

	resp := &PushResponse{
		Provider:     f.Name(),
		Reference:    req.Reference,
		ProviderRef:  str(env.Data["id"]),
		Status:       StatusPending,
		Instructions: firstNonEmpty(str(env.Data["processor_response"]), env.Message, "Approve the payment prompt on your phone with your mobile money PIN."),
		Raw:          rawMap(raw),
	}
	// a redirect-style flow (e.g. Zambia's captcha/authorization page) needs a
	// browser step; surface it so the caller can send the customer there.
	if meta, ok := env.Data["meta"].(map[string]any); ok {
		if auth, ok := meta["authorization"].(map[string]any); ok {
			if redirect := str(auth["redirect"]); redirect != "" {
				resp.Instructions = redirect
			}
		}
	}
	return resp, nil
}

func (f *flutterwave) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	var path string
	switch {
	case req.ProviderRef != "":
		path = "/transactions/" + url.PathEscape(req.ProviderRef) + "/verify"
	case req.Reference != "":
		path = "/transactions/verify_by_reference?tx_ref=" + url.QueryEscape(req.Reference)
	default:
		return nil, fmt.Errorf("%w: reference or provider reference is required", ErrInvalidRequest)
	}

	env, raw, err := f.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		// Unknown tx_ref: the payer has not started the payment yet.
		var pe *ProviderError
		if asProviderError(err, &pe) && pe.HTTPStatus == http.StatusNotFound {
			return &PaymentResult{Provider: f.Name(), Reference: req.Reference, Status: StatusPending, Raw: rawMap(raw)}, nil
		}
		return nil, err
	}
	if env.Data == nil {
		return &PaymentResult{Provider: f.Name(), Reference: req.Reference, Status: StatusPending, Raw: rawMap(raw)}, nil
	}
	return f.result(env.Data, raw), nil
}

func (f *flutterwave) result(d map[string]any, raw []byte) *PaymentResult {
	res := &PaymentResult{
		Provider:          f.Name(),
		Reference:         str(d["tx_ref"]),
		ProviderRef:       str(d["id"]),
		ProviderPaymentID: str(d["id"]),
		Amount:            parseAmount(d["amount"]),
		Currency:          str(d["currency"]),
		Method:            str(d["payment_type"]),
		Raw:               rawMap(raw),
	}
	switch strings.ToLower(str(d["status"])) {
	case "successful", "success", "completed":
		res.Status = StatusSucceeded
	case "failed", "cancelled":
		res.Status = StatusFailed
		res.FailureReason = firstNonEmpty(str(d["processor_response"]), str(d["status"]))
	default:
		res.Status = StatusPending
	}
	// a fully refunded charge stays "successful"; amount_refunded tells them apart
	if refunded := parseAmount(d["amount_refunded"]); res.Status == StatusSucceeded && refunded > 0 && refunded >= res.Amount {
		res.Status = StatusRefunded
	}
	return res
}

func (f *flutterwave) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	body := map[string]any{}
	if req.Amount > 0 {
		body["amount"] = req.Amount
	}
	env, raw, err := f.call(ctx, http.MethodPost, "/transactions/"+url.PathEscape(req.ProviderPaymentID)+"/refund", body)
	if err != nil {
		return nil, err
	}
	if env.Status != "success" {
		return nil, &ProviderError{Provider: f.Name(), Message: firstNonEmpty(env.Message, "refund rejected")}
	}
	status := StatusPending
	if strings.EqualFold(str(env.Data["status"]), "completed") {
		status = StatusRefunded
	}
	return &RefundResult{
		Provider:         f.Name(),
		ProviderRefundID: str(env.Data["id"]),
		Status:           status,
		Raw:              rawMap(raw),
	}, nil
}

func flwRefundResult(provider string, d map[string]any) RefundResult {
	res := RefundResult{
		Provider:          provider,
		ProviderRefundID:  str(d["id"]),
		ProviderPaymentID: str(d["transaction_id"]),
		Amount:            parseAmount(d["amount"]),
		Currency:          str(d["currency"]),
		Raw:               d,
	}
	if strings.EqualFold(str(d["status"]), "completed") {
		res.Status = StatusRefunded
	} else {
		res.Status = StatusPending
	}
	return res
}

// GetRefund wraps GET /v3/refunds/{id}.
func (f *flutterwave) GetRefund(ctx context.Context, req RefundQuery) (*RefundResult, error) {
	if req.RefundID == "" {
		return nil, fmt.Errorf("%w: refund id is required", ErrInvalidRequest)
	}
	env, raw, err := f.call(ctx, http.MethodGet, "/refunds/"+url.PathEscape(req.RefundID), nil)
	if err != nil {
		return nil, err
	}
	if env.Status != "success" {
		return nil, &ProviderError{Provider: f.Name(), Message: firstNonEmpty(env.Message, "refund not found")}
	}
	res := flwRefundResult(f.Name(), env.Data)
	res.Raw = rawMap(raw)
	return &res, nil
}

// ListRefunds wraps GET /v3/transactions/{id}/refunds when scoped to one
// payment (req.ProviderPaymentID), or GET /v3/refunds for every refund on
// the account otherwise — the latter is referenced in Flutterwave's docs
// sidebar but its exact response shape wasn't independently confirmed, so
// treat it as best-effort.
func (f *flutterwave) ListRefunds(ctx context.Context, req RefundQuery) (*RefundList, error) {
	path := "/refunds"
	if req.ProviderPaymentID != "" {
		path = "/transactions/" + url.PathEscape(req.ProviderPaymentID) + "/refunds"
	}
	q := url.Values{}
	if req.Page > 0 {
		q.Set("page", strconv.Itoa(req.Page))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	var out struct {
		Status string           `json:"status"`
		Data   []map[string]any `json:"data"`
	}
	raw, err := doJSON(ctx, f.cfg.httpClient(), f.Name(), http.MethodGet, f.base+path, f.headers(), nil, &out)
	if err != nil {
		return nil, err
	}
	items := make([]RefundResult, 0, len(out.Data))
	for _, d := range out.Data {
		items = append(items, flwRefundResult(f.Name(), d))
	}
	return &RefundList{Provider: f.Name(), Items: items, Raw: rawMap(raw)}, nil
}

// CancelPayment is not offered: Flutterwave has no api to cancel a charge
// once it has been initiated (mobile money/card pushes can only complete,
// fail, or be refunded after the fact).
func (f *flutterwave) CancelPayment(context.Context, CancelRequest) (*CancelResult, error) {
	return nil, fmt.Errorf("%w: flutterwave has no api to cancel an initiated transaction", ErrNotSupported)
}

// ListPayments wraps GET /v3/transactions. Flutterwave requires a date range;
// when From/To are zero this defaults to "everything up to today" so callers
// can omit them.
func (f *flutterwave) ListPayments(ctx context.Context, req ListRequest) (*ListResult, error) {
	from, to := req.From, req.To
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = time.Unix(0, 0)
	}
	q := url.Values{
		"from": {from.Format("2006-01-02")},
		"to":   {to.Format("2006-01-02")},
	}
	if req.Status != "" {
		q.Set("status", req.Status)
	}
	if req.Page > 0 {
		q.Set("page", strconv.Itoa(req.Page))
	}

	// The list response's "data" is a JSON array, unlike every other
	// Flutterwave endpoint this package calls (where it's an object), so this
	// bypasses f.call's flwEnvelope (Data map[string]any) and decodes directly.
	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Meta    struct {
			PageInfo struct {
				Total       int `json:"total"`
				CurrentPage int `json:"current_page"`
				TotalPages  int `json:"total_pages"`
			} `json:"page_info"`
		} `json:"meta"`
		Data []map[string]any `json:"data"`
	}
	raw, err := doJSON(ctx, f.cfg.httpClient(), f.Name(), http.MethodGet, f.base+"/transactions?"+q.Encode(), f.headers(), nil, &out)
	if err != nil {
		return nil, err
	}

	results := make([]PaymentResult, 0, len(out.Data))
	for _, d := range out.Data {
		res := f.result(d, nil)
		res.Raw = d
		results = append(results, *res)
	}
	return &ListResult{
		Provider:   f.Name(),
		Items:      results,
		Page:       out.Meta.PageInfo.CurrentPage,
		TotalPages: out.Meta.PageInfo.TotalPages,
		Total:      out.Meta.PageInfo.Total,
		HasMore:    out.Meta.PageInfo.CurrentPage < out.Meta.PageInfo.TotalPages,
		Raw:        rawMap(raw),
	}, nil
}

// QuoteFee wraps GET /v3/transactions/fee, Flutterwave's pre-charge fee
// estimate. req.PaymentType is passed through as-is (Flutterwave's own
// values: "card", "debit_ng_account", "mobilemoney", "banktransfer",
// "ach_payment"); leaving it empty asks for the default channel's fee.
func (f *flutterwave) QuoteFee(ctx context.Context, req FeeRequest) (*FeeQuote, error) {
	q := url.Values{
		"amount":   {formatAmount(req.Amount, 2)},
		"currency": {strings.ToUpper(req.Currency)},
	}
	if req.PaymentType != "" {
		q.Set("payment_type", req.PaymentType)
	}
	env, raw, err := f.call(ctx, http.MethodGet, "/transactions/fee?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if env.Status != "success" {
		return nil, &ProviderError{Provider: f.Name(), Message: firstNonEmpty(env.Message, "fee quote rejected")}
	}
	return &FeeQuote{
		Provider: f.Name(),
		Fee:      parseAmount(env.Data["fee"]),
		Currency: firstNonEmpty(str(env.Data["currency"]), strings.ToUpper(req.Currency)),
		Raw:      rawMap(raw),
	}, nil
}

// CreateVirtualCard issues a Flutterwave virtual card (POST /v3/virtual-cards).
//
// IMPORTANT: as of this writing, Flutterwave's own official Node SDK has this
// endpoint's request schema entirely commented out with the note "THE VIRTUAL
// CARD SERVICE IS CURRENTLY UNAVAILABLE" — calling it there would fail
// validation before the request is even sent. The endpoint path and
// request/response shape below are taken from Flutterwave's docs and SDK
// examples, but whether it actually accepts calls depends on whether Card
// Issuing is enabled (a separately approved product) on your account. Expect
// this to error until you've confirmed that with Flutterwave.
func (f *flutterwave) CreateVirtualCard(ctx context.Context, req CardRequest) (*Card, error) {
	if req.Customer.Email == "" {
		return nil, fmt.Errorf("%w: flutterwave virtual cards need a customer email", ErrInvalidRequest)
	}
	first, last := splitName(req.Customer.Name)
	body := map[string]any{
		"currency":            firstNonEmpty(strings.ToUpper(req.Currency), "USD"),
		"amount":              req.Amount,
		"debit_currency":      strings.ToUpper(req.DebitCurrency),
		"billing_name":        req.Customer.Name,
		"billing_address":     req.BillingAddress,
		"billing_city":        req.BillingCity,
		"billing_state":       req.BillingState,
		"billing_postal_code": req.BillingPostalCode,
		"billing_country":     strings.ToUpper(req.BillingCountry),
		"first_name":          first,
		"last_name":           last,
		"date_of_birth":       req.DateOfBirth,
		"email":               req.Customer.Email,
		"phone":               req.Customer.Phone,
		"title":               strings.ToUpper(req.Title),
		"gender":              strings.ToUpper(req.Gender),
	}
	if req.CallbackURL != "" {
		body["callback_url"] = req.CallbackURL
	}

	env, raw, err := f.call(ctx, http.MethodPost, "/virtual-cards", body)
	if err != nil {
		return nil, err
	}
	if env.Status != "success" || env.Data["id"] == nil {
		return nil, &ProviderError{Provider: f.Name(), Message: firstNonEmpty(env.Message, "card was not created")}
	}
	d := env.Data
	return &Card{
		ID:         str(d["id"]),
		MaskedPAN:  str(d["masked_pan"]),
		FullPAN:    str(d["card_pan"]),
		CVV:        str(d["cvv"]),
		Currency:   str(d["currency"]),
		Amount:     parseAmount(d["amount"]),
		Expiration: str(d["expiration"]),
		CardType:   str(d["card_type"]),
		IsActive:   d["is_active"] == true,
		Raw:        rawMap(raw),
	}, nil
}

// ParseWebhook checks the verif-hash header against the configured secret hash,
// then re-verifies the transaction through the API as Flutterwave recommends.
func (f *flutterwave) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	want := f.cfg.cred(KeyWebhookHash)
	if want == "" {
		return nil, fmt.Errorf("%w: flutterwave webhook_hash is not configured", ErrInvalidRequest)
	}
	got := r.Header.Get("verif-hash")
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return nil, fmt.Errorf("%w: flutterwave hash mismatch", ErrInvalidWebhook)
	}

	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Event string         `json:"event"`
		Data  map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: flutterwave body: %v", ErrInvalidWebhook, err)
	}

	ev := &WebhookEvent{Provider: f.Name(), Type: payload.Event}
	if payload.Event != "charge.completed" || payload.Data == nil {
		return ev.withSuccess(), nil
	}
	id := str(payload.Data["id"])
	if id == "" {
		return nil, fmt.Errorf("%w: flutterwave event without transaction id", ErrInvalidWebhook)
	}
	res, err := f.VerifyPayment(ctx, VerifyRequest{ProviderRef: id})
	if err != nil {
		return nil, err
	}
	ev.Result = res
	return ev.withSuccess(), nil
}
