//! The WebSocket server: rooms, broadcasts, the built-in events and database
//! change events. Runs a real server on an ephemeral port and connects with a
//! WebSocket client.

use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use serde_json::{json, Value};
use tokio::net::TcpStream;
use tokio_tungstenite::tungstenite::Message as WsMessage;
use tokio_tungstenite::{connect_async, MaybeTlsStream, WebSocketStream};
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

type Socket = WebSocketStream<MaybeTlsStream<TcpStream>>;

/// Starts a server on an ephemeral port and returns the app and its address.
async fn serve(config: Value, schema: Value) -> (Yekonga, SocketAddr) {
    let config: YekongaConfig = serde_json::from_value(config).unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&schema),
        Arc::new(LocalBackend::in_memory()),
    );

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let serving = app.clone();
    tokio::spawn(async move {
        let service = serving
            .router()
            .into_make_service_with_connect_info::<SocketAddr>();
        axum::serve(listener, service).await.unwrap();
    });
    (app, addr)
}

async fn connect(addr: SocketAddr) -> Socket {
    let url = format!("ws://{addr}/yekonga.io/");
    let (mut socket, _) = connect_async(&url).await.unwrap();
    // Every connection opens with the client id then a welcome.
    assert_eq!(next_event(&mut socket).await["event"], "id");
    assert_eq!(next_event(&mut socket).await["event"], "message");
    socket
}

async fn send(socket: &mut Socket, value: Value) {
    socket
        .send(WsMessage::Text(value.to_string()))
        .await
        .unwrap();
}

/// The next text frame, parsed as JSON.
async fn next_event(socket: &mut Socket) -> Value {
    loop {
        let message = tokio::time::timeout(Duration::from_secs(2), socket.next())
            .await
            .expect("timed out waiting for a frame")
            .expect("socket closed")
            .unwrap();
        if let WsMessage::Text(text) = message {
            return serde_json::from_str(&text).unwrap();
        }
    }
}

/// Asserts no frame arrives within a short window.
async fn expect_silence(socket: &mut Socket) {
    if let Ok(Some(Ok(WsMessage::Text(text)))) =
        tokio::time::timeout(Duration::from_millis(150), socket.next()).await
    {
        panic!("expected no frame, got {text}");
    }
}

fn app_config() -> Value {
    json!({"graphql": {"apiRoute": "/graphql"}, "authentication": {"secretToken": "s"}})
}

fn schema() -> Value {
    json!({"Orders": {"title": {"type": "String"}}})
}

#[tokio::test]
async fn rooms_and_broadcast() {
    let (_app, addr) = serve(app_config(), schema()).await;
    let mut a = connect(addr).await;
    let mut b = connect(addr).await;

    // A joins a room; a message to that room reaches A, not others.
    send(&mut a, json!({"type": "join", "data": {"room": "vip"}})).await;
    send(
        &mut b,
        json!({"type": "toRoom", "data": {"room": "vip", "event": "ping", "payload": {"n": 1}}}),
    )
    .await;
    let event = next_event(&mut a).await;
    assert_eq!(event, json!({"event": "ping", "data": {"n": 1}}));

    // A broadcast reaches everyone but the sender.
    send(
        &mut a,
        json!({"type": "broadcast", "data": {"event": "hello", "payload": "hi"}}),
    )
    .await;
    assert_eq!(
        next_event(&mut b).await,
        json!({"event": "hello", "data": "hi"})
    );
    expect_silence(&mut a).await;
}

#[tokio::test]
async fn subscribe_and_ack() {
    let (_app, addr) = serve(app_config(), schema()).await;
    let mut client = connect(addr).await;

    // `subscribe` joins the userId and deviceId rooms; a frame with a msgId
    // gets an ack.
    send(
        &mut client,
        json!({"type": "emit", "msgId": "m1", "data": {"event": "subscribe", "payload": {"userId": "u1", "deviceId": "d1"}}}),
    )
    .await;
    assert_eq!(
        next_event(&mut client).await,
        json!({"type": "ack", "msgId": "m1", "payload": "ok"})
    );

    // Another client sends to the user's room.
    let mut other = connect(addr).await;
    send(
        &mut other,
        json!({"type": "toRoom", "data": {"room": "u1", "event": "note", "payload": 7}}),
    )
    .await;
    assert_eq!(
        next_event(&mut client).await,
        json!({"event": "note", "data": 7})
    );
}

#[tokio::test]
async fn graphql_over_socket() {
    let (app, addr) = serve(app_config(), schema()).await;
    app.query("Order")
        .unwrap()
        .create(json!({"title": "A"}))
        .await
        .unwrap();

    let mut client = connect(addr).await;
    send(
        &mut client,
        json!({"type": "emit", "data": {"event": "graphql-request", "payload": {"listener": "L1", "body": {"query": "{ orders { title } }"}}}}),
    )
    .await;

    // The ack and the graphql-response both come back; find the response.
    let response = loop {
        let event = next_event(&mut client).await;
        if event["event"] == "graphql-response" {
            break event;
        }
    };
    assert_eq!(response["data"]["listener"], "L1");
    assert_eq!(
        response["data"]["body"]["data"]["orders"],
        json!([{"title": "A"}])
    );
}

#[tokio::test]
async fn database_change_events() {
    let (app, addr) = serve(app_config(), schema()).await;
    let mut client = connect(addr).await;

    app.query("Order")
        .unwrap()
        .create(json!({"title": "A"}))
        .await
        .unwrap();
    assert_eq!(
        next_event(&mut client).await,
        json!({"event": "database", "data": {"action": "create", "model": "Order"}})
    );

    app.query("Order")
        .unwrap()
        .where_("title", "A")
        .update(json!({"title": "B"}))
        .await
        .unwrap();
    assert_eq!(
        next_event(&mut client).await,
        json!({"event": "database", "data": {"action": "update", "model": "Order"}})
    );
}

#[tokio::test]
async fn change_events_respect_the_tenant() {
    let (app, addr) = serve(
        json!({"hasTenant": true, "graphql": {"apiRoute": "/graphql"}, "authentication": {"secretToken": "s"}}),
        json!({"Orders": {"title": {"type": "String"}, "tenantId": {"type": "ID", "foreignKey": "Tenant.id"}}}),
    )
    .await;

    let shop = app
        .query("Tenant")
        .unwrap()
        .create(json!({"name": "Shop", "domain": "shop.tz"}))
        .await
        .unwrap();
    let shop_id = shop["id"].as_str().unwrap().to_string();
    app.query("Tenant")
        .unwrap()
        .create(json!({"name": "Other", "domain": "other.tz"}))
        .await
        .unwrap();

    // A client connected for another tenant's host.
    let url = format!("ws://{addr}/yekonga.io/");
    let request = tokio_tungstenite::tungstenite::client::IntoClientRequest::into_client_request(
        url.as_str(),
    )
    .unwrap();
    let request = with_host(request, "other.tz");
    let (mut other, _) = connect_async(request).await.unwrap();
    assert_eq!(next_event(&mut other).await["event"], "id");
    assert_eq!(next_event(&mut other).await["event"], "message");

    // A write scoped to the shop tenant (a mutation as shop.tz) reaches shop
    // clients and tenant-less clients, but not the other tenant's client.
    let _ = shop_id;
    let mut plain = connect(addr).await;
    let request = http::Request::post("/graphql")
        .header("content-type", "application/json")
        .header("host", "shop.tz")
        .body(axum::body::Body::from(
            json!({"query": r#"mutation { createOrder(input: {title: "A"}) { success } }"#})
                .to_string(),
        ))
        .unwrap();
    tower::ServiceExt::oneshot(app.router(), request)
        .await
        .unwrap();

    assert_eq!(next_event(&mut plain).await["event"], "database");
    expect_silence(&mut other).await;
}

/// Adds a `Host` header to a client handshake request.
fn with_host(
    mut request: tokio_tungstenite::tungstenite::handshake::client::Request,
    host: &str,
) -> tokio_tungstenite::tungstenite::handshake::client::Request {
    request.headers_mut().insert("host", host.parse().unwrap());
    request
}
