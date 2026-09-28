//! The WebSocket server and database change events (port of
//! `yekonga/socket.go`).
//!
//! Clients connect to `/yekonga.io/` (optionally `?ns=/name` for a
//! namespace). The wire protocol matches the Go server: an incoming frame is
//! `{"type", "msgId"?, "data"}` and an event sent to a client is
//! `{"event", "data"}`. Clients can join and leave rooms, broadcast, send to a
//! room or to one client, and use the built-in `subscribe`, `unsubscribe`,
//! `acknowledge` and `graphql-request` events. After every database write the
//! query builder emits a `database` event to the affected tenant's clients.

use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};

use axum::extract::ws::{Message as WsMessage, WebSocket, WebSocketUpgrade};
use axum::response::Response;
use futures_util::{SinkExt, StreamExt};
use serde_json::{json, Value};
use tokio::sync::mpsc;

use crate::app::Yekonga;
use crate::db::values::{is_empty, new_object_id};
use crate::helper::extract_domain;

/// A connected client.
struct Client {
    id: String,
    /// The tenant the client connected as (`""` when it has none).
    tenant_id: String,
    sender: mpsc::UnboundedSender<String>,
    rooms: Mutex<HashSet<String>>,
}

impl Client {
    /// Queues an already-encoded frame, dropping it if the client is gone.
    fn send(&self, frame: String) {
        let _ = self.sender.send(frame);
    }
}

/// An isolated group of clients (a Socket.IO namespace).
#[derive(Default)]
struct Namespace {
    clients: Mutex<HashMap<String, Arc<Client>>>,
    rooms: Mutex<HashMap<String, HashSet<String>>>,
}

impl Namespace {
    fn add_client(&self, client: Arc<Client>) {
        self.clients
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .insert(client.id.clone(), client);
    }

    fn remove_client(&self, id: &str) {
        if let Some(client) = self
            .clients
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(id)
        {
            let mut rooms = self.rooms.lock().unwrap_or_else(|e| e.into_inner());
            for room in client
                .rooms
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .iter()
            {
                if let Some(members) = rooms.get_mut(room) {
                    members.remove(id);
                    if members.is_empty() {
                        rooms.remove(room);
                    }
                }
            }
        }
    }

    fn join_room(&self, room: &str, client: &Arc<Client>) {
        self.rooms
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .entry(room.to_string())
            .or_default()
            .insert(client.id.clone());
        client
            .rooms
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .insert(room.to_string());
    }

    fn leave_room(&self, room: &str, client: &Arc<Client>) {
        if let Some(members) = self
            .rooms
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get_mut(room)
        {
            members.remove(&client.id);
        }
        client
            .rooms
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(room);
    }

    /// Sends an event to every client, except `exclude`.
    fn broadcast(&self, event: &str, data: &Value, exclude: Option<&str>) {
        let frame = encode_event(event, data);
        for client in self
            .clients
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .values()
        {
            if Some(client.id.as_str()) != exclude {
                client.send(frame.clone());
            }
        }
    }

    /// Sends to the clients of `tenant_id` and clients without a tenant; an
    /// empty `tenant_id` sends to everyone (Go's `broadcastToTenant`).
    fn broadcast_to_tenant(&self, tenant_id: &str, event: &str, data: &Value) {
        let frame = encode_event(event, data);
        for client in self
            .clients
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .values()
        {
            if tenant_id.is_empty() || client.tenant_id.is_empty() || client.tenant_id == tenant_id
            {
                client.send(frame.clone());
            }
        }
    }

    fn send_to_room(&self, room: &str, event: &str, data: &Value, exclude: Option<&str>) {
        let frame = encode_event(event, data);
        let clients = self.clients.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(members) = self
            .rooms
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get(room)
        {
            for id in members {
                if Some(id.as_str()) != exclude {
                    if let Some(client) = clients.get(id) {
                        client.send(frame.clone());
                    }
                }
            }
        }
    }

    fn emit_to_client(&self, id: &str, event: &str, data: &Value) {
        if let Some(client) = self
            .clients
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get(id)
        {
            client.send(encode_event(event, data));
        }
    }
}

/// Manages the WebSocket namespaces (Go's `SocketServer`).
#[derive(Default)]
pub struct SocketServer {
    namespaces: Mutex<HashMap<String, Arc<Namespace>>>,
}

impl SocketServer {
    pub(crate) fn new() -> Self {
        Self::default()
    }

    /// The namespace for `path`, created on first use. A blank path is `/`.
    fn namespace(&self, path: &str) -> Arc<Namespace> {
        let mut path = path.trim().to_string();
        if path.is_empty() {
            path = "/".into();
        }
        if !path.starts_with('/') {
            path = format!("/{path}");
        }
        self.namespaces
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .entry(path)
            .or_default()
            .clone()
    }

    /// Tells subscribed clients a model's data changed (Go's
    /// `emitDatabaseEvent`): a write to a tenant's data reaches that tenant's
    /// clients and clients without a tenant; an empty tenant reaches all.
    pub(crate) fn emit_database_event(&self, model: &str, action: &str, tenant_id: &str) {
        let namespaces = self.namespaces.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(root) = namespaces.get("/") {
            root.broadcast_to_tenant(
                tenant_id,
                "database",
                &json!({"action": action, "model": model}),
            );
        }
    }
}

/// Encodes an event frame (`{"event", "data"}`) as the Go server does.
fn encode_event(event: &str, data: &Value) -> String {
    json!({"event": event, "data": data}).to_string()
}

/// Accepts a WebSocket upgrade at `/yekonga.io/`: reads the namespace from
/// `?ns=`, resolves the client's tenant from its host, then serves the
/// connection.
pub(crate) async fn upgrade(
    app: Yekonga,
    ws: WebSocketUpgrade,
    parts: &http::request::Parts,
) -> Response {
    let query: Vec<(String, String)> = parts
        .uri
        .query()
        .map(|q| form_urlencoded::parse(q.as_bytes()).into_owned().collect())
        .unwrap_or_default();
    let ns_path = query
        .iter()
        .find(|(k, _)| k == "ns")
        .map(|(_, v)| v.clone())
        .unwrap_or_else(|| "/".into());

    let host = parts
        .headers
        .get("host")
        .and_then(|v| v.to_str().ok())
        .map(extract_domain)
        .unwrap_or_default();

    let tenant_id = if app.config().has_tenant && !host.is_empty() {
        app.tenant_by_host(&host)
            .await
            .tenant
            .and_then(|t| t.get("_id").cloned())
            .filter(|v| !is_empty(v))
            .and_then(|v| v.as_str().map(String::from))
            .unwrap_or_default()
    } else {
        String::new()
    };

    ws.on_upgrade(move |socket| handle_connection(app, socket, ns_path, tenant_id))
}

/// Handles one accepted WebSocket connection until it closes.
pub(crate) async fn handle_connection(
    app: Yekonga,
    socket: WebSocket,
    ns_path: String,
    tenant_id: String,
) {
    let namespace = app.sockets().namespace(&ns_path);
    let (mut sink, mut stream) = socket.split();
    let (tx, mut rx) = mpsc::unbounded_channel::<String>();

    let client = Arc::new(Client {
        id: new_object_id(),
        tenant_id,
        sender: tx,
        rooms: Mutex::new(HashSet::new()),
    });
    namespace.add_client(client.clone());

    // The write side: forward queued frames to the socket.
    let writer = tokio::spawn(async move {
        while let Some(frame) = rx.recv().await {
            if sink.send(WsMessage::Text(frame.into())).await.is_err() {
                break;
            }
        }
    });

    // Compatible with the Go client: the id first, then a welcome.
    client.send(encode_event("id", &json!(client.id)));
    client.send(encode_event(
        "message",
        &json!("Welcome to the Yekonga WebSocket server!"),
    ));

    while let Some(Ok(message)) = stream.next().await {
        match message {
            WsMessage::Text(text) => {
                handle_message(&app, &namespace, &client, &text).await;
            }
            WsMessage::Close(_) => break,
            _ => {}
        }
    }

    namespace.remove_client(&client.id);
    writer.abort();
}

/// One incoming frame from a client.
async fn handle_message(
    app: &Yekonga,
    namespace: &Arc<Namespace>,
    client: &Arc<Client>,
    text: &str,
) {
    let Ok(Value::Object(message)) = serde_json::from_str::<Value>(text) else {
        return;
    };
    let kind = message
        .get("type")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let data = message.get("data").cloned().unwrap_or(Value::Null);
    let msg_id = message.get("msgId").and_then(Value::as_str);

    // A custom "emit" is dispatched to the built-in event handlers first
    // (Go checks the registered handlers before the generic broadcast).
    if kind == "emit" {
        let event = data
            .get("event")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let payload = data.get("payload").cloned().unwrap_or(Value::Null);
        if !event.is_empty() && handle_event(app, namespace, client, event, payload).await {
            ack(client, msg_id, json!("ok"));
            return;
        }
    }

    match kind {
        "join" => {
            if let Some(room) = data.get("room").and_then(Value::as_str) {
                namespace.join_room(room, client);
            }
        }
        "leave" => {
            if let Some(room) = data.get("room").and_then(Value::as_str) {
                namespace.leave_room(room, client);
            }
        }
        "rejoinRooms" => {
            if let Some(rooms) = data.get("rooms").and_then(Value::as_array) {
                for room in rooms.iter().filter_map(Value::as_str) {
                    namespace.join_room(room, client);
                }
            }
        }
        "createChannel" => {} // rooms are created on demand
        "emit" | "broadcast" => {
            let event = data
                .get("event")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let payload = data.get("payload").cloned().unwrap_or(Value::Null);
            namespace.broadcast(event, &payload, Some(&client.id));
        }
        "toRoom" => {
            let room = data.get("room").and_then(Value::as_str).unwrap_or_default();
            let event = data
                .get("event")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let payload = data.get("payload").cloned().unwrap_or(Value::Null);
            namespace.send_to_room(room, event, &payload, Some(&client.id));
        }
        "toClient" => {
            let target = data
                .get("clientId")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let event = data
                .get("event")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let payload = data.get("payload").cloned().unwrap_or(Value::Null);
            namespace.emit_to_client(target, event, &payload);
        }
        _ => {}
    }

    ack(client, msg_id, json!("ok"));
}

/// The built-in namespace events. Returns whether the event was handled.
async fn handle_event(
    app: &Yekonga,
    namespace: &Arc<Namespace>,
    client: &Arc<Client>,
    event: &str,
    payload: Value,
) -> bool {
    match event {
        "subscribe" => {
            for key in ["userId", "deviceId"] {
                if let Some(room) = payload.get(key).and_then(Value::as_str) {
                    if !room.is_empty() {
                        namespace.join_room(room, client);
                    }
                }
            }
        }
        "unsubscribe" => {
            if let Some(room) = payload.as_str() {
                namespace.leave_room(room, client);
            }
        }
        "acknowledge" => {
            if let Some(id) = payload.as_str() {
                if let Ok(query) = app.query("PushNotification") {
                    let _ = query
                        .where_("id", id)
                        .update(json!({"acknowledged": true, "status": "delivered"}))
                        .await;
                }
            }
        }
        "graphql-request" => {
            // Socket queries run without a request, so no tenant scoping is
            // applied (the Go server uses the connection's request).
            let body = payload.get("body").cloned().unwrap_or(Value::Null);
            let query = body
                .get("query")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let variables = body.get("variables").cloned().unwrap_or(json!({}));
            let listener = payload.get("listener").cloned().unwrap_or(Value::Null);
            let response = app.graphql(query, variables, "", None).await;
            client.send(encode_event(
                "graphql-response",
                &json!({"listener": listener, "body": response}),
            ));
        }
        "run-on-server" | "run-on-client" | "run-on-desktop" => {}
        _ => return false,
    }
    true
}

/// Replies to a frame that carried a `msgId`.
fn ack(client: &Arc<Client>, msg_id: Option<&str>, payload: Value) {
    if let Some(msg_id) = msg_id {
        client.send(json!({"type": "ack", "msgId": msg_id, "payload": payload}).to_string());
    }
}
