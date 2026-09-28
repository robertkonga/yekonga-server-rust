package yekonga

import (
	"net/http"
	"testing"

	mainConfig "github.com/robertkonga/yekonga-server-go/config"
)

func newTestRouter(paths ...string) *YekongaData {
	y := &YekongaData{
		Config: &mainConfig.YekongaConfig{},
		routes: make(map[string][]Route),
	}

	for _, p := range paths {
		y.Get(p, func(req *Request, res *Response) {})
	}

	return y
}

func TestFindRoute(t *testing.T) {
	y := newTestRouter("/graphql", "/api/:model", "/api/:model/:id", "/me/:moduleName?", "/image/:w/:h/:file")

	tests := []struct {
		path    string
		pattern string
		params  map[string]string
	}{
		{"/graphql", "^/graphql$", map[string]string{}},
		{"/api/orders", "", map[string]string{"model": "orders"}},
		{"/api/orders/abc123", "", map[string]string{"model": "orders", "id": "abc123"}},
		{"/me", "", map[string]string{"moduleName": ""}},
		{"/me/admin", "", map[string]string{"moduleName": "admin"}},
		{"/image/100/200/logo", "", map[string]string{"w": "100", "h": "200", "file": "logo"}},
	}

	for _, tt := range tests {
		route, params := y.findRoute(http.MethodGet, tt.path)
		if route == nil {
			t.Fatalf("%s: no route matched", tt.path)
		}

		if route.re == nil {
			t.Fatalf("%s: route regex was not compiled at registration", tt.path)
		}

		for k, want := range tt.params {
			if got := params[k]; got != want {
				t.Errorf("%s: param %q = %q, want %q", tt.path, k, got, want)
			}
		}
	}

	for _, path := range []string{"/nope", "/api/orders/abc/extra", "/graphqlx"} {
		if route, _ := y.findRoute(http.MethodGet, path); route != nil {
			t.Errorf("%s: unexpectedly matched %s", path, route.pattern)
		}
	}

	if route, _ := y.findRoute(http.MethodPost, "/graphql"); route != nil {
		t.Errorf("POST /graphql matched a GET-only route")
	}
}

func BenchmarkFindRoute(b *testing.B) {
	paths := []string{"/graphql", "/graphql-auth", "/api", "/api/:model", "/api/:model/:id", "/me", "/me/:moduleName",
		"/logout", "/logout/:moduleName", "/refresh", "/refresh/:moduleName", "/languages", "/translations/:lang",
		"/upload", "/upload-files", "/download/:file", "/image/:w/:h/:file", "/config", "/config/data", "/config/report",
		"/permissions", "/tenant", "/tenant-config", "/theme.css", "/custom-style.css", "/excel-to-csv",
		"/payments/webhook/:provider", "/payments/create", "/auth/login", "/auth/register", "/auth/otp", "/auth/reset",
		"/users/:id", "/reports/:name", "/files/:id", "/health", "/orders/:id", "/orders", "/products", "/products/:id"}
	y := newTestRouter(paths...)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		y.findRoute(http.MethodGet, "/products/abc123")
	}
}
