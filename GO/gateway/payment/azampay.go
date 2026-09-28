package payment

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AzamPay MNO Checkout (Tanzania/Uganda mobile money push).
// Credentials: KeyAppName, KeyClientID, KeyClientSecret; KeyAPIKey (X-API-Key,
// sandbox only — AzamPay's docs say it is not needed in production, but it is
// sent whenever configured since that is harmless there); KeyPublicKey (PEM,
// needed to verify callback signatures).
//
// AzamPay's product here is a direct mobile-money push, not a hosted
// checkout redirect, so CreatePayment is not supported — use PushUSSD.
//
// The production base URLs below are inferred from the sandbox ones by
// AzamPay's own naming convention (documented sandbox hosts drop the
// "-sandbox"/"sandbox." prefix); they are not independently confirmed, so
// override them with Config.BaseURL if they turn out to be wrong.
const (
	azampaySandboxAuthURL = "https://authenticator-sandbox.azampay.co.tz"
	azampaySandboxAPIURL  = "https://sandbox.azampay.co.tz"
	azampayLiveAuthURL    = "https://authenticator.azampay.co.tz"
	azampayLiveAPIURL     = "https://checkout.azampay.co.tz"
)

// azampayNetworks maps a case-insensitive network name to the exact casing
// AzamPay's "provider" field expects.
var azampayNetworks = map[string]string{
	"airtel": "Airtel", "tigo": "Tigo", "halopesa": "Halopesa",
	"azampesa": "Azampesa", "azampay": "Azampesa", "mpesa": "Mpesa",
}

func init() {
	RegisterFactory(ProviderAzamPay, func(cfg Config) (Provider, error) {
		if err := cfg.require(KeyAppName, KeyClientID, KeyClientSecret); err != nil {
			return nil, err
		}
		authURL, apiURL := azampayLiveAuthURL, azampayLiveAPIURL
		if cfg.Sandbox {
			authURL, apiURL = azampaySandboxAuthURL, azampaySandboxAPIURL
		}
		if cfg.BaseURL != "" {
			// A single override stands in for both hosts (a proxy or a test
			// double); production splits them across two real hosts.
			authURL = strings.TrimRight(cfg.BaseURL, "/")
			apiURL = strings.TrimRight(cfg.BaseURL, "/")
		}
		return &azampay{cfg: cfg, authURL: authURL, apiURL: apiURL}, nil
	})
}

type azampay struct {
	cfg     Config
	authURL string
	apiURL  string

	mu        sync.Mutex
	token     string
	tokenExpr time.Time
}

func (a *azampay) Name() string { return ProviderAzamPay }

func (a *azampay) accessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.tokenExpr) {
		return a.token, nil
	}

	headers := map[string]string{}
	if key := a.cfg.cred(KeyAPIKey); key != "" {
		headers["X-API-Key"] = key
	}
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			AccessToken string `json:"accessToken"`
			Expire      string `json:"expire"`
		} `json:"data"`
	}
	_, err := doJSON(ctx, a.cfg.httpClient(), a.Name(), http.MethodPost, a.authURL+"/AppRegistration/GenerateToken", headers,
		map[string]string{"appName": a.cfg.cred(KeyAppName), "clientId": a.cfg.cred(KeyClientID), "clientSecret": a.cfg.cred(KeyClientSecret)}, &out)
	if err != nil {
		return "", err
	}
	if !out.Success || out.Data.AccessToken == "" {
		return "", &ProviderError{Provider: a.Name(), Message: firstNonEmpty(out.Message, "no access token returned")}
	}
	a.token = out.Data.AccessToken
	a.tokenExpr = time.Now().Add(50 * time.Minute)
	if exp, err := time.Parse(time.RFC3339, out.Data.Expire); err == nil && time.Until(exp) < 50*time.Minute {
		a.tokenExpr = exp.Add(-30 * time.Second)
	}
	return a.token, nil
}

func (a *azampay) call(ctx context.Context, method, path string, in any, out any) ([]byte, error) {
	tok, err := a.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "Bearer " + tok}
	if key := a.cfg.cred(KeyAPIKey); key != "" {
		headers["X-API-Key"] = key
	}
	raw, err := doJSON(ctx, a.cfg.httpClient(), a.Name(), method, a.apiURL+path, headers, in, out)
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

// CreatePayment is not offered: AzamPay's checkout product is a direct
// mobile-money push, not a hosted redirect.
func (a *azampay) CreatePayment(context.Context, PaymentRequest) (*PaymentResponse, error) {
	return nil, fmt.Errorf("%w: azampay has no hosted checkout, use PushUSSD", ErrNotSupported)
}

func (a *azampay) PushUSSD(ctx context.Context, req PushRequest) (*PushResponse, error) {
	network, ok := azampayNetworks[strings.ToLower(req.Network)]
	if !ok {
		return nil, fmt.Errorf("%w: azampay needs a network (one of airtel, tigo, halopesa, azampesa, mpesa)", ErrInvalidRequest)
	}
	body := map[string]any{
		"accountNumber": strings.NewReplacer("+", "", " ", "", "-", "").Replace(req.Phone),
		"amount":        req.Amount,
		"currency":      strings.ToUpper(req.Currency),
		"externalId":    req.Reference,
		"provider":      network,
	}
	if len(req.Metadata) > 0 {
		body["additionalProperties"] = req.Metadata
	}

	var out struct {
		Success       bool   `json:"success"`
		TransactionID string `json:"transactionId"`
		Message       string `json:"message"`
	}
	raw, err := a.call(ctx, http.MethodPost, "/azampay/mno/checkout", body, &out)
	if err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, &ProviderError{Provider: a.Name(), Message: firstNonEmpty(out.Message, "push request rejected")}
	}
	return &PushResponse{
		Provider:     a.Name(),
		Reference:    req.Reference,
		ProviderRef:  out.TransactionID,
		Status:       StatusPending,
		Instructions: firstNonEmpty(out.Message, "Enter your mobile money PIN on your phone to complete the payment."),
		Raw:          rawMap(raw),
	}, nil
}

// VerifyPayment is best-effort: AzamPay's own SDKs describe this status
// endpoint in terms of disbursement (payout) lookups rather than collection
// pushes, so its behaviour for a checkout reference is not fully confirmed.
// The callback signed with KeyPublicKey (see ParseWebhook) is the
// authoritative source of truth; prefer waiting for it over polling this.
func (a *azampay) VerifyPayment(ctx context.Context, req VerifyRequest) (*PaymentResult, error) {
	if req.ProviderRef == "" {
		return nil, fmt.Errorf("%w: azampay looks payments up by transaction id (provider reference)", ErrInvalidRequest)
	}
	var out struct {
		Success bool   `json:"success"`
		Data    string `json:"data"`
		Message string `json:"message"`
	}
	raw, err := a.call(ctx, http.MethodGet, "/azampay/gettransactionstatus?"+url.Values{
		"pgReferenceId": {req.ProviderRef},
	}.Encode(), nil, &out)
	if err != nil {
		return nil, err
	}
	res := &PaymentResult{Provider: a.Name(), ProviderRef: req.ProviderRef, Reference: req.Reference, Raw: rawMap(raw)}
	if !out.Success {
		res.Status, res.FailureReason = StatusFailed, firstNonEmpty(out.Message, "failed")
		return res, nil
	}
	res.Status, res.FailureReason = azampayStatus(out.Data)
	return res, nil
}

func azampayStatus(status string) (Status, string) {
	switch strings.ToUpper(status) {
	case "SUCCESS", "SUCCESSFUL", "COMPLETED":
		return StatusSucceeded, ""
	case "FAILED", "FAILURE", "CANCELLED":
		return StatusFailed, strings.ToLower(status)
	}
	return StatusPending, ""
}

// Refund is not documented for AzamPay's collection API.
func (a *azampay) Refund(context.Context, RefundRequest) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: azampay has no documented collection refund api", ErrNotSupported)
}

// GetRefund is not documented for AzamPay's collection API.
func (a *azampay) GetRefund(context.Context, RefundQuery) (*RefundResult, error) {
	return nil, fmt.Errorf("%w: azampay has no documented refund api", ErrNotSupported)
}

// ListRefunds is not documented for AzamPay's collection API.
func (a *azampay) ListRefunds(context.Context, RefundQuery) (*RefundList, error) {
	return nil, fmt.Errorf("%w: azampay has no documented refund api", ErrNotSupported)
}

// CancelPayment is not documented for AzamPay's collection API.
func (a *azampay) CancelPayment(context.Context, CancelRequest) (*CancelResult, error) {
	return nil, fmt.Errorf("%w: azampay has no documented api to cancel a push", ErrNotSupported)
}

// ListPayments is not documented for AzamPay's collection API.
func (a *azampay) ListPayments(context.Context, ListRequest) (*ListResult, error) {
	return nil, fmt.Errorf("%w: azampay has no documented transaction listing api", ErrNotSupported)
}

// ParseWebhook verifies the RSA-PKCS1v15/SHA256 signature AzamPay attaches to
// its checkout callback (over utilityref+externalreference+transactionstatus
// +operator), using the PEM certificate AzamPay issues per merchant.
func (a *azampay) ParseWebhook(ctx context.Context, r *http.Request) (*WebhookEvent, error) {
	pubPEM := a.cfg.cred(KeyPublicKey)
	if pubPEM == "" {
		return nil, fmt.Errorf("%w: azampay public_key is not configured", ErrInvalidRequest)
	}
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	var payload struct {
		TransactionStatus string `json:"transactionstatus"`
		ExternalReference string `json:"externalreference"`
		UtilityRef        string `json:"utilityref"`
		Operator          string `json:"operator"`
		Amount            string `json:"amount"`
		MNOReference      string `json:"mnoreference"`
		MSISDN            string `json:"msisdn"`
		TransID           string `json:"transid"`
		Signature         string `json:"signature"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: azampay body: %v", ErrInvalidWebhook, err)
	}
	signed := payload.UtilityRef + payload.ExternalReference + payload.TransactionStatus + payload.Operator
	if err := verifyAzamPaySignature(pubPEM, signed, payload.Signature); err != nil {
		return nil, err
	}

	status, reason := azampayStatus(payload.TransactionStatus)
	res := &PaymentResult{
		Provider:          a.Name(),
		Reference:         payload.ExternalReference,
		ProviderRef:       firstNonEmpty(payload.TransID, payload.MNOReference, payload.UtilityRef),
		ProviderPaymentID: firstNonEmpty(payload.TransID, payload.MNOReference),
		Status:            status,
		Amount:            parseAmount(payload.Amount),
		Method:            payload.Operator,
		FailureReason:     reason,
		Raw:               rawMap(body),
	}
	return (&WebhookEvent{Provider: a.Name(), Type: "mno.checkout", Result: res}).withSuccess(), nil
}

func verifyAzamPaySignature(pubPEM, signed, signatureB64 string) error {
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		return fmt.Errorf("%w: azampay public_key is not valid PEM", ErrInvalidRequest)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		if cert, cerr := x509.ParseCertificate(block.Bytes); cerr == nil {
			pub = cert.PublicKey
		} else {
			return fmt.Errorf("%w: azampay public_key could not be parsed: %v", ErrInvalidRequest, err)
		}
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: azampay public_key is not an RSA key", ErrInvalidRequest)
	}
	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("%w: azampay signature is not valid base64", ErrInvalidWebhook)
	}
	sum := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("%w: azampay signature verification failed", ErrInvalidWebhook)
	}
	return nil
}
