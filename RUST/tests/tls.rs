//! TLS: the server serves HTTPS with `ports.secure` and redirects plain HTTP.
//! Uses `openssl` to make a self-signed certificate and `curl -k` as the
//! client; skips when either tool is missing.

use std::process::Command;
use std::sync::Arc;
use std::time::Duration;

use serde_json::json;
use tempfile::TempDir;
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

fn have(tool: &str, version_arg: &str) -> bool {
    Command::new(tool)
        .arg(version_arg)
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// A free TCP port (bound then released; good enough for a test).
fn free_port() -> u16 {
    std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
        .port()
}

fn make_cert(dir: &TempDir) {
    let cert_dir = dir.path().join("certificate");
    std::fs::create_dir_all(&cert_dir).unwrap();
    let status = Command::new("openssl")
        .args([
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-days",
            "1",
            "-subj",
            "/CN=localhost",
            "-keyout",
        ])
        .arg(cert_dir.join("key.pem"))
        .arg("-out")
        .arg(cert_dir.join("cert.pem"))
        .output()
        .unwrap();
    assert!(
        status.status.success(),
        "openssl failed: {}",
        String::from_utf8_lossy(&status.stderr)
    );
}

fn curl(args: &[&str]) -> (String, String) {
    let output = Command::new("curl")
        .args(["-s", "--noproxy", "*", "--max-time", "4"])
        .args(args)
        .output()
        .unwrap();
    (
        String::from_utf8_lossy(&output.stdout).into_owned(),
        String::from_utf8_lossy(&output.stderr).into_owned(),
    )
}

// A multi-thread runtime: the blocking `curl` calls must not starve the
// spawned server task.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn serves_https_and_redirects_http() {
    if !have("openssl", "version") || !have("curl", "--version") {
        eprintln!("skipping: openssl or curl not available");
        return;
    }

    let dir = tempfile::tempdir().unwrap();
    make_cert(&dir);
    std::env::set_current_dir(dir.path()).unwrap();

    let https_port = free_port();
    let http_port = free_port();
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "ports": {"secure": true, "sslServer": https_port, "server": http_port}
    }))
    .unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    );

    tokio::spawn(async move {
        if let Err(err) = app.start(None).await {
            eprintln!("server error: {err}");
        }
    });

    // Wait for the HTTPS listener to accept a request.
    let mut body = String::new();
    let mut last_err = String::new();
    for _ in 0..30 {
        let (out, err) = curl(&[
            "--connect-timeout",
            "2",
            "-k",
            &format!("https://127.0.0.1:{https_port}/health"),
        ]);
        last_err = err;
        if out == "Ok!" {
            body = out;
            break;
        }
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    assert_eq!(
        body, "Ok!",
        "HTTPS did not serve /health (curl: {last_err})"
    );

    // Plain HTTP on the redirect port answers with a 301 to https.
    let (headers, _) = curl(&["-i", &format!("http://127.0.0.1:{http_port}/health")]);
    assert!(
        headers.contains("301"),
        "expected a redirect, got: {headers}"
    );
    assert!(
        headers.to_lowercase().contains("location: https://"),
        "expected an https Location, got: {headers}"
    );
}
