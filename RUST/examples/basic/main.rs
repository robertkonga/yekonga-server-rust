//! A minimal server: `cargo run --example basic`, then
//! `curl localhost:8080/health` or `curl localhost:8080/models`.

use serde_json::json;
use yekonga::{Abort, Yekonga};

#[tokio::main]
async fn main() -> yekonga::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();

    let dir = concat!(env!("CARGO_MANIFEST_DIR"), "/examples/basic");
    let app = Yekonga::from_files(format!("{dir}/config.json"), format!("{dir}/database.json"))?;

    // Models come from database.json plus the framework's built-in collections.
    app.get("/models", |req, res| async move {
        let models: Vec<_> = req
            .app()
            .models()
            .values()
            .map(|m| json!({"name": m.name, "collection": m.collection, "fields": m.valid_fields}))
            .collect();
        res.json(&models);
    });

    app.get("/hello/:name?", |req, res| async move {
        let name = match req.param("name") {
            name if name.is_empty() => "world".to_string(),
            name => name,
        };
        res.json(&json!({"hello": name, "client": req.client()}));
    });

    // Needs an access token: `Authorization: Bearer <jwt>`.
    app.get("/me-example", |req, res| async move {
        match req.auth() {
            Some(auth) => res.json(&auth),
            None => {
                res.status(401).json(&json!({"error": "sign in first"}));
            }
        }
    });

    app.use_middleware(|req, _res| async move {
        if req.header("x-blocked") == "yes" {
            return Err(Abort::new(403, "blocked by example middleware"));
        }
        Ok(())
    });

    app.start(None).await
}
