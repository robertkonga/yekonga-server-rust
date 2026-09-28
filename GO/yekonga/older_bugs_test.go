package yekonga

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	mainConfig "github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		token  string
		ok     bool
	}{
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"bearer abc", "abc", true}, // the scheme is case-insensitive
		{"  Bearer   abc  ", "abc", true},
		{"Basic dXNlcjpwYXNz", "", false},
		{"Bearer", "", false},
		{"abc", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		if token, ok := bearerToken(tt.header); token != tt.token || ok != tt.ok {
			t.Errorf("bearerToken(%q) = (%q, %v), want (%q, %v)", tt.header, token, ok, tt.token, tt.ok)
		}
	}
}

func TestRequestTokenDoesNotPanic(t *testing.T) {
	tests := []struct {
		header string
		query  string
		want   string
	}{
		{"Bearer abc", "", "abc"},
		{"", "abc", "abc"}, // a bare token used to lose its first 7 characters
		{"", "Bearer abc", "abc"},
		{"xyz", "", "xyz"}, // used to panic: shorter than "Bearer "
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/?token="+strings.ReplaceAll(tt.query, " ", "%20"), nil)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}

		req := &Request{HttpRequest: r, Context: datatype.Context{}}
		if got := req.Token(); got != tt.want {
			t.Errorf("Token() with header %q, query %q = %q, want %q", tt.header, tt.query, got, tt.want)
		}
	}
}

func TestRequestContextConcurrentAccess(t *testing.T) {
	req := &Request{Context: datatype.Context{}}
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			req.SetContext(string(ClientPayloadKey), ClientPayload{Host: "h"})
			req.SetTenantId(i)
		}(i)
		go func() {
			defer wg.Done()
			req.Client()
			req.TenantId()
			req.TokenPayload()
			req.Tenant()
			req.Auth()
		}()
	}
	wg.Wait()
}

func TestStaticConfigConcurrentUse(t *testing.T) {
	y := &YekongaData{Config: &mainConfig.YekongaConfig{}}
	dir := t.TempDir()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			y.Static(StaticConfig{Directory: dir})
		}()
		go func() {
			defer wg.Done()
			y.isStaticPath("/app.js")
			y.handleStaticFile(httptest.NewRecorder(), httptest.NewRequest("GET", "/missing.js", nil))
		}()
	}
	wg.Wait()

	for _, static := range y.staticConfigs() {
		if static == nil {
			t.Fatal("Static left a nil entry")
		}
	}
	if len(y.staticConfigs()) != 20 {
		t.Fatalf("got %d static configs, want 20", len(y.staticConfigs()))
	}
}

func newCatchTestServer() *YekongaData {
	return &YekongaData{
		Config: &mainConfig.YekongaConfig{},
		routes: make(map[string][]Route),
	}
}

func serve(y *YekongaData, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	y.ServeHTTP(w, r)
	return w
}

func TestCatchMiddlewareChain(t *testing.T) {
	respond := func(body string) Middleware {
		return func(req *Request, res *Response) (int, error) {
			res.Text(body)
			return http.StatusOK, nil
		}
	}
	passThrough := func(ran *bool) Middleware {
		return func(req *Request, res *Response) (int, error) {
			*ran = true
			return http.StatusOK, nil
		}
	}

	t.Run("first to respond wins", func(t *testing.T) {
		y := newCatchTestServer()
		y.Catch(respond("first"))
		y.Catch(respond("second"))

		if w := serve(y, httptest.NewRequest("GET", "/nope", nil)); w.Body.String() != "first" {
			t.Errorf("body %q, want first", w.Body.String())
		}
	})

	t.Run("runs on until one responds", func(t *testing.T) {
		var ran bool
		y := newCatchTestServer()
		y.Catch(passThrough(&ran))
		y.Middleware(respond("second"), CatchMiddleware) // used to be ignored

		w := serve(y, httptest.NewRequest("GET", "/nope", nil))
		if !ran || w.Body.String() != "second" {
			t.Errorf("first ran: %v, body %q; want true and second", ran, w.Body.String())
		}
	})

	t.Run("no response falls through to 404", func(t *testing.T) {
		var ran bool
		y := newCatchTestServer()
		y.Catch(passThrough(&ran))

		if w := serve(y, httptest.NewRequest("GET", "/nope", nil)); !ran || w.Code != http.StatusNotFound {
			t.Errorf("ran %v, status %d; want true and 404 (used to be an empty 200)", ran, w.Code)
		}
	})

	t.Run("an error aborts", func(t *testing.T) {
		y := newCatchTestServer()
		y.Catch(func(req *Request, res *Response) (int, error) {
			return http.StatusForbidden, errors.New("blocked")
		})
		y.Catch(respond("second"))

		if w := serve(y, httptest.NewRequest("GET", "/nope", nil)); w.Code != http.StatusForbidden {
			t.Errorf("status %d, want 403", w.Code)
		}
	})

	t.Run("serving a file counts as a response", func(t *testing.T) {
		index := filepath.Join(t.TempDir(), "index.html")
		os.WriteFile(index, []byte("<html>app</html>"), 0644)

		y := newCatchTestServer()
		y.Catch(func(req *Request, res *Response) (int, error) {
			res.File(index) // e.g. a single-page app fallback
			return http.StatusOK, nil
		})
		y.Catch(respond("second"))

		if w := serve(y, httptest.NewRequest("GET", "/some/client/route", nil)); w.Body.String() != "<html>app</html>" {
			t.Errorf("body %q, want the index file", w.Body.String())
		}
	})
}

// An Authorization header that isn't "Bearer ..." used to panic the request.
func TestNonBearerAuthorizationHeader(t *testing.T) {
	y := newCatchTestServer()
	y.Get("/hello", func(req *Request, res *Response) { res.Text("hi") })

	r := httptest.NewRequest("GET", "/hello", nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	if w := serve(y, r); w.Code != http.StatusOK || w.Body.String() != "hi" {
		t.Errorf("status %d, body %q; want 200 and hi", w.Code, w.Body.String())
	}
}

func TestOpenSQLUnreachableServer(t *testing.T) {
	var cfg mainConfig.YekongaConfig
	cfg.Database.Host = "127.0.0.1"
	cfg.Database.Port = "1" // nothing listens here
	cfg.Database.DatabaseName = "test"
	cfg.Database.ConnectTimeoutSeconds = 1

	dc := NewDatabaseConnections(&cfg)
	dc.mysqlConnect()
	dc.sqlConnect()

	if dc.mysqlClient == nil || dc.sqlClient == nil {
		t.Fatal("expected connection pools even while the server is unreachable")
	}

	dc.mysqlClose()
	dc.sqlClose()
}
