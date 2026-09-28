//! The auth API (`graphql.apiAuthRoute`) and the `/me`, `/logout` and
//! `/refresh` routes of an authorization server.

use std::sync::{Arc, Mutex};

use axum::body::Body;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::auth::OtpMessage;
use yekonga::helper::jwt;
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

/// "s3cret-pass", hashed by the Go server's bcrypt.
const GO_HASH: &str = "$2a$04$UBxnkoSw9AtrXg7b7CP1uuQZ1V7eUHK4h.Tdb92Mx0dwyobwozfhe";

struct Server {
    app: Yekonga,
    sent: Arc<Mutex<Vec<OtpMessage>>>,
}

fn server(config: Value) -> Server {
    let mut base = json!({
        "isAuthorizationServer": true,
        "graphql": {"apiRoute": "/graphql", "apiAuthRoute": "/auth"},
        "authentication": {"secretToken": "secret"}
    });
    for (k, v) in config.as_object().unwrap() {
        base[k] = v.clone();
    }

    let config: YekongaConfig = serde_json::from_value(base).unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    );
    let sent = Arc::new(Mutex::new(Vec::new()));
    let outbox = sent.clone();
    app.set_otp_sender(move |message| outbox.lock().unwrap().push(message));
    Server { app, sent }
}

struct Reply {
    status: u16,
    cookies: Vec<String>,
    location: String,
    body: Value,
}

async fn send(app: &Yekonga, request: HttpRequest<Body>) -> Reply {
    let response = app.router().oneshot(request).await.unwrap();
    let status = response.status().as_u16();
    let header = |name: &str| -> Vec<String> {
        response
            .headers()
            .get_all(name)
            .iter()
            .map(|v| v.to_str().unwrap().to_string())
            .collect()
    };
    // Leaves out the pipeline's own YEKONGA_ENABLED cookie.
    let cookies = header("set-cookie")
        .into_iter()
        .filter(|c| !c.starts_with("YEKONGA_ENABLED="))
        .collect();
    let location = header("location").join("");
    let body = response.into_body().collect().await.unwrap().to_bytes();
    Reply {
        status,
        cookies,
        location,
        body: serde_json::from_slice(&body).unwrap_or(Value::Null),
    }
}

async fn auth(app: &Yekonga, query: &str, headers: &[(&str, &str)]) -> Reply {
    let mut request = HttpRequest::post("/auth")
        .header("content-type", "application/json")
        .header("host", "shop.tz");
    for (name, value) in headers {
        request = request.header(*name, *value);
    }
    let body = json!({"query": query}).to_string();
    send(app, request.body(Body::from(body)).unwrap()).await
}

async fn get(app: &Yekonga, uri: &str, headers: &[(&str, &str)]) -> Reply {
    let mut request = HttpRequest::get(uri).header("host", "shop.tz");
    for (name, value) in headers {
        request = request.header(*name, *value);
    }
    send(app, request.body(Body::empty()).unwrap()).await
}

impl Server {
    /// Requests an OTP code for `username` and returns it.
    async fn otp(&self, username: &str) -> String {
        let reply = auth(
            &self.app,
            &format!(
                r#"mutation {{ otp(input: {{username: "{username}"}}) {{ status message }} }}"#
            ),
            &[],
        )
        .await;
        assert_eq!(
            reply.body["data"]["otp"],
            json!({"status": true, "message": "Success"}),
            "{}",
            reply.body
        );
        self.sent.lock().unwrap().last().unwrap().code.clone()
    }
}

fn login_query(username: &str, password: &str, kind: &str, fields: &str) -> String {
    format!(
        r#"mutation {{ login(input: {{username: "{username}", password: "{password}", type: {kind}}}) {{ {fields} }} }}"#
    )
}

#[tokio::test]
async fn otp_login() {
    let s = server(json!({}));
    let code = s.otp("0712 345 678").await;
    {
        let sent = s.sent.lock().unwrap();
        assert_eq!(sent[0].username, "255712345678");
        assert_eq!(sent[0].channel, "phone");
        assert_eq!(code.len(), 4);
        assert!(sent[0].text.starts_with(&code));
    }

    let wrong = if code == "1234" { "4321" } else { "1234" };
    let reply = auth(&s.app, &login_query("0712345678", wrong, "OTP", "id"), &[]).await;
    assert_eq!(reply.body["errors"][0]["message"], "Wrong credential");
    assert!(reply.cookies.is_empty());

    let reply = auth(
        &s.app,
        &login_query("0712345678", &code, "OTP", "id username usernameType token isPhoneVerified role profileName profileRole profileUrl"),
        &[],
    )
    .await;
    let user = &reply.body["data"]["login"];
    assert_eq!(user["username"], "255712345678", "{}", reply.body);
    assert_eq!(user["usernameType"], "phone");
    assert_eq!(user["role"], "user");
    assert_eq!(user["profileName"], "Private Profile");
    assert_eq!(user["profileRole"], "admin");
    assert_eq!(user["profileUrl"], "https://shop.tz/image/profile.png");

    let (_, claims) = jwt::decode_into::<Value>(user["token"].as_str().unwrap(), "secret").unwrap();
    assert_eq!(claims["userId"], user["id"]);
    assert_eq!(claims["domain"], "shop.tz");

    // The access token cookie goes everywhere, the refresh token cookie to
    // /refresh and the auth API.
    let paths: Vec<&str> = reply
        .cookies
        .iter()
        .map(|c| c.split("; ").find(|p| p.starts_with("Path=")).unwrap())
        .collect();
    assert_eq!(paths, ["Path=/", "Path=/", "Path=/refresh", "Path=/auth"]);
    assert!(reply.cookies[0].starts_with(&format!(
        "access_token={}; ",
        user["token"].as_str().unwrap()
    )));
    assert!(reply.cookies[0].contains("; Domain=shop.tz; Max-Age=900; HttpOnly"));
    assert!(reply.cookies[2].contains("; Max-Age=604800; HttpOnly"));

    let stored = s.app.query("User").unwrap().find().await.unwrap();
    assert_eq!(stored.len(), 1, "the OTP login created the user");
}

#[tokio::test]
async fn reset_otp_clears_the_code() {
    // Go checks the code twice and the first check clears it, so OTP
    // logins always fail there with resetOTP.
    let s = server(json!({"resetOTP": true, "userIdentifiers": ["email"]}));
    let code = s.otp("a@b.tz").await;
    assert_eq!(s.sent.lock().unwrap()[0].channel, "email");

    let query = format!(
        r#"mutation {{ login(input: {{username: "a@b.tz", usernameType: email, password: "{code}", type: OTP}}) {{ username isEmailVerified }} }}"#
    );
    let reply = auth(&s.app, &query, &[]).await;
    assert_eq!(
        reply.body["data"]["login"]["username"], "a@b.tz",
        "{}",
        reply.body
    );

    // Without "username" among the identifiers, the login verifies the
    // email address.
    let stored = s.app.query("User").unwrap().find().await.unwrap();
    assert_eq!(stored[0]["isEmailVerified"], true);

    let again = auth(
        &s.app,
        &login_query("a@b.tz", &code, "OTP", "username"),
        &[],
    )
    .await;
    assert_eq!(again.body["errors"][0]["message"], "Wrong credential");
}

#[tokio::test]
async fn password_login() {
    let s = server(json!({}));
    s.app
        .query("User")
        .unwrap()
        .create(json!({"username": "amina", "password": GO_HASH, "status": "active", "role": "1"}))
        .await
        .unwrap();

    // As in Go, a password login needs a verification record (an OTP
    // request) for the username.
    let reply = auth(
        &s.app,
        &login_query("amina", "s3cret-pass", "PASSWORD", "id"),
        &[],
    )
    .await;
    assert_eq!(reply.body["errors"][0]["message"], "Wrong credential");
    s.otp("amina").await;

    for password in ["true", "wrong", ""] {
        let reply = auth(
            &s.app,
            &login_query("amina", password, "PASSWORD", "id"),
            &[],
        )
        .await;
        assert_eq!(
            reply.body["errors"][0]["message"], "Wrong credential",
            "password {password:?}"
        );
    }

    // A registration login needs the OTP code or the password too.
    let reply = auth(
        &s.app,
        &login_query("amina", "x", "REGISTRATION", "id"),
        &[],
    )
    .await;
    assert_eq!(reply.body["errors"][0]["message"], "Wrong credential");

    let reply = auth(
        &s.app,
        &login_query("amina", "s3cret-pass", "PASSWORD", "username role isAdmin"),
        &[],
    )
    .await;
    assert_eq!(
        reply.body["data"]["login"],
        json!({"username": "amina", "role": "admin", "isAdmin": true})
    );

    let reply = auth(&s.app, &login_query("nobody", "x", "PASSWORD", "id"), &[]).await;
    assert_eq!(reply.body["errors"][0]["message"], "Wrong credential");

    let attempts = s.app.query("LoginAttempt").unwrap().find().await.unwrap();
    let statuses: Vec<&str> = attempts
        .iter()
        .map(|a| a["status"].as_str().unwrap())
        .collect();
    assert_eq!(statuses.iter().filter(|s| **s == "success").count(), 1);
    assert_eq!(statuses.iter().filter(|s| **s == "otp").count(), 1);

    s.app
        .query("User")
        .unwrap()
        .where_("username", "amina")
        .update(json!({"isBanned": true}))
        .await
        .unwrap();
    let reply = auth(
        &s.app,
        &login_query("amina", "s3cret-pass", "PASSWORD", "id"),
        &[],
    )
    .await;
    assert_eq!(
        reply.body["errors"][0]["message"],
        "You are banned from accessing "
    );
}

#[tokio::test]
async fn global_password() {
    let s = server(json!({"globalPassword": "master-pass"}));
    s.app
        .query("User")
        .unwrap()
        .create(json!({"username": "juma", "password": GO_HASH, "status": 1}))
        .await
        .unwrap();
    s.otp("juma").await;

    let reply = auth(
        &s.app,
        &login_query("juma", "master-pass", "PASSWORD", "username"),
        &[],
    )
    .await;
    assert_eq!(
        reply.body["data"]["login"]["username"], "juma",
        "{}",
        reply.body
    );
}

#[tokio::test]
async fn refresh_tokens() {
    let s = server(json!({"secureAuthentication": true}));
    let code = s.otp("a@b.tz").await;
    let reply = auth(
        &s.app,
        &login_query("a@b.tz", &code, "OTP", "accessToken refreshToken"),
        &[],
    )
    .await;
    let tokens = &reply.body["data"]["login"];
    let refresh = tokens["refreshToken"].as_str().unwrap().to_string();
    assert_eq!(refresh.len(), 64);
    assert!(jwt::decode(tokens["accessToken"].as_str().unwrap(), "secret").is_some());

    // Only the hash is stored.
    let stored = s.app.query("RefreshToken").unwrap().find().await.unwrap();
    assert_eq!(
        stored[0]["tokenHash"],
        yekonga::helper::hash_refresh_token(&refresh)
    );

    let json_headers = [
        ("accept", "application/json"),
        ("x-refresh-token", refresh.as_str()),
    ];
    let renewed = get(&s.app, "/refresh", &json_headers).await;
    assert_eq!(renewed.status, 200, "{}", renewed.body);
    let new_refresh = renewed.body["refreshToken"].as_str().unwrap().to_string();
    assert_ne!(new_refresh, refresh);
    assert_eq!(renewed.cookies.len(), 4);

    let reused = get(&s.app, "/refresh", &json_headers).await;
    assert_eq!(reused.status, 401);
    assert_eq!(reused.body, json!({"error": "refresh_token is revoked"}));

    // Through the auth API, with the token as an argument.
    let query = format!(
        r#"{{ refreshToken(refreshToken: "{new_refresh}") {{ accessToken refreshToken }} }}"#
    );
    let reply = auth(&s.app, &query, &[]).await;
    let latest = &reply.body["data"]["refreshToken"];
    assert!(latest["accessToken"].is_string(), "{}", reply.body);
    let latest = latest["refreshToken"].as_str().unwrap().to_string();

    let reply = auth(
        &s.app,
        r#"{ refreshToken(refreshToken: "nope") { accessToken } }"#,
        &[],
    )
    .await;
    assert_eq!(reply.body["errors"][0]["message"], "Invalid Refresh Token");

    let other_domain = get(
        &s.app,
        "/refresh",
        &[
            ("accept", "application/json"),
            ("x-refresh-token", &latest),
            ("origin", "https://evil.tz"),
        ],
    )
    .await;
    assert_eq!(other_domain.body["error"], "Domain mismatch");

    let empty = get(&s.app, "/refresh", &[("accept", "application/json")]).await;
    assert_eq!(empty.body, json!({"error": "Empty Refresh Token"}));
}

#[tokio::test]
async fn me_and_logout() {
    let s = server(json!({}));
    let code = s.otp("0712345678").await;
    let reply = auth(
        &s.app,
        &login_query("0712345678", &code, "OTP", "token"),
        &[],
    )
    .await;
    let bearer = format!(
        "Bearer {}",
        reply.body["data"]["login"]["token"].as_str().unwrap()
    );

    let me = get(&s.app, "/me", &[("authorization", &bearer)]).await;
    assert_eq!(me.status, 200);
    assert_eq!(me.body["username"], "255712345678");
    assert_eq!(me.body["token"], Value::Null);

    let profile = auth(
        &s.app,
        "{ profile { username token } }",
        &[("authorization", &bearer)],
    )
    .await;
    assert_eq!(profile.body["data"]["profile"]["username"], "255712345678");
    assert!(profile.body["data"]["profile"]["token"].is_string());

    let anonymous = get(&s.app, "/me/sales", &[("accept", "application/json")]).await;
    assert_eq!(anonymous.status, 401);
    assert_eq!(anonymous.body, json!({"error": "Missing or Invalid token"}));
    let profile = auth(&s.app, "{ profile { username } }", &[]).await;
    assert_eq!(profile.body["errors"][0]["message"], "Not authorized");

    let logout = get(&s.app, "/logout", &[("accept", "application/json")]).await;
    assert_eq!(logout.body, json!({"status": "SUCCESS"}));
    assert_eq!(logout.cookies.len(), 4);
    assert!(logout
        .cookies
        .iter()
        .all(|c| c.contains("=; ") && c.contains("Max-Age=0")));

    let redirect = get(&s.app, "/logout", &[]).await;
    assert_eq!(redirect.status, 307);
    assert_eq!(redirect.location, "https://shop.tz/");
}

#[tokio::test]
async fn tenants() {
    let s = server(json!({"hasTenant": true}));
    let code = s.otp("a@b.tz").await;
    let reply = auth(&s.app, &login_query("a@b.tz", &code, "OTP", "token"), &[]).await;
    let bearer = format!(
        "Bearer {}",
        reply.body["data"]["login"]["token"].as_str().unwrap()
    );

    let register = auth(
        &s.app,
        r#"mutation { register(input: {organization: "Duka", type: COMPANY, firstName: "Asha", domain: "duka.tz"}) { status message data } }"#,
        &[("authorization", &bearer)],
    )
    .await;
    let result = &register.body["data"]["register"];
    assert_eq!(result["status"], true, "{}", register.body);
    assert_eq!(result["data"]["name"], "Duka");
    assert_eq!(result["data"]["type"], "company");

    let users = s.app.query("User").unwrap().find().await.unwrap();
    assert_eq!(users[0]["firstName"], "Asha");

    let taken = auth(&s.app, r#"{ tenantAvailability(domain: "duka.tz") }"#, &[]).await;
    assert_eq!(taken.body["data"]["tenantAvailability"], true);
    let free = auth(
        &s.app,
        r#"{ tenantAvailability(domain: "x.tz", subdomain: "x") }"#,
        &[],
    )
    .await;
    assert_eq!(free.body["data"]["tenantAvailability"], false);
    let nothing = auth(&s.app, "{ tenantAvailability }", &[]).await;
    assert_eq!(nothing.body["data"]["tenantAvailability"], false);

    let anonymous = auth(
        &s.app,
        r#"mutation { register(input: {organization: "X"}) { status } }"#,
        &[],
    )
    .await;
    assert_eq!(anonymous.body["errors"][0]["message"], "Not authorized");
}

#[tokio::test]
async fn unported_mutations_and_disabled_routes() {
    let s = server(json!({}));
    let reply = auth(
        &s.app,
        "mutation { switchAccount(userId: \"x\") { email } }",
        &[],
    )
    .await;
    assert_eq!(
        reply.body["errors"][0]["message"],
        "switchAccount is not supported by the Rust port yet"
    );

    let introspection = auth(&s.app, "{ __schema { queryType { name } } }", &[]).await;
    assert_eq!(
        introspection.body,
        json!({"errors": [{"message": "Introspection is disabled"}]})
    );

    let plain = server(json!({"isAuthorizationServer": false}));
    assert_eq!(
        auth(&plain.app, "{ profile { id } }", &[]).await.status,
        404
    );
    assert_eq!(get(&plain.app, "/me", &[]).await.status, 404);
}
