//! Field resolvers (port of the resolve functions and `setModelParams` in
//! `yekonga/graphql.go`).

use std::sync::Arc;

use async_graphql::dynamic::{FieldValue, ResolverContext};
use async_graphql::{Error, Value as GqlValue};
use chrono::SecondsFormat;
use serde_json::{json, Map, Value};

use super::schema::RelationKeys;
use super::{ExecutionData, Node, Relation, Summary};
use crate::app::Yekonga;
use crate::db::values::{is_empty, parse_datetime};
use crate::db::DataMap;
use crate::helper::{get_base_url, to_variable};
use crate::model::DataModel;
use crate::query::ModelQuery;

type FieldResult<'a> = Result<Option<FieldValue<'a>>, Error>;

pub(crate) fn not_ported(what: &str) -> Error {
    Error::new(format!("{what} is not supported by the Rust port yet"))
}

fn db_error(err: crate::db::DbError) -> Error {
    Error::new(err.to_string())
}

/// How a value is coerced for its GraphQL output type.
#[derive(Clone, Copy)]
pub(crate) enum Kind {
    Int,
    Float,
    Bool,
    String,
    Date,
    Array,
    Raw,
}

impl Kind {
    pub fn for_scalar(scalar: &str) -> Self {
        match scalar {
            "Int" => Kind::Int,
            "Float" => Kind::Float,
            "Boolean" => Kind::Bool,
            "String" => Kind::String,
            "Date" => Kind::Date,
            "Array" => Kind::Array,
            _ => Kind::Raw,
        }
    }
}

/// Converts a stored value to its output type, as the Go scalars serialize.
pub(crate) fn coerce(kind: Kind, value: Value) -> Option<GqlValue> {
    let value = match (kind, value) {
        (Kind::Array, Value::Array(items)) => Value::Array(items),
        (Kind::Array, _) => Value::Array(Vec::new()),
        (_, Value::Null) => return None,
        (Kind::Int, Value::Number(n)) => match n.as_i64() {
            Some(i) => json!(i),
            None => json!(n.as_f64().unwrap_or(0.0).trunc() as i64),
        },
        (Kind::Int, Value::String(s)) => s.trim().parse::<i64>().map(|i| json!(i)).ok()?,
        (Kind::Int, Value::Bool(b)) => json!(b as i64),
        (Kind::Float, Value::String(s)) => s.trim().parse::<f64>().map(|f| json!(f)).ok()?,
        (Kind::Float, Value::Bool(b)) => json!(if b { 1.0 } else { 0.0 }),
        (Kind::Bool, Value::Number(n)) => json!(n.as_f64() != Some(0.0)),
        (Kind::Bool, Value::String(s)) => json!(!s.is_empty() && s != "false"),
        (Kind::String, Value::Number(n)) => json!(n.to_string()),
        (Kind::String, Value::Bool(b)) => json!(b.to_string()),
        (Kind::String, v @ (Value::Array(_) | Value::Object(_))) => json!(v.to_string()),
        // Go formats dates as RFC 3339 with whole seconds.
        (Kind::Date, Value::String(s)) => match parse_datetime(&s) {
            Some(at) => json!(at.to_rfc3339_opts(SecondsFormat::Secs, true)),
            None => Value::String(s),
        },
        (_, v) => v,
    };

    GqlValue::from_json(whole_numbers(value)).ok()
}

/// Writes whole floats without a fraction (`200`, not `200.0`), as Go's JSON
/// encoder does, inside lists and objects too.
fn whole_numbers(value: Value) -> Value {
    match value {
        Value::Number(n) if n.is_f64() => match n.as_f64() {
            Some(f) if f.fract() == 0.0 && f.abs() < 9.0e15 => json!(f as i64),
            _ => Value::Number(n),
        },
        Value::Array(items) => Value::Array(items.into_iter().map(whole_numbers).collect()),
        Value::Object(map) => Value::Object(
            map.into_iter()
                .map(|(k, v)| (k, whole_numbers(v)))
                .collect(),
        ),
        other => other,
    }
}

fn execution<'a>(ctx: &'a ResolverContext<'_>) -> Option<&'a ExecutionData> {
    ctx.data_opt::<ExecutionData>()
}

/// A record field for output: protected fields are hidden and file fields
/// become URLs (Go's `formateOutputData`).
pub(crate) fn output_value(
    ctx: &ResolverContext<'_>,
    model: &DataModel,
    key: &str,
    record: &DataMap,
) -> Value {
    let value = record.get(key).cloned().unwrap_or(Value::Null);

    if model.protected.iter().any(|p| p == key) {
        return json!("--protected--");
    }

    if model.file_fields.iter().any(|f| f == key) {
        let request = execution(ctx).and_then(|e| e.request.as_ref());
        let domain = request
            .and_then(|r| r.client())
            .map(|c| c.origin_domain())
            .unwrap_or_default();
        let (base_url, port) = request
            .map(|r| {
                (
                    r.app().config().base_url.clone(),
                    r.app().config().ports.server as u16,
                )
            })
            .unwrap_or_default();

        let path = match &value {
            Value::String(s) if !s.is_empty() => s.as_str(),
            _ => "placeholder.jpg",
        };
        return Value::String(get_base_url(path, &domain, &base_url, port));
    }

    value
}

/// The field's arguments as JSON (enum values become their names).
pub(crate) fn args(ctx: &ResolverContext<'_>) -> Map<String, Value> {
    ctx.args
        .as_index_map()
        .iter()
        .map(|(k, v)| (k.to_string(), v.clone().into_json().unwrap_or(Value::Null)))
        .collect()
}

fn names(value: Option<&Value>) -> Vec<String> {
    match value {
        Some(Value::Array(items)) => items
            .iter()
            .filter_map(|v| v.as_str().map(String::from))
            .collect(),
        Some(Value::String(s)) => vec![s.clone()],
        _ => Vec::new(),
    }
}

fn new_query(
    ctx: &ResolverContext<'_>,
    app: &Yekonga,
    model: &DataModel,
) -> Result<ModelQuery, Error> {
    let query = app.query(&model.name).map_err(db_error)?;
    Ok(match execution(ctx).and_then(|e| e.request.as_ref()) {
        Some(request) => query.set_request(request),
        None => query,
    })
}

/// Adds a relation's link to the parent record. Returns `false` when the
/// parent has nothing to link on: the relation is then empty.
fn link(query: ModelQuery, model: &DataModel, relation: &Relation) -> (ModelQuery, bool) {
    let (fk, tk) = (relation.foreign_key.as_str(), relation.target_key.as_str());
    let parent = |key: &str| relation.parent.get(key).cloned().unwrap_or(Value::Null);

    if fk.is_empty() {
        return (query, true);
    }

    if model.parent_keys.iter().any(|k| k == fk) {
        let query = if relation.is_parent {
            query.where_(tk, parent(fk))
        } else {
            query.where_(fk, parent(tk))
        };
        return (query, true);
    }

    if is_empty(&parent(fk)) {
        // Callers that don't check must still get no records.
        return (query.where_("_id", Value::Null), false);
    }

    (query.where_(tk, parent(fk)), true)
}

/// Applies `where`, `orderBy`, `limit`, `page` and `skip` (Go's
/// `setModelParams` for a field's own arguments).
fn apply_args(mut query: ModelQuery, args: &Map<String, Value>) -> Result<ModelQuery, Error> {
    if let Some(Value::Object(where_)) = args.get("where") {
        query = query.where_many(Value::Object(where_.clone()));
    }
    if let Some(Value::Object(order)) = args.get("orderBy") {
        for (field, direction) in order {
            query = query.order_by(field, direction.as_str().unwrap_or("ASC"));
        }
    }
    // `groupBy` collapses the rows into one record per group (see `group_by`),
    // so its limit/skip/page apply to the groups, not the raw rows: leave them
    // off the query and let `list` window the grouped result instead. `orderBy`
    // stays on the query — sorting the rows by the group keys means each group's
    // first row (the one kept) already comes out in the right order.
    if names(args.get("groupBy")).is_empty() {
        if let Some(limit) = args.get("limit").and_then(Value::as_i64) {
            query = query.take(limit);
        }
        if let Some(page) = args.get("page").and_then(Value::as_i64) {
            query = query.page(page);
        }
        if let Some(skip) = args.get("skip").and_then(Value::as_i64) {
            query = query.skip(skip);
        }
    }
    Ok(query)
}

/// The parent record of a relation field, if the parent is a record.
fn relation_for(ctx: &ResolverContext<'_>, keys: Option<&RelationKeys>) -> Option<Relation> {
    let keys = keys?;
    match ctx.parent_value.downcast_ref::<Node>() {
        Some(Node::Record(parent)) => Some(Relation {
            foreign_key: keys.foreign_key.clone(),
            target_key: keys.target_key.clone(),
            is_parent: keys.is_parent,
            parent: parent.clone(),
        }),
        _ => None,
    }
}

/// A query for a field: its arguments, then its relation to the parent.
fn field_query(
    ctx: &ResolverContext<'_>,
    app: &Yekonga,
    model: &DataModel,
    keys: Option<&RelationKeys>,
) -> Result<(ModelQuery, bool), Error> {
    let args = args(ctx);
    let query = apply_args(new_query(ctx, app, model)?, &args)?;

    Ok(match relation_for(ctx, keys) {
        Some(relation) => link(query, model, &relation),
        None => (query, true),
    })
}

fn record(data: DataMap) -> FieldValue<'static> {
    FieldValue::owned_any(Node::Record(data))
}

pub(crate) async fn single<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
    keys: Option<RelationKeys>,
) -> FieldResult<'a> {
    let (query, linked) = field_query(ctx, app, model, keys.as_ref())?;
    if !linked {
        return Ok(None);
    }

    Ok(query.find_one().await.map_err(db_error)?.map(record))
}

pub(crate) async fn list<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
    keys: Option<RelationKeys>,
) -> FieldResult<'a> {
    let args = args(ctx);
    let (query, _) = field_query(ctx, app, model, keys.as_ref())?;
    let records = query.find().await.map_err(db_error)?;

    let group_fields = names(args.get("groupBy"));
    let records = if group_fields.is_empty() {
        distinct(records, &names(args.get("distinct")))
    } else {
        group_window(group_by(records, &group_fields, model), &args)
    };
    Ok(Some(FieldValue::list(records.into_iter().map(record))))
}

/// Keeps the first record for each distinct combination of `fields` (Go's
/// `distinct` argument). No fields means every record is kept.
pub(crate) fn distinct(records: Vec<DataMap>, fields: &[String]) -> Vec<DataMap> {
    if fields.is_empty() {
        return records;
    }
    let mut seen = std::collections::HashSet::new();
    records
        .into_iter()
        .filter(|record| {
            let key: Vec<String> = fields
                .iter()
                .map(|f| record.get(f).map(ToString::to_string).unwrap_or_default())
                .collect();
            seen.insert(key)
        })
        .collect()
}

/// Collapses `records` into one per distinct combination of `fields` (Go's
/// `groupBy` argument). Each group keeps the group fields at the top level and
/// together under `_id`/`id`, plus `_collection`/`_model`, matching the shape
/// the MongoDB and SQL backends return from a `$group`. The first row seen for
/// a group wins, so a caller that sorted the rows by the group keys gets the
/// groups in order.
pub(crate) fn group_by(
    records: Vec<DataMap>,
    fields: &[String],
    model: &DataModel,
) -> Vec<DataMap> {
    let mut seen = std::collections::HashSet::new();
    let mut groups = Vec::new();
    for row in records {
        let key: Vec<String> = fields
            .iter()
            .map(|f| row.get(f).map(ToString::to_string).unwrap_or_default())
            .collect();
        if !seen.insert(key) {
            continue;
        }

        let id: Map<String, Value> = fields
            .iter()
            .map(|f| (f.clone(), row.get(f).cloned().unwrap_or(Value::Null)))
            .collect();
        let id = Value::Object(id);

        let mut group = DataMap::new();
        for f in fields {
            group.insert(f.clone(), row.get(f).cloned().unwrap_or(Value::Null));
        }
        group.insert("_id".into(), id.clone());
        group.insert("id".into(), id);
        group.insert(
            "_collection".into(),
            Value::String(model.collection.clone()),
        );
        group.insert("_model".into(), Value::String(model.name.clone()));
        groups.push(group);
    }
    groups
}

/// Applies a `groupBy` query's `skip`/`page`/`limit` to the grouped records
/// (Go applies these after the `GROUP BY`, so they page the groups). `skip`
/// wins over `page`; a non-positive `limit` means no limit.
fn group_window(groups: Vec<DataMap>, args: &Map<String, Value>) -> Vec<DataMap> {
    let arg = |key| args.get(key).and_then(Value::as_i64);
    let limit = arg("limit").filter(|n| *n > 0);
    let skip = match arg("skip").filter(|n| *n > 0) {
        Some(skip) => skip,
        None => limit.unwrap_or(0) * (arg("page").unwrap_or(1) - 1).max(0),
    };

    let mut windowed: Vec<DataMap> = groups.into_iter().skip(skip as usize).collect();
    if let Some(limit) = limit {
        windowed.truncate(limit as usize);
    }
    windowed
}

pub(crate) async fn paginate<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
    keys: Option<RelationKeys>,
) -> FieldResult<'a> {
    if !names(args(ctx).get("groupBy")).is_empty() {
        // Grouped pagination needs group-aware totals/pages, which the ported
        // backends don't compute; only the plural list applies `groupBy`.
        return Err(not_ported("groupBy on a paginated query"));
    }
    let (query, _) = field_query(ctx, app, model, keys.as_ref())?;
    let page = query.paginate().await.map_err(db_error)?;
    Ok(Some(FieldValue::owned_any(Node::Json(Value::Object(page)))))
}

/// The summary object only records its context; its fields run the queries.
pub(crate) fn summary<'a>(
    ctx: &ResolverContext<'a>,
    keys: Option<&RelationKeys>,
) -> Option<FieldValue<'a>> {
    Some(FieldValue::owned_any(Node::Summary(Summary {
        args: args(ctx),
        relation: relation_for(ctx, keys).map(|r| Relation {
            is_parent: false,
            ..r
        }),
    })))
}

pub(crate) async fn aggregate<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
    op: &str,
) -> FieldResult<'a> {
    let Some(Node::Summary(summary)) = ctx.parent_value.downcast_ref::<Node>() else {
        return Ok(None);
    };

    // The summary's where and relation, then the field's own arguments.
    let mut query = new_query(ctx, app, model)?;
    if let Some(Value::Object(where_)) = summary.args.get("where") {
        query = query.where_many(Value::Object(where_.clone()));
    }
    if let Some(relation) = &summary.relation {
        query = link(query, model, relation).0;
    }
    let args = args(ctx);
    query = apply_args(query, &args)?;
    let target = args
        .get("targetKey")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string();

    let value = match op {
        "count" => json!(query.count().await.map_err(db_error)?),
        "sum" => json!(query.sum(&target).await.map_err(db_error)?),
        "average" => json!(query.average(&target).await.map_err(db_error)?),
        "max" => query.max(&target).await.map_err(db_error)?,
        "min" => query.min(&target).await.map_err(db_error)?,
        _ => Value::Null,
    };

    let kind = if matches!(op, "count" | "sum" | "average") {
        Kind::Float
    } else {
        Kind::Raw
    };
    Ok(coerce(kind, value).map(FieldValue::value))
}

// ----- mutations ------------------------------------------------------------------------------

/// Go's `getInputData`: `input`, else `inputData`, else `inputRaw`.
fn input_data(args: &Map<String, Value>) -> Value {
    ["input", "inputData", "inputRaw"]
        .iter()
        .find_map(|k| args.get(*k).cloned())
        .unwrap_or(Value::Null)
}

fn result(success: bool, data: Value) -> FieldValue<'static> {
    let message = if success { "Success" } else { "Fail" };
    FieldValue::owned_any(Node::Json(
        json!({"success": success, "status": success, "message": message, "data": data}),
    ))
}

/// Creates or updates the child records given inside a parent's input (e.g.
/// `orderItems` in `createOrder`), linked to `parent`.
async fn save_children(
    ctx: &ResolverContext<'_>,
    app: &Yekonga,
    model: &DataModel,
    input: &Value,
    parent: &DataMap,
) -> Result<(), Error> {
    for (relation_name, relation) in &model.children_fields {
        let Some(Value::Array(children)) = input.get(to_variable(relation_name)) else {
            continue;
        };
        let Some(child_model) = app.models().get(&relation.model_name) else {
            continue;
        };

        let linked: Vec<Value> = children
            .iter()
            .filter_map(|child| {
                let mut child = child.as_object()?.clone();
                child.insert(
                    relation.foreign_key.clone(),
                    parent
                        .get(&relation.primary_key)
                        .cloned()
                        .unwrap_or(Value::Null),
                );
                Some(Value::Object(child))
            })
            .collect();

        if !linked.is_empty() {
            new_query(ctx, app, child_model)?
                .import(linked, &[])
                .await
                .map_err(db_error)?;
        }
    }
    Ok(())
}

pub(crate) async fn create<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
) -> FieldResult<'a> {
    let input = input_data(&args(ctx));
    let created = match new_query(ctx, app, model)?.create(input.clone()).await {
        Ok(created) => created,
        Err(err) => {
            tracing::warn!(model = %model.name, %err, "create failed");
            return Ok(Some(result(false, Value::Null)));
        }
    };

    save_children(ctx, app, model, &input, &created).await?;
    Ok(Some(result(true, Value::Object(created))))
}

pub(crate) async fn update<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
) -> FieldResult<'a> {
    let args = args(ctx);
    let input = input_data(&args);
    let query = apply_args(new_query(ctx, app, model)?, &args)?;

    let updated = match query.update(input.clone()).await {
        Ok(Some(updated)) => updated,
        Ok(None) => return Ok(Some(result(false, Value::Null))),
        Err(err) => {
            tracing::warn!(model = %model.name, %err, "update failed");
            return Ok(Some(result(false, Value::Null)));
        }
    };

    save_children(ctx, app, model, &input, &updated).await?;
    Ok(Some(result(true, Value::Object(updated))))
}

pub(crate) async fn delete<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
) -> FieldResult<'a> {
    let query = apply_args(new_query(ctx, app, model)?, &args(ctx))?;
    let deleted = query.delete().await.unwrap_or(0);
    Ok(Some(result(deleted > 0, Value::Null)))
}

pub(crate) async fn import<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
) -> FieldResult<'a> {
    let args = args(ctx);
    let data = match input_data(&args) {
        Value::Array(items) => items,
        _ => Vec::new(),
    };
    let unique_keys = names(args.get("uniqueKeys"));

    let imported = new_query(ctx, app, model)?
        .import(data.clone(), &unique_keys)
        .await
        .map_err(db_error)?;

    // Children of each saved record, from the input it came from.
    for saved in imported
        .get("data")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
    {
        let Some(saved) = saved.as_object() else {
            continue;
        };
        if let Some(input) = data
            .iter()
            .find(|input| input_matches(saved, input, &unique_keys))
        {
            save_children(ctx, app, model, input, saved).await?;
        }
    }

    Ok(Some(FieldValue::owned_any(Node::Json(Value::Object(
        imported,
    )))))
}

/// Runs a model's `xAction` mutation through a registered action function
/// (Go's `getMutationActionField`). Errors when no handler is registered for
/// the model, action and access role/route.
pub(crate) async fn action<'a>(
    ctx: &ResolverContext<'a>,
    app: &Yekonga,
    model: &Arc<DataModel>,
) -> FieldResult<'a> {
    let args = args(ctx);
    let request = execution(ctx).and_then(|e| e.request.as_ref());
    let action_name = args
        .get("action")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string();

    let context = crate::cloud::ActionContext {
        app: app.clone(),
        request: request.cloned(),
        model: model.name.clone(),
        action: action_name.clone(),
        input: input_data(&args),
        filters: args.get("where").cloned().unwrap_or(Value::Null),
        access_role: args
            .get("accessRole")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string(),
        route: args
            .get("route")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string(),
    };

    match app.run_graphql_action(context).await {
        Some(result) => Ok(Some(FieldValue::owned_any(Node::Json(json!({
            "data": result.data,
            "success": result.success,
            "status": result.status,
            "message": result.message,
        }))))),
        None => Err(Error::new(format!(
            "no action \"{action_name}\" is registered for {}",
            model.name
        ))),
    }
}

/// Whether a saved record came from `input` (Go's `importInputMatches`): by
/// the unique keys, or the id when there are none.
fn input_matches(saved: &DataMap, input: &Value, unique_keys: &[String]) -> bool {
    let id_keys = ["id".to_string()];
    let keys = if unique_keys.is_empty() {
        &id_keys[..]
    } else {
        unique_keys
    };

    keys.iter().all(|key| {
        let wanted = match key.as_str() {
            "id" | "_id" => input.get("_id").or_else(|| input.get("id")),
            _ => input.get(key),
        };
        match wanted.filter(|v| !v.is_null()) {
            Some(wanted) => saved
                .get(key.as_str())
                .is_some_and(|v| v == wanted || v.as_str() == wanted.as_str()),
            None => false,
        }
    })
}
