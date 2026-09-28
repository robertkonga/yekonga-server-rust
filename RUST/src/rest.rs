//! HTTP routes for GraphQL (`graphql.apiRoute`) and the REST API
//! (`restApiEnabled`), ports of the route setup in `yekonga/initializer.go`
//! and `yekonga/rest_api_controller.go`.
//!
//! The REST routes run GraphQL operations on the model named in the URL
//! (`/api/orders` or `/api/order`):
//!
//! | Route | Operation | Response |
//! |---|---|---|
//! | `GET /api/:model` | `orders { …all fields }` | the list |
//! | `GET /api/:model/:id` | `order(where: {id: …})` | the record |
//! | `POST /api/:model/create`, `PUT /api/:model` | `createOrder(input: body)` | `data` of the result |
//! | `POST /api/:model/update/:id` | `updateOrder(where: {id: …}, input: body)` | `data` of the result |
//! | `PATCH /api/:model/:id` | `updateOrder(where: {id: …}, input: body)` | the whole result |
//! | `POST /api/:model/delete/:id`, `DELETE /api/:model/:id` | `deleteOrder(where: {id: …})` | `data` of the result |
//!
//! In the Go server the write routes send malformed operations (a query
//! named `createOrders` instead of the `createOrder` mutation, without the
//! input), so only the two GET routes work there.

use serde_json::{json, Value};

use crate::app::Yekonga;
use crate::graphql::is_introspection_query;
use crate::helper::{pluralize, singularize, to_camel_case, to_variable};
use crate::model::DataModel;
use crate::request::Request;
use crate::response::Response;

/// Serves the GraphQL API at `graphql.apiRoute`: the query comes from the
/// JSON body (`query`, `operationName`, `variables`) or the `query` URL
/// parameter.
pub(crate) fn register_graphql_route(app: &Yekonga) {
    let route = app.config().graphql.api_route.clone();
    if route.is_empty() {
        return;
    }

    app.all(&route, |req, res| async move {
        let mut query = req.query("query");
        let mut operation_name = String::new();
        let mut variables = json!({});

        if let Value::Object(body) = req.body() {
            if let Some(Value::String(q)) = body.get("query") {
                query = q.clone();
            }
            if let Some(Value::String(name)) = body.get("operationName") {
                operation_name = name.clone();
            }
            if let Some(vars @ Value::Object(_)) = body.get("variables") {
                variables = vars.clone();
            }
        }

        if !req.app().config().api_playground_enable && is_introspection_query(&query) {
            res.json(&json!({"errors": [{"message": "Introspection is disabled"}]}));
            return;
        }

        let result = req
            .app()
            .graphql(&query, variables, &operation_name, Some(&req))
            .await;
        res.json(&result);
    });
}

pub(crate) fn register_rest_routes(app: &Yekonga) {
    if !app.config().rest_api_enabled {
        return;
    }

    app.get("/api/:moduleName", |req, res| async move {
        let Some(model) = rest_model(&req, &res) else {
            return;
        };
        let name = pluralize(&to_variable(&singularize(&model.name)));
        let result = run(
            &req,
            &format!("query{{{name}{{{}}}}}", fields(&model)),
            json!({}),
        )
        .await;
        res.json(&result["data"][&name]);
    });

    app.get("/api/:moduleName/:id", |req, res| async move {
        let Some(model) = rest_model(&req, &res) else {
            return;
        };
        let name = to_variable(&singularize(&model.name));
        let query = format!(
            "query{{{name}(where:{{id:{{equalTo:\"{}\"}}}}){{{}}}}}",
            req.param("id"),
            fields(&model)
        );
        let result = run(&req, &query, json!({})).await;
        res.json(&result["data"][&name]);
    });

    for (method, path) in [
        ("POST", "/api/:moduleName/create"),
        ("PUT", "/api/:moduleName"),
    ] {
        app.route(method.parse().unwrap(), path, |req, res| async move {
            let Some(model) = rest_model(&req, &res) else {
                return;
            };
            let result = create(&req, &model).await;
            res.json(&result["data"]);
        });
    }

    app.post("/api/:moduleName/update/:id", |req, res| async move {
        let Some(model) = rest_model(&req, &res) else {
            return;
        };
        let result = update(&req, &model).await;
        res.json(&result["data"]);
    });

    app.patch("/api/:moduleName/:id", |req, res| async move {
        let Some(model) = rest_model(&req, &res) else {
            return;
        };
        let result = update(&req, &model).await;
        res.json(&result);
    });

    for (method, path) in [
        ("POST", "/api/:moduleName/delete/:id"),
        ("DELETE", "/api/:moduleName/:id"),
    ] {
        app.route(method.parse().unwrap(), path, |req, res| async move {
            let Some(model) = rest_model(&req, &res) else {
                return;
            };
            let name = to_variable(&format!("delete_{}", singularize(&model.name)));
            let query = format!(
                "mutation{{{name}(where:{{id:{{equalTo:\"{}\"}}}}){{status success message}}}}",
                req.param("id")
            );
            let result = run(&req, &query, json!({})).await;
            res.json(&result["data"]);
        });
    }
}

/// The model named in the URL (`orders`, `order`, `order-items`), or a 404.
fn rest_model(req: &Request, res: &Response) -> Option<std::sync::Arc<DataModel>> {
    let name = to_camel_case(&singularize(&pluralize(&to_variable(
        &req.param("moduleName"),
    ))));
    let model = req.app().models().get(&name).cloned();
    if model.is_none() {
        res.status(404).json(&json!({"status": 404, "error": format!("unknown model {:?}", req.param("moduleName"))}));
    }
    model
}

/// Every field of the model, as a GraphQL selection.
fn fields(model: &DataModel) -> String {
    model.valid_fields.join(",")
}

async fn run(req: &Request, query: &str, variables: Value) -> Value {
    req.app().graphql(query, variables, "", Some(req)).await
}

fn input_type(model: &DataModel) -> String {
    to_camel_case(&format!("{}_input", model.variable_single))
}

async fn create(req: &Request, model: &DataModel) -> Value {
    let name = to_variable(&format!("create_{}", singularize(&model.name)));
    let query = format!(
        "mutation($input:{}){{{name}(input:$input){{status success message data{{{}}}}}}}",
        input_type(model),
        fields(model)
    );
    run(req, &query, json!({"input": req.body()})).await
}

async fn update(req: &Request, model: &DataModel) -> Value {
    let name = to_variable(&format!("update_{}", singularize(&model.name)));
    let query = format!(
        "mutation($input:{}){{{name}(where:{{id:{{equalTo:\"{}\"}}}},input:$input){{status success message data{{{}}}}}}}",
        input_type(model),
        req.param("id"),
        fields(model)
    );
    run(req, &query, json!({"input": req.body()})).await
}
