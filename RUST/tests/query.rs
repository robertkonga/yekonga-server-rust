//! The query builder against the local backend.

use std::sync::Arc;

use axum::body::Body;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::db::DbError;
use yekonga::helper::jwt;
use yekonga::{DataMap, DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

fn schema() -> DatabaseStructure {
    DatabaseStructure::from_value(&json!({
        "Users": {
            "email": {"type": "String"},
            "name": {"type": "String"}
        },
        "Orders": {
            "userId": {"type": "ID", "foreignKey": "User.id"},
            "tenantId": {"type": "ID", "foreignKey": "Tenant.id"},
            "title": {"type": "String"},
            "status": {"type": "String", "default": "pending"},
            "quantity": {"type": "Integer", "default": 1},
            "total": {"type": "Float", "default": 0},
            "tags": {"type": "[String]"},
            "placedAt": {"type": "Date", "default": "now"},
            "note": {"type": "String"}
        }
    }))
}

/// A fresh app per backend: the in-memory local backend, plus MongoDB and
/// MySQL when `YEKONGA_TEST_MONGO_PORT` / `YEKONGA_TEST_MYSQL_PORT` point at
/// throwaway servers (each test uses, and first drops, its own database;
/// MySQL connects as `YEKONGA_TEST_MYSQL_USER`, default root, with
/// `YEKONGA_TEST_MYSQL_PASSWORD`, default empty).
#[cfg_attr(
    not(all(feature = "mongodb", feature = "mysql")),
    allow(unused_mut, unused_variables)
)]
async fn apps(test: &str, config: Value) -> Vec<Yekonga> {
    let config: YekongaConfig = serde_json::from_value(config).unwrap();
    let mut apps = vec![Yekonga::with_backend(
        config.clone(),
        schema(),
        Arc::new(LocalBackend::in_memory()),
    )];

    #[cfg(feature = "mongodb")]
    if let Ok(port) = std::env::var("YEKONGA_TEST_MONGO_PORT") {
        use yekonga::db::mongo::{mongodb, MongoBackend};

        let mut database = config.database.clone();
        database.host = "127.0.0.1".into();
        database.port = port.clone();
        database.database_name = format!("yekonga_rust_test_{test}");

        let client = mongodb::Client::with_uri_str(format!("mongodb://127.0.0.1:{port}"))
            .await
            .unwrap();
        client
            .database(&database.database_name)
            .drop()
            .await
            .unwrap();

        apps.push(Yekonga::with_backend(
            config.clone(),
            schema(),
            Arc::new(MongoBackend::new(database)),
        ));
    }

    #[cfg(feature = "mysql")]
    if let Ok(port) = std::env::var("YEKONGA_TEST_MYSQL_PORT") {
        use yekonga::db::sql::mysql_async::{self, prelude::Queryable};
        use yekonga::db::sql::SqlBackend;

        let mut database = config.database.clone();
        database.host = "127.0.0.1".into();
        database.port = port.clone();
        database.username = std::env::var("YEKONGA_TEST_MYSQL_USER")
            .unwrap_or_else(|_| "root".into())
            .into();
        database.password = std::env::var("YEKONGA_TEST_MYSQL_PASSWORD")
            .unwrap_or_default()
            .into();
        database.database_name = format!("yekonga_rust_test_{test}");

        let admin = mysql_async::OptsBuilder::default()
            .ip_or_hostname("127.0.0.1")
            .tcp_port(port.parse().unwrap())
            .user(database.username.as_str())
            .pass(database.password.as_str());
        let mut conn = mysql_async::Conn::new(admin).await.unwrap();
        let name = &database.database_name;
        conn.query_drop(format!("DROP DATABASE IF EXISTS `{name}`"))
            .await
            .unwrap();
        conn.query_drop(format!("CREATE DATABASE `{name}`"))
            .await
            .unwrap();
        conn.disconnect().await.unwrap();

        apps.push(Yekonga::with_backend(
            config,
            schema(),
            Arc::new(SqlBackend::new(database)),
        ));
    }

    apps
}

fn ids(records: &[DataMap], field: &str) -> Vec<Value> {
    records.iter().map(|r| r[field].clone()).collect()
}

async fn seed(app: &Yekonga) -> (String, String) {
    let users = app.query("User").unwrap();
    let asha = users
        .create(json!({"email": "asha@shop.tz", "name": "Asha Konga"}))
        .await
        .unwrap();
    let juma = users
        .create(json!({"email": "juma@shop.tz", "name": "Juma"}))
        .await
        .unwrap();
    let (asha, juma) = (
        asha["id"].as_str().unwrap().to_string(),
        juma["id"].as_str().unwrap().to_string(),
    );

    let orders = app.query("Order").unwrap();
    for (title, user, total, status, tags) in [
        ("A", &asha, 50.0, "paid", json!(["x"])),
        ("B", &asha, 150.0, "paid", json!(["x", "y"])),
        ("C", &juma, 300.0, "void", json!([])),
        ("D", &juma, 20.0, "pending", json!(["y"])),
    ] {
        orders
            .create(json!({"title": title, "userId": user, "total": total, "status": status, "tags": tags}))
            .await
            .unwrap();
    }

    (asha, juma)
}

async fn create_formats_input_body(app: Yekonga) {
    let order = app
        .query("Order")
        .unwrap()
        .create(json!({"title": "A", "quantity": 12, "total": "9.5", "placedAt": "2026-09-28", "unknown": 1}))
        .await
        .unwrap();

    assert_eq!(order["quantity"], 12, "no base-32 surprise");
    assert_eq!(order["total"], 9.5);
    assert_eq!(order["placedAt"], "2026-09-28T00:00:00.000Z");
    assert_eq!(order["status"], "pending", "defaults fill missing fields");
    assert!(!order.contains_key("unknown"), "unknown fields are dropped");
    assert_eq!(order["id"], order["_id"]);
    assert_eq!(order["_model"], "Order");
    assert_eq!(order["_collection"], "orders");
    assert_eq!(order["id"].as_str().unwrap().len(), 24);

    let defaulted = app
        .query("Order")
        .unwrap()
        .create(json!({"title": "B"}))
        .await
        .unwrap();
    assert_eq!(defaulted["quantity"], 1);
    assert!(
        defaulted["placedAt"].as_str().unwrap().starts_with("20"),
        "\"now\" default is a timestamp"
    );
    assert_eq!(defaulted["tags"], json!([]), "array fields default to []");
}

async fn filters_body(app: Yekonga) {
    let (asha, _) = seed(&app).await;
    let orders = || app.query("Order").unwrap();
    let titles = |r: Vec<DataMap>| {
        let mut t: Vec<String> = r
            .iter()
            .map(|o| o["title"].as_str().unwrap().to_string())
            .collect();
        t.sort();
        t
    };

    assert_eq!(
        titles(orders().where_("status", "paid").find().await.unwrap()),
        ["A", "B"]
    );
    assert_eq!(
        titles(
            orders()
                .where_("total", json!({"greaterThan": 100}))
                .find()
                .await
                .unwrap()
        ),
        ["B", "C"]
    );
    assert_eq!(
        titles(
            orders()
                .where_("total", json!({"greaterThan": "40"}))
                .where_("total", json!({"lessThan": 200}))
                .find()
                .await
                .unwrap()
        ),
        ["A", "B"],
        "operators on one field merge; numeric strings compare as numbers"
    );
    assert_eq!(
        titles(
            orders()
                .where_("status", json!({"in": ["void", "pending"]}))
                .find()
                .await
                .unwrap()
        ),
        ["C", "D"]
    );
    assert_eq!(
        titles(
            orders()
                .where_("status", json!({"notIn": ["paid"]}))
                .find()
                .await
                .unwrap()
        ),
        ["C", "D"]
    );
    assert_eq!(
        titles(orders().where_("tags", "y").find().await.unwrap()),
        ["B", "D"],
        "array contains"
    );
    assert_eq!(
        titles(
            orders()
                .where_("tags", json!({"all": ["x", "y"]}))
                .find()
                .await
                .unwrap()
        ),
        ["B"]
    );
    assert_eq!(
        titles(
            orders()
                .where_("note", json!({"exists": false}))
                .find()
                .await
                .unwrap()
        ),
        ["A", "B", "C", "D"]
    );
    assert_eq!(
        titles(
            orders()
                .where_many(json!({"OR": [{"status": "void"}, {"total": {"lessThan": 30}}]}))
                .find()
                .await
                .unwrap()
        ),
        ["C", "D"]
    );
    assert_eq!(
        titles(
            orders()
                .where_many(json!({"NOR": [{"status": "paid"}]}))
                .find()
                .await
                .unwrap()
        ),
        ["C", "D"]
    );

    // Id fields match regardless of hex case.
    let upper = asha.to_uppercase();
    assert_eq!(
        titles(
            orders()
                .where_("userId", upper.as_str())
                .find()
                .await
                .unwrap()
        ),
        ["A", "B"]
    );
    assert_eq!(
        orders()
            .where_("userId", "not-an-id")
            .count()
            .await
            .unwrap(),
        0
    );

    // Dates.
    assert_eq!(
        orders()
            .where_("placedAt", json!({"greaterThan": "2000-01-01"}))
            .count()
            .await
            .unwrap(),
        4
    );
    assert_eq!(
        orders()
            .where_("placedAt", json!({"lessThan": "2000-01-01"}))
            .count()
            .await
            .unwrap(),
        0
    );

    let users = app.query("User").unwrap();
    let found = users
        .where_("name", json!({"matchesRegex": "a konga"}))
        .find()
        .await
        .unwrap();
    assert_eq!(ids(&found, "email"), [json!("asha@shop.tz")]);
}

async fn relation_filters_body(app: Yekonga) {
    seed(&app).await;

    // Orders of users matching a condition (parent relation).
    let orders = app
        .query("Order")
        .unwrap()
        .where_("user", json!({"email": "juma@shop.tz"}))
        .order_by("title", "asc")
        .find()
        .await
        .unwrap();
    assert_eq!(ids(&orders, "title"), [json!("C"), json!("D")]);

    // Users with a matching order (child relation).
    let users = app
        .query("User")
        .unwrap()
        .where_("orders", json!({"total": {"greaterThan": 200}}))
        .find()
        .await
        .unwrap();
    assert_eq!(ids(&users, "email"), [json!("juma@shop.tz")]);
}

async fn sorting_paging_and_aggregates_body(app: Yekonga) {
    seed(&app).await;
    let orders = || app.query("Order").unwrap();

    let by_total = orders().order_by("total", "desc").find().await.unwrap();
    assert_eq!(
        ids(&by_total, "title"),
        [json!("C"), json!("B"), json!("A"), json!("D")]
    );

    let multi = orders()
        .order_by("status", "asc")
        .order_by("total", "desc")
        .find()
        .await
        .unwrap();
    assert_eq!(
        ids(&multi, "title"),
        [json!("B"), json!("A"), json!("D"), json!("C")]
    );

    let page2 = orders()
        .order_by("title", "asc")
        .take(3)
        .page(2)
        .find()
        .await
        .unwrap();
    assert_eq!(ids(&page2, "title"), [json!("D")]);
    let skipped = orders()
        .order_by("title", "asc")
        .skip(1)
        .take(2)
        .find()
        .await
        .unwrap();
    assert_eq!(ids(&skipped, "title"), [json!("B"), json!("C")]);

    let page = orders()
        .order_by("title", "asc")
        .take(3)
        .page(2)
        .paginate()
        .await
        .unwrap();
    assert_eq!(page["total"], 4);
    assert_eq!(page["perPage"], 3);
    assert_eq!(page["currentPage"], 2);
    assert_eq!(page["lastPage"], 2);
    assert_eq!(page["from"], 4);
    assert_eq!(page["to"], 6);
    assert_eq!(page["data"].as_array().unwrap().len(), 1);

    let paid = || orders().where_("status", "paid");
    assert_eq!(paid().count().await.unwrap(), 2);
    assert_eq!(paid().sum("total").await.unwrap(), 200.0);
    assert_eq!(paid().average("total").await.unwrap(), 100.0);
    assert_eq!(paid().max("total").await.unwrap(), 150.0);
    assert_eq!(paid().min("total").await.unwrap(), 50.0);
    assert_eq!(
        orders()
            .where_("status", "none")
            .max("total")
            .await
            .unwrap(),
        Value::Null
    );
    assert_eq!(
        orders()
            .where_("status", "none")
            .average("total")
            .await
            .unwrap(),
        0.0
    );

    assert!(orders().where_("title", "A").exists().await.unwrap());
    assert_eq!(
        orders().where_("title", "A").value("total").await.unwrap(),
        50.0
    );
    assert_eq!(
        orders()
            .order_by("title", "asc")
            .values("title")
            .await
            .unwrap()
            .len(),
        4
    );
}

async fn updates_and_deletes_body(app: Yekonga) {
    seed(&app).await;
    let orders = || app.query("Order").unwrap();

    // update changes the first match in sort order only.
    let updated = orders()
        .where_("status", "paid")
        .order_by("total", "desc")
        .update(json!({"note": "big", "quantity": "7", "_id": "ignored", "unknown": 1}))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(updated["title"], "B");
    assert_eq!(updated["note"], "big");
    assert_eq!(updated["quantity"], 7);
    assert!(!updated.contains_key("unknown"));
    assert_eq!(orders().where_("note", "big").count().await.unwrap(), 1);

    assert!(orders()
        .where_("title", "nope")
        .update(json!({"note": "x"}))
        .await
        .unwrap()
        .is_none());

    // update_many returns the records even when the change moves them out of the filter.
    let archived = orders()
        .where_("status", "paid")
        .update_many(json!({"status": "archived"}))
        .await
        .unwrap();
    assert_eq!(archived.len(), 2);
    assert!(archived.iter().all(|o| o["status"] == "archived"));

    assert!(
        matches!(orders().delete().await, Err(DbError::EmptyDeleteFilter)),
        "deleting everything is refused"
    );
    assert_eq!(
        orders()
            .where_("status", "archived")
            .delete()
            .await
            .unwrap(),
        2
    );
    assert_eq!(orders().count().await.unwrap(), 2);

    assert!(matches!(app.query("Nope"), Err(DbError::UnknownModel(_))));
}

#[tokio::test]
async fn local_backend_persists() {
    let dir = tempfile::tempdir().unwrap();
    let config = YekongaConfig::default();

    let first = Yekonga::with_backend(
        config.clone(),
        schema(),
        Arc::new(LocalBackend::open(dir.path())),
    );
    let created = first
        .query("User")
        .unwrap()
        .create(json!({"email": "a@b.tz"}))
        .await
        .unwrap();
    assert!(dir.path().join("users.json").is_file());

    let second = Yekonga::with_backend(config, schema(), Arc::new(LocalBackend::open(dir.path())));
    let found = second
        .query("User")
        .unwrap()
        .where_("id", created["id"].clone())
        .find_one()
        .await
        .unwrap();
    assert_eq!(found.unwrap()["email"], "a@b.tz");
}

async fn get(app: &Yekonga, request: HttpRequest<Body>) -> Value {
    let response = app.router().oneshot(request).await.unwrap();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    serde_json::from_slice(&body)
        .unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&body).into()))
}

async fn tenant_resolution_and_scoping_body(app: Yekonga) {
    let tenant = app
        .query("Tenant")
        .unwrap()
        .create(json!({"name": "Shop", "subdomain": "shop.example.com"}))
        .await
        .unwrap();
    let tenant_id = tenant["id"].as_str().unwrap().to_string();
    app.query("TenantConfig")
        .unwrap()
        .create(json!({"tenantId": tenant_id, "language": "sw"}))
        .await
        .unwrap();

    // Records of another tenant must stay invisible.
    app.query("Order")
        .unwrap()
        .create(json!({"title": "other", "tenantId": "aaaaaaaaaaaaaaaaaaaaaaaa"}))
        .await
        .unwrap();

    app.post("/orders", |req, res| async move {
        let query = req.app().query("Order").unwrap().set_request(&req);
        query.create(json!({"title": "mine"})).await.unwrap();
        let titles = query.values("title").await.unwrap();
        res.json(&json!({
            "tenant": req.tenant_id(),
            "language": req.tenant_config().and_then(|c| c.get("language").cloned()),
            "titles": titles,
        }));
    });

    let reply = get(
        &app,
        HttpRequest::post("/orders")
            .header("host", "shop.example.com")
            .header("accept", "application/json")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply["tenant"], tenant_id.as_str());
    assert_eq!(reply["language"], "sw");
    assert_eq!(
        reply["titles"],
        json!(["mine"]),
        "queries are scoped to the tenant"
    );

    let stored = app
        .query("Order")
        .unwrap()
        .where_("title", "mine")
        .find_one()
        .await
        .unwrap()
        .unwrap();
    assert_eq!(
        stored["tenantId"],
        tenant_id.as_str(),
        "creates get the tenant id"
    );

    // Unknown subdomains are still rejected.
    let reply = get(
        &app,
        HttpRequest::get("/health")
            .header("host", "nope.example.com")
            .header("accept", "application/json")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply["error"], "Tenant not found");

    // A write to Tenant clears the host cache.
    app.query("Tenant")
        .unwrap()
        .create(json!({"name": "New", "domain": "new.example.com"}))
        .await
        .unwrap();
    let reply = get(
        &app,
        HttpRequest::get("/health")
            .header("host", "new.example.com")
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply, "Ok!");
}

async fn authorization_server_loads_the_user_body(app: Yekonga) {
    let user = app
        .query("User")
        .unwrap()
        .create(json!({"email": "a@b.tz", "firstName": "Asha"}))
        .await
        .unwrap();

    app.get("/whoami", |req, res| async move {
        res.json(&json!({"email": req.auth().map(|a| a.email), "first": req.user_info().and_then(|u| u.get("firstName").cloned())}));
    });

    let token = jwt::encode(
        json!({"userId": user["id"], "expiresAt": "2999-01-01T00:00:00Z"})
            .as_object()
            .unwrap()
            .clone(),
        "secret",
    );
    let reply = get(
        &app,
        HttpRequest::get("/whoami")
            .header("authorization", format!("Bearer {token}"))
            .body(Body::empty())
            .unwrap(),
    )
    .await;
    assert_eq!(reply, json!({"email": "a@b.tz", "first": "Asha"}));
}

// One test per scenario, run against every available backend.

#[tokio::test]
async fn create_formats_input() {
    for app in apps(
        "create_formats_input",
        json!({"authentication": {"secretToken": "secret"}}),
    )
    .await
    {
        create_formats_input_body(app).await;
    }
}

#[tokio::test]
async fn filters() {
    for app in apps(
        "filters",
        json!({"authentication": {"secretToken": "secret"}}),
    )
    .await
    {
        filters_body(app).await;
    }
}

#[tokio::test]
async fn relation_filters() {
    for app in apps(
        "relation_filters",
        json!({"authentication": {"secretToken": "secret"}}),
    )
    .await
    {
        relation_filters_body(app).await;
    }
}

#[tokio::test]
async fn sorting_paging_and_aggregates() {
    for app in apps(
        "sorting_paging_and_aggregates",
        json!({"authentication": {"secretToken": "secret"}}),
    )
    .await
    {
        sorting_paging_and_aggregates_body(app).await;
    }
}

#[tokio::test]
async fn updates_and_deletes() {
    for app in apps(
        "updates_and_deletes",
        json!({"authentication": {"secretToken": "secret"}}),
    )
    .await
    {
        updates_and_deletes_body(app).await;
    }
}

#[tokio::test]
async fn tenant_resolution_and_scoping() {
    for app in apps(
        "tenant_resolution_and_scoping",
        json!({"hasTenant": true, "authentication": {"secretToken": "secret"}}),
    )
    .await
    {
        tenant_resolution_and_scoping_body(app).await;
    }
}

#[tokio::test]
async fn authorization_server_loads_the_user() {
    for app in apps(
        "authorization_server_loads_the_user",
        json!({"isAuthorizationServer": true, "authentication": {"secretToken": "secret"}}),
    )
    .await
    {
        authorization_server_loads_the_user_body(app).await;
    }
}
