//! File uploads and downloads over HTTP.

use std::sync::{Arc, OnceLock};

use axum::body::Body;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tempfile::TempDir;
use tower::ServiceExt;
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

/// A temp directory used as the working directory, so uploads land in an
/// isolated `public/` instead of the repository. Kept alive for the process.
static ROOT: OnceLock<TempDir> = OnceLock::new();

fn setup() -> &'static TempDir {
    let dir = ROOT.get_or_init(|| {
        let dir = tempfile::tempdir().unwrap();
        std::env::set_current_dir(dir.path()).unwrap();
        std::fs::create_dir_all(dir.path().join("public/tmp")).unwrap();
        dir
    });
    dir
}

fn app() -> Yekonga {
    let config: YekongaConfig =
        serde_json::from_value(json!({"authentication": {"secretToken": "s"}})).unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    )
}

/// A multipart/form-data body with the given (field, filename, content) parts.
fn multipart(parts: &[(&str, &str, &[u8])]) -> (String, Vec<u8>) {
    let boundary = "YEKONGABOUNDARY";
    let mut body = Vec::new();
    for (field, filename, content) in parts {
        body.extend_from_slice(format!("--{boundary}\r\n").as_bytes());
        body.extend_from_slice(
            format!(
                "Content-Disposition: form-data; name=\"{field}\"; filename=\"{filename}\"\r\n\r\n"
            )
            .as_bytes(),
        );
        body.extend_from_slice(content);
        body.extend_from_slice(b"\r\n");
    }
    body.extend_from_slice(format!("--{boundary}--\r\n").as_bytes());
    (format!("multipart/form-data; boundary={boundary}"), body)
}

async fn call(app: &Yekonga, request: HttpRequest<Body>) -> (u16, Value, Vec<u8>) {
    let response = app.router().oneshot(request).await.unwrap();
    let status = response.status().as_u16();
    let bytes = response
        .into_body()
        .collect()
        .await
        .unwrap()
        .to_bytes()
        .to_vec();
    let json = serde_json::from_slice(&bytes).unwrap_or(Value::Null);
    (status, json, bytes)
}

#[tokio::test]
async fn upload_single_file() {
    setup();
    let app = app();
    let (content_type, body) = multipart(&[("file", "photo.PNG", b"image-bytes")]);
    let request = HttpRequest::post("/upload")
        .header("content-type", content_type)
        .header("host", "shop.tz")
        .body(Body::from(body))
        .unwrap();

    let (status, json, _) = call(&app, request).await;
    assert_eq!(status, 200, "{json}");
    assert_eq!(json["status"], "success");
    let files = json["files"].as_array().unwrap();
    assert_eq!(files.len(), 1);
    let url = files[0].as_str().unwrap();
    assert!(url.starts_with("https://shop.tz/uploads/"), "{url}");
    assert!(url.ends_with(".PNG"), "keeps the original extension: {url}");

    // The file was written under public/uploads.
    let saved = url.rsplit('/').next().unwrap();
    let path = std::env::current_dir()
        .unwrap()
        .join("public/uploads")
        .join(saved);
    assert_eq!(std::fs::read(path).unwrap(), b"image-bytes");
}

/// Encodes a solid-colour PNG of the given size.
fn png(width: u32, height: u32) -> Vec<u8> {
    let image = image::DynamicImage::ImageRgb8(image::RgbImage::from_pixel(
        width,
        height,
        image::Rgb([12, 34, 56]),
    ));
    let mut buffer = std::io::Cursor::new(Vec::new());
    image
        .write_to(&mut buffer, image::ImageFormat::Png)
        .unwrap();
    buffer.into_inner()
}

fn saved_bytes(url: &str) -> Vec<u8> {
    let name = url.rsplit('/').next().unwrap();
    let path = std::env::current_dir()
        .unwrap()
        .join("public/uploads")
        .join(name);
    std::fs::read(path).unwrap()
}

#[tokio::test]
async fn upload_resizes_a_wide_image_to_webp() {
    setup();
    let app = app();
    let (content_type, body) = multipart(&[("file", "banner.png", &png(2000, 500))]);
    let request = HttpRequest::post("/upload")
        .header("content-type", content_type)
        .header("host", "shop.tz")
        .body(Body::from(body))
        .unwrap();

    let (status, json, _) = call(&app, request).await;
    assert_eq!(status, 200, "{json}");
    let url = json["files"][0].as_str().unwrap();
    assert!(url.ends_with(".webp"), "image is re-encoded as webp: {url}");

    // Saved as a valid WebP, shrunk to fit 1400px wide (aspect preserved).
    let decoded = image::load_from_memory(&saved_bytes(url)).unwrap();
    assert_eq!(decoded.width(), 1400);
    assert_eq!(decoded.height(), 350);
}

#[tokio::test]
async fn upload_keeps_a_small_image_size() {
    setup();
    let app = app();
    let (content_type, body) = multipart(&[("file", "avatar.jpg", &png(300, 200))]);
    let request = HttpRequest::post("/upload")
        .header("content-type", content_type)
        .header("host", "shop.tz")
        .body(Body::from(body))
        .unwrap();

    let (status, json, _) = call(&app, request).await;
    assert_eq!(status, 200, "{json}");
    let url = json["files"][0].as_str().unwrap();
    assert!(url.ends_with(".webp"), "{url}");

    // Below the max width, so its dimensions are unchanged (never upscaled).
    let decoded = image::load_from_memory(&saved_bytes(url)).unwrap();
    assert_eq!((decoded.width(), decoded.height()), (300, 200));
}

#[tokio::test]
async fn upload_multiple_files() {
    setup();
    let app = app();
    let (content_type, body) = multipart(&[("files", "a.txt", b"one"), ("files", "b.txt", b"two")]);
    let request = HttpRequest::post("/upload-files")
        .header("content-type", content_type)
        .header("host", "shop.tz")
        .body(Body::from(body))
        .unwrap();

    let (status, json, _) = call(&app, request).await;
    assert_eq!(status, 200, "{json}");
    assert_eq!(json["files"].as_array().unwrap().len(), 2);
}

#[tokio::test]
async fn upload_rejects_non_multipart() {
    setup();
    let app = app();
    let request = HttpRequest::post("/upload")
        .header("content-type", "application/json")
        .header("host", "shop.tz")
        .body(Body::from("{}"))
        .unwrap();
    assert_eq!(call(&app, request).await.0, 415);
}

#[tokio::test]
async fn download_serves_a_temp_file() {
    let dir = setup();
    std::fs::write(dir.path().join("public/tmp/report.csv"), b"a,b,c\n1,2,3\n").unwrap();
    let app = app();

    let request = HttpRequest::get("/download/report.csv?title=Sales.csv")
        .header("host", "shop.tz")
        .body(Body::empty())
        .unwrap();
    let response = app.router().oneshot(request).await.unwrap();
    assert_eq!(response.status(), 200);
    let disposition = response
        .headers()
        .get("content-disposition")
        .unwrap()
        .to_str()
        .unwrap()
        .to_string();
    assert!(disposition.contains("Sales.csv"), "{disposition}");
    let body = response.into_body().collect().await.unwrap().to_bytes();
    assert_eq!(&body[..], b"a,b,c\n1,2,3\n");
}

#[tokio::test]
async fn download_missing_file_is_404() {
    setup();
    let app = app();
    let request = HttpRequest::get("/download/nope.txt")
        .header("host", "shop.tz")
        .body(Body::empty())
        .unwrap();
    assert_eq!(app.router().oneshot(request).await.unwrap().status(), 404);
}

#[tokio::test]
async fn excel_to_csv_converts_the_first_sheet() {
    setup();
    let app = app();

    // Build a small workbook: a header row, a numeric cell, and a value that
    // needs CSV quoting.
    let mut workbook = rust_xlsxwriter::Workbook::new();
    let sheet = workbook.add_worksheet();
    sheet.write_string(0, 0, "name").unwrap();
    sheet.write_string(0, 1, "qty").unwrap();
    sheet.write_string(1, 0, "Acme, Inc").unwrap();
    sheet.write_number(1, 1, 42.0).unwrap();
    let xlsx = workbook.save_to_buffer().unwrap();

    let (content_type, body) = multipart(&[("file", "data.xlsx", &xlsx)]);
    let request = HttpRequest::post("/excel-to-csv")
        .header("content-type", content_type)
        .header("host", "shop.tz")
        .body(Body::from(body))
        .unwrap();
    let (status, json, _) = call(&app, request).await;
    assert_eq!(status, 200, "{json}");
    assert_eq!(json["status"], "success");
    assert_eq!(json["csv"], "name,qty\n\"Acme, Inc\",42\n");
}
