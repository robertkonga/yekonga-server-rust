//! The model query builder (port of `DataModelQuery` in
//! `yekonga/model_query.go`).
//!
//! ```no_run
//! # async fn demo(app: yekonga::Yekonga) -> Result<(), yekonga::db::DbError> {
//! use serde_json::json;
//!
//! let orders = app
//!     .query("Order")?
//!     .where_("status", "paid")
//!     .where_("total", json!({"greaterThan": 100}))
//!     .order_by("createdAt", "desc")
//!     .take(20)
//!     .find()
//!     .await?;
//! # Ok(()) }
//! ```
//!
//! `where` values follow the Go API: a plain value is an equality check, an
//! object holds operators (`equalTo`, `notEqualTo`, `lessThan`,
//! `lessThanOrEqualTo`, `greaterThan`, `greaterThanOrEqualTo` and their
//! `not…` forms, `in`, `notIn`, `all`, `exists`, `matchesRegex`), `AND` /
//! `OR` / `NOR` hold lists of where objects, and an object on a relation
//! name (`{"user": {"email": "a@b.tz"}}`) filters by the related records.
//! Queries made with a request are limited to the request's tenant.

use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;

use regex::Regex;
use serde_json::{json, Value};

use crate::app::Yekonga;
use crate::db::values::{
    format_datetime, fuzzy_regex, get_timestamp, is_empty, is_numeric, new_object_id, now_string,
    object_id_from_str, to_float, to_int, ZERO_OBJECT_ID,
};
use crate::db::{Aggregate, Cond, DataMap, DbError, Filter, Operand, Query, SortOrder};
use crate::model::{DataModel, FieldKind, TENANT_ID_KEY};
use crate::request::Request;

type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// Operators a where object may use on a field.
const OPERATORS: &[&str] = &[
    "equalTo",
    "notEqualTo",
    "lessThan",
    "notLessThan",
    "lessThanOrEqualTo",
    "notLessThanOrEqualTo",
    "greaterThan",
    "notGreaterThan",
    "greaterThanOrEqualTo",
    "notGreaterThanOrEqualTo",
    "matchesRegex",
    "options",
    "in",
    "all",
    "notIn",
    "exists",
    "$type",
];

const LOGICAL_KEYS: &[&str] = &["AND", "OR", "NOR"];

/// Strings that mean `null` in where values.
fn is_null_word(value: &Value) -> bool {
    matches!(value.as_str(), Some("NULL" | "Null" | "null"))
}

#[derive(Clone)]
pub struct ModelQuery {
    app: Yekonga,
    model: Arc<DataModel>,
    request: Option<Request>,
    where_: DataMap,
    order_by: Vec<(String, String)>,
    limit: i64,
    page: i64,
    skip: i64,
    skip_tenant: bool,
    skip_before_commit: bool,
    is_admin: bool,
}

impl ModelQuery {
    pub(crate) fn new(app: Yekonga, model: Arc<DataModel>) -> Self {
        Self {
            app,
            model,
            request: None,
            where_: DataMap::new(),
            order_by: Vec::new(),
            limit: 0,
            page: 0,
            skip: 0,
            skip_tenant: false,
            skip_before_commit: false,
            is_admin: false,
        }
    }

    pub fn model(&self) -> &Arc<DataModel> {
        &self.model
    }

    /// Runs the query on behalf of a request: it is limited to the
    /// request's tenant, and new records get the tenant's id.
    pub fn set_request(mut self, request: &Request) -> Self {
        self.request = Some(request.clone());
        self
    }

    /// Doesn't limit the query to the request's tenant.
    pub fn skip_tenant(mut self) -> Self {
        self.skip_tenant = true;
        self
    }

    /// Doesn't run database triggers (kept for API parity; triggers aren't
    /// ported yet).
    pub fn skip_before_commit(mut self) -> Self {
        self.skip_before_commit = true;
        self
    }

    pub fn admin(mut self) -> Self {
        self.is_admin = true;
        self
    }

    pub fn is_admin(&self) -> bool {
        self.is_admin
    }

    /// Adds a condition. Operator objects on the same field are merged
    /// (`{"greaterThan": 1}` then `{"lessThan": 9}`); other values replace
    /// the previous condition. `id` means `_id`.
    pub fn where_(mut self, name: &str, value: impl Into<Value>) -> Self {
        merge_where(&mut self.where_, name, value.into());
        self
    }

    /// Adds every entry of a where object.
    pub fn where_many(mut self, conditions: impl Into<Value>) -> Self {
        if let Value::Object(conditions) = conditions.into() {
            for (name, value) in conditions {
                merge_where(&mut self.where_, &name, value);
            }
        }
        self
    }

    /// Sorts by a field, `"asc"` or `"desc"`. Earlier calls take priority.
    pub fn order_by(mut self, name: &str, direction: &str) -> Self {
        match self.order_by.iter_mut().find(|(n, _)| n == name) {
            Some(entry) => entry.1 = direction.to_string(),
            None => self
                .order_by
                .push((name.to_string(), direction.to_string())),
        }
        self
    }

    /// At most `n` records (0 = no limit).
    pub fn take(mut self, n: i64) -> Self {
        self.limit = n;
        self
    }

    /// Skips to page `n` (1-based) of `take`-sized pages. Call after `take`.
    pub fn page(mut self, n: i64) -> Self {
        self.page = n;
        self.skip = (n - 1) * self.limit;
        self
    }

    pub fn skip(mut self, n: i64) -> Self {
        self.skip = n;
        self
    }

    // ----- reads -----------------------------------------------------------

    pub async fn find(&self) -> Result<Vec<DataMap>, DbError> {
        let query = self.build(self.limit_value()).await?;
        let records = self.app.backend().find(&query).await?;
        Ok(records.into_iter().map(|r| self.decorate(r)).collect())
    }

    pub async fn find_one(&self) -> Result<Option<DataMap>, DbError> {
        let mut query = self.build(Some(1)).await?;
        query.skip = 0;
        let mut records = self.app.backend().find(&query).await?;
        Ok(records.pop().map(|r| self.decorate(r)))
    }

    /// Alias of [`find_one`](Self::find_one).
    pub async fn first(&self) -> Result<Option<DataMap>, DbError> {
        self.find_one().await
    }

    pub async fn exists(&self) -> Result<bool, DbError> {
        Ok(self.find_one().await?.is_some())
    }

    /// One field of the first matching record.
    pub async fn value(&self, key: &str) -> Result<Value, DbError> {
        Ok(self
            .find_one()
            .await?
            .and_then(|mut r| r.remove(key))
            .unwrap_or(Value::Null))
    }

    /// One field of every matching record.
    pub async fn values(&self, key: &str) -> Result<Vec<Value>, DbError> {
        Ok(self
            .find()
            .await?
            .into_iter()
            .map(|mut r| r.remove(key).unwrap_or(Value::Null))
            .collect())
    }

    pub async fn count(&self) -> Result<u64, DbError> {
        let query = self.build(None).await?;
        self.app.backend().count(&query).await
    }

    pub async fn sum(&self, field: &str) -> Result<f64, DbError> {
        Ok(self
            .aggregate(Aggregate::Sum, field)
            .await?
            .as_f64()
            .unwrap_or(0.0))
    }

    pub async fn average(&self, field: &str) -> Result<f64, DbError> {
        Ok(self
            .aggregate(Aggregate::Average, field)
            .await?
            .as_f64()
            .unwrap_or(0.0))
    }

    pub async fn max(&self, field: &str) -> Result<Value, DbError> {
        self.aggregate(Aggregate::Max, field).await
    }

    pub async fn min(&self, field: &str) -> Result<Value, DbError> {
        self.aggregate(Aggregate::Min, field).await
    }

    async fn aggregate(&self, op: Aggregate, field: &str) -> Result<Value, DbError> {
        let query = self.build(None).await?;
        self.app.backend().aggregate(&query, op, field).await
    }

    /// A page of records with totals: `{total, perPage, currentPage,
    /// lastPage, from, to, data}`. The page size is `take` (10 if unset).
    pub async fn paginate(&self) -> Result<DataMap, DbError> {
        let per_page = if self.limit > 0 { self.limit } else { 10 };
        let current_page = self.page.max(1);

        let page = self.clone().take(per_page).page(current_page);
        let total = self.count().await?;
        let data = page.find().await?;
        let last_page = (total as i64 + per_page - 1) / per_page;

        let json = json!({
            "total": total,
            "perPage": per_page,
            "currentPage": current_page,
            "lastPage": last_page,
            "from": per_page * (current_page - 1) + 1,
            "to": per_page * current_page,
            "data": data,
        });
        Ok(json.as_object().cloned().unwrap_or_default())
    }

    // ----- writes ----------------------------------------------------------

    /// Stores a new record built from the model's fields: missing fields get
    /// their defaults (`"now"` for dates means the current time) and values
    /// are converted to the field types. Unknown fields are dropped.
    pub async fn create(&self, data: impl Into<Value>) -> Result<DataMap, DbError> {
        let mut records = self.create_many(vec![data.into()]).await?;
        records
            .pop()
            .ok_or_else(|| DbError::Other("record was not stored".into()))
    }

    pub async fn create_many(&self, data: Vec<Value>) -> Result<Vec<DataMap>, DbError> {
        let tenant_id = self.tenant_id_for_writes();
        let records = data
            .into_iter()
            .map(|value| {
                let mut input = value.as_object().cloned().unwrap_or_default();
                if let Some(tenant_id) = &tenant_id {
                    input.insert(TENANT_ID_KEY.into(), Value::String(tenant_id.clone()));
                }
                self.format_create(&input)
            })
            .collect();

        let query = Query::new(self.model.clone(), Filter::All);
        let stored = self.app.backend().insert(&query, records).await?;
        self.app.invalidate_caches(&self.model.name);

        Ok(stored.into_iter().map(|r| self.decorate(r)).collect())
    }

    /// Sets the given fields on the first matching record (in sort order) and
    /// returns it as updated, or `None` if nothing matched.
    pub async fn update(&self, data: impl Into<Value>) -> Result<Option<DataMap>, DbError> {
        Ok(self.update_records(data.into(), false).await?.pop())
    }

    /// Sets the given fields on every matching record.
    pub async fn update_many(&self, data: impl Into<Value>) -> Result<Vec<DataMap>, DbError> {
        self.update_records(data.into(), true).await
    }

    async fn update_records(&self, data: Value, many: bool) -> Result<Vec<DataMap>, DbError> {
        let input = data.as_object().cloned().unwrap_or_default();
        let fields = self.format_update(&input);
        let query = self.build(None).await?;

        let updated = self.app.backend().update(&query, fields, many).await?;
        self.app.invalidate_caches(&self.model.name);

        Ok(updated.into_iter().map(|r| self.decorate(r)).collect())
    }

    /// Deletes every matching record and returns how many. A query without
    /// any condition is refused.
    pub async fn delete(&self) -> Result<u64, DbError> {
        let query = self.build(None).await?;
        let deleted = self.app.backend().delete(&query).await?;
        self.app.invalidate_caches(&self.model.name);
        Ok(deleted)
    }

    // ----- building ----------------------------------------------------------

    fn limit_value(&self) -> Option<u64> {
        (self.limit > 0).then_some(self.limit as u64)
    }

    async fn build(&self, limit: Option<u64>) -> Result<Query, DbError> {
        let mut where_ = self.where_.clone();
        if let Some(tenant_id) = self.tenant_scope() {
            merge_where(&mut where_, TENANT_ID_KEY, Value::String(tenant_id));
        }

        let filter = self.compile(&where_).await?;
        let mut query = Query::new(self.model.clone(), filter);

        query.sort = self
            .order_by
            .iter()
            .map(|(f, d)| (f.clone(), SortOrder::parse(d)))
            .collect();
        query.limit = limit;
        query.skip = if self.skip > 0 {
            self.skip as u64
        } else {
            (self.limit.max(0) * (self.page.max(1) - 1)) as u64
        };

        Ok(query)
    }

    /// Whether this query is limited to a tenant, and which.
    fn tenant_scope(&self) -> Option<String> {
        if !self.tenant_applies() {
            return None;
        }

        Some(
            self.request_tenant_id()
                .unwrap_or_else(|| ZERO_OBJECT_ID.to_string()),
        )
    }

    fn tenant_id_for_writes(&self) -> Option<String> {
        if !self.tenant_applies() {
            return None;
        }
        self.request_tenant_id()
    }

    fn tenant_applies(&self) -> bool {
        let config = self.app.config();
        !self.skip_tenant
            && self.model.has_tenant
            && self.request.is_some()
            && (config.has_tenant || config.has_tenant_catch)
    }

    fn request_tenant_id(&self) -> Option<String> {
        let request = self.request.as_ref()?;
        let from_token = request
            .token_payload()
            .map(|p| p.tenant_id)
            .filter(|t| !is_empty(t));
        let tenant = from_token.or_else(|| request.tenant_id())?;

        Some(match tenant {
            Value::String(s) => object_id_from_str(&s),
            other => object_id_from_str(&other.to_string()),
        })
    }

    fn is_id_field(&self, key: &str) -> bool {
        key == "_id" || key == "id" || key == TENANT_ID_KEY || self.model.id_keys.contains(key)
    }

    fn field_kind(&self, key: &str) -> Option<FieldKind> {
        self.model.fields.get(key).map(|f| f.kind)
    }

    /// Adds the fields every record read through a query carries.
    fn decorate(&self, mut record: DataMap) -> DataMap {
        let id = record.get("_id").cloned().unwrap_or(Value::Null);
        record.insert("id".into(), id);
        record.insert(
            "_collection".into(),
            Value::String(self.model.collection.clone()),
        );
        record.insert("_model".into(), Value::String(self.model.name.clone()));
        record
    }

    // ----- where -> filter ------------------------------------------------------

    fn compile<'a>(&'a self, where_: &'a DataMap) -> BoxFuture<'a, Result<Filter, DbError>> {
        Box::pin(async move {
            let mut filters = Vec::new();

            for (key, value) in where_ {
                if LOGICAL_KEYS.contains(&key.as_str()) || is_list_of_maps(value) {
                    let mut parts = Vec::new();
                    for item in value.as_array().into_iter().flatten() {
                        if let Value::Object(item) = item {
                            parts.push(self.compile(item).await?);
                        }
                    }
                    if parts.is_empty() {
                        continue;
                    }
                    filters.push(match key.as_str() {
                        "OR" => Filter::Or(parts),
                        "NOR" => Filter::Nor(parts),
                        _ => Filter::and(parts),
                    });
                    continue;
                }

                let field = if key == "id" { "_id" } else { key.as_str() };

                match value {
                    Value::Object(ops) => {
                        let mut is_relation = false;
                        for (op, operand) in ops {
                            if OPERATORS.contains(&op.as_str()) {
                                if let Some(cond) = self.condition(field, op, operand) {
                                    filters.push(Filter::Field(field.to_string(), cond));
                                }
                            } else {
                                is_relation = true;
                            }
                        }
                        if is_relation {
                            if let Some(filter) = self.relation_filter(key, ops).await? {
                                filters.push(filter);
                            }
                        }
                    }
                    plain => filters.push(Filter::Field(
                        field.to_string(),
                        Cond::Eq(self.plain_operand(field, plain)),
                    )),
                }
            }

            Ok(Filter::and(filters))
        })
    }

    /// Operand for a plain `where` value.
    fn plain_operand(&self, key: &str, value: &Value) -> Operand {
        if self.is_id_field(key) {
            if is_null_word(value) || value.as_str() == Some("") {
                return Operand::null();
            }
            return self.id_operand(value);
        }
        Operand::Value(value.clone())
    }

    /// Operand for an operator's value.
    fn operand(&self, key: &str, value: &Value) -> Operand {
        if is_null_word(value) {
            return Operand::null();
        }
        if self.is_id_field(key) && !is_empty(value) {
            return self.id_operand(value);
        }
        Operand::Value(value.clone())
    }

    fn id_operand(&self, value: &Value) -> Operand {
        match value {
            Value::String(s) => Operand::ObjectId(object_id_from_str(s)),
            Value::Array(items) => Operand::Value(Value::Array(
                items
                    .iter()
                    .map(|v| match v {
                        Value::String(s) => Value::String(object_id_from_str(s)),
                        other => other.clone(),
                    })
                    .collect(),
            )),
            other => Operand::Value(other.clone()),
        }
    }

    fn operands(&self, key: &str, value: &Value) -> Vec<Operand> {
        match value {
            Value::Array(items) => items.iter().map(|v| self.operand(key, v)).collect(),
            other => vec![self.operand(key, other)],
        }
    }

    /// Go's `ConvertCalculatedValue`: numbers (or numeric strings) compare as
    /// numbers, anything else as a date (the current time if it isn't one).
    fn comparable(&self, key: &str, value: &Value) -> Operand {
        let operand = self.operand(key, value);
        match &operand {
            Operand::ObjectId(_) => operand,
            Operand::Value(Value::Number(_)) => operand,
            Operand::Value(Value::String(s)) if is_numeric(value) => Operand::Value(
                s.trim()
                    .parse::<f64>()
                    .map(json_number)
                    .unwrap_or(Value::Null),
            ),
            Operand::Value(v) => Operand::Date(get_timestamp(v)),
            Operand::Date(_) => operand,
        }
    }

    fn condition(&self, key: &str, op: &str, value: &Value) -> Option<Cond> {
        let not = |cond: Cond| Cond::Not(Box::new(cond));

        Some(match op {
            "equalTo" | "options" => Cond::Eq(self.operand(key, value)),
            "notEqualTo" => Cond::Ne(self.operand(key, value)),
            "lessThan" => Cond::Lt(self.comparable(key, value)),
            "notLessThan" => not(Cond::Lt(self.comparable(key, value))),
            "lessThanOrEqualTo" => Cond::Lte(self.comparable(key, value)),
            "notLessThanOrEqualTo" => not(Cond::Lte(self.comparable(key, value))),
            "greaterThan" => Cond::Gt(self.comparable(key, value)),
            "notGreaterThan" => not(Cond::Gt(self.comparable(key, value))),
            "greaterThanOrEqualTo" => Cond::Gte(self.comparable(key, value)),
            "notGreaterThanOrEqualTo" => not(Cond::Gte(self.comparable(key, value))),
            "in" => Cond::In(self.operands(key, value)),
            "notIn" => Cond::Nin(self.operands(key, value)),
            "all" => Cond::All(self.operands(key, value)),
            "exists" => Cond::Exists(value.as_bool()?),
            "matchesRegex" => Cond::Regex(Regex::new(&fuzzy_regex(value.as_str()?)).ok()?),
            _ => return None,
        })
    }

    /// `{"user": {"email": ...}}`: records whose linking key is among the ids
    /// of the related records that match.
    async fn relation_filter(
        &self,
        name: &str,
        where_: &DataMap,
    ) -> Result<Option<Filter>, DbError> {
        let (model_name, read_key, filter_key) =
            if let Some(parent) = self.model.parent_fields.get(name) {
                (&parent.model_name, &parent.primary_key, &parent.foreign_key)
            } else if let Some(child) = self.model.children_fields.get(name) {
                (&child.model_name, &child.foreign_key, &child.primary_key)
            } else {
                return Ok(None);
            };

        let Ok(related) = self.app.query(model_name) else {
            return Ok(None);
        };
        let mut related = related.where_many(Value::Object(where_.clone()));
        related.request = self.request.clone();
        related.skip_tenant = self.skip_tenant;

        let ids = related
            .find()
            .await?
            .into_iter()
            .filter_map(|mut r| r.remove(read_key.as_str()))
            .map(|v| match v {
                Value::String(s) => Operand::ObjectId(object_id_from_str(&s)),
                other => Operand::Value(other),
            })
            .collect();

        Ok(Some(Filter::Field(filter_key.clone(), Cond::In(ids))))
    }

    // ----- input formatting ------------------------------------------------------

    /// Go's `formatInputData` for creates.
    fn format_create(&self, input: &DataMap) -> DataMap {
        let mut record = DataMap::new();

        for key in &self.model.valid_fields {
            if key == "id" {
                let id = input
                    .get("id")
                    .or_else(|| input.get("_id"))
                    .filter(|v| !is_empty(v));
                let id = match id {
                    Some(Value::String(s)) => object_id_from_str(s),
                    _ => new_object_id(),
                };
                record.insert("_id".into(), Value::String(id));
                continue;
            }

            let value = match input.get(key) {
                Some(value) => value.clone(),
                None => match self.model.fields.get(key) {
                    Some(field)
                        if field.kind == FieldKind::Date && field.default_value == "now" =>
                    {
                        Value::String(now_string())
                    }
                    Some(field) => field.default_value.clone(),
                    None => Value::Null,
                },
            };

            record.insert(key.clone(), self.format_field(key, value));
        }

        record
    }

    /// Go's `formatInputData` for updates: only known fields that are given.
    fn format_update(&self, input: &DataMap) -> DataMap {
        self.model
            .valid_fields
            .iter()
            .filter(|key| *key != "id" && *key != "_id")
            .filter_map(|key| Some((key.clone(), self.format_field(key, input.get(key)?.clone()))))
            .collect()
    }

    /// Converts a value to its field's type. Empty values are kept as given.
    fn format_field(&self, key: &str, value: Value) -> Value {
        if is_empty(&value) {
            return value;
        }

        if self.is_id_field(key) {
            return match &value {
                Value::String(s) => Value::String(object_id_from_str(s)),
                Value::Array(items) => Value::Array(
                    items
                        .iter()
                        .map(|v| match v {
                            Value::String(s) => Value::String(object_id_from_str(s)),
                            other => other.clone(),
                        })
                        .collect(),
                ),
                _ => value,
            };
        }

        match self.field_kind(key) {
            Some(FieldKind::Date) => Value::String(format_datetime(get_timestamp(&value))),
            Some(FieldKind::Number) => Value::from(to_int(&value)),
            Some(FieldKind::Float) => json_number(to_float(&value)),
            _ => value,
        }
    }
}

fn json_number(f: f64) -> Value {
    crate::db::values::float_value(f)
}

fn is_list_of_maps(value: &Value) -> bool {
    matches!(value, Value::Array(items) if !items.is_empty() && items.iter().all(Value::is_object))
}

/// Go's `Where` merge: operator objects on the same field are merged, other
/// values replace. `id` is stored as `_id`.
fn merge_where(where_: &mut DataMap, name: &str, value: Value) {
    let name = if name == "id" { "_id" } else { name };

    match (where_.get_mut(name), value) {
        (Some(Value::Object(existing)), Value::Object(new)) => {
            for (k, v) in new {
                existing.insert(k, v);
            }
        }
        (_, value) => {
            where_.insert(name.to_string(), value);
        }
    }
}
