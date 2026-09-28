# Yekonga Server (Rust port)

A Rust port of the Go framework in [`../GO`](../GO). It reads the same
`config.json` and `database.json` files and follows the Go server's
behavior, including its route patterns, middleware order, error responses
and access tokens.

The port is being done in steps. **This is step 1**: the foundation that
everything else builds on.

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
| Tenant lookup by domain, user lookup on authorization servers | ⏳ needs the database layer (step 2) |
| Query builder + local / MongoDB / MySQL / SQL backends | ⏳ step 2 |
| REST API (`restAPI`) and auto-generated GraphQL | ⏳ steps 2–3 |
| Auth endpoints (`/me`, `/logout`, `/refresh`, login/OTP) | ⏳ step 3 |
| Cloud functions, DB triggers, cron jobs, WebSocket / Socket.IO | ⏳ step 4 |
| SMS / WhatsApp / payment gateways, mail, uploads, rate limit, error guard, TLS | ⏳ step 5 |

Until the tenant lookup is ported, `hasTenant` apps can set the tenant
themselves in a preload middleware (`req.set_tenant_id(...)`).

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
- The client IP falls back to the connection's address when there is no
  `X-Forwarded-For` or `X-Real-Ip` header.

## Development

```bash
cargo test                      # unit, HTTP pipeline and Go-parity tests
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
