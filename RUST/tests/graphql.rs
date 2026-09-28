//! The GraphQL API and REST routes over HTTP.

use std::sync::Arc;

use axum::body::Body;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

fn app_with(config: Value) -> Yekonga {
    let mut base = json!({
        "graphql": {"apiRoute": "/graphql"},
        "restApiEnabled": true,
        "authentication": {"secretToken": "secret"}
    });
    for (k, v) in config.as_object().unwrap() {
        base[k] = v.clone();
    }

    let config: YekongaConfig = serde_json::from_value(base).unwrap();
    let schema = DatabaseStructure::from_value(&json!({
        "Users": {"email": {"type": "String"}, "password": {"type": "String", "protected": true}, "avatar": {"type": "File"}},
        "Orders": {
            "userId": {"type": "ID", "foreignKey": "User.id"},
            "tenantId": {"type": "ID", "foreignKey": "Tenant.id"},
            "title": {"type": "String", "required": true},
            "total": {"type": "Float"}
        },
        "OrderItems": {"orderId": {"type": "ID", "foreignKey": "Order.id"}, "name": {"type": "String"}}
    }));
    Yekonga::with_backend(config, schema, Arc::new(LocalBackend::in_memory()))
}

async fn call(app: &Yekonga, request: HttpRequest<Body>) -> (u16, Value) {
    let response = app.router().oneshot(request).await.unwrap();
    let status = response.status().as_u16();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    (status, serde_json::from_slice(&body).unwrap_or(Value::Null))
}

async fn gql(app: &Yekonga, query: &str, variables: Value) -> Value {
    gql_as(app, query, variables, "localhost").await
}

async fn gql_as(app: &Yekonga, query: &str, variables: Value, host: &str) -> Value {
    let body = json!({"query": query, "variables": variables}).to_string();
    let request = HttpRequest::post("/graphql")
        .header("content-type", "application/json")
        .header("host", host)
        .body(Body::from(body))
        .unwrap();
    call(app, request).await.1
}

#[tokio::test]
async fn queries_and_mutations() {
    let app = app_with(json!({}));

    let created = gql(
        &app,
        "mutation($in: UserInput){ createUser(input: $in) { success message data { id email password } } }",
        json!({"in": {"email": "a@b.tz", "password": "hash"}}),
    )
    .await;
    let user = &created["data"]["createUser"];
    assert_eq!(user["success"], true);
    assert_eq!(user["data"]["password"], "--protected--");
    let user_id = user["data"]["id"].as_str().unwrap().to_string();

    let order = gql(
        &app,
        r#"mutation($uid: ID){ createOrder(input: {title: "A", total: 10, userId: $uid, orderItems: [{name: "pen"}, {name: "ink"}]}) {
            success data { title orderItems(orderBy: {name: ASC}) { name } user { email } } } }"#,
        json!({"uid": user_id}),
    )
    .await;
    assert_eq!(
        order["data"]["createOrder"]["data"],
        json!({"title": "A", "orderItems": [{"name": "ink"}, {"name": "pen"}], "user": {"email": "a@b.tz"}})
    );

    let summary = gql(
        &app,
        "{ users { email orderSummary { count sum(targetKey: total) } orders { orderItemSummary { count } } } orderPaginate(limit: 1) { total lastPage data { title } } }",
        json!({}),
    )
    .await;
    assert_eq!(
        summary["data"],
        json!({
            "users": [{"email": "a@b.tz", "orderSummary": {"count": 1, "sum": 10}, "orders": [{"orderItemSummary": {"count": 2}}]}],
            "orderPaginate": {"total": 1, "lastPage": 1, "data": [{"title": "A"}]}
        })
    );

    let updated = gql(&app, r#"mutation { updateOrder(where: {title: {equalTo: "A"}}, input: {title: "A", total: 12}) { success data { total } } }"#, json!({})).await;
    assert_eq!(
        updated["data"]["updateOrder"],
        json!({"success": true, "data": {"total": 12}})
    );

    let deleted = gql(
        &app,
        r#"mutation { deleteOrder(where: {title: {equalTo: "A"}}) { success message } }"#,
        json!({}),
    )
    .await;
    assert_eq!(
        deleted["data"]["deleteOrder"],
        json!({"success": true, "message": "Success"})
    );
    let missing = gql(
        &app,
        r#"mutation { deleteOrder(where: {title: {equalTo: "A"}}) { success message } }"#,
        json!({}),
    )
    .await;
    assert_eq!(
        missing["data"]["deleteOrder"],
        json!({"success": false, "message": "Fail"})
    );

    let imported = gql(
        &app,
        r#"mutation { importOrders(uniqueKeys: [title], input: [{title: "X", total: 1}, {title: "Y"}]) { status message imported updated } }"#,
        json!({}),
    )
    .await;
    assert_eq!(
        imported["data"]["importOrders"],
        json!({"status": true, "message": "SUCCESS", "imported": 2, "updated": 0})
    );
    let again = gql(&app, r#"mutation { importOrders(uniqueKeys: [title], input: [{title: "X", total: 5}]) { imported updated } }"#, json!({})).await;
    assert_eq!(
        again["data"]["importOrders"],
        json!({"imported": 0, "updated": 1})
    );
}

#[tokio::test]
async fn errors_and_unported_arguments() {
    let app = app_with(json!({}));

    let unknown = gql(&app, "{ orders { nope } }", json!({})).await;
    assert!(unknown["errors"][0]["message"]
        .as_str()
        .unwrap()
        .starts_with("Cannot query field \"nope\" on type \"Order\""));

    let bad_input = gql(
        &app,
        r#"{ orders(where: {nope: {equalTo: 1}}) { title } }"#,
        json!({}),
    )
    .await;
    assert_eq!(
        bad_input["errors"][0]["message"],
        "Invalid request format. Please check your input fields."
    );

    let group = gql(&app, "{ orders(groupBy: [title]) { title } }", json!({})).await;
    assert_eq!(
        group["errors"][0]["message"],
        "groupBy is not supported by the Rust port yet"
    );
}

#[tokio::test]
async fn distinct_dedupes_by_field() {
    let app = app_with(json!({}));
    for title in ["A", "A", "B"] {
        gql(
            &app,
            r#"mutation($t: String){ createOrder(input: {title: $t}) { success } }"#,
            json!({ "t": title }),
        )
        .await;
    }

    let all = gql(
        &app,
        "{ orders(orderBy: {title: ASC}) { title } }",
        json!({}),
    )
    .await;
    assert_eq!(all["data"]["orders"].as_array().unwrap().len(), 3);

    let distinct = gql(
        &app,
        "{ orders(distinct: [title], orderBy: {title: ASC}) { title } }",
        json!({}),
    )
    .await;
    assert_eq!(
        distinct["data"]["orders"],
        json!([{"title": "A"}, {"title": "B"}])
    );
}

#[tokio::test]
async fn model_action_runs_a_registered_handler() {
    use yekonga::cloud::ActionResult;

    let app = app_with(json!({}));
    app.set_graphql_action("Order", "archive", "", "", |ctx| {
        Box::pin(async move {
            let title = ctx.input.get("title").cloned().unwrap_or_default();
            ActionResult {
                data: serde_json::json!({"archived": title}),
                success: true,
                status: true,
                message: "Archived".into(),
            }
        })
    });

    let ok = gql(
        &app,
        r#"mutation { orderAction(action: "archive", inputData: {title: "A"}) { success message data } }"#,
        json!({}),
    )
    .await;
    assert_eq!(
        ok["data"]["orderAction"],
        json!({"success": true, "message": "Archived", "data": {"archived": "A"}})
    );

    // An unregistered action is an error naming the action and model.
    let missing = gql(
        &app,
        r#"mutation { orderAction(action: "nope") { success } }"#,
        json!({}),
    )
    .await;
    assert!(missing["errors"][0]["message"]
        .as_str()
        .unwrap()
        .contains("no action \"nope\" is registered for Order"));
}

#[tokio::test]
async fn introspection_needs_the_playground() {
    let app = app_with(json!({}));
    let blocked = gql(&app, "{ __schema { queryType { name } } }", json!({})).await;
    assert_eq!(
        blocked,
        json!({"errors": [{"message": "Introspection is disabled"}]})
    );

    let app = app_with(json!({"apiPlaygroundEnable": true}));
    let allowed = gql(&app, "{ __schema { queryType { name } } }", json!({})).await;
    assert_eq!(allowed["data"]["__schema"]["queryType"]["name"], "Query");

    // GET with the query in the URL works too.
    let request = HttpRequest::get("/graphql?query=%7Borders%7Btitle%7D%7D")
        .body(Body::empty())
        .unwrap();
    assert_eq!(call(&app, request).await.1, json!({"data": {"orders": []}}));
}

#[tokio::test]
async fn file_urls_use_the_origin() {
    let app = app_with(json!({}));
    gql(
        &app,
        r#"mutation { createUser(input: {email: "a@b.tz", avatar: "u/a.png"}) { success } }"#,
        json!({}),
    )
    .await;
    gql(
        &app,
        r#"mutation { createUser(input: {email: "c@d.tz"}) { success } }"#,
        json!({}),
    )
    .await;

    let users = gql_as(
        &app,
        "{ users(orderBy: {email: ASC}) { avatar } }",
        json!({}),
        "shop.tz",
    )
    .await;
    assert_eq!(
        users["data"]["users"],
        json!([{"avatar": "https://shop.tz/u/a.png"}, {"avatar": "https://shop.tz/placeholder.jpg"}])
    );
}

#[tokio::test]
async fn graphql_is_scoped_to_the_tenant() {
    let app = app_with(json!({"hasTenant": true}));
    let shop = app
        .query("Tenant")
        .unwrap()
        .create(json!({"name": "Shop", "domain": "shop.example.com"}))
        .await
        .unwrap();
    app.query("Tenant")
        .unwrap()
        .create(json!({"name": "Other", "domain": "other.example.com"}))
        .await
        .unwrap();

    gql_as(
        &app,
        r#"mutation { createOrder(input: {title: "mine"}) { success } }"#,
        json!({}),
        "shop.example.com",
    )
    .await;
    gql_as(
        &app,
        r#"mutation { createOrder(input: {title: "theirs"}) { success } }"#,
        json!({}),
        "other.example.com",
    )
    .await;

    let mine = gql_as(
        &app,
        "{ orders { title tenantId } }",
        json!({}),
        "shop.example.com",
    )
    .await;
    assert_eq!(
        mine["data"]["orders"],
        json!([{"title": "mine", "tenantId": shop["id"]}])
    );
}

#[tokio::test]
async fn rest_routes() {
    let app = app_with(json!({}));
    let json_request = |method: &str, uri: &str, body: Value| {
        HttpRequest::builder()
            .method(method)
            .uri(uri)
            .header("content-type", "application/json")
            .body(Body::from(body.to_string()))
            .unwrap()
    };

    let (_, created) = call(
        &app,
        json_request(
            "POST",
            "/api/order/create",
            json!({"title": "A", "total": 3}),
        ),
    )
    .await;
    assert_eq!(created["createOrder"]["success"], true);
    let id = created["createOrder"]["data"]["id"]
        .as_str()
        .unwrap()
        .to_string();

    let (_, put) = call(
        &app,
        json_request("PUT", "/api/orders", json!({"title": "B"})),
    )
    .await;
    assert_eq!(put["createOrder"]["data"]["title"], "B");

    let (_, list) = call(&app, json_request("GET", "/api/orders", json!(null))).await;
    assert_eq!(list.as_array().unwrap().len(), 2);
    assert!(list[0].get("tenantId").is_some(), "every field is selected");

    let (_, one) = call(
        &app,
        json_request("GET", &format!("/api/order/{id}"), json!(null)),
    )
    .await;
    assert_eq!(one["title"], "A");

    let (_, updated) = call(
        &app,
        json_request(
            "POST",
            &format!("/api/order/update/{id}"),
            json!({"title": "A2"}),
        ),
    )
    .await;
    assert_eq!(updated["updateOrder"]["data"]["title"], "A2");

    let (_, patched) = call(
        &app,
        json_request("PATCH", &format!("/api/order/{id}"), json!({"title": "A3"})),
    )
    .await;
    assert_eq!(
        patched["data"]["updateOrder"]["data"]["title"], "A3",
        "PATCH returns the whole result"
    );

    let (_, deleted) = call(
        &app,
        json_request("DELETE", &format!("/api/order/{id}"), json!(null)),
    )
    .await;
    assert_eq!(deleted["deleteOrder"]["success"], true);

    let (status, unknown) = call(&app, json_request("GET", "/api/nothings", json!(null))).await;
    assert_eq!(status, 404);
    assert!(unknown["error"].as_str().unwrap().contains("unknown model"));
}
