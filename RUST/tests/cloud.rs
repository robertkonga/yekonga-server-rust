//! Cloud functions, database triggers, the audit trail and the
//! FetchTenantByDomain fallback (step 4).

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use axum::body::Body;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::cloud::{CloudContext, TriggerContext, TriggerReturn};
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

fn app_with(config: Value, schema: Value) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(config).unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&schema),
        Arc::new(LocalBackend::in_memory()),
    )
}

fn orders() -> Yekonga {
    app_with(
        json!({"authentication": {"secretToken": "s"}}),
        json!({"Orders": {"title": {"type": "String"}, "total": {"type": "Float"}}}),
    )
}

#[tokio::test]
async fn cloud_functions() {
    let app = orders();
    app.define("sum", |data, _ctx: CloudContext| {
        Box::pin(async move {
            let a = data["a"].as_i64().unwrap_or(0);
            let b = data["b"].as_i64().unwrap_or(0);
            Ok(json!(a + b))
        })
    })
    .unwrap();

    assert_eq!(
        app.run("sum", json!({"a": 2, "b": 3}), None).await,
        Ok(json!(5))
    );
    // An unregistered function is a no-op returning null.
    assert_eq!(app.run("missing", json!({}), None).await, Ok(Value::Null));
    // A name can't be registered twice.
    assert!(app
        .define("sum", |_d, _c| Box::pin(async { Ok(Value::Null) }))
        .is_err());
}

#[tokio::test]
async fn before_and_after_triggers_change_data() {
    let app = orders();

    // A before-create trigger fills in a default and can be seen to run.
    app.before_create("Order", |ctx: TriggerContext| {
        Box::pin(async move {
            let mut input = ctx.data.as_object().cloned().unwrap_or_default();
            if input.get("total").is_none() {
                input.insert("total".into(), json!(100.0));
            }
            TriggerReturn::Replace(Value::Object(input))
        })
    });

    // An after-find trigger tags every record.
    app.after_find("Order", |ctx: TriggerContext| {
        Box::pin(async move {
            let items = ctx
                .data
                .as_array()
                .unwrap_or(&Vec::new())
                .iter()
                .map(|record| {
                    let mut record = record.as_object().cloned().unwrap_or_default();
                    record.insert("seen".into(), json!(true));
                    Value::Object(record)
                })
                .collect();
            TriggerReturn::Replace(Value::Array(items))
        })
    });

    let created = app
        .query("Order")
        .unwrap()
        .create(json!({"title": "A"}))
        .await
        .unwrap();
    assert_eq!(
        created["total"], 100.0,
        "before-create trigger set the default"
    );

    let found = app.query("Order").unwrap().find().await.unwrap();
    assert_eq!(
        found[0]["seen"], true,
        "after-find trigger tagged the record"
    );

    // A skip_before_commit query runs no triggers.
    let raw = app
        .query("Order")
        .unwrap()
        .skip_before_commit()
        .find()
        .await
        .unwrap();
    assert!(raw[0].get("seen").is_none());
}

#[tokio::test]
async fn before_trigger_can_reject() {
    let app = orders();
    app.before_create("Order", |_ctx| Box::pin(async { TriggerReturn::Reject }));
    app.before_delete("Order", |_ctx| Box::pin(async { TriggerReturn::Reject }));

    // The pre-seeded record is written with triggers skipped.
    app.query("Order")
        .unwrap()
        .skip_before_commit()
        .create(json!({"title": "kept"}))
        .await
        .unwrap();

    let created = app
        .query("Order")
        .unwrap()
        .create_many(vec![json!({"title": "X"})])
        .await
        .unwrap();
    assert!(created.is_empty(), "rejected create stored nothing");

    let deleted = app
        .query("Order")
        .unwrap()
        .where_("title", "kept")
        .delete()
        .await
        .unwrap();
    assert_eq!(deleted, 0, "rejected delete removed nothing");
    assert_eq!(
        app.query("Order")
            .unwrap()
            .skip_before_commit()
            .count()
            .await
            .unwrap(),
        1
    );
}

#[tokio::test]
async fn trigger_all_runs_for_every_model() {
    let app = app_with(
        json!({"authentication": {"secretToken": "s"}}),
        json!({"Orders": {"title": {"type": "String"}}, "Notes": {"body": {"type": "String"}}}),
    );
    let count = Arc::new(AtomicUsize::new(0));
    let seen = count.clone();
    app.set_trigger_all(yekonga::cloud::TriggerAction::BeforeCreate, move |_ctx| {
        let seen = seen.clone();
        Box::pin(async move {
            seen.fetch_add(1, Ordering::SeqCst);
            TriggerReturn::Continue
        })
    });

    app.query("Order")
        .unwrap()
        .create(json!({"title": "A"}))
        .await
        .unwrap();
    app.query("Note")
        .unwrap()
        .create(json!({"body": "B"}))
        .await
        .unwrap();
    assert_eq!(count.load(Ordering::SeqCst), 2);
}

// ----- audit trail and FetchTenantByDomain go through HTTP (they need a request) -----

async fn call(app: &Yekonga, request: HttpRequest<Body>) -> Value {
    let response = app.router().oneshot(request).await.unwrap();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    serde_json::from_slice(&body).unwrap_or(Value::Null)
}

async fn gql(app: &Yekonga, query: &str) -> Value {
    let body = json!({"query": query}).to_string();
    let request = HttpRequest::post("/graphql")
        .header("content-type", "application/json")
        .header("host", "shop.tz")
        .header("x-forwarded-for", "10.1.2.3")
        .header("user-agent", "test-agent")
        .body(Body::from(body))
        .unwrap();
    call(app, request).await
}

#[tokio::test]
async fn audit_trail_records_writes() {
    let app = app_with(
        json!({
            "graphql": {"apiRoute": "/graphql"},
            "auditTrail": {"enabled": true},
            "authentication": {"secretToken": "s"}
        }),
        json!({"Orders": {"title": {"type": "String"}, "total": {"type": "Float"}}}),
    );

    let created = gql(
        &app,
        r#"mutation { createOrder(input: {title: "A", total: 5}) { data { id } } }"#,
    )
    .await;
    let id = created["data"]["createOrder"]["data"]["id"]
        .as_str()
        .unwrap()
        .to_string();
    gql(&app, &format!(r#"mutation {{ updateOrder(where: {{id: {{equalTo: "{id}"}}}}, input: {{total: 9}}) {{ success }} }}"#)).await;
    gql(
        &app,
        &format!(r#"mutation {{ deleteOrder(where: {{id: {{equalTo: "{id}"}}}}) {{ success }} }}"#),
    )
    .await;

    // The flush runs off the request; give it a moment.
    let entries = loop {
        let entries = app
            .query("AuditTrail")
            .unwrap()
            .order_by("createdAt", "asc")
            .find()
            .await
            .unwrap();
        if entries.len() >= 3 {
            break entries;
        }
        tokio::time::sleep(std::time::Duration::from_millis(20)).await;
    };

    let actions: Vec<&str> = entries
        .iter()
        .map(|e| e["action"].as_str().unwrap())
        .collect();
    assert_eq!(actions, ["create", "update", "delete"]);
    assert!(entries
        .iter()
        .all(|e| e["model"] == "Order" && e["documentId"] == id));
    assert_eq!(entries[0]["ipAddress"], "10.1.2.3");
    assert_eq!(entries[0]["userAgent"], "test-agent");
    // The update entry keeps the record as it was and as it became.
    assert_eq!(entries[1]["oldValues"]["total"], 5.0);
    assert_eq!(entries[1]["newValues"]["total"], 9.0);
    // The delete entry keeps the deleted record.
    assert_eq!(entries[2]["oldValues"]["title"], "A");
}

#[tokio::test]
async fn audit_trail_off_by_default() {
    let app = app_with(
        json!({"graphql": {"apiRoute": "/graphql"}, "authentication": {"secretToken": "s"}}),
        json!({"Orders": {"title": {"type": "String"}}}),
    );
    gql(
        &app,
        r#"mutation { createOrder(input: {title: "A"}) { success } }"#,
    )
    .await;
    tokio::time::sleep(std::time::Duration::from_millis(30)).await;
    assert_eq!(app.query("AuditTrail").unwrap().count().await.unwrap(), 0);
}

#[tokio::test]
async fn fetch_tenant_by_domain_fallback() {
    let app = app_with(
        json!({"hasTenantCatch": true, "authentication": {"secretToken": "s"}}),
        json!({}),
    );
    let calls = Arc::new(Mutex::new(Vec::<String>::new()));
    let seen = calls.clone();
    app.set_fetch_tenant_by_domain(move |data, _ctx: CloudContext| {
        let seen = seen.clone();
        Box::pin(async move {
            let host = data.as_str().unwrap_or_default().to_string();
            seen.lock().unwrap().push(host);
            Ok(json!({"domain": "shop.tz", "tenantId": "5f00000000000000000000aa"}))
        })
    })
    .unwrap();

    // Any request runs the tenant middleware, which calls the function.
    let request = HttpRequest::get("/health")
        .header("host", "shop.tz")
        .body(Body::empty())
        .unwrap();
    app.router().oneshot(request).await.unwrap();

    assert_eq!(calls.lock().unwrap().as_slice(), ["shop.tz"]);

    // Its result is cached as a TenantCatch record, so a second request is
    // served from the cache without calling the function again.
    let cached = app
        .query("TenantCatch")
        .unwrap()
        .where_("domain", "shop.tz")
        .find_one()
        .await
        .unwrap();
    assert_eq!(cached.unwrap()["tenantId"], "5f00000000000000000000aa");
}
