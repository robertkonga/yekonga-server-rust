//! File uploads and downloads (port of the `/upload`, `/upload-files`,
//! `/excel-to-csv` and `/download/:filename.:ext` routes in
//! `yekonga/initializer_other_routes.go`).
//!
//! Uploaded files are saved under `public/uploads` with a random name and the
//! original extension; the response is `{"status": "success", "files": [url…]}`
//! with each file's public URL. Downloads serve `public/tmp/<name>.<ext>` as
//! an attachment.
//!
//! Image resizing/WebP conversion (Go resizes images on upload) and the Excel
//! to CSV conversion aren't ported: files are stored as uploaded, and
//! `/excel-to-csv` returns "not supported by the Rust port yet".

use std::path::{Path, PathBuf};

use bytes::Bytes;
use futures_util::stream;
use serde_json::json;

use crate::app::Yekonga;
use crate::db::values::new_object_id;
use crate::helper::get_base_url;
use crate::request::Request;
use crate::response::Response;

/// Registers the upload and download routes.
pub(crate) fn register_routes(app: &Yekonga) {
    for path in ["/upload", "/upload-files"] {
        app.all(path, |req, res| async move {
            let multiple = path_is_multiple(&req);
            upload(req, res, multiple).await;
        });
    }

    app.all("/excel-to-csv", |_req, res| async move {
        res.status(501).json(&json!({
            "status": "error",
            "error": "excel-to-csv is not supported by the Rust port yet",
        }));
    });

    app.all("/download/:filename.:ext", |req, res| async move {
        let name = format!("{}.{}", req.param("filename"), req.param("ext"));
        let Some(public) = public_dir(&req) else {
            res.abort(404, "");
            return;
        };
        let file = public.join("tmp").join(&name);
        if !is_within(&public, &file) {
            res.abort(404, "");
            return;
        }
        res.download(file, &req.query("title"));
    });
}

/// The `/upload-files` route accepts many files under `files`; `/upload`
/// accepts one under `file`.
fn path_is_multiple(req: &Request) -> bool {
    req.path().trim_end_matches('/').ends_with("upload-files")
}

async fn upload(req: Request, res: Response, multiple: bool) {
    let content_type = req.header("content-type");
    let Some(boundary) = content_type
        .split("boundary=")
        .nth(1)
        .map(|b| b.trim_matches('"').to_string())
    else {
        res.status(415).text("Expected multipart/form-data");
        return;
    };

    let Some(public) = public_dir(&req) else {
        res.status(500).text("Error saving the file");
        return;
    };
    let upload_dir = public.join("uploads");
    if tokio::fs::create_dir_all(&upload_dir).await.is_err() {
        res.status(500).text("Error saving the file");
        return;
    }

    let field = if multiple { "files" } else { "file" };
    let body = req.raw_body().clone();
    let mut multipart = multer::Multipart::new(
        stream::once(async move { Ok::<Bytes, std::io::Error>(body) }),
        boundary,
    );

    let config = req.app().config();
    let host = req.client().map(|c| c.origin_domain()).unwrap_or_default();
    let mut urls: Vec<String> = Vec::new();

    while let Ok(Some(part)) = multipart.next_field().await {
        if part.name() != Some(field) {
            continue;
        }
        let ext = part
            .file_name()
            .and_then(|n| {
                Path::new(n)
                    .extension()
                    .map(|e| e.to_string_lossy().into_owned())
            })
            .map(|e| format!(".{e}"))
            .unwrap_or_default();
        let Ok(data) = part.bytes().await else {
            res.status(400).text("Error retrieving the file");
            return;
        };

        let saved = format!("{}{ext}", new_object_id());
        if tokio::fs::write(upload_dir.join(&saved), &data)
            .await
            .is_err()
        {
            res.status(500).text("Error saving the file");
            return;
        }
        urls.push(get_base_url(
            &format!("uploads/{saved}"),
            &host,
            &config.base_url,
            config.ports.server as u16,
        ));

        if !multiple {
            break;
        }
    }

    if urls.is_empty() {
        res.status(400).text("Error retrieving the file");
        return;
    }
    res.json(&json!({"status": "success", "files": urls}));
}

/// The `public` directory: relative to the working directory, else the
/// executable's directory.
fn public_dir(req: &Request) -> Option<PathBuf> {
    let relative = PathBuf::from("public");
    if relative.is_dir() {
        return Some(relative);
    }
    let candidate = req.app().root_path().join("public");
    candidate.is_dir().then_some(candidate)
}

/// Whether `path`, once resolved, stays inside `base` (guards the download
/// route against `..` traversal).
fn is_within(base: &Path, path: &Path) -> bool {
    match (base.canonicalize(), path.canonicalize()) {
        (Ok(base), Ok(path)) => path.starts_with(base),
        // The file doesn't exist yet / can't be resolved: fall back to a
        // lexical check on the untrusted segment.
        _ => path.starts_with(base) && !path.components().any(|c| c.as_os_str() == ".."),
    }
}
