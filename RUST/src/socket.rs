//! The WebSocket server and database change events (port of
//! `yekonga/socket.go`).
//!
//! Step 4a defines the change-event hook the query builder calls after every
//! write. The Socket.IO-style connection handling, rooms and namespaces are
//! filled in by step 4b; until then there are no connected clients, so an
//! event has nowhere to go.

/// Manages WebSocket namespaces and clients (Go's `SocketServer`).
#[derive(Default)]
pub struct SocketServer {}

impl SocketServer {
    pub(crate) fn new() -> Self {
        Self::default()
    }

    /// Tells subscribed clients a model's data changed (Go's
    /// `emitDatabaseEvent`): a write to a tenant's data reaches that tenant's
    /// clients and clients without a tenant; an empty tenant reaches all.
    pub(crate) fn emit_database_event(&self, model: &str, action: &str, tenant_id: &str) {
        // Connection handling arrives with step 4b; nothing is connected yet.
        tracing::trace!(model, action, tenant_id, "database change event");
    }
}
