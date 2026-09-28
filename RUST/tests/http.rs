//! End-to-end tests of the request pipeline through the server's tower service.

use std::sync::{Arc, Mutex};

use axum::body::Body;
use http::{Request as HttpRequest, StatusCode};
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::{
    helper::jwt, Abort, DatabaseStructure, MiddlewareKind, StaticConfig, Yekonga, YekongaConfig,
};

fn app_with(config: Value) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(config).unwrap();
    Yekonga::new(config, DatabaseStructure::default())
}

fn app() -> Yekonga {
    app_with(
        json!({"appName": "Test App", "masterKey": "MASTER", "authentication": {"secretToken": "secret"}}),
    )
}

struct Reply {
    status: StatusCode,
    headers: http::HeaderMap,
    body: Vec<u8>,
}

impl Reply {
    fn text(&self) -> String {
        String::from_utf8_lossy(&self.body).into_owned()
    }

    fn json(&self) -> Value {
        serde_json::from_slice(&self.body).unwrap_or_else(|_| panic!("not JSON: {}", self.text()))
    }

    fn header(&self, name: &str) -> &str {
        self.headers
            .get(name)
            .map(|v| v.to_str().unwrap())
            .unwrap_or("")
    }
}

async fn send(app: &Yekonga, request: HttpRequest<Body>) -> Reply {
    let response = app.router().oneshot(request).await.unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let body = response
        .into_body()
        .collect()
        .await
        .unwrap()
        .to_bytes()
        .to_vec();

    Reply {
        status,
        headers,
        body,
    }
}

async fn get(app: &Yekonga, uri: &str) -> Reply {
    send(app, HttpRequest::get(uri).body(Body::empty()).unwrap()).await
}

fn get_json(uri: &str) -> http::request::Builder {
    HttpRequest::get(uri).header("accept", "application/json")
}

#[tokio::test]
async fn builtin_routes() {
    let app = app();

    let health = get(&app, "/health").await;
    assert_eq!(health.status, StatusCode::OK);
    assert_eq!(health.text(), "Ok!");
    assert_eq!(health.header("content-type"), "text/plain");
    assert_eq!(health.header("yekonga-application"), "Yesu");
    assert!(health
        .header("set-cookie")
        .starts_with("YEKONGA_ENABLED=YEKONGA_CONNECTED; Max-Age=2592000; HttpOnly"));

    assert_eq!(
        get(&app, "/api-health").await.json(),
        json!({"status": "OK!"})
    );
    assert_eq!(
        get(&app, "/check-connection").await.text(),
        "YEKONGA_CONNECTED"
    );

    let posted = send(
        &app,
        HttpRequest::post("/health").body(Body::empty()).unwrap(),
    )
    .await;
    assert_eq!(posted.text(), "Ok!", "`all` registers every method");
}

#[tokio::test]
async fn cookie_is_only_set_once() {
    let app = app();
    let request = HttpRequest::get("/health")
        .header("cookie", "a=b; YEKONGA_ENABLED=x")
        .body(Body::empty())
        .unwrap();

    assert_eq!(send(&app, request).await.header("set-cookie"), "");
}

#[tokio::test]
async fn index_and_not_found_pages() {
    let app = app();

    let index = get(&app, "/").await;
    assert_eq!(index.status, StatusCode::OK);
    assert!(index.text().contains("<html") || index.text().contains("<!DOCTYPE"));

    let missing = get(&app, "/nope").await;
    assert_eq!(missing.status, StatusCode::NOT_FOUND);
    assert_eq!(missing.header("content-type"), "text/html");
}

#[tokio::test]
async fn route_params_query_and_body() {
    let app = app_with(json!({"baseURL": "/v1/"}));

    app.get("/orders/:id/:tab?", |req, res| async move {
        res.json(&json!({"id": req.param("id"), "tab": req.param("tab"), "page": req.query_int("page", 1)}));
    });
    app.post("/echo", |req, res| async move {
        res.json(&json!({"body": req.body(), "form": req.form_value("name")}));
    });

    assert_eq!(
        get(&app, "/v1/orders/42?page=3").await.json(),
        json!({"id": "42", "tab": "", "page": 3})
    );
    assert_eq!(
        get(&app, "/v1/orders/42/items").await.json()["tab"],
        "items"
    );
    assert_eq!(
        get(&app, "/orders/42").await.status,
        StatusCode::NOT_FOUND,
        "baseURL is prefixed"
    );
    assert_eq!(get(&app, "/v1/health").await.text(), "Ok!");

    let echo = send(
        &app,
        HttpRequest::post("/v1/echo")
            .header("content-type", "application/json")
            .body(Body::from(r#"{"a":1}"#))
            .unwrap(),
    )
    .await;
    assert_eq!(echo.json(), json!({"body": {"a": 1}, "form": ""}));

    let form = send(
        &app,
        HttpRequest::post("/v1/echo")
            .header("content-type", "application/x-www-form-urlencoded")
            .body(Body::from("name=Asha+K"))
            .unwrap(),
    )
    .await;
    assert_eq!(form.json(), json!({"body": null, "form": "Asha K"}));
}

#[tokio::test]
async fn master_key() {
    let app = app();

    let bad = send(
        &app,
        get_json("/health")
            .header("master-key", "WRONG")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(bad.status, StatusCode::UNAUTHORIZED);
    assert_eq!(
        bad.json(),
        json!({"status": 401, "error": "master key invalid"})
    );

    let browser = send(
        &app,
        HttpRequest::get("/health?master-key=WRONG")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(browser.status, StatusCode::UNAUTHORIZED);
    assert_eq!(
        browser.header("content-type"),
        "text/html",
        "browsers get the error page"
    );

    let good = send(
        &app,
        get_json("/health")
            .header("x-core-master-key", "MASTER")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(good.text(), "Ok!");
}

#[tokio::test]
async fn application_key() {
    let app = app_with(json!({"enableAppKey": true, "appKey": "APP"}));

    let missing = send(&app, get_json("/health").body(Body::empty()).unwrap()).await;
    assert_eq!(missing.json()["error"], "application key not provided");

    let wrong = send(
        &app,
        get_json("/health?app-key=NOPE")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(wrong.json()["error"], "application key invalid");

    let good = send(
        &app,
        get_json("/health")
            .header("application-key", "APP")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(good.text(), "Ok!");

    // Static asset extensions don't need the key.
    assert_eq!(
        get(&app, "/missing.css").await.status,
        StatusCode::NOT_FOUND
    );
}

fn token(claims: Value) -> String {
    jwt::encode(claims.as_object().unwrap().clone(), "secret")
}

#[tokio::test]
async fn access_tokens() {
    let app = app();
    app.get("/whoami", |req, res| async move {
        let auth = req.auth();
        res.json(&json!({
            "userId": auth.as_ref().map(|a| a.user_id.clone()),
            "email": auth.map(|a| a.email),
            "tenant": req.tenant_id(),
        }));
    });

    let valid = token(
        json!({"userId": "u1", "email": "a@b.tz", "tenantId": "t1", "expiresAt": "2999-01-01T00:00:00Z"}),
    );
    let reply = send(
        &app,
        get_json("/whoami")
            .header("authorization", format!("Bearer {valid}"))
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(
        reply.json(),
        json!({"userId": "u1", "email": "a@b.tz", "tenant": "t1"})
    );

    // The cookie works too.
    let reply = send(
        &app,
        get_json("/whoami")
            .header("cookie", format!("access_token={valid}"))
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply.json()["userId"], "u1");

    // A bad token on a route that needs one: JSON clients get an error...
    let reply = send(
        &app,
        get_json("/whoami")
            .header("authorization", "Bearer x.y.z")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(
        reply.json(),
        json!({"status": 307, "error": "Access token invalid"})
    );

    // ...browsers are sent to the logout page.
    let reply = send(
        &app,
        HttpRequest::get("/whoami")
            .header("host", "app.tz")
            .header("authorization", "Bearer x.y.z")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply.status, StatusCode::TEMPORARY_REDIRECT);
    assert_eq!(reply.header("location"), "https://app.tz/logout");

    // Expired claims.
    let expired = token(json!({"userId": "u1", "expiresAt": "2000-01-01T00:00:00Z"}));
    let reply = send(
        &app,
        get_json("/whoami")
            .header("authorization", format!("Bearer {expired}"))
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(
        reply.json(),
        json!({"status": 401, "error": "Token expired"})
    );

    // Token-optional routes ignore a bad token.
    let reply = send(
        &app,
        get_json("/me/x")
            .header("authorization", "Bearer x.y.z")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply.status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn domain_mismatch() {
    let app = app();
    app.get("/whoami", |_req, res| async move { res.text("ok") });

    let other =
        token(json!({"userId": "u1", "domain": "other.tz", "expiresAt": "2999-01-01T00:00:00Z"}));
    let reply = send(
        &app,
        get_json("/whoami")
            .header("host", "app.tz")
            .header("authorization", format!("Bearer {other}"))
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(
        reply.json(),
        json!({"status": 401, "error": "Domain mismatch expired"})
    );
}

#[tokio::test]
async fn authorized_only() {
    let app = app_with(json!({"authorizedOnly": true, "masterKey": "MASTER"}));
    app.get("/private", |_req, res| async move { res.text("secret") });
    app.get("/shop/list", |_req, res| async move { res.text("public") });
    app.set_public_route("/shop/*");

    let reply = send(&app, get_json("/private").body(Body::empty()).unwrap()).await;
    assert_eq!(
        reply.json(),
        json!({"status": 401, "error": "Must be authorized/login"})
    );

    let reply = send(
        &app,
        get_json("/private")
            .header("master-key", "MASTER")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply.text(), "secret");

    let reply = send(&app, get_json("/shop/list").body(Body::empty()).unwrap()).await;
    assert_eq!(reply.text(), "public");
}

#[tokio::test]
async fn middleware_order_and_abort() {
    let app = app();
    let order = Arc::new(Mutex::new(Vec::new()));

    for (kind, name) in [
        (MiddlewareKind::Global, "global"),
        (MiddlewareKind::Init, "init"),
        (MiddlewareKind::Preload, "preload"),
    ] {
        let order = order.clone();
        app.middleware(kind, move |req, _res| {
            let order = order.clone();
            async move {
                // Built-in client info is available after preload only.
                order
                    .lock()
                    .unwrap()
                    .push(format!("{name}:{}", req.client().is_some()));
                Ok(())
            }
        });
    }

    let seen = order.clone();
    app.get("/run", move |req, res| {
        let seen = seen.clone();
        async move {
            seen.lock().unwrap().push("handler".into());
            res.text(&req.context_string("from"));
        }
    });

    assert_eq!(get(&app, "/run").await.status, StatusCode::OK);
    assert_eq!(
        *order.lock().unwrap(),
        vec!["preload:false", "init:true", "global:true", "handler"]
    );

    app.use_middleware(|req, _res| async move {
        if req.query("block") == "1" {
            return Err(Abort::new(403, "blocked"));
        }
        req.set_context("from", "middleware");
        Ok(())
    });

    let blocked = send(&app, get_json("/run?block=1").body(Body::empty()).unwrap()).await;
    assert_eq!(blocked.json(), json!({"status": 403, "error": "blocked"}));
    assert_eq!(
        get(&app, "/run").await.text(),
        "middleware",
        "context flows to the handler"
    );
}

#[tokio::test]
async fn catch_middleware() {
    let app = app();
    app.catch(|_req, _res| async move { Ok(()) });
    app.catch(|req, res| async move {
        if req.path().starts_with("/spa/") {
            res.html("<app/>");
        }
        Ok(())
    });

    assert_eq!(get(&app, "/spa/page").await.text(), "<app/>");
    assert_eq!(get(&app, "/other").await.status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn handler_panic_is_a_500() {
    let app = app();
    app.get("/boom", |_req, _res| async move { panic!("boom") });

    let reply = send(&app, get_json("/boom").body(Body::empty()).unwrap()).await;
    assert_eq!(reply.status, StatusCode::INTERNAL_SERVER_ERROR);
    assert_eq!(reply.json()["error"], "500 Internal Server Error");
}

#[tokio::test]
async fn static_files() {
    let dir = tempfile::tempdir().unwrap();
    std::fs::write(dir.path().join("app.css"), "body{}").unwrap();
    std::fs::write(dir.path().join("secret.env"), "KEY=1").unwrap();
    std::fs::create_dir(dir.path().join("docs")).unwrap();
    std::fs::write(dir.path().join("docs/index.html"), "<h1>docs</h1>").unwrap();

    let app = app();
    app.serve_static(StaticConfig::new(dir.path(), "/assets"))
        .unwrap();
    assert!(app
        .serve_static(StaticConfig::new(dir.path().join("missing"), "/x"))
        .is_err());

    let css = get(&app, "/assets/app.css").await;
    assert_eq!(css.status, StatusCode::OK);
    assert_eq!(css.text(), "body{}");
    assert_eq!(css.header("content-type"), "text/css");
    assert_eq!(css.header("cache-control"), "max-age=2592000");

    assert_eq!(
        get(&app, "/assets/secret.env").await.status,
        StatusCode::NOT_FOUND,
        "extension not allowed"
    );
    assert_eq!(
        get(&app, "/assets/../Cargo.toml").await.status,
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        get(&app, "/assets/%2e%2e/Cargo.toml").await.status,
        StatusCode::NOT_FOUND
    );

    let range = send(
        &app,
        HttpRequest::get("/assets/app.css")
            .header("range", "bytes=0-3")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(range.status, StatusCode::PARTIAL_CONTENT);
    assert_eq!(range.text(), "body");
}

#[tokio::test]
async fn file_and_download_responses() {
    let dir = tempfile::tempdir().unwrap();
    let report = dir.path().join("report.csv");
    std::fs::write(&report, "a,b\n1,2\n").unwrap();

    let app = app();
    let path = report.clone();
    app.get("/report", move |_req, res| {
        let path = path.clone();
        async move { res.download(&path, "Monthly Report.csv") }
    });
    app.get("/inline", move |_req, res| {
        let path = report.clone();
        async move { res.file(&path) }
    });

    let download = get(&app, "/report").await;
    assert_eq!(download.text(), "a,b\n1,2\n");
    assert_eq!(
        download.header("content-disposition"),
        "attachment; filename=\"Monthly Report.csv\""
    );
    assert_eq!(download.header("content-type"), "text/csv");

    let inline = get(&app, "/inline").await;
    assert_eq!(inline.text(), "a,b\n1,2\n");
    assert_eq!(inline.header("cache-control"), "max-age=7");
}

#[tokio::test]
async fn gzip_and_cors() {
    let app = app_with(json!({"cors": true}));
    let big = "x".repeat(4096);
    app.get("/big", move |_req, res| {
        let big = big.clone();
        async move { res.text(&big) }
    });

    let reply = send(
        &app,
        HttpRequest::get("/big")
            .header("accept-encoding", "gzip")
            .header("origin", "https://web.tz")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply.header("content-encoding"), "gzip");
    assert!(reply.body.len() < 4096);
    assert_eq!(
        reply.header("access-control-allow-origin"),
        "https://web.tz"
    );

    let small = send(
        &app,
        HttpRequest::get("/health")
            .header("accept-encoding", "gzip")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(
        small.header("content-encoding"),
        "",
        "small bodies aren't compressed"
    );
}

#[tokio::test]
async fn body_limit() {
    let app = app_with(json!({"security": {"maxBodyBytes": 10}}));
    app.post("/upload", |_req, res| async move { res.text("ok") });

    let reply = send(
        &app,
        HttpRequest::post("/upload")
            .body(Body::from("x".repeat(11)))
            .unwrap(),
    )
    .await;
    assert_eq!(reply.status, StatusCode::PAYLOAD_TOO_LARGE);
}

#[tokio::test]
async fn tenant_rules_without_database() {
    let app = app_with(json!({"hasTenant": true}));

    // Main domain is fine; an unknown tenant subdomain is rejected.
    let main = send(
        &app,
        get_json("/health")
            .header("host", "example.com")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(main.text(), "Ok!");

    let sub = send(
        &app,
        get_json("/health")
            .header("host", "shop.example.com")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(
        sub.json(),
        json!({"status": 404, "error": "Tenant not found"})
    );

    // A preload middleware can resolve the tenant until the database layer is ported.
    app.middleware(MiddlewareKind::Preload, |req, _res| async move {
        req.set_tenant_id("t1");
        Ok(())
    });
    let sub = send(
        &app,
        get_json("/health")
            .header("host", "shop.example.com")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(sub.text(), "Ok!");
}

#[tokio::test]
async fn models_are_available() {
    let structure = DatabaseStructure::from_value(
        &json!({"Orders": {"userId": {"type": "ID", "foreignKey": "User.id"}}}),
    );
    let app = Yekonga::new(YekongaConfig::default(), structure);

    let order = app.model("Order").unwrap();
    assert_eq!(order.collection, "orders");
    assert!(
        app.model("Notification").is_some(),
        "built-in collections are included"
    );
    assert!(app.model("Nope").is_none());
}
