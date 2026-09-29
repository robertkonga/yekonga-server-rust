//! The auto-generated GraphQL API (port of `yekonga/graphql.go` and
//! `graphql_types.go`).
//!
//! The schema is built from the models, with the same type, field and
//! argument names as the Go server, so existing clients keep working. For
//! each model (e.g. `Order`) there are:
//!
//! - queries `order`, `orders`, `orderPaginate`, `orderSummary`,
//!   `downloadOrders`;
//! - mutations `createOrder`, `updateOrder`, `deleteOrder`, `importOrders`,
//!   `orderAction`;
//! - relation fields on the object types (`order.user`, `user.orders`,
//!   `user.orderPaginate`, `user.orderSummary`).
//!
//! The `groupBy` and `distinct` arguments are applied to the plural list.
//!
//! Not ported yet: `groupBy` on paginated and summary queries, summary `graph`,
//! `download…` queries and custom GraphQL fields, which return an error saying
//! so.

mod auth;
mod resolve;
mod schema;

use async_graphql::dynamic::Schema;
use async_graphql::{Request as GqlRequest, Variables};
use serde_json::{json, Map, Value};

use crate::app::Yekonga;
use crate::db::DataMap;
use crate::request::Request;
use crate::response::Response;

pub use auth::build_auth_schema;
pub use schema::build_schema;

/// Per-execution data: the HTTP exchange the query runs for, if any.
#[derive(Clone, Default)]
pub(crate) struct ExecutionData {
    pub request: Option<Request>,
    /// For resolvers that set cookies (the auth API).
    pub response: Option<Response>,
}

/// A value passed from a resolver to the fields below it.
pub(crate) enum Node {
    /// A record of a model (the field resolvers know which).
    Record(DataMap),
    /// A summary field's context: which model, its arguments, and the parent
    /// record when it's a relation.
    Summary(Summary),
    /// Plain data (results, pages, downloads).
    Json(Value),
}

pub(crate) struct Summary {
    pub args: Map<String, Value>,
    pub relation: Option<Relation>,
}

/// How a relation field links to its parent record.
#[derive(Clone)]
pub(crate) struct Relation {
    pub foreign_key: String,
    pub target_key: String,
    /// The field returns the parent side (a single record).
    pub is_parent: bool,
    pub parent: DataMap,
}

/// Strings in a query that mean it asks for the schema (Go's
/// `isIntrospectionQuery`).
pub fn is_introspection_query(query: &str) -> bool {
    ["__schema", "__type", "__typename", "IntrospectionQuery"]
        .iter()
        .any(|p| query.contains(p))
}

impl Yekonga {
    /// Runs a GraphQL query against the auto-generated schema, as the request
    /// (tenant, user) if one is given. Returns `{data, errors}` as the Go
    /// server does.
    pub async fn graphql(
        &self,
        query: &str,
        variables: Value,
        operation_name: &str,
        request: Option<&Request>,
    ) -> Value {
        let data = ExecutionData {
            request: request.cloned(),
            response: None,
        };
        execute(
            self.graphql_schema(),
            query,
            variables,
            operation_name,
            data,
        )
        .await
    }

    /// Runs a query against the auth schema (`graphql.apiAuthRoute`: login,
    /// OTP, tokens). Login and token refreshes set cookies on `response`.
    pub async fn auth_graphql(
        &self,
        query: &str,
        variables: Value,
        operation_name: &str,
        request: Option<&Request>,
        response: Option<&Response>,
    ) -> Value {
        let data = ExecutionData {
            request: request.cloned(),
            response: response.cloned(),
        };
        execute(
            self.auth_graphql_schema(),
            query,
            variables,
            operation_name,
            data,
        )
        .await
    }

    /// The auth schema, built on first use.
    pub fn auth_graphql_schema(&self) -> Result<&Schema, String> {
        self.auth_schema_cell()
            .get_or_init(|| build_auth_schema(self).map_err(|e| e.to_string()))
            .as_ref()
            .map_err(Clone::clone)
    }

    /// The schema, built on first use (after the app has registered its
    /// models and routes).
    pub fn graphql_schema(&self) -> Result<&Schema, String> {
        self.schema_cell()
            .get_or_init(|| build_schema(self).map_err(|e| e.to_string()))
            .as_ref()
            .map_err(Clone::clone)
    }
}

async fn execute(
    schema: Result<&Schema, String>,
    query: &str,
    variables: Value,
    operation_name: &str,
    data: ExecutionData,
) -> Value {
    let schema = match schema {
        Ok(schema) => schema,
        Err(err) => return json!({"data": null, "errors": [{"message": err}]}),
    };

    let mut gql = GqlRequest::new(query)
        .variables(Variables::from_json(variables))
        .data(data);
    if !operation_name.is_empty() {
        gql = gql.operation_name(operation_name);
    }

    let response = schema.execute(gql).await;
    let data = response.data.into_json().unwrap_or(Value::Null);
    let mut result = Map::new();
    result.insert("data".into(), data);

    if !response.errors.is_empty() {
        let errors: Vec<Value> = response
            .errors
            .iter()
            .map(|e| json!({"message": format_error(&e.message), "locations": null}))
            .collect();
        result.insert("errors".into(), Value::Array(errors));
    }

    Value::Object(result)
}

/// Go's `formatErrors`: input validation errors get a generic message. A
/// selection of a field that doesn't exist keeps graphql-go's wording.
fn format_error(message: &str) -> String {
    if let Some(rest) = message.strip_prefix("Unknown field ") {
        if rest.contains("\" on type \"") {
            return format!("Cannot query field {rest}");
        }
    }

    // The messages Go masks: unknown input fields and invalid variables.
    let lower = message.to_lowercase();
    if lower.contains("unknown field") || lower.contains("got invalid value") {
        "Invalid request format. Please check your input fields.".into()
    } else {
        message.to_string()
    }
}
