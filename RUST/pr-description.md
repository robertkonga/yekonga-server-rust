First step of porting the Go framework in `GO/` to Rust. This adds a `yekonga` crate under `RUST/` with the foundation the rest of the port builds on. It reads the same `config.json` and `database.json` files and follows the Go server's behavior.

## What's included
- **Config:** loads every `config.json` field. Keys match the way Go's `encoding/json` matches them (ignoring case, so `connectionId` fills `connectionID`), and a `null` or wrongly typed value falls back to the default.
- **Schema:** loads `database.json` and adds the 46 built-in collections (auth, tenant, billing, payment and the always-on extras), merged the same way `NewDatabaseStructure` does.
- **Models:** naming, field types, and parent/child relations.
- **Routing:** `:param`, optional `:param?`, several parameters in one segment, and the `baseURL` prefix.
- **Request/response API:** JSON error responses or HTML error pages from `abort`, redirects, file and download responses (with range requests).
- **Middleware chain, in the Go order:** master key → app key → preload hooks → client → tenant rule → token → billing → user info → init hooks → global hooks → handler, or catch hooks and then the 404 page.
- **Access tokens:** HS256 JWT. The encoding is compatible with the Go server in both directions (padded base64).
- **Other:** static files, gzip for responses of 1 KB or more, CORS headers, the request body size limit, the `YEKONGA_ENABLED` cookie, and a 500 response when a handler panics.
- **Example:** a runnable server in `RUST/examples/basic`.

## Not in this PR
- Looking up the tenant by domain and loading the user on authorization servers. Both need the database layer, which is the next step. Until then, an app can set the tenant in a preload middleware.
- REST, GraphQL, the auth endpoints, cloud functions, cron jobs, sockets, the SMS/WhatsApp/payment gateways, rate limiting, the error guard, and TLS. Setting `ports.secure` makes startup fail instead of serving plain HTTP.

`RUST/README.md` lists the status of each area and the deliberate differences from Go.

## Other change
`RUST` was tracked as a gitlink with no `.gitmodules` entry, pointing at this repo's own first commit, so git couldn't track any files under it. It is now a normal directory.

## Testing
- `cargo test`: 47 tests. These cover unit tests, 18 HTTP tests that run requests through the server, and comparison tests against the Go code.
- **Comparison with Go:** `tests/fixtures/go_*.json` were produced by running the Go `helper` functions and `NewSystemModels` on the same inputs. The Rust naming helpers, path matching and domain helpers produce the same results, and so does the model build: all 49 models, with every field, default and relation.
- `cargo clippy --all-targets` and `cargo fmt --check` are clean.
- I ran `cargo run --example basic` and checked `/health`, route parameters, middleware aborts and the 404 page with curl.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_012ZeeqHTVHFxqXYrFnmrqvz
