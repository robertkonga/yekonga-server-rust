package yekonga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/gateway"
	"github.com/robertkonga/yekonga-server-go/gateway/payment"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

const (
	PaymentModelName                = "Payment"
	PaymentProviderRequestModelName = "PaymentProviderRequest"
	defaultPaymentWebhookRoute      = "/payment/webhook"
	paymentWebhookMaxBodyBytes      = 1 << 20
	paymentAmountMatchTolerance     = 0.01
	paymentStatusPending            = string(payment.StatusPending)
	paymentStatusSucceeded          = string(payment.StatusSucceeded)
	paymentStatusRefunded           = string(payment.StatusRefunded)
	paymentStatusFailed             = string(payment.StatusFailed)
	paymentWebhookProviderParam     = "provider"
	paymentMetadataWebhookKey       = "lastWebhook"
	paymentMetadataOrderRefKey      = "orderReference"
)

// paymentWebhookRoute is the route prefix provider notifications are served
// under, as <route>/:provider.
func (y *YekongaData) paymentWebhookRoute() string {
	route := strings.TrimSpace(y.Config.ApiGateway.Payment.WebhookRoute)
	if route == "" {
		route = defaultPaymentWebhookRoute
	}

	return "/" + strings.Trim(route, "/")
}

// paymentEnabled reports whether payments are in use: default providers are
// configured, or the Payment models are on so tenants can bring their own.
func (y *YekongaData) paymentEnabled() bool {
	return len(y.Config.ApiGateway.Payment.Providers) > 0 ||
		y.Config.HasPaymentModule ||
		(y.Config.HasTenant && y.Config.HasTenantBilling)
}

// isPaymentWebhookPath reports whether path is a provider notification
// request. Always false when payments aren't in use.
func (y *YekongaData) isPaymentWebhookPath(path string) bool {
	if !y.paymentEnabled() {
		return false
	}

	return strings.HasPrefix(path, y.AppendBaseUrl(y.paymentWebhookRoute())+"/")
}

// PaymentController returns the payment gateways configured in
// config.apiGateway.payment.providers, built once on first use. Use it for
// calls CreatePayment doesn't cover (refunds, verification, USSD push), or
// call Use on it to install a custom provider.
func (y *YekongaData) PaymentController() (*payment.Controller, error) {
	y.paymentControllerOnce.Do(func() {
		ctrl, _ := payment.NewController()

		for _, p := range y.Config.ApiGateway.Payment.Providers {
			provider, err := gateway.NewPaymentProvider(paymentConfigFrom(p))
			if err != nil {
				y.paymentControllerErr = fmt.Errorf("payment provider %q: %w", p.Provider, err)
				return
			}

			ctrl.Use(provider)
		}

		y.paymentController = ctrl
	})

	return y.paymentController, y.paymentControllerErr
}

// CreatePayment records a pending Payment, starts a hosted checkout with the
// given provider and saves the provider's response (providerReference,
// checkoutUrl, status) on the record. The call itself — request, raw
// response or error, and duration — is logged to PaymentProviderRequest. The Payment id is always
// used as the provider reference so webhooks can find the record; a
// caller-supplied Reference is kept in metadata.orderReference.
//
// req may be nil; when set, the record is scoped to the request's tenant and
// the webhook URL defaults to this server's host. When that tenant has its
// own config for provider (TenantConfig.payment), it's used instead of the
// server default and the webhook URL gets the tenant id appended. On a
// provider error the record is marked failed and returned together with the
// error.
func (y *YekongaData) CreatePayment(req *Request, provider string, input payment.PaymentRequest) (*datatype.DataMap, *payment.PaymentResponse, error) {
	tenantId := y.requestTenantID(req)

	gateways, err := y.paymentGatewaysFor(tenantId)
	if err != nil {
		return nil, nil, err
	}

	if _, err := gateways.ctrl.Get(provider); err != nil {
		return nil, nil, err
	}

	providerConfig := paymentConfigDefault
	webhookTenantId := ""
	if gateways.isTenant(provider) {
		providerConfig = paymentConfigTenant
		webhookTenantId = tenantId
	}

	if y.Model(PaymentModelName) == nil {
		return nil, nil, fmt.Errorf("%s model is not available: enable hasPaymentModule (or hasTenant with hasTenantBilling)", PaymentModelName)
	}

	metadata := datatype.DataMap{
		"description": input.Description,
		"customer": datatype.DataMap{
			"name":        input.Customer.Name,
			"email":       input.Customer.Email,
			"phone":       input.Customer.Phone,
			"countryCode": input.Customer.CountryCode,
		},
	}
	if input.Reference != "" {
		metadata[paymentMetadataOrderRefKey] = input.Reference
	}
	if len(input.Metadata) > 0 {
		metadata["metadata"] = input.Metadata
	}

	now := time.Now()
	created := y.modelQueryFor(req, PaymentModelName).Create(datatype.DataMap{
		"provider":       strings.ToLower(strings.TrimSpace(provider)),
		"providerConfig": providerConfig,
		"amount":         input.Amount,
		"currency":       strings.ToUpper(input.Currency),

		"method":        input.Method,
		"channel":       input.Channel,
		"paymentMethod": input.PaymentMethod,
		"txnAmount":     input.Amount,
		"txnPhone":      input.Customer.Phone,

		"status":    paymentStatusPending,
		"metadata":  metadata,
		"isPaid":    false,
		"createdAt": now,
		"updatedAt": now,
	})

	record, err := toPaymentRecord(created)
	if err != nil {
		return nil, nil, err
	}

	id := paymentRecordID(*record)
	if id == "" {
		return record, nil, errors.New("payment record was created without an id")
	}

	input.Reference = id
	if cfg := y.providerConfig(gateways, provider); input.WebhookURL == "" && (cfg == nil || strings.TrimSpace(cfg.WebhookURL) == "") && req != nil {
		input.WebhookURL = y.paymentWebhookURL(req, provider, webhookTenantId)
	}

	ctx := context.Background()
	if req != nil && req.HttpRequest != nil {
		ctx = req.HttpRequest.Context()
	}

	startedAt := time.Now()
	response, providerErr := gateways.ctrl.CreatePayment(ctx, provider, input)
	y.recordPaymentProviderRequest(req, provider, id, "createPayment", startedAt, paymentRequestData(input), response, providerErr)

	update := datatype.DataMap{"updatedAt": time.Now()}

	if providerErr != nil {
		update["status"] = paymentStatusFailed
		update["failureReason"] = providerErr.Error()
	} else {
		update["status"] = string(response.Status)
		update["providerReference"] = response.ProviderRef
		update["checkoutUrl"] = response.CheckoutURL
	}

	if helper.IsNotEmpty(response) {
		update["transactionData"] = response.Raw
	}

	if updated, err := toPaymentRecord(y.modelQueryFor(req, PaymentModelName).Where("id", id).Update(update, nil)); err == nil {
		record = updated
	} else {
		logger.Error("Failed to save payment response", id, err.Error())
	}

	return record, response, providerErr
}

func (y *YekongaData) PushUSSD(req *Request, provider string, input payment.PushRequest) (*payment.PushResponse, error) {
	tenantId := y.requestTenantID(req)
	ctx := req.HttpRequest.Context()

	gateways, err := y.paymentGatewaysFor(tenantId)

	if err != nil {
		return nil, err
	}

	if _, err := gateways.ctrl.Get(provider); err != nil {
		return nil, err
	}

	pushRes, err := gateways.ctrl.PushUSSD(ctx, provider, input)

	if err != nil {
		y.modelQueryFor(req, PaymentModelName).SetRequest(req, nil).Update(datatype.DataMap{
			"status":        paymentStatusFailed,
			"failureReason": err.Error(),
			"message":       err.Error(),
			"updatedAt":     time.Now(),
		}, datatype.DataMap{"id": input.Reference})
	}

	return pushRes, nil
}

// initializePaymentRoutes serves provider notifications at
// <webhookRoute>/:provider for the server's own gateway accounts, and at
// <webhookRoute>/:provider/:tenantId for tenants using their own.
func (y *YekongaData) initializePaymentRoutes() {
	if !y.paymentEnabled() {
		return
	}

	route := y.paymentWebhookRoute()
	path := route + "/:" + paymentWebhookProviderParam
	tenantPath := path + "/:" + paymentWebhookTenantParam

	for _, p := range []string{path, tenantPath} {
		y.Get(p, y.paymentWebhookHandler)
		y.Post(p, y.paymentWebhookHandler)
	}
	y.SetPublicRoute(route + "/*")
	y.SetPublicRoute(route + "/*/*")

	logger.Info("Payment webhook", y.AppendBaseUrl(path))
}

// paymentWebhookHandler authenticates a provider notification and applies
// the payment state it carries to the matching Payment record. Gateways
// deliver more than once, so applying the same event twice is a no-op.
func (y *YekongaData) paymentWebhookHandler(req *Request, res *Response) {
	provider := req.Param(paymentWebhookProviderParam)
	tenantId := req.Param(paymentWebhookTenantParam)

	gateways, err := y.paymentGatewaysFor(tenantId)
	if err != nil {
		logger.Error("Payment webhook", provider, tenantId, err.Error())
		y.writePaymentWebhookError(req, res, http.StatusInternalServerError)
		return
	}

	// A tenant URL is only valid for a provider the tenant configures itself;
	// otherwise the signature would be checked with the server's secrets.
	if tenantId != "" && !gateways.isTenant(provider) {
		y.writePaymentWebhookError(req, res, http.StatusNotFound)
		return
	}

	event, err := gateways.ctrl.ParseWebhook(req.HttpRequest.Context(), provider, req.HttpRequest)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, payment.ErrInvalidWebhook):
			status = http.StatusUnauthorized
		case errors.Is(err, payment.ErrUnknownProvider):
			status = http.StatusNotFound
		case errors.Is(err, payment.ErrInvalidRequest):
			status = http.StatusBadRequest
		}

		logger.Error("Payment webhook rejected", provider, err.Error())
		y.writePaymentWebhookError(req, res, status)
		return
	}

	var record *datatype.DataMap
	if event.Result != nil {
		if record, err = y.applyPaymentResult(req, res, event, tenantId); err != nil {
			// 500 makes the gateway retry later.
			logger.Error("Payment webhook failed", provider, err.Error())
			y.writePaymentWebhookError(req, res, http.StatusInternalServerError)
			return
		}
	}

	if err := y.PaymentWebhook(&PaymentWebhookData{Event: event, Payment: record, Request: req}); err != nil {
		logger.Error("Payment webhook function failed", provider, err.Error())
		y.writePaymentWebhookError(req, res, http.StatusInternalServerError)
		return
	}

	if event.Ack == nil {
		res.Text("")
		return
	}

	res.Json(event.Ack)
}

func (y *YekongaData) writePaymentWebhookError(req *Request, res *Response, status int) {
	y.recordErrorResponse(req, status)
	res.Status(status)
	res.Text(http.StatusText(status))
}

// applyPaymentResult updates the Payment record an authenticated webhook
// refers to and returns it (nil when no record matches). A payment never
// moves back from succeeded/refunded, and a success whose amount or currency
// differs from the record is saved as failed instead — and reported that way
// on the event — so a tampered or partial payment can't settle an invoice.
//
// tenantId is the tenant whose credentials authenticated the webhook ("" for
// the server's). Only payments made on those same credentials match, so one
// tenant can't sign a webhook that settles another tenant's payment.
func (y *YekongaData) applyPaymentResult(req *Request, res *Response, event *payment.WebhookEvent, tenantId string) (*datatype.DataMap, error) {
	result := event.Result
	provider := strings.ToLower(event.Provider)

	record := y.findPaymentForResult(provider, result)
	if record != nil && !paymentMadeWith(*record, tenantId) {
		record = nil
	}
	if record == nil {
		// Retrying won't make the record appear, so acknowledge the event.
		logger.Error("Payment webhook: no payment matches", provider, result.Reference, result.ProviderRef)
		return nil, nil
	}

	id := paymentRecordID(*record)
	current := helper.GetValueOfString(*record, "status")
	next := string(result.Status)

	if current == paymentStatusRefunded ||
		(current == paymentStatusSucceeded && next != paymentStatusRefunded) {
		return record, nil
	}

	if next == paymentStatusSucceeded {
		if reason := paymentMismatch(*record, result); reason != "" {
			next = paymentStatusFailed
			result.Status = payment.StatusFailed
			result.FailureReason = reason
			event.Success = false
		}
	}

	if next == current &&
		(result.ProviderPaymentID == "" || result.ProviderPaymentID == helper.GetValueOfString(*record, "providerPaymentId")) {
		return record, nil
	}

	metadata := paymentMetadata(*record)
	metadata[paymentMetadataWebhookKey] = datatype.DataMap{
		"type":       event.Type,
		"method":     result.Method,
		"amount":     result.Amount,
		"currency":   result.Currency,
		"receivedAt": time.Now(),
		"raw":        result.Raw,
	}

	now := time.Now()
	update := datatype.DataMap{
		"status":    next,
		"metadata":  metadata,
		"updatedAt": now,
	}

	if result.ProviderPaymentID != "" {
		update["providerPaymentId"] = result.ProviderPaymentID
	}
	if result.ProviderRef != "" && helper.GetValueOfString(*record, "providerReference") == "" {
		update["providerReference"] = result.ProviderRef
	}
	if method := normalizePaymentMethod(result.Method); method != "" {
		update["paymentMethod"] = method
	}
	if result.FailureReason != "" {
		update["failureReason"] = result.FailureReason
	}

	switch next {
	case paymentStatusSucceeded:
		update["paidAt"] = now
	case paymentStatusRefunded:
		update["refundedAt"] = now
	}

	updated := y.ModelQuery(PaymentModelName).SetRequest(req, res).SkipTenant().Where("id", id).Update(update, nil)

	return toPaymentRecord(updated)
}

// findPaymentForResult looks the record up by our reference (the Payment id)
// first and the gateway's order id second, always within the same provider
// so one gateway's webhook can't touch another gateway's payments.
func (y *YekongaData) findPaymentForResult(provider string, result *payment.PaymentResult) *datatype.DataMap {
	if result.Reference != "" {
		record := y.ModelQuery(PaymentModelName).SkipTenant().SkipBeforeCommit().
			Where("id", result.Reference).Where("provider", provider).FindOne(nil)
		if helper.IsNotEmpty(record) {
			return record
		}
	}

	if result.ProviderRef != "" {
		record := y.ModelQuery(PaymentModelName).SkipTenant().SkipBeforeCommit().
			Where("providerReference", result.ProviderRef).Where("provider", provider).FindOne(nil)
		if helper.IsNotEmpty(record) {
			return record
		}
	}

	return nil
}

func (y *YekongaData) modelQueryFor(req *Request, modelName string) *DataModelQuery {
	query := y.ModelQuery(modelName)
	if req != nil {
		query.SetRequest(req, nil)
	}

	return query
}

// recordPaymentProviderRequest logs one call to a payment gateway. A failure
// to log never fails the payment itself, and it's skipped when the
// PaymentProviderRequest model isn't part of the schema.
func (y *YekongaData) recordPaymentProviderRequest(req *Request, provider, paymentId, action string, startedAt time.Time, request datatype.DataMap, response *payment.PaymentResponse, providerErr error) {
	if _, ok := y.models[PaymentProviderRequestModelName]; !ok {
		return
	}

	data := datatype.DataMap{
		"provider":   strings.ToLower(strings.TrimSpace(provider)),
		"paymentId":  paymentId,
		"action":     action,
		"reference":  request["reference"],
		"request":    request,
		"durationMs": int(time.Since(startedAt).Milliseconds()),
		"createdAt":  startedAt,
	}

	if providerErr != nil {
		data["status"] = "failed"
		data["errorMessage"] = providerErr.Error()

		var pe *payment.ProviderError
		if errors.As(providerErr, &pe) {
			data["errorCode"] = pe.Code
			if pe.HTTPStatus != 0 {
				data["httpStatus"] = pe.HTTPStatus
			}
		}
	} else if response != nil {
		data["status"] = "succeeded"
		data["providerReference"] = response.ProviderRef
		data["response"] = datatype.DataMap{
			"status":      string(response.Status),
			"providerRef": response.ProviderRef,
			"checkoutUrl": response.CheckoutURL,
			"raw":         response.Raw,
		}
	}

	if _, err := toPaymentRecord(y.modelQueryFor(req, PaymentProviderRequestModelName).Create(data)); err != nil {
		logger.Error("Failed to log payment provider request", paymentId, err.Error())
	}
}

// paymentRequestData is what was sent to the gateway, for the request log.
func paymentRequestData(in payment.PaymentRequest) datatype.DataMap {
	return datatype.DataMap{
		"reference":   in.Reference,
		"amount":      in.Amount,
		"currency":    in.Currency,
		"description": in.Description,
		"customer": datatype.DataMap{
			"name":        in.Customer.Name,
			"email":       in.Customer.Email,
			"phone":       in.Customer.Phone,
			"countryCode": in.Customer.CountryCode,
		},
		"returnUrl":  in.ReturnURL,
		"cancelUrl":  in.CancelURL,
		"webhookUrl": in.WebhookURL,
		"metadata":   in.Metadata,
	}
}

// paymentMadeWith reports whether the payment was made on tenantId's own
// gateway account, or on the server's when tenantId is "".
func paymentMadeWith(record datatype.DataMap, tenantId string) bool {
	madeWithTenant := helper.GetValueOfString(record, "providerConfig") == paymentConfigTenant

	if tenantId == "" {
		return !madeWithTenant
	}

	return madeWithTenant && idString(record["tenantId"]) == tenantId
}

// paymentWebhookURL builds this server's notification URL for provider from
// the host the current request came in on, with tenantId appended when the
// payment runs on that tenant's own gateway account.
func (y *YekongaData) paymentWebhookURL(req *Request, provider, tenantId string) string {
	// No client info on requests that didn't come through ServeHTTP.
	if req.Client() == nil {
		return ""
	}

	client := *req.Client()
	port := client.Port

	if helper.IsEmpty(port) || port == "80" || port == "443" {
		port = ""
	} else {
		port = ":" + port
	}

	path := y.paymentWebhookRoute() + "/" + strings.ToLower(strings.TrimSpace(provider))
	if tenantId != "" {
		path += "/" + tenantId
	}

	return client.Proto + "://" + client.Host + port + y.AppendBaseUrl(path)
}

// paymentMismatch explains why a reported success doesn't match the record,
// or returns "" when it does. Values the gateway didn't report are skipped.
func paymentMismatch(record datatype.DataMap, result *payment.PaymentResult) string {
	expectedAmount := helper.ToFloat(record["amount"])
	expectedCurrency := helper.GetValueOfString(record, "currency")

	if result.Amount > 0 && math.Abs(result.Amount-expectedAmount) > paymentAmountMatchTolerance {
		return fmt.Sprintf("amount mismatch: expected %.2f, provider reported %.2f", expectedAmount, result.Amount)
	}

	if result.Currency != "" && expectedCurrency != "" && !strings.EqualFold(result.Currency, expectedCurrency) {
		return fmt.Sprintf("currency mismatch: expected %s, provider reported %s", expectedCurrency, result.Currency)
	}

	return ""
}

// normalizePaymentMethod maps a gateway's channel name onto the Payments
// paymentMethod options, or "" when it doesn't fit one.
func normalizePaymentMethod(method string) string {
	m := strings.ToLower(method)

	switch {
	case m == "":
		return ""
	case strings.Contains(m, "paypal"):
		return "paypal"
	case strings.Contains(m, "crypto"):
		return "crypto"
	case strings.Contains(m, "card"), strings.Contains(m, "visa"), strings.Contains(m, "mastercard"):
		return "card"
	case strings.Contains(m, "mobile"), strings.Contains(m, "momo"), strings.Contains(m, "mpesa"),
		strings.Contains(m, "m-pesa"), strings.Contains(m, "ussd"), strings.Contains(m, "airtel"),
		strings.Contains(m, "tigo"), strings.Contains(m, "halo"):
		return "mobile_money"
	case strings.Contains(m, "bank"), strings.Contains(m, "transfer"):
		return "bank_transfer"
	}

	return ""
}

// paymentMetadata returns the record's metadata as a map. "Any" fields can
// come back as a JSON string or a bson.D depending on the backend.
func paymentMetadata(record datatype.DataMap) datatype.DataMap {
	metadata := datatype.DataMap{}

	switch v := record["metadata"].(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &metadata)
	case bson.D:
		for _, e := range v {
			metadata[e.Key] = e.Value
		}
	default:
		if helper.IsMap(v) {
			metadata = helper.ToDataMap(v)
		}
	}

	return metadata
}

func toPaymentRecord(value interface{}) (*datatype.DataMap, error) {
	switch v := value.(type) {
	case *datatype.DataMap:
		if v != nil {
			return v, nil
		}
	case error:
		return nil, v
	}

	return nil, errors.New("payment record was not saved")
}

func paymentRecordID(record datatype.DataMap) string {
	for _, key := range []string{"id", "_id"} {
		if id := helper.GetValueOfString(record, key); id != "" {
			return id
		}

		// SQL backends use numeric ids.
		if v := helper.GetMapValue(record, key); helper.IsNotEmpty(v) {
			return fmt.Sprint(v)
		}
	}

	return ""
}
