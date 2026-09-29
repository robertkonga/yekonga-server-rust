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
pub mod audit;
pub mod auth;
pub mod cloud;
pub mod config;
pub mod cron;
pub mod db;
mod error;
pub mod gateway;
pub mod graphql;
pub mod helper;
mod lookup;
pub mod middleware;
pub mod model;
pub mod notify;
pub mod payload;
pub mod payment;
mod query;
mod request;
mod response;
mod rest;
pub mod router;
pub mod schema;
pub mod security;
pub mod socket;
mod upload;

pub use app::{BoxFuture, StaticConfig, Yekonga, COOKIE_ENABLED_KEY, DEFAULT_EXTENSIONS};
pub use config::YekongaConfig;
pub use db::{DbError, LocalBackend};
pub use error::{Error, Result};
pub use middleware::{Abort, MiddlewareKind, MiddlewareResult};
pub use query::ModelQuery;
pub use request::{keys, Request};
pub use response::Response;
pub use schema::DatabaseStructure;

/// JSON object type used for records and loosely-typed data (Go's `DataMap`).
pub use db::DataMap;
