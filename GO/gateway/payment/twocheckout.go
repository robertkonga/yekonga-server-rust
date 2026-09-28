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
	"sort"
	"strconv"
	"strings"
	"time"
)

// 2Checkout / Verifone (ConvertPlus hosted checkout + REST Orders API).
// Credentials: KeyMerchantCode, KeySecretKey (the REST API / IPN "Secret
// Key"), KeyBuyLinkSecret (ConvertPlus's separate "Secret Word", used only to
// sign the buy-link and validate IPNs is done with KeySecretKey instead).
//
// CreatePayment builds the buy-link locally (no HTTP call). VerifyPayment and
// Refund call the REST Orders API; 2Checkout's public docs do not enumerate
// the Order object's exact JSON field names, so the response is read
// defensively through a short list of plausible field names per value (see
// pick/pickStr) rather than a rigid struct — confirm against a live sandbox
// order before relying on this in production. ParseWebhook (the IPN) is on
// solid ground: its field names and the SIGNATURE_SHA2_256 HMAC-SHA256
// scheme are documented precisely.
const (
	twoCheckoutRESTURL    = "https://api.2checkout.com/rest/6.0"
	twoCheckoutBuyLinkURL = "https://secure.2checkout.com/checkout/buy/"
)

func init() {
	RegisterFactory(Provider2Checkout, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeyMerchantCode, KeySecretKey); err != nil {
			return nil, err
		}
		return &twoCheckout{cfg: cfg, base: firstNonEmpty(cfg.BaseURL, twoCheckoutRESTURL)}, nil
	})
}

type twoCheckout struct {
	cfg  Config
	base string
}

func (t *twoCheckout) Name() string { return Provider2Checkout }

// authHeader builds X-Avangate-Authentication: code="..." date="..." hash="..." algo="sha256".
func (t *twoCheckout) authHeader() string {
	code := t.cfg.cred(KeyMerchantCode)
	date := time.Now().UTC().Format("2006-01-02 15:04:05")
	sig := strconv.Itoa(len(code)) + code + strconv.Itoa(len(date)) + date
	mac := hmac.New(sha256.New, []byte(t.cfg.cred(KeySecretKey)))
	mac.Write([]byte(sig))
	hash := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf(`code="%s" date="%s" hash="%s" algo="sha256"`, code, date, hash)
}

func (t *twoCheckout) call(ctx context.Context, method, path string, in any, out any) ([]byte, error) {
	headers := map[string]string{"X-Avangate-Authentication": t.authHeader(), "Accept": "application/json"}
	raw, err := doJSON(ctx, t.cfg.httpClient(), t.Name(), method, t.base+path, headers, in, out)
	if err != nil {
		var pe *ProviderError
		var e struct {
			Message      string `json:"Message"`
			ErrorMessage string `json:"error_message"`
		}
		if asProviderError(err, &pe) && json.Unmarshal(raw, &e) == nil {
			if msg := firstNonEmpty(e.Message, e.ErrorMessage); msg != "" {
				pe.Message = msg
			}
		}
	}
	return raw, err
}

func (t *twoCheckout) PushUSSD(context.Context, PushRequest) (*PushResponse, error) {
	return nil, fmt.Errorf("%w: 2checkout has no mobile money push flow, use CreatePayment", ErrNotSupported)
}

// CreatePayment builds a ConvertPlus buy-link. It makes no HTTP call — the
// order is only created once the payer completes the hosted page — so the
// merchant reference (order-ext-ref) is the only handle available until the
// return redirect or the IPN reports 2Checkout's own order number (RefNo).
func (t *twoCheckout) CreatePayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	if req.ReturnURL == "" {
		return nil, fmt.Errorf("%w: 2checkout needs a return url", ErrInvalidRequest)
	}
	q := url.Values{
		"merchant":      {t.cfg.cred(KeyMerchantCode)},
		"dynamic":       {"1"},
		"prod":          {truncate(firstNonEmpty(req.Description, "Payment "+req.Reference), 128)},
		"type":          {"PRODUCT"},
		"price":         {formatAmount(req.Amount, 2)},
		"currency":      {strings.ToUpper(req.Currency)},
		"qty":           {"1"},
		"order-ext-ref": {req.Reference},
		"return-url":    {req.ReturnURL},
	}
	if t.cfg.Sandbox {
		q.Set("test", "1")
	}
	if req.Customer.Email != "" {
		q.Set("email", req.Customer.Email)
	}
	if req.Customer.Name != "" {
		q.Set("name", req.Customer.Name)
	}

	if secret := t.cfg.cred(KeyBuyLinkSecret); secret != "" {
		q.Set("signature", twoCheckoutSign(q, secret))
	}
	return &PaymentResponse{
		Provider:    t.Name(),
		Reference:   req.Reference,
		CheckoutURL: twoCheckoutBuyLinkURL + "?" + q.Encode(),
		Status:      StatusPending,
	}, nil
}

// twoCheckoutSign implements 2Checkout's documented ConvertPlus signing rule:
// sort parameter names alphabetically (the "signature" param itself is never
// included), serialize each value as its UTF-8 byte length followed by the
// value with no separators, concatenate, then HMAC-SHA256 with the Buy-Link
// Secret Word. The same rule validates the return-url's own signature.
func twoCheckoutSign(q url.Values, secret string) string {
	names := make([]string, 0, len(q))
	for k := range q {
		if k == "signature" {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)

	var buf strings.Builder
	for _, k := range names {
		v := q.Get(k)
		buf.WriteString(strconv.Itoa(len(v)))
		buf.WriteString(v)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(buf.String()))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyPayment looks an order up by 2Checkout's own RefNo when given as
// ProviderRef, or by the merchant's ExternalRefNo (order-ext-ref) otherwise.
func (t *twoCheckout) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	var path string
	switch {
	case req.ProviderRef != "":
		path = "/orders/" + url.PathEscape(req.ProviderRef) + "/"
	case req.Reference != "":
		path = "/orders/?" + url.Values{"ExternalRefNo": {req.Reference}}.Encode()
	default:
		return nil, fmt.Errorf("%w: reference or provider reference is required", ErrInvalidRequest)
	}

	var raw []byte
	var order map[string]any
	if req.ProviderRef != "" {
		raw2, err := t.call(ctx, http.MethodGet, path, nil, &order)
		if err != nil {
			return nil, err
		}
		raw = raw2
	} else {
		var list struct {
			Content []map[string]any `json:"Content"`
			Items   []map[string]any `json:"Items"`
		}
		raw2, err := t.call(ctx, http.MethodGet, path, nil, &list)
		if err != nil {
			return nil, err
		}
		raw = raw2
		items := list.Content
		if len(items) == 0 {
			items = list.Items
		}
		if len(items) == 0 {
			return &PaymentResult{Provider: t.Name(), Reference: req.Reference, Status: StatusPending, Raw: rawMap(raw)}, nil
		}
		order = items[len(items)-1] // most recent
	}

	res := &PaymentResult{
		Provider:    t.Name(),
		Reference:   firstNonEmpty(pickStr(order, "RefNoExt", "ExternalRefNo", "OrderExtRef"), req.Reference),
		ProviderRef: firstNonEmpty(pickStr(order, "RefNo", "OrderNumber"), req.ProviderRef),
		Currency:    pickStr(order, "Currency"),
		Amount:      parseAmount(pick(order, "TotalGeneral", "Total", "OrderTotal")),
		Method:      "card",
		Raw:         rawMap(raw),
	}
	res.Status, res.FailureReason = twoCheckoutStatus(pickStr(order, "Status", "OrderStatus"))
	return res, nil
}

func twoCheckoutStatus(status string) (Status, string) {
	switch strings.ToUpper(status) {
	case "COMPLETE", "COMPLETED", "APPROVED":
		return StatusSucceeded, ""
	case "REFUND", "REFUNDED":
		return StatusRefunded, ""
	case "CANCELED", "CANCELLED", "DECLINED", "DENY", "DENIED":
		return StatusFailed, strings.ToLower(status)
	}
	return StatusPending, "" // PENDING, PROCESSING, WAITING
}

func (t *twoCheckout) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if req.ProviderPaymentID == "" {
		return nil, fmt.Errorf("%w: 2checkout refunds need the order's RefNo as provider payment id", ErrInvalidRequest)
	}
	body := map[string]any{"comment": firstNonEmpty(req.Reason, "Refund"), "reason": firstNonEmpty(req.Reason, "Requested by merchant")}
	if req.Amount > 0 {
		body["amount"] = formatAmount(req.Amount, 2)
	}
	raw, err := t.call(ctx, http.MethodPost, "/orders/"+url.PathEscape(req.ProviderPaymentID)+"/refund/", body, nil)
	if err != nil {
		return nil, err
	}
	// refunds are asynchronous; MESSAGE_TYPE=REFUND_ISSUED on the IPN confirms it
	return &RefundResult{Provider: t.Name(), ProviderRefundID: req.ProviderPaymentID, Status: StatusPending, Raw: rawMap(raw)}, nil
}

// CancelPayment is not offered: the official 2Checkout PHP SDK's Order class
// exposes exactly three operations — place, getOrder, issueRefund — with
// nothing to cancel an order.
func (t *twoCheckout) CancelPayment(context.Context, CancelRequest) (*CancelResult, error) {
	return nil, fmt.Errorf("%w: 2checkout has no documented api to cancel an order", ErrNotSupported)
}

// GetRefund/ListRefunds are not offered: the same SDK source that confirms
// Refund's endpoint (POST /orders/{RefNo}/refund/) shows no matching lookup
// call — refund status arrives only through the order's own Status field or
// the IPN's REFUND_ISSUED event.
func (t *twoCheckout) GetRefund(context.Context, RefundQuery) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: 2checkout has no refund lookup api, check the order's status instead", ErrNotSupported)
}

func (t *twoCheckout) ListRefunds(context.Context, RefundQuery) (*RefundList, error) {
	return nil, fmt.Errorf("%w: 2checkout has no refund listing api", ErrNotSupported)
}

// ListPayments wraps the order search (GET /orders/?Status=&StartDate=&EndDate=&Page=&Limit=),
// whose accepted parameters are confirmed from the official PHP SDK's source.
// As with VerifyPayment's search path, the exact response field names are
// not publicly documented, so items are read defensively (pick/pickStr).
func (t *twoCheckout) ListPayments(ctx context.Context, req ListRequest) (*ListResult, error) {
	q := url.Values{}
	if req.Status != "" {
		q.Set("Status", req.Status)
	}
	if !req.From.IsZero() {
		q.Set("StartDate", req.From.Format("2006-01-02"))
	}
	if !req.To.IsZero() {
		q.Set("EndDate", req.To.Format("2006-01-02"))
	}
	if req.Page > 0 {
		q.Set("Page", strconv.Itoa(req.Page))
	}
	if req.Limit > 0 {
		q.Set("Limit", strconv.Itoa(req.Limit))
	}

	var list map[string]any
	raw, err := t.call(ctx, http.MethodGet, "/orders/?"+q.Encode(), nil, &list)
	if err != nil {
		return nil, err
	}
	rawItems, _ := pick(list, "Content", "Items", "Orders").([]any)

	items := make([]PaymentResult, 0, len(rawItems))
	for _, ri := range rawItems {
		order, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		status, reason := twoCheckoutStatus(pickStr(order, "Status", "OrderStatus"))
		items = append(items, PaymentResult{
			Provider:      t.Name(),
			Reference:     pickStr(order, "RefNoExt", "ExternalRefNo", "OrderExtRef"),
			ProviderRef:   pickStr(order, "RefNo", "OrderNumber"),
			Currency:      pickStr(order, "Currency"),
			Amount:        parseAmount(pick(order, "TotalGeneral", "Total", "OrderTotal")),
			Method:        "card",
			Status:        status,
			FailureReason: reason,
			Raw:           order,
		})
	}
	return &ListResult{
		Provider:   t.Name(),
		Items:      items,
		Page:       req.Page,
		TotalPages: int(parseAmount(pick(list, "TotalPages"))),
		Total:      int(parseAmount(pick(list, "TotalCount", "Count"))),
		Raw:        rawMap(raw),
	}, nil
}

// ParseWebhook verifies the IPN's SIGNATURE_SHA2_256 field: HMAC-SHA256, keyed
// with the account Secret Key, over every posted field's value (each
// prefixed with its UTF-8 byte length) concatenated in the order the fields
// were received — not sorted. Go's r.PostForm loses that order, so the raw
// body is walked by hand. (2Checkout also offers a SHA3-256 signature; it is
// not verified here since that needs a non-stdlib hash implementation.)
func (t *twoCheckout) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	fields, err := parseOrderedForm(string(body))
	if err != nil {
		return nil, fmt.Errorf("%w: 2checkout ipn body: %v", ErrInvalidWebhook, err)
	}

	// last value wins on a duplicate key, matching net/http's own form parsing.
	form := make(map[string]string, len(fields))
	var got string
	var buf strings.Builder
	for _, f := range fields {
		if f.key == "SIGNATURE_SHA2_256" {
			got = f.value
			continue
		}
		if f.key == "SIGNATURE_SHA3_256" || f.key == "HASH" {
			continue
		}
		form[f.key] = f.value
		buf.WriteString(strconv.Itoa(len(f.value)))
		buf.WriteString(f.value)
	}
	if got == "" {
		return nil, fmt.Errorf("%w: 2checkout ipn is missing SIGNATURE_SHA2_256", ErrInvalidWebhook)
	}
	mac := hmac.New(sha256.New, []byte(t.cfg.cred(KeySecretKey)))
	mac.Write([]byte(buf.String()))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(got)), []byte(want)) {
		return nil, fmt.Errorf("%w: 2checkout ipn signature mismatch", ErrInvalidWebhook)
	}

	messageType := form["MESSAGE_TYPE"]
	ev := &WebhookEvent{Provider: t.Name(), Type: messageType}
	if refNo := form["REFNO"]; refNo != "" {
		status, reason := twoCheckoutIPNStatus(messageType, form["STATUS"])
		raw := make(map[string]any, len(form))
		for k, v := range form {
			raw[k] = v
		}
		ev.Result = &PaymentResult{
			Provider:      t.Name(),
			Reference:     form["REFNOEXT"],
			ProviderRef:   refNo,
			Currency:      form["CURRENCY"],
			Amount:        parseAmount(form["IPN_TOTALGENERAL"]),
			Method:        "card",
			Status:        status,
			FailureReason: reason,
			Raw:           raw,
		}
	}
	return ev.withSuccess(), nil
}

func twoCheckoutIPNStatus(messageType, status string) (Status, string) {
	switch strings.ToUpper(messageType) {
	case "REFUND_ISSUED":
		return StatusRefunded, ""
	case "FRAUD_STATUS_CHANGED":
		if strings.EqualFold(status, "pass") {
			return StatusPending, ""
		}
		return StatusFailed, "fraud review: " + status
	}
	return twoCheckoutStatus(firstNonEmpty(status, messageType))
}

type orderedField struct{ key, value string }

// parseOrderedForm splits a raw application/x-www-form-urlencoded body into
// key/value pairs, preserving the order they appear in — url.ParseQuery loses
// that order by returning a map.
func parseOrderedForm(body string) ([]orderedField, error) {
	var fields []orderedField
	for _, pair := range strings.Split(body, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		k, err := url.QueryUnescape(kv[0])
		if err != nil {
			return nil, err
		}
		var v string
		if len(kv) == 2 {
			v, err = url.QueryUnescape(kv[1])
			if err != nil {
				return nil, err
			}
		}
		fields = append(fields, orderedField{key: k, value: v})
	}
	return fields, nil
}
