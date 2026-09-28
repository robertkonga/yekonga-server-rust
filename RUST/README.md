# Yekonga Server (Rust port)

A Rust port of the Go framework in [`../GO`](../GO). It reads the same
`config.json` and `database.json` files and follows the Go server's
behavior, including its route patterns, middleware order, error responses
and access tokens.

The port is being done in steps. Steps 1 (the foundation) and 2 (the query
builder, local database and MongoDB) are done.

## Status

| Area | Status |
|---|---|
| `config.json` loading (all fields, Go-style case-insensitive keys) | ✅ |
| `database.json` schema + built-in collections (auth, tenant, billing, payment, extra) | ✅ |
| Models: naming, field types, parent/child relations | ✅ matches Go output for all 49 models (see `tests/go_parity.rs`) |
| Routing: `:param`, `:param?`, several params per segment, `baseURL` | ✅ |
| Request / response API, error pages, redirects, file & download responses | ✅ |
| Middleware chain: master key, app key, client, token, user info + preload/init/global/catch hooks | ✅ |
| Access tokens (HS256 JWT, wire-compatible with the Go server) | ✅ |
| Static files (ranges, cache headers, extension allow-list) | ✅ |
| gzip, CORS headers, body size limit, `YEKONGA_ENABLED` cookie | ✅ |
| Query builder: where operators, `AND`/`OR`/`NOR`, relation filters, sort, paging, aggregates, create/update/delete | ✅ |
| Local database (`database.kind: "local"`) | ✅ JSON files, for development and tests |
| Tenant lookup by domain (with TenantConfig), TenantCatch, user lookup on authorization servers, lookup caches | ✅ |
| Tenant scoping of queries made with a request | ✅ |
| MongoDB (`database.kind: "mongodb"`), including startup indexes | ✅ shares a database with the Go server (see below) |
| MySQL / SQL backends | ⏳ next; queries fail with "not supported yet" until then |
| REST API (`restAPI`) and auto-generated GraphQL | ⏳ step 3 |
| Auth endpoints (`/me`, `/logout`, `/refresh`, login/OTP) | ⏳ step 3 |
| Cloud functions, DB triggers, cron jobs, WebSocket / Socket.IO | ⏳ step 4 |
| SMS / WhatsApp / payment gateways, mail, uploads, rate limit, error guard, TLS | ⏳ step 5 |

A tenant id set by a preload middleware (`req.set_tenant_id(...)`) is kept
when the domain lookup finds no tenant.

## Usage

```rust
use serde_json::json;
use yekonga::{Abort, MiddlewareKind, Yekonga};

#[tokio::main]
async fn main() -> yekonga::Result<()> {
    let app = Yekonga::from_files("config.json", "database.json")?;

    app.get("/orders/:id", |req, res| async move {
        res.json(&json!({"id": req.param("id"), "user": req.auth()}));
    });

    app.get("/orders", |req, res| async move {
        // Limited to the request's tenant when multi-tenancy is on.
        let orders = req.app().query("Order").unwrap().set_request(&req)
            .where_("status", "paid")
            .where_("total", json!({"greaterThan": 100}))
            .order_by("createdAt", "desc")
            .take(20)
            .find()
            .await;
        match orders {
            Ok(orders) => res.json(&orders),
            Err(err) => res.abort(500, &err.to_string()),
        }
    });

    app.middleware(MiddlewareKind::Init, |req, _res| async move {
        if req.header("x-blocked") == "yes" {
            return Err(Abort::new(403, "blocked"));
        }
        Ok(())
    });

    app.start(None).await // uses ports.server from config.json
}
```

Run the example with `cargo run --example basic`, then try
`curl localhost:8080/health` or `curl localhost:8080/models`.

### Differences from the Go API

| Go | Rust |
|---|---|
| `yekonga.ServerConfig(config, structure)` | `Yekonga::new(config, structure)` |
| `yekonga.ServerLoad("config.json", "database.json")` | `Yekonga::from_files(...)?` (errors are returned, not printed) |
| `func(req *Request, res *Response)` | `\|req, res\| async move { ... }` |
| middleware `(int, error)` | `Result<(), Abort>`, where `Abort::new(status, message)` |
| `app.Middleware(fn, yekonga.InitMiddleware)` | `app.middleware(MiddlewareKind::Init, fn)` |
| `app.Catch(fn)` / global middleware | `app.catch(fn)` / `app.use_middleware(fn)` |
| `app.Static(StaticConfig{...})` | `app.serve_static(StaticConfig::new(dir, prefix))?` |
| `req.GetContext(key)` (any Go value) | `req.get_context(key)` (a `serde_json::Value`) |
| `req.Client()`, `req.Auth()`, `req.TokenPayload()` | same names in snake_case; they return `Option<...>` |
| `app.Start(port)` | `app.start(Some(port)).await` |
| `app.ModelQuery("Order").Where(...).Find(nil)` | `app.query("Order")?.where_(...).find().await?` |
| `query.SetRequest(req, res)` | `query.set_request(&req)` |
| `Find` / `FindOne` return `nil` or an empty map | `find()` returns `Vec`, `find_one()` returns `Option`, errors are `Err` |
| `Update(data, where)` / `Delete(where)` | `.where_(...)` first, then `update(data)` / `delete()` |

`Request` and `Response` are cheap handles that can be cloned. Every clone
refers to the same request, so a value one middleware stores is visible to
everything that runs after it.

### Intentional differences

- Relative `public` directories keep their first character. The Go code
  turns `"public"` into `"./ublic"`.
- `ports.secure: true` fails at startup instead of serving TLS. Until TLS
  is ported, terminate TLS at a reverse proxy.
- `security.rateLimit` and `security.errorGuard` are not enforced yet. A
  warning is logged at startup if they are enabled.
- `res.redirect(url)` uses 302 if no redirect status was set. Go's
  `http.Redirect` would send the current status, often 200.
- If a handler panics, the client gets a 500 response. Go drops the
  connection.
- Numbers written to Number fields are converted correctly. Go's
  `helper.ToInt` parses JSON numbers in base 32, so `12` is stored as 34.
- The local database stores JSON files, not the Go server's tiedot files.
  Unlike Go's local backend, it applies sorting, and count/sum/max/min/average
  respect the filter.
- Deleting with no conditions is refused on every backend. In Go, only the
  MongoDB backend refuses it.
- In a create, `id` or `_id` in the input sets the record's id. In Go, an
  `_id` without `id` ends up stored as `"id": null` next to a new `_id`.
- Sort fields apply in the order given. Go keeps them in a map, so their
  priority is random.
- Database triggers, the audit trail and socket change events are not run
  yet (step 4).
- The client IP falls back to the connection's address when there is no
  `X-Forwarded-For` or `X-Real-Ip` header.

## Sharing a MongoDB database with the Go server

The MongoDB backend stores records in the same shape as the Go server:
- the same collection names
- ObjectIds for `_id`, `tenantId` and ID fields
- BSON dates for Date fields
- numbers for Number and Float fields

This was checked against MongoDB 7. The Go server wrote 4 orders and the
Rust port wrote 4 more into the same collection. Both then ran the same 13
queries (plain values, `in`/`notIn`/`all`, `exists`, `OR`, number and date
comparisons, a relation filter) and the aggregates over all 8 records, and
the results were identical.

## Development

```bash
cargo test                      # unit, HTTP pipeline and Go-parity tests
YEKONGA_TEST_MONGO_PORT=27017 cargo test   # also run the query tests on MongoDB (drops yekonga_rust_test_* databases)
cargo clippy --all-targets
cargo fmt --check
```

`tests/fixtures/go_*.json` hold outputs recorded from the Go
implementation: naming helpers, path matching, domains and the built
models. `tests/go_parity.rs` checks the Rust port against them. To
regenerate the fixtures, add a Go test that dumps the same values and run it
in `../GO`.

`src/schema/defaults.json` is the Go `Default*DatabaseStructure` values
(`GO/yekonga/database_json.go`) converted to JSON. If the Go file changes,
regenerate this file.
