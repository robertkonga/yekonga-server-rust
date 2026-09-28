//! `database.kind = "mysql"` or `"sql"`, on MySQL-compatible servers (MySQL
//! 5.7+, MariaDB 10.2+). Port of `yekonga/dbconnect_sql*.go`.
//!
//! It behaves like the MongoDB backend, so nothing above it needs to know
//! which one is in use:
//! - Filters are translated to SQL with MongoDB's meaning, including for
//!   missing values: `{$ne: x}` also matches NULL, `NOR` matches records
//!   without the field, and so on.
//! - The primary key column is `_id`, holding the id as hex.
//! - Tables and columns are created from the schema on first use (unless
//!   `database.disableAutoMigrate`). Existing columns are never changed or
//!   dropped. Indexes are created by [`Backend::ensure_indexes`].
//!
//! Table layout and column types match the Go server's, so both can share a
//! database.

use std::collections::{HashMap, HashSet};
use std::time::Duration;

use chrono::{Datelike, NaiveDate, TimeZone, Timelike, Utc};
use mysql_async::prelude::Queryable;
use mysql_async::{
    Opts, OptsBuilder, Params, Pool, PoolConstraints, PoolOpts, Row, TxOpts, Value as SqlValue,
};
use serde_json::Value;
use tokio::sync::Mutex;

use super::filter::{Cond, Filter, Operand};
use super::values::{format_datetime, new_object_id, parse_datetime};
use super::{model_indexes, Aggregate, Backend, DataMap, DbError, DbFuture, Query, SortOrder};
use crate::config::DatabaseConfig;
use crate::model::{DataModel, FieldKind};

pub use mysql_async;

/// Most ids in one `IN (...)` list.
const IN_CHUNK: usize = 1000;

pub struct SqlBackend {
    config: DatabaseConfig,
    pool: Pool,
    /// Tables already created or checked in this process.
    migrated: Mutex<HashSet<String>>,
}

impl SqlBackend {
    /// Connects lazily, on the first query.
    pub fn new(config: DatabaseConfig) -> Self {
        let pool = Pool::new(Self::opts(&config));
        Self {
            config,
            pool,
            migrated: Mutex::default(),
        }
    }

    fn opts(c: &DatabaseConfig) -> Opts {
        let port = c.port.parse().unwrap_or(3306);
        let mut pool_opts = PoolOpts::default();
        let max = if c.max_pool_size > 0 {
            c.max_pool_size as usize
        } else {
            10
        };
        let min = (c.min_pool_size.max(0) as usize).min(max);
        if let Some(constraints) = PoolConstraints::new(min, max) {
            pool_opts = pool_opts.with_constraints(constraints);
        }
        if c.max_conn_idle_time_seconds > 0 {
            pool_opts = pool_opts.with_inactive_connection_ttl(Duration::from_secs(
                c.max_conn_idle_time_seconds as u64,
            ));
        }

        OptsBuilder::default()
            .ip_or_hostname(c.host.clone())
            .tcp_port(port)
            .user(c.username.as_str().map(String::from))
            .pass(c.password.as_str().map(String::from))
            .db_name(Some(c.database_name.clone()))
            .pool_opts(pool_opts)
            .into()
    }

    /// Runs an operation within `database.queryTimeoutSeconds`, if set.
    async fn timed<T>(
        &self,
        op: impl std::future::Future<Output = Result<T, DbError>>,
    ) -> Result<T, DbError> {
        match self.config.query_timeout_seconds {
            s if s > 0 => tokio::time::timeout(Duration::from_secs(s as u64), op)
                .await
                .map_err(|_| DbError::Other(format!("database query timed out after {s}s")))?,
            _ => op.await,
        }
    }

    /// Creates the model's table, or adds its missing columns, once per process.
    async fn migrate(&self, model: &DataModel) -> Result<(), DbError> {
        if self.config.disable_auto_migrate {
            return Ok(());
        }

        let mut migrated = self.migrated.lock().await;
        if migrated.contains(&model.collection) {
            return Ok(());
        }

        let indexed: HashSet<String> = model_indexes(model).into_iter().map(|(f, _)| f).collect();
        let columns = model_columns(model);
        let mut conn = self.pool.get_conn().await.map_err(err)?;

        let mut definitions = vec!["`_id` VARCHAR(64) NOT NULL".to_string()];
        for (name, kind) in &columns {
            definitions.push(format!(
                "{} {} NULL",
                quote(name),
                column_type(*kind, indexed.contains(name))
            ));
        }
        definitions.push("PRIMARY KEY (`_id`)".into());
        let create = format!(
            "CREATE TABLE IF NOT EXISTS {} (\n  {}\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
            quote(&model.collection),
            definitions.join(",\n  ")
        );
        conn.query_drop(create).await.map_err(err)?;

        // The table may already exist from an older schema.
        let existing: Vec<String> = conn
            .exec(
                "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
                (model.collection.clone(),),
            )
            .await
            .map_err(err)?;
        let existing: HashSet<String> = existing.into_iter().map(|c| c.to_lowercase()).collect();

        for (name, kind) in &columns {
            if existing.contains(&name.to_lowercase()) {
                continue;
            }
            let add = format!(
                "ALTER TABLE {} ADD COLUMN {} {} NULL",
                quote(&model.collection),
                quote(name),
                column_type(*kind, indexed.contains(name))
            );
            conn.query_drop(add).await.map_err(err)?;
            tracing::info!(column = %format!("{}.{name}", model.collection), "added column");
        }

        migrated.insert(model.collection.clone());
        Ok(())
    }

    async fn select(
        &self,
        query: &Query,
        sql: String,
        params: Vec<SqlValue>,
    ) -> Result<Vec<DataMap>, DbError> {
        let mut conn = self.pool.get_conn().await.map_err(err)?;
        let rows: Vec<Row> = conn.exec(sql, params_of(params)).await.map_err(err)?;
        Ok(rows
            .into_iter()
            .map(|row| decode_row(row, &query.model))
            .collect())
    }

    async fn records_by_id(
        conn: &mut impl Queryable,
        model: &DataModel,
        ids: &[String],
    ) -> Result<Vec<DataMap>, DbError> {
        let mut by_id: HashMap<String, DataMap> = HashMap::new();
        for chunk in ids.chunks(IN_CHUNK) {
            let sql = format!(
                "SELECT * FROM {} WHERE `_id` IN ({})",
                quote(&model.collection),
                placeholders(chunk.len())
            );
            let rows: Vec<Row> = conn
                .exec(
                    sql,
                    params_of(chunk.iter().map(|id| id.as_str().into()).collect()),
                )
                .await
                .map_err(err)?;
            for row in rows {
                let record = decode_row(row, model);
                if let Some(id) = record.get("_id").and_then(Value::as_str) {
                    by_id.insert(id.to_string(), record);
                }
            }
        }

        Ok(ids.iter().filter_map(|id| by_id.remove(id)).collect())
    }

    /// Matching ids in sort order, locked for update.
    async fn locked_ids(
        tx: &mut impl Queryable,
        query: &Query,
        limit: Option<u64>,
    ) -> Result<Vec<String>, DbError> {
        let (where_sql, params) = where_clause(&query.filter, &query.model);
        let limit = limit.map(|l| format!(" LIMIT {l}")).unwrap_or_default();
        let sql = format!(
            "SELECT `_id` FROM {}{where_sql}{}{limit} FOR UPDATE",
            quote(&query.model.collection),
            order_clause(query)
        );
        tx.exec(sql, params_of(params)).await.map_err(err)
    }
}

fn err(error: mysql_async::Error) -> DbError {
    DbError::Other(format!("MySQL: {error}"))
}

pub fn quote(name: &str) -> String {
    format!("`{}`", name.replace('`', "``"))
}

fn placeholders(n: usize) -> String {
    vec!["?"; n].join(", ")
}

fn params_of(values: Vec<SqlValue>) -> Params {
    if values.is_empty() {
        Params::Empty
    } else {
        Params::Positional(values)
    }
}

fn is_json_kind(kind: FieldKind) -> bool {
    matches!(kind, FieldKind::Object | FieldKind::Any | FieldKind::Array)
}

/// Columns other than `_id`, sorted.
fn model_columns(model: &DataModel) -> Vec<(String, FieldKind)> {
    model
        .fields
        .iter()
        .filter(|(name, _)| *name != "id" && *name != "_id")
        .map(|(name, field)| (name.clone(), field.kind))
        .collect()
}

/// The column type for a field kind. Indexed strings are VARCHAR(255):
/// MySQL can't index a whole TEXT column.
pub fn column_type(kind: FieldKind, indexed: bool) -> &'static str {
    match kind {
        FieldKind::Id => "VARCHAR(64)",
        FieldKind::Number => "BIGINT",
        FieldKind::Float => "DOUBLE",
        FieldKind::Bool => "BOOLEAN",
        FieldKind::Date => "DATETIME(3)",
        FieldKind::Object | FieldKind::Any | FieldKind::Array => "JSON",
        FieldKind::String | FieldKind::File if indexed => "VARCHAR(255)",
        FieldKind::String | FieldKind::File => "TEXT",
    }
}

/// Whether a column holds JSON: Object/Any/Array fields (JSON columns), and
/// list fields like `[String]`, which the Go server stores as JSON text in a
/// column of the element type. Filters on the latter match elements, as in
/// MongoDB (the Go SQL backend compares the whole text instead).
fn holds_json(model: &DataModel, column: &str) -> bool {
    model
        .fields
        .get(column)
        .is_some_and(|f| is_json_kind(f.kind) || f.is_array)
}

/// The kind of a column, if the model has it.
fn column_kind(model: &DataModel, column: &str) -> Option<FieldKind> {
    match column {
        "_id" | "id" => Some(FieldKind::Id),
        _ => model.fields.get(column).map(|f| f.kind),
    }
}

/// The SQL expression for a filter or sort key: a column, a path inside a
/// JSON column (`address.city`), or `NULL` for a field the table doesn't
/// have (which then behaves like a missing field does in MongoDB).
pub fn column_expr(model: &DataModel, key: &str) -> (String, FieldKind, bool) {
    let column = if key == "id" { "_id" } else { key };
    if let Some(kind) = column_kind(model, column) {
        return (quote(column), kind, holds_json(model, column));
    }

    if let Some((base, path)) = key.split_once('.') {
        if holds_json(model, base) {
            let mut json_path = String::from("$");
            for part in path.split('.') {
                if part.is_empty() || !part.chars().all(|c| c.is_ascii_alphanumeric() || c == '_') {
                    return ("NULL".into(), FieldKind::String, false);
                }
                if part.parse::<u64>().is_ok() {
                    json_path.push_str(&format!("[{part}]"));
                } else {
                    json_path.push_str(&format!(".\"{part}\""));
                }
            }
            return (
                format!("JSON_UNQUOTE(JSON_EXTRACT({}, '{json_path}'))", quote(base)),
                FieldKind::String,
                false,
            );
        }
    }

    ("NULL".into(), FieldKind::String, false)
}

// ----- values ------------------------------------------------------------------------

fn sql_datetime(at: chrono::DateTime<Utc>) -> SqlValue {
    SqlValue::Date(
        at.year() as u16,
        at.month() as u8,
        at.day() as u8,
        at.hour() as u8,
        at.minute() as u8,
        at.second() as u8,
        at.timestamp_subsec_micros(),
    )
}

/// A JSON value as a parameter for a column of the given kind.
pub fn encode_value(kind: FieldKind, value: &Value) -> SqlValue {
    if value.is_null() {
        return SqlValue::NULL;
    }
    if is_json_kind(kind) {
        return SqlValue::from(value.to_string());
    }

    match (kind, value) {
        (FieldKind::Date, Value::String(s)) => parse_datetime(s)
            .map(sql_datetime)
            .unwrap_or_else(|| s.as_str().into()),
        (_, Value::Bool(b)) => SqlValue::Int(*b as i64),
        (_, Value::Number(n)) => match n.as_i64() {
            Some(i) => SqlValue::Int(i),
            None => SqlValue::Double(n.as_f64().unwrap_or(0.0)),
        },
        (_, Value::String(s)) => s.as_str().into(),
        // A list or object in a plain column is stored as JSON text.
        (_, other) => SqlValue::from(other.to_string()),
    }
}

fn operand_value(kind: FieldKind, operand: &Operand) -> SqlValue {
    match operand {
        Operand::Value(v) => encode_value(kind, v),
        Operand::Date(at) => sql_datetime(*at),
        Operand::ObjectId(id) => id.as_str().into(),
    }
}

fn operand_json(operand: &Operand) -> String {
    match operand {
        Operand::Value(v) => v.to_string(),
        Operand::Date(at) => Value::String(format_datetime(*at)).to_string(),
        Operand::ObjectId(id) => Value::String(id.clone()).to_string(),
    }
}

fn is_composite(operand: &Operand) -> bool {
    matches!(operand, Operand::Value(Value::Array(_) | Value::Object(_)))
}

/// A column value as JSON, by the column's kind.
pub fn decode_value(kind: Option<FieldKind>, raw: SqlValue) -> Value {
    let text = |bytes: Vec<u8>| String::from_utf8_lossy(&bytes).into_owned();

    match (kind, raw) {
        (_, SqlValue::NULL) => Value::Null,
        (Some(FieldKind::Date), SqlValue::Date(y, mo, d, h, mi, s, us)) => {
            NaiveDate::from_ymd_opt(y as i32, mo as u32, d as u32)
                .and_then(|date| date.and_hms_micro_opt(h as u32, mi as u32, s as u32, us))
                .map(|at| Value::String(format_datetime(Utc.from_utc_datetime(&at))))
                .unwrap_or(Value::Null)
        }
        (Some(FieldKind::Date), SqlValue::Bytes(b)) => {
            let s = text(b);
            let parsed = chrono::NaiveDateTime::parse_from_str(&s, "%Y-%m-%d %H:%M:%S%.f")
                .map(|at| Utc.from_utc_datetime(&at))
                .ok()
                .or_else(|| parse_datetime(&s));
            parsed
                .map(|at| Value::String(format_datetime(at)))
                .unwrap_or(Value::String(s))
        }
        (Some(FieldKind::Bool), SqlValue::Int(i)) => Value::Bool(i != 0),
        (Some(FieldKind::Bool), SqlValue::UInt(i)) => Value::Bool(i != 0),
        (Some(FieldKind::Bool), SqlValue::Bytes(b)) => {
            let s = text(b);
            Value::Bool(s == "1" || s.eq_ignore_ascii_case("true"))
        }
        (Some(FieldKind::Float), SqlValue::Int(i)) => Value::from(i as f64),
        (Some(FieldKind::Float | FieldKind::Number), SqlValue::Bytes(b)) => {
            let s = text(b);
            match (kind, s.parse::<i64>()) {
                (Some(FieldKind::Number), Ok(i)) => Value::from(i),
                _ => s
                    .parse::<f64>()
                    .map(super::values::float_value)
                    .unwrap_or(Value::String(s)),
            }
        }
        (Some(FieldKind::Object | FieldKind::Any | FieldKind::Array), SqlValue::Bytes(b)) => {
            let s = text(b);
            serde_json::from_str(&s).unwrap_or(Value::String(s))
        }
        (_, SqlValue::Int(i)) => Value::from(i),
        (_, SqlValue::UInt(u)) => Value::from(u),
        (_, SqlValue::Float(f)) => super::values::float_value(f as f64),
        (_, SqlValue::Double(f)) => super::values::float_value(f),
        (_, SqlValue::Bytes(b)) => Value::String(text(b)),
        (_, date @ SqlValue::Date(..)) => decode_value(Some(FieldKind::Date), date),
        (_, SqlValue::Time(..)) => Value::Null,
    }
}

fn decode_row(row: Row, model: &DataModel) -> DataMap {
    let names: Vec<String> = row
        .columns_ref()
        .iter()
        .map(|c| c.name_str().into_owned())
        .collect();
    names
        .into_iter()
        .zip(row.unwrap())
        .map(|(name, raw)| {
            let kind = if holds_json(model, &name) {
                Some(FieldKind::Any)
            } else {
                column_kind(model, &name)
            };
            let value = decode_value(kind, raw);
            (name, value)
        })
        .collect()
}

// ----- filters -------------------------------------------------------------------------

/// Translates a compiled filter into a SQL condition.
struct FilterBuilder<'a> {
    model: &'a DataModel,
    params: Vec<SqlValue>,
}

/// ` WHERE ...` and its parameters, or nothing for a filter matching everything.
pub fn where_clause(filter: &Filter, model: &DataModel) -> (String, Vec<SqlValue>) {
    if filter.is_all() {
        return (String::new(), Vec::new());
    }

    let mut builder = FilterBuilder {
        model,
        params: Vec::new(),
    };
    let condition = builder.filter(filter);
    (format!(" WHERE {condition}"), builder.params)
}

impl FilterBuilder<'_> {
    fn filter(&mut self, filter: &Filter) -> String {
        match filter {
            Filter::All => "1=1".into(),
            Filter::And(filters) => self.join(filters, " AND ", "1=1"),
            Filter::Or(filters) => self.join(filters, " OR ", "1=0"),
            Filter::Nor(filters) if filters.is_empty() => "1=1".into(),
            Filter::Nor(filters) => format!("NOT {}", self.join(filters, " OR ", "1=0")),
            Filter::Field(key, cond) => {
                let (expr, kind, json) = column_expr(self.model, key);
                // SQL comparisons with NULL are unknown, not false, so NOT would
                // drop those rows. IS TRUE makes each condition true or false, as
                // in MongoDB.
                format!("(({}) IS TRUE)", self.cond(&expr, kind, json, cond))
            }
        }
    }

    fn join(&mut self, filters: &[Filter], separator: &str, empty: &str) -> String {
        if filters.is_empty() {
            return empty.into();
        }
        let parts: Vec<String> = filters.iter().map(|f| self.filter(f)).collect();
        format!("({})", parts.join(separator))
    }

    fn param(&mut self, value: SqlValue) -> &'static str {
        self.params.push(value);
        "?"
    }

    fn cond(&mut self, expr: &str, kind: FieldKind, json: bool, cond: &Cond) -> String {
        match cond {
            Cond::Eq(op) => self.equal(expr, kind, json, op),
            Cond::Ne(op) if op.is_null() => format!("{expr} IS NOT NULL"),
            Cond::Ne(op) => format!(
                "(NOT ({}) OR {expr} IS NULL)",
                self.equal(expr, kind, json, op)
            ),
            Cond::Lt(op) => format!("{expr} < {}", self.param(operand_value(kind, op))),
            Cond::Lte(op) => format!("{expr} <= {}", self.param(operand_value(kind, op))),
            Cond::Gt(op) => format!("{expr} > {}", self.param(operand_value(kind, op))),
            Cond::Gte(op) => format!("{expr} >= {}", self.param(operand_value(kind, op))),
            Cond::In(ops) => self.in_list(expr, kind, json, ops),
            Cond::Nin(ops) => {
                let within = self.in_list(expr, kind, json, ops);
                // MongoDB's $nin matches missing values, unless null is in the list.
                if ops.iter().any(Operand::is_null) {
                    format!("NOT {within}")
                } else {
                    format!("(NOT {within} OR {expr} IS NULL)")
                }
            }
            Cond::All(ops) if ops.is_empty() => "1=0".into(),
            Cond::All(ops) => {
                let parts: Vec<String> = ops
                    .iter()
                    .map(|op| self.equal(expr, kind, json, op))
                    .collect();
                format!("({})", parts.join(" AND "))
            }
            Cond::Exists(true) => format!("{expr} IS NOT NULL"),
            Cond::Exists(false) => format!("{expr} IS NULL"),
            Cond::Regex(re) => {
                // Case-insensitive matching comes from the column's collation;
                // MySQL 5.7 doesn't understand inline flags.
                let pattern = re.as_str().trim_start_matches("(?i)").to_string();
                format!("{expr} REGEXP {}", self.param(pattern.into()))
            }
            // A missing field doesn't match the condition, so it matches NOT.
            Cond::Not(inner) => format!(
                "(NOT ({}) OR {expr} IS NULL)",
                self.cond(expr, kind, json, inner)
            ),
        }
    }

    /// Equality. On a JSON column it matches like MongoDB does for arrays: an
    /// element equal to the value, or the whole value for a list or object.
    fn equal(&mut self, expr: &str, kind: FieldKind, json: bool, op: &Operand) -> String {
        if op.is_null() {
            return format!("{expr} IS NULL");
        }

        let composite = is_composite(op);
        if json {
            let value = operand_json(op);
            let first = self.param(value.clone().into());
            if !composite {
                return format!("JSON_CONTAINS({expr}, {first})");
            }
            let reverse = self.param(value.into());
            return format!(
                "(JSON_CONTAINS({expr}, {first}) AND JSON_CONTAINS({reverse}, {expr}))"
            );
        }

        if composite {
            return "1=0".into(); // a plain column never holds a list or object
        }

        format!("{expr} = {}", self.param(operand_value(kind, op)))
    }

    fn in_list(&mut self, expr: &str, kind: FieldKind, json: bool, ops: &[Operand]) -> String {
        if ops.is_empty() {
            return "1=0".into();
        }

        if json {
            let parts: Vec<String> = ops
                .iter()
                .map(|op| self.equal(expr, kind, json, op))
                .collect();
            return format!("({})", parts.join(" OR "));
        }

        let values: Vec<&Operand> = ops.iter().filter(|op| !op.is_null()).collect();
        let mut parts = Vec::new();
        if !values.is_empty() {
            for op in &values {
                let value = operand_value(kind, op);
                self.params.push(value);
            }
            parts.push(format!("{expr} IN ({})", placeholders(values.len())));
        }
        if values.len() < ops.len() {
            parts.push(format!("{expr} IS NULL"));
        }

        format!("({})", parts.join(" OR "))
    }
}

fn order_clause(query: &Query) -> String {
    let parts: Vec<String> = query
        .sort
        .iter()
        .filter_map(|(key, order)| {
            let (expr, _, _) = column_expr(&query.model, key);
            (expr != "NULL").then(|| {
                format!(
                    "{expr} {}",
                    if *order == SortOrder::Desc {
                        "DESC"
                    } else {
                        "ASC"
                    }
                )
            })
        })
        .collect();

    if parts.is_empty() {
        String::new()
    } else {
        format!(" ORDER BY {}", parts.join(", "))
    }
}

fn limit_clause(query: &Query) -> String {
    match (query.limit, query.skip) {
        (Some(limit), skip) if skip > 0 => format!(" LIMIT {limit} OFFSET {skip}"),
        (Some(limit), _) => format!(" LIMIT {limit}"),
        // MySQL has no OFFSET without LIMIT; this is its documented "all rows".
        (None, skip) if skip > 0 => format!(" LIMIT 18446744073709551615 OFFSET {skip}"),
        (None, _) => String::new(),
    }
}

/// `(columns, rows of values)` for inserting records: every known column
/// present in any record, sorted.
fn insert_rows(model: &DataModel, records: &mut [DataMap]) -> (Vec<String>, Vec<Vec<SqlValue>>) {
    let mut columns: Vec<String> = Vec::new();
    for record in records.iter_mut() {
        if !record.contains_key("_id") {
            record.insert("_id".into(), Value::String(new_object_id()));
        }
        for key in record.keys() {
            let column = if key == "id" { "_id" } else { key.as_str() };
            if column_kind(model, column).is_some() && !columns.iter().any(|c| c == column) {
                columns.push(column.to_string());
            }
        }
    }
    columns.sort();

    let rows = records
        .iter()
        .map(|record| {
            columns
                .iter()
                .map(|c| {
                    encode_value(
                        column_kind(model, c).unwrap_or(FieldKind::String),
                        record.get(c).unwrap_or(&Value::Null),
                    )
                })
                .collect()
        })
        .collect();

    (columns, rows)
}

/// `` `a` = ?, `b` = ?`` and its values for the known, non-key fields.
fn set_clause(model: &DataModel, fields: &DataMap) -> (String, Vec<SqlValue>) {
    let mut keys: Vec<&String> = fields
        .keys()
        .filter(|k| *k != "_id" && *k != "id" && column_kind(model, k).is_some())
        .collect();
    keys.sort();

    let sets = keys
        .iter()
        .map(|k| format!("{} = ?", quote(k)))
        .collect::<Vec<_>>()
        .join(", ");
    let values = keys
        .iter()
        .map(|k| encode_value(column_kind(model, k).unwrap(), &fields[k.as_str()]))
        .collect();
    (sets, values)
}

// ----- backend -------------------------------------------------------------------------

impl Backend for SqlBackend {
    fn kind(&self) -> &str {
        "mysql"
    }

    fn find<'a>(&'a self, query: &'a Query) -> DbFuture<'a, Vec<DataMap>> {
        Box::pin(self.timed(async move {
            self.migrate(&query.model).await?;
            let (where_sql, params) = where_clause(&query.filter, &query.model);
            let sql = format!(
                "SELECT * FROM {}{where_sql}{}{}",
                quote(&query.model.collection),
                order_clause(query),
                limit_clause(query)
            );
            self.select(query, sql, params).await
        }))
    }

    fn count<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64> {
        Box::pin(self.timed(async move {
            self.migrate(&query.model).await?;
            let (where_sql, params) = where_clause(&query.filter, &query.model);
            let sql = format!(
                "SELECT COUNT(*) FROM {}{where_sql}",
                quote(&query.model.collection)
            );
            let mut conn = self.pool.get_conn().await.map_err(err)?;
            let count: Option<u64> = conn.exec_first(sql, params_of(params)).await.map_err(err)?;
            Ok(count.unwrap_or(0))
        }))
    }

    fn aggregate<'a>(
        &'a self,
        query: &'a Query,
        op: Aggregate,
        field: &'a str,
    ) -> DbFuture<'a, Value> {
        Box::pin(self.timed(async move {
            self.migrate(&query.model).await?;
            let (expr, kind, _) = column_expr(&query.model, field);
            let selected = match op {
                Aggregate::Sum => format!("SUM({expr})"),
                // Double precision, as MongoDB computes it (MySQL's AVG of an
                // integer column is a DECIMAL rounded to 4 places).
                Aggregate::Average => format!("(SUM({expr}) * 1e0 / COUNT({expr}))"),
                Aggregate::Max => format!("MAX({expr})"),
                Aggregate::Min => format!("MIN({expr})"),
            };
            let (where_sql, params) = where_clause(&query.filter, &query.model);
            let sql = format!(
                "SELECT {selected} FROM {}{where_sql}",
                quote(&query.model.collection)
            );

            let mut conn = self.pool.get_conn().await.map_err(err)?;
            let row: Option<Row> = conn.exec_first(sql, params_of(params)).await.map_err(err)?;
            let raw = row
                .and_then(|r| r.unwrap().into_iter().next())
                .unwrap_or(SqlValue::NULL);

            Ok(match op {
                Aggregate::Sum | Aggregate::Average => {
                    match decode_value(Some(FieldKind::Float), raw) {
                        Value::Null => Value::from(0.0),
                        v => v,
                    }
                }
                _ => decode_value(Some(kind), raw),
            })
        }))
    }

    fn insert<'a>(
        &'a self,
        query: &'a Query,
        mut records: Vec<DataMap>,
    ) -> DbFuture<'a, Vec<DataMap>> {
        Box::pin(self.timed(async move {
            if records.is_empty() {
                return Ok(Vec::new());
            }
            self.migrate(&query.model).await?;

            let model = &query.model;
            let (columns, rows) = insert_rows(model, &mut records);
            let ids: Vec<String> = records
                .iter()
                .filter_map(|r| r.get("_id").and_then(Value::as_str).map(String::from))
                .collect();
            let quoted = columns
                .iter()
                .map(|c| quote(c))
                .collect::<Vec<_>>()
                .join(", ");
            let row_placeholder = format!("({})", placeholders(columns.len()));
            // Keep each statement well under the server's placeholder limit.
            let chunk = (60_000 / columns.len().max(1)).clamp(1, 500);

            let mut tx = self
                .pool
                .start_transaction(TxOpts::default())
                .await
                .map_err(err)?;
            for batch in rows.chunks(chunk) {
                let sql = format!(
                    "INSERT INTO {} ({quoted}) VALUES {}",
                    quote(&model.collection),
                    vec![row_placeholder.as_str(); batch.len()].join(", ")
                );
                tx.exec_drop(sql, params_of(batch.concat()))
                    .await
                    .map_err(err)?;
            }
            // Read back, so values have exactly the types and precision stored.
            let created = Self::records_by_id(&mut tx, model, &ids).await?;
            tx.commit().await.map_err(err)?;

            Ok(created)
        }))
    }

    fn update<'a>(
        &'a self,
        query: &'a Query,
        fields: DataMap,
        many: bool,
    ) -> DbFuture<'a, Vec<DataMap>> {
        Box::pin(self.timed(async move {
            self.migrate(&query.model).await?;
            let model = &query.model;
            let (set, set_values) = set_clause(model, &fields);

            let mut tx = self
                .pool
                .start_transaction(TxOpts::default())
                .await
                .map_err(err)?;
            // The ids are read (and locked) first, so records the update moves
            // out of the filter are still returned.
            let ids = Self::locked_ids(&mut tx, query, (!many).then_some(1)).await?;

            if !ids.is_empty() && !set.is_empty() {
                for chunk in ids.chunks(IN_CHUNK) {
                    let sql = format!(
                        "UPDATE {} SET {set} WHERE `_id` IN ({})",
                        quote(&model.collection),
                        placeholders(chunk.len())
                    );
                    let mut params = set_values.clone();
                    params.extend(chunk.iter().map(|id| SqlValue::from(id.as_str())));
                    tx.exec_drop(sql, params_of(params)).await.map_err(err)?;
                }
            }

            let updated = Self::records_by_id(&mut tx, model, &ids).await?;
            tx.commit().await.map_err(err)?;
            Ok(updated)
        }))
    }

    fn delete<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64> {
        Box::pin(self.timed(async move {
            if query.filter.is_all() {
                return Err(DbError::EmptyDeleteFilter);
            }
            self.migrate(&query.model).await?;

            let (where_sql, params) = where_clause(&query.filter, &query.model);
            let sql = format!("DELETE FROM {}{where_sql}", quote(&query.model.collection));
            let mut conn = self.pool.get_conn().await.map_err(err)?;
            conn.exec_drop(sql, params_of(params)).await.map_err(err)?;
            Ok(conn.affected_rows())
        }))
    }

    fn ensure_indexes<'a>(&'a self, models: Vec<&'a DataModel>) -> DbFuture<'a, usize> {
        Box::pin(async move {
            let mut created = 0;
            let mut conn = self.pool.get_conn().await.map_err(err)?;

            for model in models {
                self.migrate(model).await?;
                let existing: Vec<String> = conn
                    .exec(
                        "SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
                        (model.collection.clone(),),
                    )
                    .await
                    .map_err(err)?;
                let existing: HashSet<String> =
                    existing.into_iter().map(|n| n.to_lowercase()).collect();

                for (field, unique) in model_indexes(model) {
                    if model
                        .fields
                        .get(&field)
                        .is_some_and(|f| is_json_kind(f.kind))
                    {
                        continue; // JSON columns can't be indexed directly
                    }

                    let mut name = format!("{}_{field}", if unique { "uniq" } else { "idx" });
                    name.truncate(64);
                    if existing.contains(&name.to_lowercase()) {
                        created += 1;
                        continue;
                    }

                    let sql = format!(
                        "CREATE {}INDEX {} ON {} ({})",
                        if unique { "UNIQUE " } else { "" },
                        quote(&name),
                        quote(&model.collection),
                        quote(&field)
                    );
                    match conn.query_drop(sql).await {
                        Ok(()) => created += 1,
                        Err(e) => {
                            tracing::error!(index = %format!("{}.{field}", model.collection), %e, "could not create index")
                        }
                    }
                }
            }

            Ok(created)
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::build_system_models;
    use crate::schema::DatabaseStructure;
    use serde_json::json;

    fn model() -> DataModel {
        let structure = DatabaseStructure::from_value(&json!({
            "Orders": {
                "status": {"type": "String", "index": true},
                "total": {"type": "Float"},
                "tags": {"type": "[String]"},
                "meta": {"type": "Object"},
                "placedAt": {"type": "Date"}
            }
        }));
        build_system_models(&Default::default(), &structure)
            .remove("Order")
            .unwrap()
    }

    #[test]
    fn column_expressions() {
        let model = model();
        assert_eq!(
            column_expr(&model, "status"),
            ("`status`".into(), FieldKind::String, false)
        );
        assert_eq!(column_expr(&model, "id").0, "`_id`");
        assert!(column_expr(&model, "tags").2, "arrays are JSON columns");
        assert_eq!(
            column_expr(&model, "meta.address.0").0,
            "JSON_UNQUOTE(JSON_EXTRACT(`meta`, '$.\"address\"[0]'))"
        );
        assert_eq!(column_expr(&model, "missing").0, "NULL");
        assert_eq!(column_expr(&model, "meta.bad-key").0, "NULL");
        assert_eq!(column_type(FieldKind::String, true), "VARCHAR(255)");
        assert_eq!(column_type(FieldKind::String, false), "TEXT");
    }

    #[test]
    fn filters_translate_with_mongodb_meaning() {
        let model = model();
        let filter = Filter::And(vec![
            Filter::Field("status".into(), Cond::Ne(Operand::Value(json!("void")))),
            Filter::Field("tags".into(), Cond::Eq(Operand::Value(json!("x")))),
            Filter::Nor(vec![Filter::Field(
                "total".into(),
                Cond::In(vec![Operand::Value(json!(1)), Operand::null()]),
            )]),
        ]);

        let (sql, params) = where_clause(&filter, &model);
        assert_eq!(
            sql,
            " WHERE ((((NOT (`status` = ?) OR `status` IS NULL)) IS TRUE) AND ((JSON_CONTAINS(`tags`, ?)) IS TRUE) \
             AND NOT ((((`total` IN (?) OR `total` IS NULL)) IS TRUE)))"
        );
        assert_eq!(
            params,
            vec![
                SqlValue::from("void"),
                SqlValue::from("\"x\""),
                SqlValue::Int(1)
            ]
        );
        assert_eq!(where_clause(&Filter::All, &model).0, "");
    }

    #[test]
    fn values_encode_by_kind() {
        assert_eq!(
            encode_value(FieldKind::Array, &json!(["a"])),
            SqlValue::from("[\"a\"]")
        );
        assert_eq!(
            encode_value(FieldKind::Bool, &json!(true)),
            SqlValue::Int(1)
        );
        assert_eq!(
            encode_value(FieldKind::Date, &json!("2026-09-28T10:11:12.345Z")),
            SqlValue::Date(2026, 9, 28, 10, 11, 12, 345_000)
        );
        assert_eq!(
            decode_value(
                Some(FieldKind::Date),
                SqlValue::Date(2026, 9, 28, 10, 11, 12, 345_000)
            ),
            json!("2026-09-28T10:11:12.345Z")
        );
        assert_eq!(
            decode_value(Some(FieldKind::Bool), SqlValue::Int(1)),
            json!(true)
        );
        assert_eq!(
            decode_value(Some(FieldKind::Array), SqlValue::Bytes(b"[1,2]".to_vec())),
            json!([1, 2])
        );
        assert_eq!(
            decode_value(Some(FieldKind::Number), SqlValue::Bytes(b"42".to_vec())),
            json!(42)
        );
    }
}
