//! Rust port of the Yekonga server framework.
//!
//! A schema-driven backend: `config.json` configures the server and
//! `database.json` describes the data; routes, middleware and (in later
//! steps of the port) REST, GraphQL, sockets and cloud functions are built
//! on top.
//!
//! ```no_run
//! use yekonga::Yekonga;
//!
//! #[tokio::main]
//! async fn main() -> yekonga::Result<()> {
//!     let app = Yekonga::from_files("config.json", "database.json")?;
//!
//!     app.get("/hello/:name", |req, res| async move {
//!         res.json(&serde_json::json!({"hello": req.param("name")}));
//!     });
//!
//!     app.start(None).await
//! }
//! ```

mod app;
pub mod config;
mod error;
pub mod helper;
pub mod middleware;
pub mod model;
pub mod payload;
mod request;
mod response;
pub mod router;
pub mod schema;

pub use app::{BoxFuture, StaticConfig, Yekonga, COOKIE_ENABLED_KEY, DEFAULT_EXTENSIONS};
pub use config::YekongaConfig;
pub use error::{Error, Result};
pub use middleware::{Abort, MiddlewareKind, MiddlewareResult};
pub use request::{keys, Request};
pub use response::Response;
pub use schema::DatabaseStructure;

/// JSON object type used for records and loosely-typed data (Go's `DataMap`).
pub type DataMap = serde_json::Map<String, serde_json::Value>;
