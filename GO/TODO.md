# TODO — Performance review

Findings from a performance review of the `yekonga` framework (2026-09-26), ordered by priority.
Line references point to the code as of commit `359c718`; re-check them before editing.

## Measured baselines

Benchmarked with a throwaway `_test.go` in `yekonga/` (run with `go test -vet=off`, see note at the bottom).

| Hot path | Current | After fix | Gain |
|---|---|---|---|
| Route matching, 40 routes | 553 µs, 218 KB, 2,530 allocs per request | 4.3 µs, 1 alloc | ~130× |
| `helper.GetValueOf(map, "name")` | 21.6 µs, 104 allocs | 23 ns (direct lookup) | ~1000× |
| `helper.ToSlug` (runs on every trigger lookup) | 9.6 µs, 52 allocs | ~0 with its regex precompiled | — |

Route matching alone caps throughput at roughly 1,800 requests per second per core before any real work happens.

---

## P0 — Large impact, low risk

- [x] **Precompile route regexes.** `matchRoute` calls `regexp.MustCompile` for every route on every request (`yekonga/main.go:366`). Compile once in `addRoute` and store a `*regexp.Regexp` on `Route`.
- [x] **Hoist per-call regexes to package-level vars** in `helper/helpers.go`: `ToSlug` (:577), `ToUnderscore` (:648, :652, :678), `GetBaseUrl` (:1481), `GetMainDomain` (:1488), email/phone validators (:2007, :2025), and `helper/template.go` (:233, :237). Also `isValidParam` and `parseSegment` in `yekonga/main.go`.
- [x] **Fix lock contention and deadlock risk on `y.mut`.** (also `authTriggerCallback`)
  - `findRoute` takes an exclusive `y.mut.Lock()` on every request (`yekonga/main.go:434`). Use `RLock`; routes are effectively immutable after `Start`.
  - `triggerCallback` / `triggerAllCallback` hold `y.mut.RLock()` while user trigger code runs, including DB I/O (`yekonga/cloud_functions.go:322`, `:417`). Routing then waits on every in-flight trigger, and nested queries in triggers re-acquire the read lock, which can deadlock when a writer is waiting. Lock only to look up the function, release, then call it.
- [x] **Cache tenant resolution.** Done in `yekonga/lookup_cache.go`: 30 s TTL (`cache.tenantSeconds`), capped at 10k hosts, cleared on any `Tenant`/`TenantConfig` write through `DataModelQuery`. With `HasTenant`, each request runs 3 DB queries:
  - `TenantCatchMiddleware` looks up `Tenant` by host (`yekonga/middleware.go:85`).
  - `GetTenantConfig` runs the **same** `Tenant` lookup again, then `TenantConfig`, then a JSON round-trip via `helper.ConvertTo` (`yekonga/main_other_functions.go:17`, `:32`, `:92`).
  - Add an in-memory cache keyed by host (TTL ~30–60 s), invalidated from after-create/update/delete triggers on `Tenant` and `TenantConfig`.
  - [x] `UserInfoMiddleware` on an authorization server: user records cached for 10 s (`cache.userSeconds`), cleared on any `User` write; each request gets its own copy.
  - [x] `HasTenantCatch` path: the `TenantCatch` lookup is cached (including misses) and cleared on `TenantCatch` writes. The `FetchTenantByDomain` cloud function itself is never cached.
- [x] **Rewrite Mongo filter building as a single pass.** Output verified identical to the old builder on 16 filter shapes; the filter is also built once per connection instead of per use.
  - `where()` rebuilds the full filter once per key (`yekonga/dbconnect_mongodb.go:512`); `extractWhereObject` rescans the whole map per map-valued key (:604). Cost grows much faster than the number of keys.
  - Relation filters run a `Find` sub-query (:764, :781) inside those passes, so the sub-query repeats: ~6× per `where()` for 3 keys with one relation filter, and `Paginate` calls `where()` twice.
  - **Security:** that sub-query has no `RequestContext`, so it skips the tenant filter and scans across tenants. Pass the parent's request context through. (Done; `skipTenant` is propagated too.)

## P1 — GraphQL

- [x] **Remove the wasted second parse.** `QuerySelectors` is still filled (it's an exported field), but computed once per unique query and cached. `yekonga/initializer.go:239-241` parses the query, converts the AST to JSON and back, and builds `QuerySelectors`. The only consumer is a loop that calls `fmt.Sprintf` and discards the result (`yekonga/graphql.go:2295`). Delete both.
- [x] **Cache parsed and validated documents.** `yekonga/graphql_cache.go`: per-schema cache, 1 h TTL, 1000 queries, queries over 16 KB not cached. ~8× less overhead per request on a small query. Add an LRU keyed by query string (or its hash) holding the AST and validation result, so repeated queries skip `parser.Parse` and validation.
- [x] **Fix N+1 on relation fields.** `yekonga/graphql_loader.go`, MongoDB only. Triggers still run per row; only the fetch is batched. Fields with `limit`/`page`/`skip`/`groupBy`/`distinct` stay per row. Integration test: `YEKONGA_TEST_MONGO_PORT=<port> go test -run TestRelationLoader ./yekonga/`. Each parent row resolves each relation with its own query (`setModelParams`, `yekonga/graphql.go:2135`). A 50-row list with 3 relations issues 150+ extra queries. Add a per-request dataloader that batches by foreign key with `$in`; the commented-out code in `loadRelatedData` was heading this way.
- [x] **Stop using JSON round-trips for type conversion on hot paths:** `ToMap` converts maps directly with identical results; `Auth()` decodes once per request; `GetMapValue` reads plain maps directly.
  - `helper.ToMap[...]` on resolver args (`yekonga/graphql.go:2150`, `:2214`, `:2240`).
  - `Request.Auth()` marshals and unmarshals on every call (`yekonga/request.go:339`). Store a typed `AuthPayload` once in `UserInfoMiddleware`.
  - `helper.GetMapValue` copies the whole map via reflection to read one key (`helper/helpers.go:1656`). Fast-path `datatype.DataMap` / `map[string]interface{}` with a type assertion. Also remove the leftover `key == "data.clients"` debug branch.
- [x] **Cheaper output formatting.** `formateOutputData` calls `GetBaseUrl` (regex) and `OriginDomain` (`url.Parse`) per row per file field (`yekonga/graphql.go:2341`). Compute the domain once per request.

## P1 — Database layer

- [x] **Propagate request context and timeouts.** Reads stop when the client disconnects mid-request; writes don't. `database.queryTimeoutSeconds` (default 0 = none). `DataModelQuery.collection()` uses `context.TODO()` (`yekonga/model_query.go:1430`), and the Mongo backend uses `context.TODO()` throughout. Queries can't be cancelled when the client disconnects and have no deadline. Pass `req.HttpRequest.Context()` with a configurable timeout.
- [x] **Create indexes.** At startup in the background: `tenantId`, foreign keys, and fields marked `"index"`/`"unique"`. Opt out with `database.disableAutoIndexes`. The framework never creates any. Add index declarations to `database.json` and an `EnsureIndexes` step at startup. Minimum: `tenantId` (added to every tenant query), foreign keys, and the tenant domain fields (`domain`, `subdomain`, `customDomain`, `customSubdomain`).
- [x] **Remove extra round trips on writes** (`yekonga/dbconnect_mongodb.go:424-473`).
  - `create`: insert then `findOne`. Build the result from the inserted data and `InsertedID`.
  - `update`: update then `findOne` (plus another `findOne` for audit in `model_query.go:391`). Use `FindOneAndUpdate` with `ReturnDocument: After`.
- [x] **Run `Paginate`'s count and find concurrently** (`yekonga/dbconnect_mongodb.go:187`). Consider `EstimatedDocumentCount` when the filter is empty.
- [x] **Make the Mongo pool configurable** (`maxPoolSize`, `minPoolSize`, timeouts) in `config.json` (`yekonga/dbconnect.go:134`).

## P2 — Smaller, cheap to fix

- [x] **Pool gzip writers.** Also fixed: `Download` (via `http.ServeContent`) sent gzipped bytes without `Content-Encoding` after the headers were out, corrupting downloads for browsers. `initGzip` allocates a new compressor (hundreds of KB) per response, even for tiny bodies (`yekonga/response.go:156`). Use `sync.Pool` and skip compression under ~1 KB. `Response.Write` also calls `WriteHeader` on every call (:168, :175); only write the header once.
- [x] **Serialize socket broadcasts once.** `database` events for tenant data go to that tenant's clients and clients without a tenant. `EmitToClient` marshals the payload for every client (`yekonga/socket.go:353`). Every create/update/delete broadcasts to *all* connected clients across all tenants (`yekonga/model_query.go:361`, `:421`, `:487`, `:699`). Marshal once and scope to a per-tenant room.
- [x] **Remove per-request / per-call allocations:**
  - `ignorePaths` rebuilt on every request in `TokenMiddleware` (`yekonga/middleware.go:161`). Build once at startup.
  - `listAll` rebuilt on every `runTriggerAction` call (`yekonga/model_query.go:1287`). Make it a package-level set.
  - Token payload decoded via `json.Unmarshal([]byte(helper.ToJson(...)))` (`yekonga/middleware.go:239`).
  - `helper.Contains` over `IDKeys`, `DateFields`, `NumberFields`, `FloatFields` per field in `formatInputDataField` (`yekonga/model_query.go:1263`). Precompute sets on `DataModel`.
- [x] **Reduce synchronous logging on hot paths:** missing-trigger warning once per model/action/role; 4xx aborts logged at most 10/s (5xx always). `logger.Warn` for missing triggers on every query (`yekonga/cloud_functions.go:356`) and `console.Error` on every `Abort` (`yekonga/response.go:107`), which is costly under 404-scan traffic.

## Bugs found during the review

- [x] **`UpdateMany` returns `[]` when the update changes a filtered field.** Both backends now update and return the records matched before the update (by id). It re-reads with the original filter after updating (MongoDB and SQL backends alike), so e.g. setting `status: void → archived` where `status = void` returns nothing, and the audit trail records no changes. Fix: collect the matching ids first, update by id, re-read by id.
- [x] **The MySQL/SQL backends were incomplete.** Rewritten as one backend for both kinds (`yekonga/dbconnect_sql*.go`), tested for identical results against MongoDB (`TestSQLBackendMatchesMongo`).
- [x] **JWT signature compared with `!=`** (not constant-time) in `helper/jwt`. Now `hmac.Equal`.
- [x] **`Import` always returns an empty `data` list.** Also meant the GraphQL import mutation never attached nested children to newly created parents. Created records are now returned after the after-create triggers, and records are matched to inputs by unique keys (or id), never by default. It passes a `*[]DataMap` to `helper.ToList`, which only accepts slices (`yekonga/model_query.go`, after `createMany`).
- [x] **Building a server changed the package-level default schemas.** `NewDatabaseStructure` added the app's collections to `DefaultExtraDatabaseStructure`, so a second `ServerConfig` in the same process saw the first one's models.
- [x] **Summary fields shared relation keys across requests.** The count/sum/max/min/average/graph resolvers wrote the relation keys into variables captured by the closure, which every request shares, so concurrent requests could overwrite each other's keys.
- [x] **To-many relation with no link value returns unrelated records.** In the list resolver (`getQueryMultipleField`), when `setModelParams` returns false because the parent row has no linking value, the result is ignored and the query runs without the relation filter. Left as is to keep behaviour unchanged; it should return an empty list.
- [x] **Numeric range filters never match.** `ConvertCalculatedValue` returns `v` (the empty string from a failed type assertion) instead of `value` for non-string numbers (`helper/helpers.go`, `else { return v }`). `{"price": {"greaterThan": 5}}` becomes `{"$gt": ""}`. One-line fix, but it changes query results.
- [x] **`exists: false` filter can't match.** Now `{field: {$eq: null}}` (missing or null). `exists` also never worked on ID fields: `Where` turned the boolean into a random ObjectID; fixed. It emits the correct `$or`, but also leaves `field: {}` in the filter, which Mongo reads as "equals an empty document" (`extractWhereItem`, `yekonga/dbconnect_mongodb.go`).
- [x] **`$or` collisions.** Conditions on the same key are now combined (merged operators, or `$and`) instead of overwritten. A top-level `OR` and an `exists: false` filter both write `$or`, so one silently replaces the other. Same for a relation filter whose linking key is also filtered directly. These should be combined under `$and`.
- [x] `Authorization` header without `"Bearer"` panics on index `[1]` (`yekonga/middleware.go:217`).
- [x] `find()` defers `cursor.Close` on a nil cursor when the query errors (`yekonga/dbconnect_mongodb.go:144`); same pattern in `count`, `sum`, `max`, `min`, `average`.
- [x] Distinct `count` uses `RemainingBatchLength`, which is wrong above 101 groups (`yekonga/dbconnect_mongodb.go:253`). Use a `$count` stage.
- [x] `isStaticPath` / `handleStaticFile` write `y.staticConfig` without a lock (data race) (`yekonga/main.go:553`, `:577`). `Static()` also pre-fills the slice with `nil` entries before appending (:527).
- [x] Catch middlewares: the loop returns after the first one, so only one ever runs (`yekonga/main.go:854`). Now they run in order until one responds; if none does, the default 404 page is sent. `Middleware(fn, CatchMiddleware)` also registers now.
- [x] `mysqlConnect` and `sqlConnect` are empty (`yekonga/dbconnect.go:180`), so those backends never open a connection.
- [x] `Request.Auth/Client/TokenPayload/TenantId/Tenant` read `r.Context` without the lock that `SetContext` takes (`yekonga/request.go:331-394`).

## Measurement

- [ ] Add `Benchmark*` tests for routing, middleware chain, `where()` building, and a GraphQL list query with relations, so each change above is measured.
- [ ] Add a `net/http/pprof` endpoint behind the master key for profiling in staging.
- [x] Fix the non-constant format strings in `yekonga/initializer_other_routes.go` (:525, :610, :703) so `go test` passes `go vet` without `-vet=off`. (Also `SortMap` in `helper/helpers.go`, which printed a generic value with `%s`.)

## Suggested order

1. **Quick wins (~1–2 days):** route regex precompile, `y.mut` locking, remove the GraphQL double parse, tenant cache, gzip pooling, and the panic/nil-cursor bugs.
2. **Structural (~1 week):** request contexts and timeouts, indexes, single-pass tenant-scoped `where()`, removing JSON round-trips.
3. **GraphQL:** dataloader for relations and the parsed-document cache.
