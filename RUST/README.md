# Yekonga Server (Rust port)

A Rust port of the Go framework in [`../GO`](../GO). It reads the same
`config.json` and `database.json` files and follows the Go server's
behavior, including its route patterns, middleware order, error responses
and access tokens.

The port is being done in steps. Step 1 (the foundation), step 2 (the query
builder and all four database backends), step 3 (GraphQL, REST and the auth
endpoints) and step 4 (cloud functions, triggers, the audit trail, cron jobs,
the WebSocket server and socket change events) are done. Step 5 is under way:
rate limiting, the error guard and the IP whitelist are done; the gateways,
mail, uploads and TLS are next.

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
| MySQL (`database.kind: "mysql"` or `"sql"`), with table/column migration and indexes | ✅ shares a database with the Go server (see below) |
| Auto-generated GraphQL schema (`graphql.apiRoute`) | ✅ identical to Go's (see below) |
| GraphQL queries: single, list, paginate, summary (count/sum/max/min/average), relations in both directions | ✅ |
| GraphQL mutations: create (with nested children), update, delete, import | ✅ |
| REST API (`restApiEnabled`, `/api/:model…`) | ✅ |
| GraphQL `groupBy`/`distinct`, summary `graph`, `download…`, `…Action`, custom fields | ⏳ return "not supported by the Rust port yet" |
| Auth GraphQL schema (`graphql.apiAuthRoute`), on authorization servers | ✅ identical to Go's, with and without `secureAuthentication` (see below) |
| Auth GraphQL: `otp`, `login` (password/OTP), `refreshToken`, `profile`, `register`, `tenantAvailability` | ✅ |
| Auth endpoints `/me`, `/logout`, `/refresh` (with optional `/:moduleName`) | ✅ |
| Access & refresh tokens (bcrypt passwords, hashed refresh tokens, auth cookies), user permissions | ✅ |
| Auth GraphQL: `socialLogin`, `contactOTP`, `contactVerify`, `resetPassword`, `confirmToken`, `changePassword`, `switchAccount` | ⏳ return "not supported by the Rust port yet" |
| OTP delivery (SMS / WhatsApp / mail gateways) | ⏳ codes are stored; a hook (`set_otp_sender`) delivers them until step 5 |
| Cloud functions (`define`/`run`) | ✅ |
| Database triggers (before/after find/create/update/delete, per model or `*_all`) | ✅ |
| Auth triggers (before/after login, OTP, register) | ✅ |
| Audit trail (`auditTrail.enabled`, buffered per request, flushed to `AuditTrail`) | ✅ |
| Cron jobs (`register_cronjob`, `register_cronjob_at`) | ✅ run when `hasCronjob` and the server is started |
| `FetchTenantByDomain` fallback on tenant-catch servers | ✅ |
| WebSocket server (`/yekonga.io/`): namespaces, rooms, broadcast/to-room/to-client, `subscribe`/`unsubscribe`/`acknowledge`/`graphql-request` | ✅ |
| Database change events pushed to a tenant's socket clients | ✅ |
| Rate limiting (`security.rateLimit`, per-client token bucket) | ✅ |
| Error guard (`security.errorGuard`, blocks error-flooding clients, persists to `IpAccessRule`) | ✅ |
| IP whitelist (`IpAccessRule` `whitelist` rows exempt a client from both) | ✅ |
| SMS / WhatsApp / payment gateways, mail, uploads/downloads, TLS | ⏳ step 5 |
| WebSocket JS SDK (`/yekonga.io/yekonga.io.js`) | ⏳ the embedded client script isn't ported |

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
- `security.rateLimit`, `security.errorGuard` and the `IpAccessRule`
  whitelist are enforced. The rate limiter is a per-client token bucket; the
  error guard blocks a client that sends more than `requestsPerSecond` error
  responses in one second (permanently, or for `blockHours`) and persists the
  block to `IpAccessRule`; `whitelist` rows exempt a client from both. The
  client is keyed by `X-Forwarded-For`/`X-Real-Ip` only when
  `security.trustProxyHeaders` is set, otherwise by the connection's address.
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
- On MySQL, list fields such as `[String]` (stored as JSON text, as in Go)
  are filtered by element like `Array` fields, as MongoDB does. Go's SQL
  backend compares the whole JSON text, so `tags = "x"` never matches there.
  String comparisons on MySQL follow the column's collation, which ignores
  case and accents by default. This is the same in Go.
- The REST write routes (`POST /api/:model/create`, `PUT`, `PATCH`,
  `…/update/:id`, `DELETE`, `…/delete/:id`) create, update and delete. In
  Go they send malformed GraphQL (a query named `createOrders` without the
  input), so only the GET routes work there.
- GraphQL mutation results hide protected fields (`--protected--`), as
  query results do. Go returns them in plain text, e.g. a user's password
  hash from `createUser`.
- Lists and objects written directly in a GraphQL query (`tags: ["x"]`) are
  stored as values. Go stores the parser's internal nodes, including a
  base64 copy of the query text. Values passed as variables work in both.
- GraphQL validation errors other than unknown fields keep the GraphQL
  library's wording. For example, a missing required input field is reported
  as `field "title" of type "String!" is required but not provided`, where
  Go says `In field "title": Expected "String!", found null.`
- Relation fields are resolved one query at a time. Go batches them on
  MongoDB; the results are the same.
- The WebSocket endpoint is `/yekonga.io/`; the namespace is chosen with
  `?ns=`. The `graphql-request` socket event runs without a request, so
  tenant scoping isn't applied to it. Go runs it as the connection's request.
  The embedded JavaScript client (`/yekonga.io/yekonga.io.js`) isn't served.
- Database triggers run around every read and write, before and after, unless
  the query is marked `skip_before_commit`. Go runs `after` triggers even on
  `skipBeforeCommit` queries (including its own internal writes); the port
  skips both `before` and `after` triggers there, so internal framework
  writes never fire triggers.
- `before`/`after` create triggers run once per record. Go's `Import` runs
  them once on the whole list, while its `Create` runs them per record; the
  port is consistent and always per record.
- The audit trail records the same fields Go does (action, model, document
  id, old and new values, tenant/profile/user and client details) and is
  flushed to the `AuditTrail` collection in the background once the request
  ends. The document id comes from the record's `_id`/`id`. Go reads it from
  the model's primary field, which for most models is a display field, not
  the id.
- The client IP falls back to the connection's address when there is no
  `X-Forwarded-For` or `X-Real-Ip` header.
- **Login checks the password.** Go's `AttemptLogin` treats the literal
  password `"true"` as a match for every account (`main_function.go`), so
  anyone can sign in as anyone. The Rust port only accepts the account's
  bcrypt password or the configured `globalPassword`. Go's `$2a$` hashes
  verify unchanged.
- OTP codes and refresh tokens use the operating system's random generator.
  Go builds them from `math/rand` seeded with the clock (`GetRandomString`),
  so its codes and tokens are predictable.
- OTP codes are not delivered on their own: the SMS, WhatsApp and mail
  gateways are step 5. A code is stored and, if a sender is registered with
  `Yekonga::set_otp_sender`, handed to it; otherwise a warning is logged.
- Auth mutations that only look a user up in Go (`socialLogin`,
  `contactOTP`, `contactVerify`, `resetPassword`, `confirmToken`,
  `changePassword`, `switchAccount`) return "not supported by the Rust port
  yet" instead.
- `getUserPermission` guards against a missing `Tenant` model. Go panics
  when it isn't in the schema.

## GraphQL compatibility

The schema is built with the same type, field, argument and enum names as
the Go server. `tests/go_parity.rs` compares it against Go's introspection
output (`tests/fixtures/go_graphql_schema.txt`: 533 types, 6,512 lines), and
against a hash of the schema with every module enabled (1,294 types). Both
match exactly.

The auth schema (`graphql.apiAuthRoute`, on authorization servers) is
compared the same way, both with and without `secureAuthentication`
(`tests/fixtures/go_auth_schema*.txt`); `login` and `refreshToken` return a
`CredentialToken` with it and a `Profile` without it, as in Go. The only
difference is the GraphQL library's built-in `Float` scalar, which no auth
field uses and Go leaves out.

The resolvers were checked the same way, with both servers on one MongoDB
database. 19 queries covered filters, sorting, paging, relations in both
directions, nested pagination and summaries, self-relations, protected and
date fields, variables and error messages. All 19 returned identical JSON.
Of 9 mutations, the ones that differed are the Go bugs and the error wording
listed below.

## Sharing a database with the Go server

The MongoDB and MySQL backends store records in the same shape as the Go
server, so both servers can use one database.

On MongoDB they use the same collection names, ObjectIds for `_id`,
`tenantId` and ID fields, BSON dates for Date fields, and numbers for Number
and Float fields.

On MySQL they use the same tables and column types (`_id VARCHAR(64)`
primary key, `DATETIME(3)` dates, `JSON` for Object/Any/Array), the same
index names, and filters with MongoDB's meaning for missing values. Tables
and missing columns are created on first use unless
`database.disableAutoMigrate` is set. Existing columns are never changed.

This was checked against MongoDB 7 and against MySQL 8.4. On each, the Go
server wrote 4 orders and the Rust port wrote 4 more into the same
collection or table. Both then ran the same 13 queries (plain values,
`in`/`notIn`/`all`, `exists`, `OR`, number and date comparisons, a relation
filter) and the aggregates over all 8 records, and the results were
identical.

## Development

```bash
cargo test                      # unit, HTTP pipeline and Go-parity tests
# Also run the query tests on MongoDB and/or MySQL (throwaway servers:
# they drop and recreate yekonga_rust_test_* databases).
YEKONGA_TEST_MONGO_PORT=27017 YEKONGA_TEST_MYSQL_PORT=3306 cargo test
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
