package payment

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestController(t *testing.T, cfg Config) *Controller {
	t.Helper()
	c, err := NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func jsonReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestControllerRouting(t *testing.T) {
	c, err := NewController(
		Config{Provider: "Flutterwave", Credentials: map[string]string{KeySecretKey: "k"}},
		Config{Provider: "selcom", Credentials: map[string]string{KeyAPIKey: "a", KeyAPISecret: "b", KeyVendor: "v"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Providers(), ","); got != "flutterwave,selcom" {
		t.Fatalf("providers = %s", got)
	}
	if _, err := c.Get("stripe"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("want ErrUnknownProvider, got %v", err)
	}
	if _, err := New(Config{Provider: "paypal"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing credentials must be rejected, got %v", err)
	}
	if _, err := c.CreatePayment(context.Background(), "flutterwave", PaymentRequest{Amount: 1, Currency: "USD"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing reference must be rejected, got %v", err)
	}
	if _, err := c.Refund(context.Background(), "selcom", RefundRequest{ProviderPaymentID: "x"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("selcom refund: want ErrNotSupported, got %v", err)
	}
	if _, err := c.PushUSSD(context.Background(), "flutterwave", PushRequest{Reference: "r", Amount: 1, Currency: "TZS"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing phone must be rejected, got %v", err)
	}
}

func TestPushUSSDNotSupported(t *testing.T) {
	ctx := context.Background()
	push := PushRequest{Reference: "pay_1", Amount: 100, Currency: "USD", Phone: "+255682555555"}

	pp := newTestController(t, Config{Provider: "paypal", Credentials: map[string]string{KeyClientID: "c", KeyClientSecret: "s"}})
	if _, err := pp.PushUSSD(ctx, "paypal", push); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("paypal push: want ErrNotSupported, got %v", err)
	}

	pesa := newTestController(t, Config{Provider: "pesapal", Credentials: map[string]string{KeyConsumerKey: "c", KeyConsumerSecret: "s"}})
	if _, err := pesa.PushUSSD(ctx, "pesapal", push); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("pesapal push: want ErrNotSupported, got %v", err)
	}
}

func TestFlutterwaveFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk" {
			jsonReply(w, 401, `{"status":"error","message":"bad key"}`)
			return
		}
		switch {
		case r.URL.Path == "/payments":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["tx_ref"] != "pay_1" || b["currency"] != "USD" {
				t.Errorf("unexpected body %v", b)
			}
			jsonReply(w, 200, `{"status":"success","data":{"link":"https://checkout.flw/x"}}`)
		case r.URL.Path == "/transactions/verify_by_reference":
			jsonReply(w, 200, `{"status":"success","data":{"id":99,"tx_ref":"pay_1","status":"successful","amount":100,"currency":"USD","payment_type":"card"}}`)
		case r.URL.Path == "/transactions/99/verify":
			jsonReply(w, 200, `{"status":"success","data":{"id":99,"tx_ref":"pay_1","status":"successful","amount":100,"currency":"USD"}}`)
		case r.URL.Path == "/transactions/99/refund":
			jsonReply(w, 200, `{"status":"success","data":{"id":7,"status":"completed"}}`)
		case r.URL.Path == "/charges":
			if got := r.URL.Query().Get("type"); got != "mobile_money_tanzania" {
				t.Errorf("charge type = %s", got)
			}
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["phone_number"] != "0782835136" || b["currency"] != "TZS" {
				t.Errorf("unexpected charge body %v", b)
			}
			jsonReply(w, 200, `{"status":"success","message":"Charge initiated","data":{"id":4376085,"tx_ref":"pay_2","status":"pending","processor_response":"Transaction in progress"}}`)
		case r.URL.Path == "/transactions":
			if r.URL.Query().Get("from") == "" || r.URL.Query().Get("to") == "" {
				t.Errorf("list must default from/to: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"status":"success","message":"Transactions fetched","meta":{"page_info":{"total":2,"current_page":1,"total_pages":2}},"data":[{"id":"1","tx_ref":"pay_a","status":"successful","amount":10,"currency":"USD"},{"id":"2","tx_ref":"pay_b","status":"failed","amount":20,"currency":"USD"}]}`)
		case r.URL.Path == "/virtual-cards":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["email"] != "card@example.com" {
				t.Errorf("card body: %v", b)
			}
			jsonReply(w, 200, `{"status":"success","message":"Card created successfully","data":{"id":"card_1","masked_pan":"531993*******0288","card_pan":"5319938155020288","cvv":"905","currency":"USD","amount":"5.00","expiration":"2028-09","card_type":"mastercard","is_active":true}}`)
		case r.URL.Path == "/refunds/7":
			jsonReply(w, 200, `{"status":"success","data":{"id":7,"amount":100,"status":"completed","transaction_id":99,"currency":"USD"}}`)
		case r.URL.Path == "/transactions/99/refunds":
			jsonReply(w, 200, `{"status":"success","data":[{"id":7,"amount":100,"status":"completed","transaction_id":99,"currency":"USD"}]}`)
		case r.URL.Path == "/transactions/fee":
			if r.URL.Query().Get("currency") != "USD" {
				t.Errorf("fee query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"status":"success","data":{"charge_amount":10000,"fee":140,"currency":"USD"}}`)
		default:
			t.Errorf("unexpected call %s", r.URL)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "flutterwave", BaseURL: srv.URL, Credentials: map[string]string{KeySecretKey: "sk", KeyWebhookHash: "hash"}})
	ctx := context.Background()

	resp, err := c.CreatePayment(ctx, "flutterwave", PaymentRequest{Reference: "pay_1", Amount: 100, Currency: "usd", ReturnURL: "https://app/return"})
	if err != nil || resp.CheckoutURL != "https://checkout.flw/x" {
		t.Fatalf("create: %v %+v", err, resp)
	}
	res, err := c.VerifyPayment(ctx, "flutterwave", VerifyRequest{Reference: "pay_1"})
	if err != nil || res.Status != StatusSucceeded || res.ProviderPaymentID != "99" || res.Amount != 100 {
		t.Fatalf("verify: %v %+v", err, res)
	}
	ref, err := c.Refund(ctx, "flutterwave", RefundRequest{ProviderPaymentID: res.ProviderPaymentID})
	if err != nil || ref.Status != StatusRefunded {
		t.Fatalf("refund: %v %+v", err, ref)
	}

	// webhook: wrong hash is rejected, right hash is re-verified through the API
	bad := httptest.NewRequest("POST", "/hook", strings.NewReader(`{}`))
	bad.Header.Set("verif-hash", "nope")
	if _, err := c.ParseWebhook(ctx, "flutterwave", bad); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("want ErrInvalidWebhook, got %v", err)
	}
	good := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"event":"charge.completed","data":{"id":99}}`))
	good.Header.Set("verif-hash", "hash")
	ev, err := c.ParseWebhook(ctx, "flutterwave", good)
	if err != nil || ev.Result == nil || ev.Result.Status != StatusSucceeded || !ev.Success {
		t.Fatalf("webhook: %v %+v", err, ev)
	}

	// country resolved from currency (no CountryCode given) picks Tanzania,
	// which needs no network.
	push, err := c.PushUSSD(ctx, "flutterwave", PushRequest{Reference: "pay_2", Amount: 150, Currency: "TZS", Phone: "0782835136"})
	if err != nil || push.Status != StatusPending || push.ProviderRef != "4376085" {
		t.Fatalf("push: %v %+v", err, push)
	}
	// Uganda needs a network
	if _, err := c.PushUSSD(ctx, "flutterwave", PushRequest{Reference: "pay_3", Amount: 150, Currency: "UGX", Phone: "0782835136"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("uganda without network: want ErrInvalidRequest, got %v", err)
	}
	// unroutable currency/country
	if _, err := c.PushUSSD(ctx, "flutterwave", PushRequest{Reference: "pay_4", Amount: 150, Currency: "XYZ", Phone: "0782835136"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown currency: want ErrInvalidRequest, got %v", err)
	}

	if _, err := c.CancelPayment(ctx, "flutterwave", CancelRequest{ProviderRef: "99"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("cancel: want ErrNotSupported, got %v", err)
	}
	list, err := c.ListPayments(ctx, "flutterwave", ListRequest{})
	if err != nil || len(list.Items) != 2 || list.Items[0].Status != StatusSucceeded || list.Items[1].Status != StatusFailed || list.Total != 2 || !list.HasMore {
		t.Fatalf("list: %v %+v", err, list)
	}

	issuer, err := c.CardIssuer("flutterwave")
	if err != nil {
		t.Fatal(err)
	}
	card, err := issuer.CreateVirtualCard(ctx, CardRequest{
		Currency: "USD", Amount: 5, DebitCurrency: "NGN",
		Customer:       Customer{Name: "Jane Doe", Email: "card@example.com", Phone: "0700000000"},
		BillingCountry: "US", DateOfBirth: "1996-12-30", Title: "MS", Gender: "F",
	})
	if err != nil || card.ID != "card_1" || card.MaskedPAN != "531993*******0288" || !card.IsActive {
		t.Fatalf("create card: %v %+v", err, card)
	}
	if _, err := issuer.CreateVirtualCard(ctx, CardRequest{Currency: "USD", Amount: 5}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("create card without email: want ErrInvalidRequest, got %v", err)
	}
	if err := c.Add(Config{Provider: "paypal", Credentials: map[string]string{KeyClientID: "c", KeyClientSecret: "s"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CardIssuer("paypal"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("paypal card issuer: want ErrNotSupported, got %v", err)
	}

	gr, err := c.GetRefund(ctx, "flutterwave", RefundQuery{RefundID: "7"})
	if err != nil || gr.Status != StatusRefunded || gr.ProviderPaymentID != "99" || gr.Amount != 100 {
		t.Fatalf("get refund: %v %+v", err, gr)
	}
	rl, err := c.ListRefunds(ctx, "flutterwave", RefundQuery{ProviderPaymentID: "99"})
	if err != nil || len(rl.Items) != 1 || rl.Items[0].Status != StatusRefunded {
		t.Fatalf("list refunds: %v %+v", err, rl)
	}
	quoter, err := c.FeeQuoter("flutterwave")
	if err != nil {
		t.Fatal(err)
	}
	quote, err := quoter.QuoteFee(ctx, FeeRequest{Amount: 10000, Currency: "USD"})
	if err != nil || quote.Fee != 140 {
		t.Fatalf("quote fee: %v %+v", err, quote)
	}
	if _, err := c.FeeQuoter("paypal"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("paypal fee quoter: want ErrNotSupported, got %v", err)
	}
}

func TestSelcomSigning(t *testing.T) {
	const secret = "s3cret"
	var gotSeen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSeen = true
		h := r.Header
		if h.Get("Authorization") != "SELCOM "+base64.StdEncoding.EncodeToString([]byte("key")) || h.Get("Digest-Method") != "HS256" {
			t.Errorf("bad auth headers: %v", h)
		}
		// recompute the digest the way Selcom's reference clients do
		data := "timestamp=" + h.Get("Timestamp")
		var body map[string]any
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			// fields must appear in the body in Signed-Fields order
			last := -1
			for _, k := range strings.Split(h.Get("Signed-Fields"), ",") {
				idx := strings.Index(string(raw), `"`+k+`"`)
				if idx <= last {
					t.Errorf("field %s out of order in %s", k, raw)
				}
				last = idx
			}
			_ = json.Unmarshal(raw, &body)
		}
		for _, k := range strings.Split(h.Get("Signed-Fields"), ",") {
			var v string
			if r.Method == http.MethodPost {
				v = str(body[k])
			} else {
				v = r.URL.Query().Get(k)
			}
			data += "&" + k + "=" + v
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(data))
		if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); h.Get("Digest") != want {
			t.Errorf("digest mismatch for %q", data)
		}
		if _, err := time.Parse(time.RFC3339, h.Get("Timestamp")); err != nil {
			t.Errorf("timestamp %q: %v", h.Get("Timestamp"), err)
		}

		switch r.URL.Path {
		case "/v1/checkout/create-order-minimal":
			jsonReply(w, 200, `{"reference":"R1","resultcode":"000","result":"SUCCESS","data":[{"payment_gateway_url":"`+
				base64.StdEncoding.EncodeToString([]byte("https://pay.selcom/abc"))+`"}]}`)
		case "/v1/checkout/order-status":
			if r.URL.Query().Get("order_id") == "push_1" {
				jsonReply(w, 404, `{"resultcode":"134","result":"FAIL","message":"Invalid order id"}`)
				return
			}
			jsonReply(w, 200, `{"reference":"R1","resultcode":"000","result":"SUCCESS","data":[{"order_id":"pay_1","payment_status":"COMPLETED","amount":"8000","transid":"T1","channel":"TIGOPESA"}]}`)
		case "/v1/wallet/pushussd":
			jsonReply(w, 200, `{"reference":"W1","resultcode":"000","result":"SUCCESS","message":"Request in progress. Please enter your PIN"}`)
		case "/v1/c2b/query-status":
			jsonReply(w, 200, `{"reference":"6927759116","transid":"push_1","resultcode":"000","result":"SUCCESS","message":"| COMPLETE | CONFIRMED | Payment successful"}`)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "selcom", BaseURL: srv.URL, WebhookURL: "https://app/hook",
		Credentials: map[string]string{KeyAPIKey: "key", KeyAPISecret: secret, KeyVendor: "TILL1"}})
	ctx := context.Background()

	resp, err := c.CreatePayment(ctx, "selcom", PaymentRequest{
		Reference: "pay_1", Amount: 8000, Currency: "TZS", ReturnURL: "https://app/return",
		Customer: Customer{Name: "John Doe", Email: "j@example.com", Phone: "+255 682 555 555"},
	})
	if err != nil || resp.CheckoutURL != "https://pay.selcom/abc" {
		t.Fatalf("create: %v %+v", err, resp)
	}
	res, err := c.VerifyPayment(ctx, "selcom", VerifyRequest{Reference: "pay_1"})
	if err != nil || res.Status != StatusSucceeded || res.Amount != 8000 || res.ProviderPaymentID != "T1" {
		t.Fatalf("verify: %v %+v", err, res)
	}

	// unsigned webhook is confirmed against the API
	hook := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"order_id":"pay_1","payment_status":"COMPLETED"}`))
	ev, err := c.ParseWebhook(ctx, "selcom", hook)
	if err != nil || ev.Result == nil || ev.Result.Status != StatusSucceeded || !ev.Success {
		t.Fatalf("webhook: %v %+v", err, ev)
	}
	if !gotSeen {
		t.Fatal("server never called")
	}

	// PushUSSD lives on the wallet API; VerifyPayment falls back to
	// /v1/c2b/query-status when the reference is not a checkout order.
	push, err := c.PushUSSD(ctx, "selcom", PushRequest{Reference: "push_1", Amount: 5000, Currency: "TZS", Phone: "0682555555"})
	if err != nil || push.Status != StatusPending {
		t.Fatalf("push: %v %+v", err, push)
	}
	pres, err := c.VerifyPayment(ctx, "selcom", VerifyRequest{Reference: "push_1"})
	if err != nil || pres.Status != StatusSucceeded {
		t.Fatalf("push verify: %v %+v", err, pres)
	}

	if _, err := c.CancelPayment(ctx, "selcom", CancelRequest{Reference: "pay_1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("cancel: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListPayments(ctx, "selcom", ListRequest{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list: want ErrNotSupported, got %v", err)
	}
	if _, err := c.GetRefund(ctx, "selcom", RefundQuery{RefundID: "x"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("get refund: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListRefunds(ctx, "selcom", RefundQuery{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list refunds: want ErrNotSupported, got %v", err)
	}
}

func TestPesapalFlow(t *testing.T) {
	registered := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/Auth/RequestToken":
			jsonReply(w, 200, `{"token":"tok","expiryDate":"`+time.Now().Add(5*time.Minute).UTC().Format(time.RFC3339)+`","error":null,"status":"200"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			jsonReply(w, 401, `{}`)
			return
		}
		switch r.URL.Path {
		case "/api/URLSetup/RegisterIPN":
			registered++
			jsonReply(w, 200, `{"ipn_id":"ipn-1","status":"200","error":null}`)
		case "/api/Transactions/SubmitOrderRequest":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["notification_id"] != "ipn-1" {
				t.Errorf("notification_id = %v", b["notification_id"])
			}
			jsonReply(w, 200, `{"order_tracking_id":"trk-1","merchant_reference":"pay_1","redirect_url":"https://pay.pesapal/x","error":null,"status":"200"}`)
		case "/api/Transactions/GetTransactionStatus":
			if r.URL.Query().Get("orderTrackingId") != "trk-1" {
				t.Errorf("tracking id = %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"status_code":1,"payment_status_description":"Completed","confirmation_code":"CONF1","amount":50,"currency":"KES","merchant_reference":"pay_1","payment_method":"Mpesa","error":null}`)
		case "/api/Transactions/RefundRequest":
			jsonReply(w, 200, `{"status":"200","message":"Refund request initiated"}`)
		default:
			t.Errorf("unexpected call %s", r.URL)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "pesapal", BaseURL: srv.URL, WebhookURL: "https://app/ipn",
		Credentials: map[string]string{KeyConsumerKey: "ck", KeyConsumerSecret: "cs"}})
	ctx := context.Background()

	for i := 0; i < 2; i++ { // the ipn is registered once and cached
		if _, err := c.CreatePayment(ctx, "pesapal", PaymentRequest{Reference: "pay_1", Amount: 50, Currency: "KES", ReturnURL: "https://app/return",
			Customer: Customer{Name: "Jane Wanjiru Doe", Email: "j@example.com"}}); err != nil {
			t.Fatal(err)
		}
	}
	if registered != 1 {
		t.Fatalf("ipn registered %d times", registered)
	}

	res, err := c.VerifyPayment(ctx, "pesapal", VerifyRequest{ProviderRef: "trk-1"})
	if err != nil || res.Status != StatusSucceeded || res.ProviderPaymentID != "CONF1" || res.Currency != "KES" {
		t.Fatalf("verify: %v %+v", err, res)
	}
	if _, err := c.Refund(ctx, "pesapal", RefundRequest{ProviderPaymentID: "CONF1"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("refund without amount: want ErrInvalidRequest, got %v", err)
	}
	if ref, err := c.Refund(ctx, "pesapal", RefundRequest{ProviderPaymentID: "CONF1", Amount: 50}); err != nil || ref.Status != StatusPending {
		t.Fatalf("refund: %v %+v", err, ref)
	}

	// the IPN handler answers with the echo body Pesapal expects
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ipn?OrderTrackingId=trk-1&OrderMerchantReference=pay_1&OrderNotificationType=IPNCHANGE", nil)
	var seen *WebhookEvent
	c.WebhookHandler("pesapal", func(_ context.Context, ev *WebhookEvent) error { seen = ev; return nil })(rec, req)
	if rec.Code != 200 || seen == nil || seen.Result.Status != StatusSucceeded || !seen.Success || !strings.Contains(rec.Body.String(), `"orderTrackingId":"trk-1"`) {
		t.Fatalf("ipn: %d %s %+v", rec.Code, rec.Body.String(), seen)
	}

	if _, err := c.CancelPayment(ctx, "pesapal", CancelRequest{ProviderRef: "trk-1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("cancel: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListPayments(ctx, "pesapal", ListRequest{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list: want ErrNotSupported, got %v", err)
	}
	if _, err := c.GetRefund(ctx, "pesapal", RefundQuery{RefundID: "x"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("get refund: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListRefunds(ctx, "pesapal", RefundQuery{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list refunds: want ErrNotSupported, got %v", err)
	}
}

func TestPayPalFlow(t *testing.T) {
	captured := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/oauth2/token":
			if u, p, _ := r.BasicAuth(); u != "cid" || p != "csec" {
				t.Errorf("basic auth = %s:%s", u, p)
			}
			jsonReply(w, 200, `{"access_token":"tok","expires_in":3600}`)
		case r.URL.Path == "/v2/checkout/orders" && r.Method == "POST":
			jsonReply(w, 201, `{"id":"ORD1","status":"PAYER_ACTION_REQUIRED","links":[{"rel":"self","href":"x"},{"rel":"payer-action","href":"https://paypal/approve"}]}`)
		case r.URL.Path == "/v2/checkout/orders/ORD1" && r.Method == "GET":
			status := "APPROVED"
			if captured {
				status = "COMPLETED"
			}
			jsonReply(w, 200, `{"id":"ORD1","status":"`+status+`","purchase_units":[{"custom_id":"pay_1","amount":{"currency_code":"USD","value":"10.00"}}]}`)
		case r.URL.Path == "/v2/checkout/orders/ORD1/capture":
			captured = true
			jsonReply(w, 201, `{"id":"ORD1","status":"COMPLETED","purchase_units":[{"custom_id":"pay_1","amount":{"currency_code":"USD","value":"10.00"},"payments":{"captures":[{"id":"CAP1","status":"COMPLETED","amount":{"currency_code":"USD","value":"10.00"}}]}}]}`)
		case r.URL.Path == "/v2/payments/captures/CAP1/refund":
			jsonReply(w, 201, `{"id":"REF1","status":"COMPLETED"}`)
		case r.URL.Path == "/v2/payments/refunds/REF1":
			jsonReply(w, 200, `{"id":"REF1","status":"COMPLETED","amount":{"currency_code":"USD","value":"10.00"}}`)
		case r.URL.Path == "/v3/vault/payment-tokens" && r.Method == http.MethodPost:
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			src, _ := b["payment_source"].(map[string]any)
			tok, _ := src["token"].(map[string]any)
			if tok["id"] != "setup_1" || tok["type"] != "SETUP_TOKEN" {
				t.Errorf("vault create body: %v", b)
			}
			jsonReply(w, 201, `{"id":"tok_1","customer":{"id":"cust_1"},"payment_source":{"card":{"last_digits":"1111","brand":"VISA","expiry":"2027-02"}}}`)
		case r.URL.Path == "/v3/vault/payment-tokens" && r.Method == http.MethodGet:
			if r.URL.Query().Get("customer_id") != "cust_1" {
				t.Errorf("vault list query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"payment_tokens":[{"id":"tok_1","customer":{"id":"cust_1"},"payment_source":{"card":{"last_digits":"1111","brand":"VISA","expiry":"2027-02"}}}]}`)
		case r.URL.Path == "/v3/vault/payment-tokens/tok_1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/notifications/verify-webhook-signature":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if _, ok := b["webhook_event"].(map[string]any); !ok || b["webhook_id"] != "WH1" {
				t.Errorf("verify body = %v", b)
			}
			status := "SUCCESS"
			if b["transmission_id"] == "bad" {
				status = "FAILURE"
			}
			jsonReply(w, 200, `{"verification_status":"`+status+`"}`)
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "paypal", BaseURL: srv.URL,
		Credentials: map[string]string{KeyClientID: "cid", KeyClientSecret: "csec", KeyWebhookID: "WH1"}})
	ctx := context.Background()

	resp, err := c.CreatePayment(ctx, "paypal", PaymentRequest{Reference: "pay_1", Amount: 10, Currency: "USD", ReturnURL: "https://app/return"})
	if err != nil || resp.ProviderRef != "ORD1" || resp.CheckoutURL != "https://paypal/approve" {
		t.Fatalf("create: %v %+v", err, resp)
	}
	res, err := c.VerifyPayment(ctx, "paypal", VerifyRequest{ProviderRef: "ORD1"})
	if err != nil || res.Status != StatusSucceeded || res.ProviderPaymentID != "CAP1" || res.Reference != "pay_1" {
		t.Fatalf("verify: %v %+v", err, res)
	}
	ref, err := c.Refund(ctx, "paypal", RefundRequest{ProviderPaymentID: "CAP1"})
	if err != nil || ref.Status != StatusRefunded {
		t.Fatalf("refund: %v %+v", err, ref)
	}

	hook := func(id string) *http.Request {
		r := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"event_type":"PAYMENT.CAPTURE.COMPLETED","resource":{"id":"CAP1","status":"COMPLETED","custom_id":"pay_1","amount":{"currency_code":"USD","value":"10.00"},"supplementary_data":{"related_ids":{"order_id":"ORD1"}}}}`))
		r.Header.Set("PAYPAL-TRANSMISSION-ID", id)
		return r
	}
	ev, err := c.ParseWebhook(ctx, "paypal", hook("ok"))
	if err != nil || ev.Result == nil || ev.Result.Status != StatusSucceeded || !ev.Success || ev.Result.ProviderRef != "ORD1" {
		t.Fatalf("webhook: %v %+v", err, ev)
	}
	if _, err := c.ParseWebhook(ctx, "paypal", hook("bad")); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("bad signature: want ErrInvalidWebhook, got %v", err)
	}

	if _, err := c.CancelPayment(ctx, "paypal", CancelRequest{ProviderRef: "ORD1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("cancel: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListPayments(ctx, "paypal", ListRequest{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list: want ErrNotSupported, got %v", err)
	}

	gr, err := c.GetRefund(ctx, "paypal", RefundQuery{RefundID: "REF1"})
	if err != nil || gr.Status != StatusRefunded || gr.Amount != 10 {
		t.Fatalf("get refund: %v %+v", err, gr)
	}
	if _, err := c.ListRefunds(ctx, "paypal", RefundQuery{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list refunds: want ErrNotSupported, got %v", err)
	}

	pmMgr, err := c.PaymentMethodManager("paypal")
	if err != nil {
		t.Fatal(err)
	}
	pm, err := pmMgr.CreatePaymentMethod(ctx, PaymentMethodRequest{Token: "setup_1"})
	if err != nil || pm.ID != "tok_1" || pm.ProviderCustomerID != "cust_1" || pm.Last4 != "1111" || pm.ExpiryYear != 2027 || pm.ExpiryMonth != 2 {
		t.Fatalf("create payment method: %v %+v", err, pm)
	}
	methods, err := pmMgr.ListPaymentMethods(ctx, "cust_1")
	if err != nil || len(methods) != 1 || methods[0].ID != "tok_1" {
		t.Fatalf("list payment methods: %v %+v", err, methods)
	}
	if err := pmMgr.DeletePaymentMethod(ctx, "cust_1", "tok_1"); err != nil {
		t.Fatalf("delete payment method: %v", err)
	}
}

func TestStripeFlow(t *testing.T) {
	const secret = "whsec_test"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test" {
			jsonReply(w, 401, `{"error":{"message":"bad key"}}`)
			return
		}
		switch {
		case r.URL.Path == "/checkout/sessions" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			if r.PostForm.Get("client_reference_id") != "pay_1" || r.PostForm.Get("line_items[0][price_data][unit_amount]") != "1000" {
				t.Errorf("unexpected form: %v", r.PostForm)
			}
			jsonReply(w, 200, `{"id":"cs_1","url":"https://checkout.stripe.com/pay/cs_1","status":"open"}`)
		case r.URL.Path == "/checkout/sessions/cs_1" && r.Method == http.MethodGet:
			jsonReply(w, 200, `{"id":"cs_1","client_reference_id":"pay_1","status":"complete","payment_status":"paid","amount_total":1000,"currency":"usd","payment_intent":{"id":"pi_1","status":"succeeded"}}`)
		case r.URL.Path == "/refunds" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			if r.PostForm.Get("payment_intent") != "pi_1" {
				t.Errorf("refund form: %v", r.PostForm)
			}
			jsonReply(w, 200, `{"id":"re_1","status":"succeeded"}`)
		case r.URL.Path == "/checkout/sessions/cs_1/expire" && r.Method == http.MethodPost:
			jsonReply(w, 200, `{"id":"cs_1","client_reference_id":"pay_1","status":"expired","payment_status":"unpaid"}`)
		case r.URL.Path == "/checkout/sessions" && r.Method == http.MethodGet:
			if r.URL.Query().Get("limit") != "20" {
				t.Errorf("list limit: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"data":[{"id":"cs_2","client_reference_id":"pay_2","status":"complete","payment_status":"paid","amount_total":500,"currency":"usd"}],"has_more":true}`)
		case r.URL.Path == "/refunds/re_1" && r.Method == http.MethodGet:
			jsonReply(w, 200, `{"id":"re_1","status":"succeeded","amount":1000,"currency":"usd","payment_intent":"pi_1"}`)
		case r.URL.Path == "/refunds" && r.Method == http.MethodGet:
			if r.URL.Query().Get("payment_intent") != "pi_1" {
				t.Errorf("list refunds query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"data":[{"id":"re_1","status":"succeeded","amount":1000,"currency":"usd","payment_intent":"pi_1"}],"has_more":false}`)
		case r.URL.Path == "/customers" && r.Method == http.MethodPost:
			jsonReply(w, 200, `{"id":"cus_1"}`)
		case r.URL.Path == "/payment_methods/pm_1/attach" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			if r.PostForm.Get("customer") != "cus_1" {
				t.Errorf("attach form: %v", r.PostForm)
			}
			jsonReply(w, 200, `{"id":"pm_1","customer":"cus_1","card":{"brand":"visa","last4":"4242","exp_month":12,"exp_year":2027}}`)
		case r.URL.Path == "/customers/cus_1" && r.Method == http.MethodPost:
			jsonReply(w, 200, `{"id":"cus_1"}`)
		case r.URL.Path == "/payment_methods" && r.Method == http.MethodGet:
			if r.URL.Query().Get("customer") != "cus_1" {
				t.Errorf("list pm query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"data":[{"id":"pm_1","card":{"brand":"visa","last4":"4242","exp_month":12,"exp_year":2027}}]}`)
		case r.URL.Path == "/payment_methods/pm_1/detach" && r.Method == http.MethodPost:
			jsonReply(w, 200, `{"id":"pm_1"}`)
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "stripe", BaseURL: srv.URL, Credentials: map[string]string{KeySecretKey: "sk_test", KeyWebhookSecret: secret}})
	ctx := context.Background()

	resp, err := c.CreatePayment(ctx, "stripe", PaymentRequest{Reference: "pay_1", Amount: 10, Currency: "USD", ReturnURL: "https://app/return"})
	if err != nil || resp.ProviderRef != "cs_1" || resp.CheckoutURL != "https://checkout.stripe.com/pay/cs_1" {
		t.Fatalf("create: %v %+v", err, resp)
	}
	res, err := c.VerifyPayment(ctx, "stripe", VerifyRequest{ProviderRef: "cs_1"})
	if err != nil || res.Status != StatusSucceeded || res.ProviderPaymentID != "pi_1" || res.Amount != 10 {
		t.Fatalf("verify: %v %+v", err, res)
	}
	ref, err := c.Refund(ctx, "stripe", RefundRequest{ProviderPaymentID: res.ProviderPaymentID})
	if err != nil || ref.Status != StatusRefunded {
		t.Fatalf("refund: %v %+v", err, ref)
	}
	if _, err := c.PushUSSD(ctx, "stripe", PushRequest{Reference: "pay_1", Amount: 10, Currency: "USD", Phone: "+255682555555"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("push: want ErrNotSupported, got %v", err)
	}
	cancel, err := c.CancelPayment(ctx, "stripe", CancelRequest{ProviderRef: "cs_1"})
	if err != nil || cancel.Status != StatusFailed || cancel.FailureReason != "cancelled" {
		t.Fatalf("cancel: %v %+v", err, cancel)
	}
	list, err := c.ListPayments(ctx, "stripe", ListRequest{})
	if err != nil || len(list.Items) != 1 || list.Items[0].Status != StatusSucceeded || !list.HasMore || list.NextCursor != "cs_2" {
		t.Fatalf("list: %v %+v", err, list)
	}

	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_1","client_reference_id":"pay_1","status":"complete","payment_status":"paid","amount_total":1000,"currency":"usd","payment_intent":"pi_1"}}}`
	sign := func(ts string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "." + body))
		return hex.EncodeToString(mac.Sum(nil))
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	good := httptest.NewRequest("POST", "/hook", strings.NewReader(body))
	good.Header.Set("Stripe-Signature", "t="+now+",v1="+sign(now))
	ev, err := c.ParseWebhook(ctx, "stripe", good)
	if err != nil || ev.Result == nil || ev.Result.Status != StatusSucceeded || !ev.Success || ev.Result.ProviderPaymentID != "pi_1" {
		t.Fatalf("webhook: %v %+v", err, ev)
	}

	bad := httptest.NewRequest("POST", "/hook", strings.NewReader(body))
	bad.Header.Set("Stripe-Signature", "t="+now+",v1=deadbeef")
	if _, err := c.ParseWebhook(ctx, "stripe", bad); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("bad signature: want ErrInvalidWebhook, got %v", err)
	}
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	stale := httptest.NewRequest("POST", "/hook", strings.NewReader(body))
	stale.Header.Set("Stripe-Signature", "t="+old+",v1="+sign(old))
	if _, err := c.ParseWebhook(ctx, "stripe", stale); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("stale timestamp: want ErrInvalidWebhook, got %v", err)
	}

	gr, err := c.GetRefund(ctx, "stripe", RefundQuery{RefundID: "re_1"})
	if err != nil || gr.Status != StatusRefunded || gr.Amount != 10 || gr.ProviderPaymentID != "pi_1" {
		t.Fatalf("get refund: %v %+v", err, gr)
	}
	rl, err := c.ListRefunds(ctx, "stripe", RefundQuery{ProviderPaymentID: "pi_1"})
	if err != nil || len(rl.Items) != 1 || rl.Items[0].Status != StatusRefunded {
		t.Fatalf("list refunds: %v %+v", err, rl)
	}

	pmMgr, err := c.PaymentMethodManager("stripe")
	if err != nil {
		t.Fatal(err)
	}
	pm, err := pmMgr.CreatePaymentMethod(ctx, PaymentMethodRequest{
		Token: "pm_1", Customer: Customer{Email: "jane@example.com"}, SetAsDefault: true,
	})
	if err != nil || pm.ID != "pm_1" || pm.ProviderCustomerID != "cus_1" || pm.Last4 != "4242" || pm.Brand != "visa" || !pm.IsDefault {
		t.Fatalf("create payment method: %v %+v", err, pm)
	}
	methods, err := pmMgr.ListPaymentMethods(ctx, "cus_1")
	if err != nil || len(methods) != 1 || methods[0].ID != "pm_1" {
		t.Fatalf("list payment methods: %v %+v", err, methods)
	}
	if err := pmMgr.DeletePaymentMethod(ctx, "cus_1", "pm_1"); err != nil {
		t.Fatalf("delete payment method: %v", err)
	}
}

func TestClickPesaFlow(t *testing.T) {
	const checksumKey = "csecret"
	clickpesaChecksum := func(body map[string]any) string {
		clean := map[string]any{}
		for k, v := range body {
			if k == "checksum" || k == "checksumMethod" {
				continue
			}
			clean[k] = v
		}
		canonical, _ := json.Marshal(clean)
		mac := hmac.New(sha256.New, []byte(checksumKey))
		mac.Write(canonical)
		return hex.EncodeToString(mac.Sum(nil))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/generate-token":
			if r.Header.Get("client-id") != "cid" || r.Header.Get("api-key") != "akey" {
				t.Errorf("token headers: %v", r.Header)
			}
			jsonReply(w, 200, `{"success":true,"token":"tok"}`)
		case r.Header.Get("Authorization") != "Bearer tok":
			jsonReply(w, 401, `{"message":"unauthorized"}`)
		case r.URL.Path == "/checkout-link/generate-checkout-url":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if got := str(body["checksum"]); got != clickpesaChecksum(body) {
				t.Errorf("checkout-link checksum mismatch: %v", body)
			}
			jsonReply(w, 200, `{"checkoutLink":"https://checkout.clickpesa.com/x"}`)
		case r.URL.Path == "/payments/initiate-ussd-push-request":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if got := str(body["checksum"]); got != clickpesaChecksum(body) {
				t.Errorf("push checksum mismatch: %v", body)
			}
			jsonReply(w, 200, `{"id":"txn1","status":"PROCESSING","channel":"TIGO-PESA"}`)
		case r.URL.Path == "/payments/pay_1":
			jsonReply(w, 200, `[{"id":"txn1","status":"SUCCESS","orderReference":"pay_1","collectedAmount":5000,"collectedCurrency":"TZS"}]`)
		case r.URL.Path == "/payments/all":
			if r.URL.Query().Get("status") != "SUCCESS" || r.URL.Query().Get("limit") != "20" {
				t.Errorf("list query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"data":[{"id":"txn1","status":"SUCCESS","orderReference":"pay_1","collectedAmount":5000,"collectedCurrency":"TZS"}],"totalCount":1}`)
		default:
			t.Errorf("unexpected call %s", r.URL)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "clickpesa", BaseURL: srv.URL,
		Credentials: map[string]string{KeyClientID: "cid", KeyAPIKey: "akey", KeyChecksumKey: checksumKey}})
	ctx := context.Background()

	resp, err := c.CreatePayment(ctx, "clickpesa", PaymentRequest{Reference: "pay_1", Amount: 5000, Currency: "TZS", ReturnURL: "https://app/return"})
	if err != nil || resp.CheckoutURL != "https://checkout.clickpesa.com/x" {
		t.Fatalf("create: %v %+v", err, resp)
	}
	push, err := c.PushUSSD(ctx, "clickpesa", PushRequest{Reference: "pay_1", Amount: 5000, Currency: "TZS", Phone: "0782835136"})
	if err != nil || push.Status != StatusPending || push.ProviderRef != "txn1" {
		t.Fatalf("push: %v %+v", err, push)
	}
	res, err := c.VerifyPayment(ctx, "clickpesa", VerifyRequest{Reference: "pay_1"})
	if err != nil || res.Status != StatusSucceeded || res.Amount != 5000 {
		t.Fatalf("verify: %v %+v", err, res)
	}
	if _, err := c.Refund(ctx, "clickpesa", RefundRequest{ProviderPaymentID: "txn1", Amount: 100}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("refund: want ErrNotSupported, got %v", err)
	}
	if _, err := c.CancelPayment(ctx, "clickpesa", CancelRequest{Reference: "pay_1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("cancel: want ErrNotSupported, got %v", err)
	}
	list, err := c.ListPayments(ctx, "clickpesa", ListRequest{Status: "SUCCESS"})
	if err != nil || len(list.Items) != 1 || list.Items[0].Status != StatusSucceeded || list.Total != 1 || list.HasMore {
		t.Fatalf("list: %v %+v", err, list)
	}

	// webhook: checksum computed over the whole payload minus checksum/checksumMethod
	payload := map[string]any{"event": "PAYMENT RECEIVED", "data": map[string]any{"orderReference": "pay_1"}}
	payload["checksum"] = clickpesaChecksum(payload)
	raw, _ := json.Marshal(payload)
	hook := httptest.NewRequest("POST", "/hook", strings.NewReader(string(raw)))
	ev, err := c.ParseWebhook(ctx, "clickpesa", hook)
	if err != nil || ev.Result == nil || ev.Result.Status != StatusSucceeded || !ev.Success {
		t.Fatalf("webhook: %v %+v", err, ev)
	}

	tampered := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"event":"PAYMENT RECEIVED","checksum":"deadbeef","data":{"orderReference":"pay_1"}}`))
	if _, err := c.ParseWebhook(ctx, "clickpesa", tampered); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("tampered webhook: want ErrInvalidWebhook, got %v", err)
	}
	if _, err := c.GetRefund(ctx, "clickpesa", RefundQuery{RefundID: "x"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("get refund: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListRefunds(ctx, "clickpesa", RefundQuery{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list refunds: want ErrNotSupported, got %v", err)
	}
}

func TestAzamPayFlow(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/AppRegistration/GenerateToken":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["appName"] != "App" || b["clientId"] != "cid" || b["clientSecret"] != "csec" {
				t.Errorf("token body: %v", b)
			}
			jsonReply(w, 200, `{"success":true,"data":{"accessToken":"tok","expire":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}}`)
		case r.Header.Get("Authorization") != "Bearer tok":
			jsonReply(w, 401, `{"success":false,"message":"unauthorized"}`)
		case r.URL.Path == "/azampay/mno/checkout":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["provider"] != "Mpesa" || b["accountNumber"] != "255682555555" {
				t.Errorf("checkout body: %v", b)
			}
			jsonReply(w, 200, `{"success":true,"transactionId":"az1","message":"Request in progress"}`)
		case r.URL.Path == "/azampay/gettransactionstatus":
			if r.URL.Query().Get("pgReferenceId") != "az1" {
				t.Errorf("status query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"success":true,"data":"success"}`)
		default:
			t.Errorf("unexpected call %s", r.URL)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "azampay", BaseURL: srv.URL,
		Credentials: map[string]string{KeyAppName: "App", KeyClientID: "cid", KeyClientSecret: "csec", KeyPublicKey: pubPEM}})
	ctx := context.Background()

	if _, err := c.CreatePayment(ctx, "azampay", PaymentRequest{Reference: "pay_1", Amount: 1, Currency: "TZS", ReturnURL: "https://app/return"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("create: want ErrNotSupported, got %v", err)
	}
	push, err := c.PushUSSD(ctx, "azampay", PushRequest{Reference: "pay_1", Amount: 5000, Currency: "TZS", Phone: "+255 682 555 555", Network: "mpesa"})
	if err != nil || push.ProviderRef != "az1" || push.Status != StatusPending {
		t.Fatalf("push: %v %+v", err, push)
	}
	res, err := c.VerifyPayment(ctx, "azampay", VerifyRequest{ProviderRef: "az1"})
	if err != nil || res.Status != StatusSucceeded {
		t.Fatalf("verify: %v %+v", err, res)
	}
	if _, err := c.Refund(ctx, "azampay", RefundRequest{ProviderPaymentID: "az1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("refund: want ErrNotSupported, got %v", err)
	}

	sign := func(externalRef, status, operator, utilityRef string) string {
		signed := utilityRef + externalRef + status + operator
		sum := sha256.Sum256([]byte(signed))
		sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(sig)
	}
	body := map[string]any{
		"transactionstatus": "success", "externalreference": "pay_1", "utilityref": "util1",
		"operator": "Mpesa", "amount": "5000", "transid": "T1",
	}
	body["signature"] = sign("pay_1", "success", "Mpesa", "util1")
	raw, _ := json.Marshal(body)
	hook := httptest.NewRequest("POST", "/hook", strings.NewReader(string(raw)))
	ev, err := c.ParseWebhook(ctx, "azampay", hook)
	if err != nil || ev.Result == nil || ev.Result.Status != StatusSucceeded || !ev.Success || ev.Result.Reference != "pay_1" {
		t.Fatalf("webhook: %v %+v", err, ev)
	}

	body["signature"] = base64.StdEncoding.EncodeToString([]byte("not-a-real-signature-not-a-real-signature-000000"))
	raw, _ = json.Marshal(body)
	tampered := httptest.NewRequest("POST", "/hook", strings.NewReader(string(raw)))
	if _, err := c.ParseWebhook(ctx, "azampay", tampered); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("tampered webhook: want ErrInvalidWebhook, got %v", err)
	}

	if _, err := c.CancelPayment(ctx, "azampay", CancelRequest{ProviderRef: "az1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("cancel: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListPayments(ctx, "azampay", ListRequest{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list: want ErrNotSupported, got %v", err)
	}
	if _, err := c.GetRefund(ctx, "azampay", RefundQuery{RefundID: "x"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("get refund: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListRefunds(ctx, "azampay", RefundQuery{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list refunds: want ErrNotSupported, got %v", err)
	}
}

func TestTwoCheckoutFlow(t *testing.T) {
	const merchant, secretKey, buyLinkSecret = "M123", "restsecret", "buylinksecret"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("X-Avangate-Authentication")
		if !strings.Contains(auth, `code="`+merchant+`"`) {
			t.Errorf("auth header: %s", auth)
		}
		switch {
		case r.URL.Path == "/orders/REF1/" && r.Method == http.MethodGet:
			jsonReply(w, 200, `{"RefNo":"REF1","RefNoExt":"pay_1","Status":"COMPLETE","Currency":"USD","TotalGeneral":10.5}`)
		case r.URL.Path == "/orders/" && r.Method == http.MethodGet && r.URL.Query().Get("ExternalRefNo") != "":
			if r.URL.Query().Get("ExternalRefNo") != "pay_1" {
				t.Errorf("search query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"Content":[{"RefNo":"REF1","RefNoExt":"pay_1","Status":"COMPLETE","Currency":"USD","TotalGeneral":10.5}]}`)
		case r.URL.Path == "/orders/" && r.Method == http.MethodGet:
			if r.URL.Query().Get("Status") != "COMPLETE" {
				t.Errorf("list query: %s", r.URL.RawQuery)
			}
			jsonReply(w, 200, `{"Content":[{"RefNo":"REF1","RefNoExt":"pay_1","Status":"COMPLETE","Currency":"USD","TotalGeneral":10.5}],"TotalCount":1}`)
		case r.URL.Path == "/orders/REF1/refund/" && r.Method == http.MethodPost:
			jsonReply(w, 200, `{}`)
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()

	c := newTestController(t, Config{Provider: "2checkout", BaseURL: srv.URL, Sandbox: true,
		Credentials: map[string]string{KeyMerchantCode: merchant, KeySecretKey: secretKey, KeyBuyLinkSecret: buyLinkSecret}})
	ctx := context.Background()

	resp, err := c.CreatePayment(ctx, "2checkout", PaymentRequest{Reference: "pay_1", Amount: 10.5, Currency: "USD", ReturnURL: "https://app/return"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u, err := url.Parse(resp.CheckoutURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("merchant") != merchant || q.Get("order-ext-ref") != "pay_1" || q.Get("test") != "1" {
		t.Fatalf("buy link params: %v", q)
	}
	wantSig := func() string {
		names := make([]string, 0, len(q))
		for k := range q {
			if k != "signature" {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		var buf strings.Builder
		for _, k := range names {
			v := q.Get(k)
			buf.WriteString(strconv.Itoa(len(v)))
			buf.WriteString(v)
		}
		mac := hmac.New(sha256.New, []byte(buyLinkSecret))
		mac.Write([]byte(buf.String()))
		return hex.EncodeToString(mac.Sum(nil))
	}()
	if q.Get("signature") != wantSig {
		t.Fatalf("buy link signature mismatch: got %s want %s", q.Get("signature"), wantSig)
	}

	byRef, err := c.VerifyPayment(ctx, "2checkout", VerifyRequest{ProviderRef: "REF1"})
	if err != nil || byRef.Status != StatusSucceeded || byRef.Amount != 10.5 {
		t.Fatalf("verify by RefNo: %v %+v", err, byRef)
	}
	byExt, err := c.VerifyPayment(ctx, "2checkout", VerifyRequest{Reference: "pay_1"})
	if err != nil || byExt.Status != StatusSucceeded || byExt.ProviderRef != "REF1" {
		t.Fatalf("verify by ExternalRefNo: %v %+v", err, byExt)
	}
	ref, err := c.Refund(ctx, "2checkout", RefundRequest{ProviderPaymentID: "REF1", Amount: 10.5})
	if err != nil || ref.Status != StatusPending {
		t.Fatalf("refund: %v %+v", err, ref)
	}
	if _, err := c.GetRefund(ctx, "2checkout", RefundQuery{RefundID: "x"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("get refund: want ErrNotSupported, got %v", err)
	}
	if _, err := c.ListRefunds(ctx, "2checkout", RefundQuery{}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("list refunds: want ErrNotSupported, got %v", err)
	}
	if _, err := c.PushUSSD(ctx, "2checkout", PushRequest{Reference: "pay_1", Amount: 1, Currency: "USD", Phone: "+1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("push: want ErrNotSupported, got %v", err)
	}
	if _, err := c.CancelPayment(ctx, "2checkout", CancelRequest{ProviderRef: "REF1"}); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("cancel: want ErrNotSupported, got %v", err)
	}
	list, err := c.ListPayments(ctx, "2checkout", ListRequest{Status: "COMPLETE"})
	if err != nil || len(list.Items) != 1 || list.Items[0].Status != StatusSucceeded || list.Items[0].ProviderRef != "REF1" || list.Total != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	// IPN: fields in received order, length-prefixed and concatenated to sign.
	fields := []struct{ k, v string }{
		{"REFNO", "REF1"}, {"REFNOEXT", "pay_1"}, {"CURRENCY", "USD"},
		{"IPN_TOTALGENERAL", "10.50"}, {"MESSAGE_TYPE", "COMPLETE"},
	}
	var buf strings.Builder
	form := url.Values{}
	for _, f := range fields {
		buf.WriteString(strconv.Itoa(len(f.v)))
		buf.WriteString(f.v)
		form.Set(f.k, f.v)
	}
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(buf.String()))
	form.Set("SIGNATURE_SHA2_256", hex.EncodeToString(mac.Sum(nil)))

	// Build the raw body preserving field order (url.Values.Encode sorts keys).
	var rawBody strings.Builder
	for i, f := range fields {
		if i > 0 {
			rawBody.WriteByte('&')
		}
		rawBody.WriteString(f.k + "=" + url.QueryEscape(f.v))
	}
	rawBody.WriteByte('&')
	rawBody.WriteString("SIGNATURE_SHA2_256=" + form.Get("SIGNATURE_SHA2_256"))

	hook := httptest.NewRequest("POST", "/hook", strings.NewReader(rawBody.String()))
	hook.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ev, err := c.ParseWebhook(ctx, "2checkout", hook)
	if err != nil || ev.Result == nil || ev.Result.Status != StatusSucceeded || !ev.Success || ev.Result.Reference != "pay_1" {
		t.Fatalf("webhook: %v %+v", err, ev)
	}

	tampered := httptest.NewRequest("POST", "/hook", strings.NewReader(rawBody.String()+"X"))
	tampered.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := c.ParseWebhook(ctx, "2checkout", tampered); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("tampered webhook: want ErrInvalidWebhook, got %v", err)
	}
}
