//! A minimal server: `cargo run --example basic`, then
//! `curl localhost:8080/health`, `curl localhost:8080/models`, or
//! `curl -XPOST localhost:8080/products -d '{"name":"Tea","price":"2.5"}'`
//! then `curl localhost:8080/products`.

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

    // The local database lives in ~/.yekonga-server/<app name>/database.
    app.post("/products", |req, res| async move {
        match req
            .app()
            .query("Product")
            .unwrap()
            .create(req.body().clone())
            .await
        {
            Ok(product) => res.json(&product),
            Err(err) => res.abort(500, &err.to_string()),
        }
    });

    app.get("/products", |req, res| async move {
        let mut query = req
            .app()
            .query("Product")
            .unwrap()
            .order_by("price", "desc");
        if !req.query("maxPrice").is_empty() {
            query = query.where_(
                "price",
                json!({"lessThanOrEqualTo": req.query_float("maxPrice", 0.0)}),
            );
        }
        match query.find().await {
            Ok(products) => res.json(&products),
            Err(err) => res.abort(500, &err.to_string()),
        }
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
