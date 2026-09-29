//! File uploads and downloads (port of the `/upload`, `/upload-files`,
//! `/excel-to-csv` and `/download/:filename.:ext` routes in
//! `yekonga/initializer_other_routes.go`).
//!
//! Uploaded files are saved under `public/uploads` with a random name; the
//! response is `{"status": "success", "files": [url…]}` with each file's public
//! URL. Images (`png`/`jpg`/`jpeg`/`webp`) are resized to fit 1400px wide and
//! re-encoded as WebP (Go's upload resize); other files keep their original
//! extension. Downloads serve `public/tmp/<name>.<ext>` as an attachment, and
//! `/excel-to-csv` returns the workbook's first sheet as CSV.

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

    app.all("/excel-to-csv", |req, res| async move {
        excel_to_csv(req, res).await;
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

/// The upload image resize box (Go's `MaxWidth`) and WebP quality.
const IMAGE_MAX_WIDTH: u32 = 1400;
const WEBP_QUALITY: f32 = 80.0;

/// Re-encodes an uploaded image as WebP, shrinking it to fit `IMAGE_MAX_WIDTH`
/// wide (preserving aspect, never upscaling), like Go's upload resize. Returns
/// `None` when the file isn't a supported image or can't be decoded/encoded, so
/// the caller keeps the original bytes. `ext` is the original extension,
/// leading dot included.
fn resize_to_webp(ext: &str, data: &[u8]) -> Option<Vec<u8>> {
    if !matches!(
        ext.to_ascii_lowercase().as_str(),
        ".png" | ".jpg" | ".jpeg" | ".webp"
    ) {
        return None;
    }
    let image = image::load_from_memory(data).ok()?;
    let (width, height) = (image.width(), image.height());
    let image = if width > IMAGE_MAX_WIDTH {
        // fitBox: scale down to the max width, preserving aspect ratio.
        let scaled_height = (height as f64 * IMAGE_MAX_WIDTH as f64 / width as f64).round() as u32;
        image.resize_exact(
            IMAGE_MAX_WIDTH,
            scaled_height.max(1),
            image::imageops::FilterType::Triangle,
        )
    } else {
        image
    };
    let encoder = webp::Encoder::from_image(&image).ok()?;
    Some(encoder.encode(WEBP_QUALITY).to_vec())
}

/// The multipart boundary of the request, if it is multipart/form-data.
fn boundary(req: &Request) -> Option<String> {
    req.header("content-type")
        .split("boundary=")
        .nth(1)
        .map(|b| b.trim_matches('"').to_string())
}

/// Reads the uploaded workbook's first sheet and returns it as CSV text (Go's
/// `/excel-to-csv`).
async fn excel_to_csv(req: Request, res: Response) {
    let Some(boundary) = boundary(&req) else {
        res.status(415).text("Expected multipart/form-data");
        return;
    };
    let body = req.raw_body().clone();
    let mut multipart = multer::Multipart::new(
        stream::once(async move { Ok::<Bytes, std::io::Error>(body) }),
        boundary,
    );

    let mut data = None;
    while let Ok(Some(part)) = multipart.next_field().await {
        if part.name() == Some("file") {
            data = part.bytes().await.ok();
            break;
        }
    }
    let Some(data) = data else {
        res.status(400).text("Error retrieving the file");
        return;
    };

    match workbook_to_csv(&data) {
        Ok(csv) => res.json(&json!({"status": "success", "csv": csv})),
        Err(err) => res
            .status(400)
            .json(&json!({"status": "error", "error": err})),
    };
}

/// Converts the first worksheet of an xlsx/xls workbook to CSV text.
fn workbook_to_csv(data: &[u8]) -> std::result::Result<String, String> {
    use calamine::{Data, Reader};

    let cursor = std::io::Cursor::new(data.to_vec());
    let mut workbook = calamine::open_workbook_auto_from_rs(cursor)
        .map_err(|e| format!("invalid workbook: {e}"))?;
    let Some(range) = workbook.worksheet_range_at(0) else {
        return Ok(String::new());
    };
    let range = range.map_err(|e| format!("could not read the sheet: {e}"))?;

    let mut out = String::new();
    for row in range.rows() {
        let fields: Vec<String> = row
            .iter()
            .map(|cell| match cell {
                Data::Empty => String::new(),
                Data::String(s) => s.clone(),
                Data::Float(f) => format_number(*f),
                Data::Int(i) => i.to_string(),
                Data::Bool(b) => b.to_string(),
                Data::DateTime(d) => d.to_string(),
                other => other.to_string(),
            })
            .collect();
        out.push_str(&csv_row(&fields));
        out.push('\n');
    }
    Ok(out)
}

/// A float as a plain (non-scientific) string, with whole numbers as integers.
fn format_number(value: f64) -> String {
    if value.fract() == 0.0 && value.abs() < 1e15 {
        format!("{}", value as i64)
    } else {
        value.to_string()
    }
}

/// One CSV record with RFC 4180 quoting, as Go's `encoding/csv` writes it.
fn csv_row(fields: &[String]) -> String {
    fields
        .iter()
        .map(|field| {
            if field.contains([',', '"', '\n', '\r']) {
                format!("\"{}\"", field.replace('"', "\"\""))
            } else {
                field.clone()
            }
        })
        .collect::<Vec<_>>()
        .join(",")
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

        // Images are resized to fit 1400px wide and re-encoded as WebP (Go's
        // upload path); anything else is stored as uploaded. On a decode/encode
        // failure the original is kept.
        let hex = new_object_id();
        let (saved, bytes): (String, Vec<u8>) = match resize_to_webp(&ext, &data) {
            Some(webp) => (format!("{hex}.webp"), webp),
            None => (format!("{hex}{ext}"), data.to_vec()),
        };
        if tokio::fs::write(upload_dir.join(&saved), &bytes)
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
