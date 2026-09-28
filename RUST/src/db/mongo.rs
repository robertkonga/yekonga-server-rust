//! `database.kind = "mongodb"` (port of `yekonga/dbconnect_mongodb.go` and
//! `dbconnect_mongodb_indexes.go`).
//!
//! Records are stored the way the Go server stores them, so both servers
//! can share a database: `_id`, `tenantId` and other ID fields are
//! ObjectIds, Date fields are BSON dates, and each model's collection is its
//! snake_case plural name. Values read back become JSON: ObjectIds as hex
//! strings, dates as RFC 3339 strings.

use std::time::Duration;

pub use mongodb;

use mongodb::bson::{doc, oid::ObjectId, Bson, DateTime as BsonDateTime, Document};
use mongodb::options::{ClientOptions, Credential, IndexOptions, ReturnDocument};
use mongodb::{Client, Collection, IndexModel};
use serde_json::{Map, Value};
use tokio::sync::OnceCell;

use super::filter::{Cond, Filter, Operand};
use super::values::{format_datetime, parse_datetime, ZERO_OBJECT_ID};
use super::{model_indexes, Aggregate, Backend, DataMap, DbError, DbFuture, Query, SortOrder};
use crate::config::DatabaseConfig;
use crate::model::{DataModel, FieldKind, TENANT_ID_KEY};

pub struct MongoBackend {
    config: DatabaseConfig,
    client: OnceCell<Client>,
}

impl MongoBackend {
    /// Connects lazily, on the first query.
    pub fn new(config: DatabaseConfig) -> Self {
        Self {
            config,
            client: OnceCell::new(),
        }
    }

    /// `mongodb[+srv]://host[:port]`, without the port when it's empty or 80
    /// (as the Go server builds it).
    pub fn connection_url(config: &DatabaseConfig) -> String {
        let srv = if config.srv { "+srv" } else { "" };
        match config.port.as_str() {
            "" | "80" => format!("mongodb{srv}://{}", config.host),
            port => format!("mongodb{srv}://{}:{port}", config.host),
        }
    }

    async fn client(&self) -> Result<&Client, DbError> {
        self.client
            .get_or_try_init(|| async {
                let mut options = ClientOptions::parse(Self::connection_url(&self.config))
                    .await
                    .map_err(err)?;
                let c = &self.config;
                let seconds = |s: i64| (s > 0).then(|| Duration::from_secs(s as u64));

                if c.max_pool_size > 0 {
                    options.max_pool_size = Some(c.max_pool_size as u32);
                }
                if c.min_pool_size > 0 {
                    options.min_pool_size = Some(c.min_pool_size as u32);
                }
                options.max_idle_time =
                    seconds(c.max_conn_idle_time_seconds).or(options.max_idle_time);
                options.connect_timeout =
                    seconds(c.connect_timeout_seconds).or(options.connect_timeout);
                options.server_selection_timeout = seconds(c.server_selection_timeout_seconds)
                    .or(options.server_selection_timeout);

                if let Some(username) = c.username.as_str().filter(|u| !u.is_empty()) {
                    options.credential = Some(
                        Credential::builder()
                            .username(username.to_string())
                            .password(c.password.as_str().map(String::from))
                            .build(),
                    );
                }

                let client = Client::with_options(options).map_err(err)?;
                tracing::info!("Connected to MongoDB!");
                Ok(client)
            })
            .await
    }

    async fn collection(&self, model: &DataModel) -> Result<Collection<Document>, DbError> {
        Ok(self
            .client()
            .await?
            .database(&self.config.database_name)
            .collection(&model.collection))
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

    async fn find_docs(&self, query: &Query) -> Result<Vec<Document>, DbError> {
        let collection = self.collection(&query.model).await?;
        let mut find = collection
            .find(filter_doc(&query.filter, &query.model))
            .sort(sort_doc(query));
        if query.skip > 0 {
            find = find.skip(query.skip);
        }
        if let Some(limit) = query.limit {
            find = find.limit(limit as i64);
        }

        collect(find.await.map_err(err)?).await
    }

    /// Creates the single-field indexes the models' queries rely on: every
    /// field marked `unique` (as a unique index), and fields marked `index`,
    /// `tenantId` and foreign keys. Existing indexes are left alone.
    pub async fn ensure_indexes<'a>(
        &self,
        models: impl Iterator<Item = &'a DataModel>,
    ) -> Result<usize, DbError> {
        let mut created = 0;

        for model in models {
            let collection = self.collection(model).await?;
            for (field, unique) in model_indexes(model) {
                let options = IndexOptions::builder()
                    .unique(unique.then_some(true))
                    .build();
                let index = IndexModel::builder()
                    .keys(doc! {field.as_str(): 1})
                    .options(options)
                    .build();

                match collection.create_index(index).await {
                    Ok(_) => created += 1,
                    // 85/86: an index on the field already exists with other options.
                    Err(e) if matches!(command_code(&e), Some(85 | 86)) => created += 1,
                    Err(e) => {
                        tracing::error!(index = %format!("{}.{field}", model.collection), %e, "could not create index")
                    }
                }
            }
        }

        Ok(created)
    }
}

fn command_code(error: &mongodb::error::Error) -> Option<i32> {
    match error.kind.as_ref() {
        mongodb::error::ErrorKind::Command(command) => Some(command.code),
        _ => None,
    }
}

fn err(error: mongodb::error::Error) -> DbError {
    DbError::Other(format!("MongoDB: {error}"))
}

async fn collect(mut cursor: mongodb::Cursor<Document>) -> Result<Vec<Document>, DbError> {
    let mut docs = Vec::new();
    while cursor.advance().await.map_err(err)? {
        docs.push(cursor.deserialize_current().map_err(err)?);
    }
    Ok(docs)
}

fn sort_doc(query: &Query) -> Document {
    query
        .sort
        .iter()
        .map(|(field, order)| {
            (
                field.clone(),
                Bson::Int32(if *order == SortOrder::Desc { -1 } else { 1 }),
            )
        })
        .collect()
}

// ----- filters -----------------------------------------------------------------

/// Translates a compiled filter into a MongoDB query document.
pub fn filter_doc(filter: &Filter, model: &DataModel) -> Document {
    let list = |filters: &[Filter]| {
        Bson::Array(
            filters
                .iter()
                .map(|f| Bson::Document(filter_doc(f, model)))
                .collect(),
        )
    };

    match filter {
        Filter::All => Document::new(),
        Filter::And(filters) => doc! {"$and": list(filters)},
        Filter::Or(filters) => doc! {"$or": list(filters)},
        Filter::Nor(filters) => doc! {"$nor": list(filters)},
        Filter::Field(name, cond) => doc! {name.as_str(): cond_doc(name, cond, model)},
    }
}

fn cond_doc(field: &str, cond: &Cond, model: &DataModel) -> Document {
    let op = |operand: &Operand| operand_bson(field, operand, model);
    let ops = |operands: &[Operand]| Bson::Array(operands.iter().map(op).collect());

    match cond {
        Cond::Eq(o) => doc! {"$eq": op(o)},
        Cond::Ne(o) => doc! {"$ne": op(o)},
        Cond::Lt(o) => doc! {"$lt": op(o)},
        Cond::Lte(o) => doc! {"$lte": op(o)},
        Cond::Gt(o) => doc! {"$gt": op(o)},
        Cond::Gte(o) => doc! {"$gte": op(o)},
        Cond::In(o) => doc! {"$in": ops(o)},
        Cond::Nin(o) => doc! {"$nin": ops(o)},
        Cond::All(o) => doc! {"$all": ops(o)},
        // Present and not null.
        Cond::Exists(true) => doc! {"$exists": true, "$nin": [Bson::Null]},
        // Missing or null: {$eq: null} matches both.
        Cond::Exists(false) => doc! {"$eq": Bson::Null},
        Cond::Regex(re) => doc! {"$regex": re.as_str(), "$options": "i"},
        Cond::Not(inner) => doc! {"$not": cond_doc(field, inner, model)},
    }
}

fn operand_bson(field: &str, operand: &Operand, model: &DataModel) -> Bson {
    match operand {
        Operand::Value(value) => field_to_bson(field, value, model),
        Operand::Date(at) => Bson::DateTime(BsonDateTime::from_millis(at.timestamp_millis())),
        Operand::ObjectId(id) => Bson::ObjectId(object_id(id)),
    }
}

fn object_id(hex: &str) -> ObjectId {
    ObjectId::parse_str(hex).unwrap_or_else(|_| ObjectId::parse_str(ZERO_OBJECT_ID).unwrap())
}

fn is_id_field(field: &str, model: &DataModel) -> bool {
    field == "_id" || field == "id" || field == TENANT_ID_KEY || model.id_keys.contains(field)
}

// ----- JSON <-> BSON -----------------------------------------------------------------

/// A field's JSON value as stored: ids as ObjectIds and dates as BSON dates.
pub fn field_to_bson(field: &str, value: &Value, model: &DataModel) -> Bson {
    if is_id_field(field, model) {
        return ids_to_bson(value);
    }

    if model.fields.get(field).map(|f| f.kind) == Some(FieldKind::Date) {
        if let Some(at) = value.as_str().and_then(parse_datetime) {
            return Bson::DateTime(BsonDateTime::from_millis(at.timestamp_millis()));
        }
    }

    json_to_bson(value)
}

fn ids_to_bson(value: &Value) -> Bson {
    match value {
        Value::String(s) if ObjectId::parse_str(s).is_ok() => Bson::ObjectId(object_id(s)),
        Value::Array(items) => Bson::Array(items.iter().map(ids_to_bson).collect()),
        other => json_to_bson(other),
    }
}

pub fn json_to_bson(value: &Value) -> Bson {
    match value {
        Value::Null => Bson::Null,
        Value::Bool(b) => Bson::Boolean(*b),
        Value::Number(n) => match n.as_i64() {
            Some(i) => i32::try_from(i).map(Bson::Int32).unwrap_or(Bson::Int64(i)),
            None => Bson::Double(n.as_f64().unwrap_or(0.0)),
        },
        Value::String(s) => Bson::String(s.clone()),
        Value::Array(items) => Bson::Array(items.iter().map(json_to_bson).collect()),
        Value::Object(map) => Bson::Document(
            map.iter()
                .map(|(k, v)| (k.clone(), json_to_bson(v)))
                .collect(),
        ),
    }
}

fn record_to_doc(record: &DataMap, model: &DataModel) -> Document {
    record
        .iter()
        .map(|(k, v)| (k.clone(), field_to_bson(k, v, model)))
        .collect()
}

pub fn bson_to_json(value: &Bson) -> Value {
    match value {
        Bson::Null | Bson::Undefined => Value::Null,
        Bson::Boolean(b) => Value::Bool(*b),
        Bson::Int32(i) => Value::from(*i),
        Bson::Int64(i) => Value::from(*i),
        Bson::Double(f) => serde_json::Number::from_f64(*f)
            .map(Value::Number)
            .unwrap_or(Value::Null),
        Bson::String(s) => Value::String(s.clone()),
        Bson::ObjectId(id) => Value::String(id.to_hex()),
        Bson::DateTime(at) => chrono::DateTime::from_timestamp_millis(at.timestamp_millis())
            .map(|at| Value::String(format_datetime(at)))
            .unwrap_or(Value::Null),
        Bson::Array(items) => Value::Array(items.iter().map(bson_to_json).collect()),
        Bson::Document(doc) => Value::Object(doc_to_json(doc)),
        Bson::Decimal128(d) => Value::String(d.to_string()),
        other => other.clone().into_relaxed_extjson(),
    }
}

fn doc_to_json(doc: &Document) -> Map<String, Value> {
    doc.iter()
        .map(|(k, v)| (k.clone(), bson_to_json(v)))
        .collect()
}

// ----- backend ---------------------------------------------------------------------

impl Backend for MongoBackend {
    fn find<'a>(&'a self, query: &'a Query) -> DbFuture<'a, Vec<DataMap>> {
        Box::pin(self.timed(async move {
            Ok(self
                .find_docs(query)
                .await?
                .iter()
                .map(doc_to_json)
                .collect())
        }))
    }

    fn count<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64> {
        Box::pin(self.timed(async move {
            let collection = self.collection(&query.model).await?;
            if query.filter.is_all() {
                // Nothing to filter on: read the count from collection metadata.
                collection.estimated_document_count().await.map_err(err)
            } else {
                collection
                    .count_documents(filter_doc(&query.filter, &query.model))
                    .await
                    .map_err(err)
            }
        }))
    }

    fn aggregate<'a>(
        &'a self,
        query: &'a Query,
        op: Aggregate,
        field: &'a str,
    ) -> DbFuture<'a, Value> {
        Box::pin(self.timed(async move {
            let operator = match op {
                Aggregate::Sum => "$sum",
                Aggregate::Average => "$avg",
                Aggregate::Max => "$max",
                Aggregate::Min => "$min",
            };
            let pipeline = [
                doc! {"$match": filter_doc(&query.filter, &query.model)},
                doc! {"$group": {"_id": Bson::Null, "value": {operator: format!("${field}")}}},
            ];

            let collection = self.collection(&query.model).await?;
            let docs = collect(collection.aggregate(pipeline).await.map_err(err)?).await?;
            let value = docs
                .first()
                .and_then(|d| d.get("value"))
                .map(bson_to_json)
                .unwrap_or(Value::Null);

            Ok(match (op, value) {
                (Aggregate::Sum | Aggregate::Average, Value::Null) => Value::from(0.0),
                (_, value) => value,
            })
        }))
    }

    fn insert<'a>(&'a self, query: &'a Query, records: Vec<DataMap>) -> DbFuture<'a, Vec<DataMap>> {
        Box::pin(self.timed(async move {
            let mut docs: Vec<Document> = records
                .iter()
                .map(|r| record_to_doc(r, &query.model))
                .collect();
            for doc in &mut docs {
                if !doc.contains_key("_id") {
                    doc.insert("_id", ObjectId::new());
                }
            }
            if docs.is_empty() {
                return Ok(Vec::new());
            }

            let collection = self.collection(&query.model).await?;
            collection.insert_many(&docs).await.map_err(err)?;

            Ok(docs.iter().map(doc_to_json).collect())
        }))
    }

    fn update<'a>(
        &'a self,
        query: &'a Query,
        fields: DataMap,
        many: bool,
    ) -> DbFuture<'a, Vec<DataMap>> {
        Box::pin(self.timed(async move {
            let set = record_to_doc(&fields, &query.model);
            let collection = self.collection(&query.model).await?;
            let filter = filter_doc(&query.filter, &query.model);

            if set.is_empty() {
                // Nothing to change ($set can't be empty): return the records as they are.
                let limited = Query {
                    limit: (!many).then_some(1),
                    skip: 0,
                    ..query.clone()
                };
                return Ok(self
                    .find_docs(&limited)
                    .await?
                    .iter()
                    .map(doc_to_json)
                    .collect());
            }

            if !many {
                let updated = collection
                    .find_one_and_update(filter, doc! {"$set": set})
                    .sort(sort_doc(query))
                    .return_document(ReturnDocument::After)
                    .await
                    .map_err(err)?;
                return Ok(updated.iter().map(doc_to_json).collect());
            }

            // Read the matching ids first: re-reading with the filter afterwards
            // would miss records the update moved out of it.
            let ids: Vec<Bson> = collect(
                collection
                    .find(filter)
                    .projection(doc! {"_id": 1})
                    .sort(sort_doc(query))
                    .await
                    .map_err(err)?,
            )
            .await?
            .into_iter()
            .filter_map(|d| d.get("_id").cloned())
            .collect();

            if ids.is_empty() {
                return Ok(Vec::new());
            }

            collection
                .update_many(doc! {"_id": {"$in": &ids}}, doc! {"$set": set})
                .await
                .map_err(err)?;

            let docs = collect(
                collection
                    .find(doc! {"_id": {"$in": &ids}})
                    .await
                    .map_err(err)?,
            )
            .await?;
            let mut by_id: std::collections::HashMap<String, Document> = docs
                .into_iter()
                .map(|d| (d.get("_id").map(|v| v.to_string()).unwrap_or_default(), d))
                .collect();

            Ok(ids
                .iter()
                .filter_map(|id| by_id.remove(&id.to_string()))
                .map(|d| doc_to_json(&d))
                .collect())
        }))
    }

    fn delete<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64> {
        Box::pin(self.timed(async move {
            if query.filter.is_all() {
                return Err(DbError::EmptyDeleteFilter);
            }

            let collection = self.collection(&query.model).await?;
            let result = collection
                .delete_many(filter_doc(&query.filter, &query.model))
                .await
                .map_err(err)?;
            Ok(result.deleted_count)
        }))
    }

    fn ensure_indexes<'a>(&'a self, models: Vec<&'a DataModel>) -> DbFuture<'a, usize> {
        Box::pin(MongoBackend::ensure_indexes(self, models.into_iter()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::values::parse_datetime;
    use crate::model::build_system_models;
    use crate::schema::DatabaseStructure;
    use serde_json::json;

    fn model() -> DataModel {
        let structure = DatabaseStructure::from_value(&json!({
            "Orders": {
                "userId": {"type": "ID", "foreignKey": "User.id"},
                "placedAt": {"type": "Date"},
                "total": {"type": "Float"},
                "code": {"type": "String", "unique": true},
                "region": {"type": "String", "index": true}
            }
        }));
        build_system_models(&Default::default(), &structure)
            .remove("Order")
            .unwrap()
    }

    #[test]
    fn url() {
        let mut config = DatabaseConfig {
            host: "db.local".into(),
            ..Default::default()
        };
        assert_eq!(MongoBackend::connection_url(&config), "mongodb://db.local");
        config.port = "27017".into();
        assert_eq!(
            MongoBackend::connection_url(&config),
            "mongodb://db.local:27017"
        );
        config.srv = true;
        config.port = "80".into();
        assert_eq!(
            MongoBackend::connection_url(&config),
            "mongodb+srv://db.local"
        );
    }

    #[test]
    fn filters_translate_to_mongodb() {
        let model = model();
        let id = "5f1a2b3c4d5e6f7a8b9c0d1e";
        let filter = Filter::And(vec![
            Filter::Field("userId".into(), Cond::Eq(Operand::Value(json!(id)))),
            Filter::Field(
                "total".into(),
                Cond::Not(Box::new(Cond::Lt(Operand::Value(json!(5))))),
            ),
            Filter::Field(
                "placedAt".into(),
                Cond::Gte(Operand::Date(parse_datetime("2026-01-01").unwrap())),
            ),
            Filter::Or(vec![
                Filter::Field("note".into(), Cond::Exists(true)),
                Filter::Field("_id".into(), Cond::In(vec![Operand::ObjectId(id.into())])),
            ]),
        ]);

        let oid = ObjectId::parse_str(id).unwrap();
        let jan1 =
            BsonDateTime::from_millis(parse_datetime("2026-01-01").unwrap().timestamp_millis());
        assert_eq!(
            filter_doc(&filter, &model),
            doc! {"$and": [
                {"userId": {"$eq": oid}},
                {"total": {"$not": {"$lt": 5}}},
                {"placedAt": {"$gte": jan1}},
                {"$or": [
                    {"note": {"$exists": true, "$nin": [Bson::Null]}},
                    {"_id": {"$in": [oid]}},
                ]},
            ]}
        );
    }

    #[test]
    fn values_round_trip() {
        let model = model();
        let record = json!({
            "_id": "5f1a2b3c4d5e6f7a8b9c0d1e", "userId": "5f1a2b3c4d5e6f7a8b9c0d1f",
            "placedAt": "2026-09-28T10:00:00.000Z", "total": 2.5, "count": 3, "meta": {"a": [1, "x"]}
        });
        let doc = record_to_doc(record.as_object().unwrap(), &model);

        assert!(matches!(doc.get("_id"), Some(Bson::ObjectId(_))));
        assert!(matches!(doc.get("userId"), Some(Bson::ObjectId(_))));
        assert!(matches!(doc.get("placedAt"), Some(Bson::DateTime(_))));
        assert_eq!(doc.get("count"), Some(&Bson::Int32(3)));
        assert_eq!(Value::Object(doc_to_json(&doc)), record);
    }

    #[test]
    fn indexes() {
        assert_eq!(
            model_indexes(&model()),
            vec![
                ("code".to_string(), true),
                ("region".to_string(), false),
                ("userId".to_string(), false)
            ]
        );
    }
}
