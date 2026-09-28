package yekonga

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// countingWriter records how many times the header was written.
type countingWriter struct {
	*httptest.ResponseRecorder
	headerWrites int
}

func (w *countingWriter) WriteHeader(code int) {
	w.headerWrites++
	w.ResponseRecorder.WriteHeader(code)
}

func newTestResponse(acceptGzip bool) (*Response, *countingWriter) {
	r := httptest.NewRequest("GET", "/file.txt", nil)
	if acceptGzip {
		r.Header.Set("Accept-Encoding", "gzip, deflate")
	}

	recorder := &countingWriter{ResponseRecorder: httptest.NewRecorder()}
	var w http.ResponseWriter = recorder

	return &Response{
		httpResponseWriter: &w,
		request:            &Request{HttpRequest: r},
		headers:            make(MIMEHeader),
		statusCode:         http.StatusOK,
	}, recorder
}

func body(t *testing.T, w *countingWriter) string {
	t.Helper()

	if w.Header().Get("Content-Encoding") != "gzip" {
		return w.Body.String()
	}

	reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("invalid gzip body: %v", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("invalid gzip body: %v", err)
	}
	return string(out)
}

func TestResponseCompressesOnlyLargeBodies(t *testing.T) {
	large := strings.Repeat("yekonga ", 500)

	tests := []struct {
		name     string
		accept   bool
		body     string
		wantGzip bool
	}{
		{"small body", true, `{"ok":true}`, false},
		{"large body", true, large, true},
		{"client without gzip", false, large, false},
	}

	for i := 0; i < 2; i++ { // the second round reuses pooled compressors
		for _, tt := range tests {
			res, w := newTestResponse(tt.accept)
			res.Text(tt.body)
			res.Close()

			if gotGzip := w.Header().Get("Content-Encoding") == "gzip"; gotGzip != tt.wantGzip {
				t.Errorf("%s: gzip = %v, want %v", tt.name, gotGzip, tt.wantGzip)
			}
			if got := body(t, w); got != tt.body {
				t.Errorf("%s: body changed (%d bytes, want %d)", tt.name, len(got), len(tt.body))
			}
		}
	}
}

func TestResponseWritesHeaderOnce(t *testing.T) {
	res, w := newTestResponse(true)
	res.Status(http.StatusCreated)

	res.Write([]byte(strings.Repeat("a", 2000)))
	res.Write([]byte(strings.Repeat("b", 10)))
	res.Close()

	if w.headerWrites != 1 {
		t.Errorf("header written %d times, want 1", w.headerWrites)
	}
	if w.Code != http.StatusCreated {
		t.Errorf("status %d, want 201", w.Code)
	}
	if got := body(t, w); got != strings.Repeat("a", 2000)+strings.Repeat("b", 10) {
		t.Errorf("body corrupted across writes")
	}
}

// http.ServeContent (used by Download) sets Content-Length and writes the
// header itself. The body used to be gzipped after that, without a
// Content-Encoding header, corrupting every download for browsers.
func TestResponseServeContentIsNotCompressed(t *testing.T) {
	content := strings.Repeat("file contents ", 400)

	res, w := newTestResponse(true)
	http.ServeContent(res, res.request.HttpRequest, "file.txt", time.Time{}, strings.NewReader(content))
	res.Close()

	if w.Header().Get("Content-Encoding") != "" {
		t.Fatal("ServeContent response was compressed")
	}
	if w.Body.String() != content {
		t.Fatalf("body differs from the file (%d bytes, want %d)", w.Body.Len(), len(content))
	}
	if w.Header().Get("Content-Length") != "5600" {
		t.Errorf("Content-Length = %q, want 5600", w.Header().Get("Content-Length"))
	}
}

func BenchmarkResponseJSON(b *testing.B) {
	payload := map[string]interface{}{"items": strings.Repeat("x", 4000)}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, _ := newTestResponse(true)
		res.Json(payload)
		res.Close()
	}
}
