package yekonga

import (
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
)

//go:embed static/*
var StaticFS embed.FS

type MIMEHeader map[string][]string

// Response represents an HTTP response wrapper
type Response struct {
	httpResponseWriter *http.ResponseWriter
	staticConfig       []*StaticConfig
	request            *Request
	headers            MIMEHeader
	statusCode         int
	gz                 *gzip.Writer
	wroteHeader        bool
}

// gzipMinBytes is the smallest body worth compressing: below this the gzip
// framing and CPU cost outweigh the bytes saved.
const gzipMinBytes = 1024

// Compressors are large (hundreds of KB), so they're reused across responses.
var gzipWriterPool = sync.Pool{
	New: func() interface{} { return gzip.NewWriter(io.Discard) },
}

// Response methods
func (res *Response) Status(code int) {
	res.statusCode = code
}

func (res *Response) Header() http.Header {
	return (*res.httpResponseWriter).Header()
}

func (res *Response) ResetHeaders() {
	for k, v := range res.headers {
		for _, s := range v {
			(*res.httpResponseWriter).Header().Set(k, s)
		}
	}
}

func (res *Response) SetHeader(key string, value string) {
	if v, ok := res.headers[key]; !ok {
		res.headers[key] = append(v, value)
	} else {
		res.headers[key] = []string{value}
	}
}

// WriteHeader sends the status and headers now. The body is then never
// compressed: whoever called this (e.g. http.ServeContent) may already have
// set Content-Length for the uncompressed body.
func (res *Response) WriteHeader(statusCode int) {
	if res.wroteHeader {
		return
	}

	res.statusCode = statusCode
	res.ResetHeaders()
	(*res.httpResponseWriter).WriteHeader(statusCode)
	res.wroteHeader = true
}

func (res *Response) Abort(code int, message string) {
	var contentUrl string

	if res.request != nil && res.request.App != nil {
		res.request.App.recordErrorResponse(res.request, code)
	}

	isJson := (strings.Contains(res.request.GetHeader("content-type"), "json"))

	if !isJson {
		isJson = (strings.Contains(res.request.GetHeader("accept"), "json"))
	}

	switch code {
	case 400:
		if helper.IsEmpty(message) {
			message = "400 Bad Request"
		}

		contentUrl = "static/400.html"
	case 401:
		if helper.IsEmpty(message) {
			message = "401 Unauthorized"
		}

		contentUrl = "static/401.html"
	case 403:
		if helper.IsEmpty(message) {
			message = "403 forbidden"
		}

		contentUrl = "static/403.html"
	case 404:
		if helper.IsEmpty(message) {
			message = "404 Page Not Found"
		}

		contentUrl = "static/404.html"
	case 500:
		if helper.IsEmpty(message) {
			message = "500 Internal Server Error"
		}

		contentUrl = "static/500.html"
	}

	logAbort(code, message)
	res.Status(code)

	if isJson {
		res.Json(map[string]interface{}{
			"status": code,
			"error":  message,
		})
		return
	} else if helper.IsNotEmpty(contentUrl) {
		if code == http.StatusTemporaryRedirect || code == http.StatusPermanentRedirect {
			res.Redirect(message)
		} else {
			content, err := StaticFS.ReadFile(contentUrl)
			if err != nil {
				res.Text(message)
			} else {
				res.Html(string(content))
			}
		}

		return
	} else {
		if code == http.StatusTemporaryRedirect || code == http.StatusPermanentRedirect {
			res.Redirect(message)
			return
		}
	}

	res.Text(message)
}

func (res *Response) acceptsGzip() bool {
	for _, enc := range strings.Split(res.request.HttpRequest.Header.Get("accept-encoding"), ",") {
		if strings.TrimSpace(strings.SplitN(enc, ";", 2)[0]) == "gzip" {
			return true
		}
	}
	return false
}

// shouldGzip decides, before the headers go out, whether this response is
// compressed. first is the first chunk of the body.
func (res *Response) shouldGzip(first []byte) bool {
	if len(first) < gzipMinBytes || !res.acceptsGzip() {
		return false
	}

	header := (*res.httpResponseWriter).Header()

	// Already encoded, or sized for the uncompressed body.
	return header.Get("content-encoding") == "" && header.Get("content-length") == ""
}

func (res *Response) Write(data []byte) (int, error) {
	if !res.wroteHeader {
		res.SetHeader("yekonga-application", "Yesu")
		res.ResetHeaders()

		if res.shouldGzip(data) {
			header := (*res.httpResponseWriter).Header()
			header.Set("content-encoding", "gzip")
			header.Add("vary", "accept-encoding")

			gz := gzipWriterPool.Get().(*gzip.Writer)
			gz.Reset(*res.httpResponseWriter)
			res.gz = gz
		}

		(*res.httpResponseWriter).WriteHeader(res.statusCode)
		res.wroteHeader = true
	}

	if res.gz != nil {
		return res.gz.Write(data)
	}

	return (*res.httpResponseWriter).Write(data)
}

// Close must be called when the response is done to flush the gzip stream.
func (res *Response) Close() error {
	if res.gz == nil {
		return nil
	}

	err := res.gz.Close()
	gzipWriterPool.Put(res.gz)
	res.gz = nil

	return err
}

func (res *Response) Text(data string) {
	res.SetHeader("content-type", "text/plain")

	res.Write([]byte(data))
}

func (res *Response) Byte(data []byte) {
	res.Write([]byte(data))
}

func (res *Response) Send(data string) {
	res.Write([]byte(data))
}

func (res *Response) Html(data string) {
	res.SetHeader("Content-Type", "text/html")

	res.Write([]byte(data))
}

func (res *Response) Json(data interface{}) {
	res.SetHeader("Content-Type", "application/json")
	res.ResetHeaders()

	// json.NewEncoder((*res.httpResponseWriter)).Encode(data)
	res.Write([]byte(helper.ToJson(data)))
}

func (res *Response) Redirect(url string) {
	console.Success("redirect", res.statusCode, url)

	http.Redirect(res, res.request.HttpRequest, url, res.statusCode)
}

func (res *Response) File(filePath string) {
	count := len(res.staticConfig)

	if helper.FileExists(filePath) {
		// // Set cache headers
		res.SetHeader("cache-control", fmt.Sprintf("max-age=%d", 7))
		res.ResetHeaders()

		file, err := os.Open(filePath)
		if err != nil {
			http.Error(res, "File not found", 404)
			return
		}
		defer file.Close()

		stat, _ := file.Stat()
		// ServeContent handles Range requests and Content-Length perfectly
		http.ServeContent(res, res.request.HttpRequest, stat.Name(), stat.ModTime(), file)
		return
	} else {
		for i := 0; i < count; i++ {
			static := res.staticConfig[i]

			if static != nil {
				filePath := helper.GetPath(filepath.Join(static.Directory, filePath))
				// console.Log("File", filePath)
				// console.Log("File", helper.GetPath(filePath))

				if helper.FileExists(filePath) {
					// // Set cache headers
					res.SetHeader("cache-control", fmt.Sprintf("max-age=%d", static.CacheMaxAge))
					res.ResetHeaders()

					file, err := os.Open(filePath)
					if err != nil {
						http.Error(res, "File not found", 404)
						return
					}
					defer file.Close()

					stat, _ := file.Stat()
					// ServeContent handles Range requests and Content-Length perfectly
					http.ServeContent(res, res.request.HttpRequest, stat.Name(), stat.ModTime(), file)
					return
				}
			}
		}
	}

	res.Abort(404, "")
}

func (res *Response) Download(filename string, name string) {
	CacheMaxAge := false

	if helper.FileExists(filename) {
		if helper.IsEmpty(name) {
			name = filepath.Base(filename)
		}
		// === 2. Validate file exists ===
		if _, err := os.Stat(filename); os.IsNotExist(err) {
			http.Error(res, "Config file not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(res, "Error accessing file", http.StatusInternalServerError)
			return
		}

		// === 3. Open file ===
		file, err := os.Open(filename)
		if err != nil {
			http.Error(res, "Failed to open file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// === 4. Get file info ===
		stat, err := file.Stat()
		if err != nil {
			http.Error(res, "Failed to read file info", http.StatusInternalServerError)
			return
		}

		// === 5. Set headers ===
		// Force download (not browser preview)
		res.SetHeader("content-disposition", fmt.Sprintf(`attachment; filename="%s"`, name))

		// MIME type (auto-detect)
		if mimeType := mime.TypeByExtension(filepath.Ext(name)); mimeType != "" {
			res.SetHeader("content-type", mimeType)
		} else {
			res.SetHeader("content-type", "application/octet-stream")
		}

		if !CacheMaxAge {
			// Cache control: short cache or no-cache
			res.SetHeader("cache-control", "max-age=1, must-revalidate") // or "no-cache, no-store"
		}

		// Optional security headers
		res.SetHeader("x-content-type-options", "nosniff")
		res.ResetHeaders()

		// === 6. Serve file efficiently (zero-copy) ===
		http.ServeContent(res, res.request.HttpRequest, filename, stat.ModTime(), file)
		return
	}

	res.Abort(404, "")
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
}

func (g gzipResponseWriter) Write(b []byte) (int, error) {
	return g.Writer.Write(b)
}
