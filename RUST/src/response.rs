//! The response handle passed to handlers and middleware (port of
//! `yekonga/response.go`).
//!
//! Like [`Request`], a [`Response`] is a clonable handle to shared state. The
//! body is buffered and sent once the handler returns; gzip compression is
//! applied by the server for bodies of 1 KB or more.

use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use axum::body::Body;
use http::header::{HeaderName, HeaderValue, CACHE_CONTROL, LOCATION};
use http::{HeaderMap, StatusCode};
use serde::Serialize;
use tower::ServiceExt;
use tower_http::services::ServeFile;

use crate::request::Request;

pub(crate) static PAGE_INDEX: &str = include_str!("../static/index.html");
pub(crate) static PAGE_400: &str = include_str!("../static/400.html");
pub(crate) static PAGE_401: &str = include_str!("../static/401.html");
pub(crate) static PAGE_403: &str = include_str!("../static/403.html");
pub(crate) static PAGE_404: &str = include_str!("../static/404.html");
pub(crate) static PAGE_500: &str = include_str!("../static/500.html");

#[derive(Clone)]
pub struct Response {
    state: Arc<Mutex<State>>,
    request: Request,
}

struct State {
    status: StatusCode,
    headers: HeaderMap,
    body: Vec<u8>,
    file: Option<PathBuf>,
    written: bool,
}

impl Response {
    pub(crate) fn new(request: Request) -> Self {
        Self {
            state: Arc::new(Mutex::new(State {
                status: StatusCode::OK,
                headers: HeaderMap::new(),
                body: Vec::new(),
                file: None,
                written: false,
            })),
            request,
        }
    }

    /// Sets the status code used when the response is sent.
    pub fn status(&self, code: u16) -> &Self {
        self.lock().status =
            StatusCode::from_u16(code).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
        self
    }

    pub fn status_code(&self) -> u16 {
        self.lock().status.as_u16()
    }

    /// Sets (replaces) a response header. Invalid names or values are ignored.
    pub fn set_header(&self, name: &str, value: &str) -> &Self {
        if let (Ok(name), Ok(value)) = (HeaderName::try_from(name), HeaderValue::try_from(value)) {
            self.lock().headers.insert(name, value);
        }
        self
    }

    /// Adds a header value, keeping existing ones (e.g. several `Set-Cookie`).
    pub fn append_header(&self, name: &str, value: &str) -> &Self {
        if let (Ok(name), Ok(value)) = (HeaderName::try_from(name), HeaderValue::try_from(value)) {
            self.lock().headers.append(name, value);
        }
        self
    }

    pub fn header(&self, name: &str) -> Option<String> {
        self.lock()
            .headers
            .get(name)
            .and_then(|v| v.to_str().ok())
            .map(String::from)
    }

    /// Whether a body has been written (a handler "responded").
    pub fn is_written(&self) -> bool {
        self.lock().written
    }

    /// Appends raw bytes to the body.
    pub fn write(&self, data: &[u8]) {
        let mut state = self.lock();
        state.body.extend_from_slice(data);
        state.written = true;
    }

    pub fn send(&self, data: &str) {
        self.write(data.as_bytes());
    }

    pub fn text(&self, data: &str) {
        self.set_header("content-type", "text/plain");
        self.write(data.as_bytes());
    }

    pub fn html(&self, data: &str) {
        self.set_header("content-type", "text/html");
        self.write(data.as_bytes());
    }

    pub fn json<T: Serialize + ?Sized>(&self, data: &T) {
        match serde_json::to_vec(data) {
            Ok(bytes) => {
                self.set_header("content-type", "application/json");
                self.write(&bytes);
            }
            Err(err) => {
                tracing::error!(%err, "cannot encode JSON response");
                self.status(500).text("500 Internal Server Error");
            }
        }
    }

    /// Redirects to `url` with the current status, or 302 if it isn't a
    /// redirect status.
    pub fn redirect(&self, url: &str) {
        {
            let mut state = self.lock();
            if !state.status.is_redirection() {
                state.status = StatusCode::FOUND;
            }
        }
        self.set_header(LOCATION.as_str(), url);

        let method = self.request.method();
        if method == http::Method::GET || method == http::Method::HEAD {
            let status = self.lock().status;
            if self.header("content-type").is_none() {
                self.set_header("content-type", "text/html; charset=utf-8");
            }
            let url = url
                .replace('&', "&amp;")
                .replace('<', "&lt;")
                .replace('>', "&gt;")
                .replace('"', "&quot;");
            self.send(&format!(
                "<a href=\"{url}\">{}</a>.\n",
                status.canonical_reason().unwrap_or("Redirect")
            ));
        } else {
            self.lock().written = true;
        }
    }

    /// Serves a file (with range and conditional request support). Relative
    /// paths that don't exist are looked up in the static directories.
    pub fn file(&self, path: impl AsRef<Path>) {
        let path = path.as_ref();

        if path.is_file() {
            self.set_header(CACHE_CONTROL.as_str(), "max-age=7");
            self.serve_file(path.to_path_buf());
            return;
        }

        for static_config in self.request.app().static_configs() {
            let candidate = static_config.directory.join(path);
            if candidate.is_file() {
                self.set_header(
                    CACHE_CONTROL.as_str(),
                    &format!("max-age={}", static_config.cache_max_age),
                );
                self.serve_file(candidate);
                return;
            }
        }

        self.abort(404, "");
    }

    /// Sends a file as an attachment named `name` (the file's own name if empty).
    pub fn download(&self, path: impl AsRef<Path>, name: &str) {
        let path = path.as_ref();
        if !path.is_file() {
            self.abort(404, "");
            return;
        }

        let name = match name {
            "" => path
                .file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_default(),
            name => name.to_string(),
        };
        let mime = mime_guess::from_path(&name).first_or_octet_stream();

        self.set_header(
            "content-disposition",
            &format!("attachment; filename=\"{}\"", name.replace('"', "")),
        );
        self.set_header("content-type", mime.as_ref());
        self.set_header("cache-control", "max-age=1, must-revalidate");
        self.set_header("x-content-type-options", "nosniff");
        self.serve_file(path.to_path_buf());
    }

    /// Ends the request with an error: JSON `{status, error}` for JSON
    /// clients, the matching error page for browsers, or a redirect to
    /// `message` for 307/308.
    pub fn abort(&self, code: u16, message: &str) {
        let (default_message, page) = match code {
            400 => ("400 Bad Request", Some(PAGE_400)),
            401 => ("401 Unauthorized", Some(PAGE_401)),
            403 => ("403 forbidden", Some(PAGE_403)),
            404 => ("404 Page Not Found", Some(PAGE_404)),
            500 => ("500 Internal Server Error", Some(PAGE_500)),
            _ => ("", None),
        };
        let message = if message.is_empty() {
            default_message
        } else {
            message
        };

        if code >= 500 {
            tracing::error!(code, message, "abort");
        } else {
            tracing::warn!(code, message, "abort");
        }

        self.status(code);
        let is_redirect = code == 307 || code == 308;

        if self.request.wants_json() {
            self.json(&serde_json::json!({"status": code, "error": message}));
        } else if is_redirect {
            self.redirect(message);
        } else if let Some(page) = page {
            self.html(page);
        } else {
            self.text(message);
        }
    }

    fn serve_file(&self, path: PathBuf) {
        let mut state = self.lock();
        state.file = Some(path);
        state.written = true;
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Builds the HTTP response once the handler is done.
    pub(crate) async fn finish(self) -> http::Response<Body> {
        let (status, mut headers, body, file, written) = {
            let mut state = self.lock();
            (
                state.status,
                std::mem::take(&mut state.headers),
                std::mem::take(&mut state.body),
                state.file.take(),
                state.written,
            )
        };

        if written {
            headers.insert("yekonga-application", HeaderValue::from_static("Yesu"));
        }

        if let Some(path) = file {
            let mut response = serve_file(&self.request, &path).await;
            // Our headers (content-type/disposition, cache-control) win over
            // the file service's guesses.
            for (name, value) in headers.iter() {
                response.headers_mut().insert(name, value.clone());
            }
            return response;
        }

        let mut response = http::Response::new(Body::from(body));
        *response.status_mut() = status;
        *response.headers_mut() = headers;
        response
    }
}

/// Serves a file for `request`, honoring its range and conditional headers.
pub(crate) async fn serve_file(request: &Request, path: &Path) -> http::Response<Body> {
    serve_path(request.method(), request.uri(), request.headers(), path).await
}

/// Serves a file for a request with these parts.
pub(crate) async fn serve_path(
    method: &http::Method,
    uri: &http::Uri,
    headers: &HeaderMap,
    path: &Path,
) -> http::Response<Body> {
    let mut file_request = http::Request::new(Body::empty());
    *file_request.method_mut() = method.clone();
    *file_request.uri_mut() = uri.clone();
    *file_request.headers_mut() = headers.clone();

    match ServeFile::new(path).oneshot(file_request).await {
        Ok(response) => response.map(Body::new),
        Err(err) => match err {},
    }
}
