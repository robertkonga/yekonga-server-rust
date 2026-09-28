# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`github.com/robertkonga/yekonga-server-go` is a Go **framework library**, not a standalone app. Consumers `go get` it and call `yekonga.ServerConfig(configFile, databaseFile)` from their own `main.go` to spin up an HTTP server with REST + auto-generated GraphQL + WebSocket/Socket.IO + cloud functions, backed by MongoDB, MySQL, generic SQL, or a local JSON file store. There is no `main` package or executable in this repo itself — the whole framework lives in the `yekonga` package.

## Commands

There is no Makefile, linter config, or CI file in this repo — just plain `go` tooling.

```bash
go build ./...                                    # build everything
go build ./yekonga/... ./config/... ./datatype/... ./helper/... ./gateway/...   # build only first-party code (skips vendored plugin examples that don't compile standalone)
go vet ./yekonga/...                              # static checks
gofmt -l .                                        # formatting check
go test ./...                                     # run tests
```

**Note:** there are currently no `_test.go` files outside `plugins/` (vendored third-party code), so `go test ./...` only exercises vendored plugin tests, not this framework's own logic. When adding tests for framework code, prefer `go test -race` — see the mutex note below.

`plugins/graphql/examples/httpdynamic` fails a plain `go build ./...` with "function main is undeclared" — this is a pre-existing vendored example, not a bug to fix.

To exercise the framework as a consumer would, a test app needs its own `config.json` and `database.json` (see `README.md` "Getting Started" for full field references, and `documentation/development/config.json` / `documentation/development/database.json` for minimal examples) and calls:

```go
app := yekonga.ServerConfig("./config.json", "./database.json")
app.Get("/", handler)
app.Start(":8080")
```

## Repo layout

- `yekonga/` — the framework itself (single package `yekonga`). Routing, request/response, middleware, models, query builder, GraphQL, cloud functions, cron, DB connections, sockets all live here as top-level files (`main.go`, `request.go`, `response.go`, `middleware.go`, `model.go`, `model_query.go`, `graphql.go`, `dbconnect*.go`, `cloud_functions.go`, `cronjob.go`, `socket.go`, ...). Treat this as one large package — grep across it rather than assuming a file's name fully scopes its contents.
- `config/` — parses `config.json` into `YekongaConfig`.
- `datatype/` — shared generic types (e.g. `DataMap`).
- `helper/` — large stdlib-style utility grab bag (type conversion/checking, string/date manipulation, data extraction, mail, images, serial numbers).
- `gateway/` — SMS/WhatsApp provider integrations (Beem, Infobip) behind a common `gateway/setting` interface.
- `plugins/` — **vendored/forked third-party source**, not app code: `mongo-driver`, `mysql`, `redigo`, `socketio`, `websocket`, `graphql` (forked graphql-go), `uuid`, and a custom local file-based DB engine (`plugins/database`). None of these have their own `go.mod` — they're imported directly under `github.com/robertkonga/yekonga-server-go/plugins/...`. Don't "fix" these to match upstream unless specifically asked; changes here are effectively patching a vendored fork.
- `documentation/` — a **separate nested git checkout** (its own `.git`), not a submodule of this repo. Contains longer-form internal docs (`documentation/documentations/INTERNAL_*.md`) plus example `config.json`/`database.json` and GraphQL schema dumps. Useful as reference but edits here don't belong to this repo's history.
- `README.md` / `README_GRAPHQL.md` — the consumer-facing docs; `README_GRAPHQL.md` documents the auto-generated GraphQL query/mutation shape for an example schema.

## Architecture

**Startup (`ServerConfig` in `yekonga/main.go`):** load `config.json` → `NewDatabaseStructure` parses `database.json` into model definitions → `NewSystemModels` builds `*DataModel`s → `NewDatabaseConnections` opens the DB backend selected by `config.database.kind` (`mongodb` / `mysql` / `sql` / `local`, strategy pattern across `dbconnect_mongodb.go` / `dbconnect_mysql.go` / `dbconnect_sql.go` / `dbconnect_local.go`) → GraphQL schema auto-built from the same `DataModel`s (`GraphqlAutoBuild`) → returns the `*YekongaData` singleton (`Server`).

**Models are schema-driven, not Go structs.** `database.json` (parsed as `yekonga.DatabaseStructureType`) is the source of truth for field names, types, and options; `NewSystemModels` turns it into `*DataModel`s used by both the REST layer and the GraphQL auto-build. There's no per-model Go type to define — extending the data model means editing `database.json`, not writing a struct.

**Request lifecycle** (`ServeHTTP` in `yekonga/main.go`) runs middleware in this fixed order — built-ins first, then the three user-registrable hook points:

1. `MasterKeyMiddleware`, `ApplicationIDMiddleware` (built-in, always run)
2. `y.preloadMiddlewares` — user hooks registered via `app.Middleware(fn, yekonga.PreloadMiddleware)`
3. `ClientMiddleware`, `TenantCatchMiddleware`, `TokenMiddleware`, `BillingMiddleware`, `UserInfoMiddleware` (built-in auth/tenant/billing pipeline)
4. `y.initMiddlewares` — user hooks via `app.Middleware(fn, yekonga.InitMiddleware)`
5. `y.middlewares` — user hooks via `app.Use(fn)` (global, runs last, right before route dispatch)
6. route handler dispatch; if no route matched, `y.catchMiddlewares` run as the 404 fallback chain

REST and GraphQL requests both terminate in the same data layer: `ModelQuery` (fluent builder — `Where/OrderBy/Take/Skip/Select` → `Find/FindOne/FindById/Create/Update/UpdateMany/Delete/Count`) dispatches to whichever `dbconnect_*.go` backend is active. GraphQL additionally wraps this in the auto-built schema/resolvers, so a new field in `database.json` shows up in both REST and GraphQL automatically without separate wiring.

**Extension points** (called on the `*YekongaData`/`app` instance):
- Routes: `app.Get/Post/Put/Patch/Delete(path, handler)`, `app.All(path, handler)`, `app.Static(StaticConfig{...})`
- Middleware: `app.Use(fn)` (global) or `app.Middleware(fn, yekonga.InitMiddleware|PreloadMiddleware)` (phase-specific, see lifecycle above)
- Cloud functions (callable/backend functions): `app.Define("name", fn)` — a `CloudFunction` with signature `func(interface{}, *RequestContext) (interface{}, error)`
- DB triggers: `app.BeforeCreate/AfterCreate/BeforeUpdate/...("ModelName", fn)` — `TriggerFunction` with signature `func(*RequestContext, *QueryContext) (interface{}, error)`, or the `*All` variants for cross-model triggers
- Custom GraphQL mutations: register `ActionCloudFunction`s (`func(*RequestContext, *QueryContext) (GraphqlActionResult, error)`) per model/action
- Public (unauthenticated) routes: `config.json`'s `public` array

## Conventions / gotchas

- `YekongaData`, `RequestContext`, and `GraphqlAutoBuild` are protected by `sync.RWMutex` and shared across concurrent requests. When touching server-wide state (routes, models, middleware slices), preserve existing lock discipline and prefer `go test -race` for anything exercising them.
- Middleware and handlers pass data to each other via `req.SetContext`/`req.GetContext`, not package-level globals.
- Two config files drive everything: `config.json` (server/db/auth/graphql/mail settings — see README "Configuration" for the full field list) and `database.json` (data schema, also drives GraphQL types). Both are required by `ServerConfig`.
- `config.database.kind = "local"` uses a file-based JSON store (in `plugins/database`) — useful for local dev without standing up a real database.
