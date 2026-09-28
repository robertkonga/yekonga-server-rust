//! Database backends (port of `yekonga/dbconnect*.go`).
//!
//! A [`Backend`] stores records for every model. The query builder
//! ([`crate::ModelQuery`]) turns its calls into a [`Query`] (a compiled
//! [`Filter`], sort, skip and limit) and hands it to the backend the config
//! selects.

pub mod filter;
mod local;
#[cfg(feature = "mongodb")]
pub mod mongo;
#[cfg(feature = "mysql")]
pub mod sql;
pub mod values;

use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;

use serde_json::{Map, Value};

pub use filter::{Cond, Filter, Operand};
pub use local::LocalBackend;
#[cfg(feature = "mongodb")]
pub use mongo::MongoBackend;
#[cfg(feature = "mysql")]
pub use sql::SqlBackend;

use crate::model::{DataModel, TENANT_ID_KEY};

pub type DataMap = Map<String, Value>;
pub type DbFuture<'a, T> = Pin<Box<dyn Future<Output = Result<T, DbError>> + Send + 'a>>;

#[derive(Debug, thiserror::Error)]
pub enum DbError {
    #[error("database kind {0:?} is not supported yet")]
    Unsupported(String),
    #[error("database I/O error: {0}")]
    Io(String),
    #[error("a record with _id {0} already exists in {1}")]
    DuplicateId(String, String),
    #[error("filter is empty, not allowed to delete all document at once")]
    EmptyDeleteFilter,
    #[error("model {0} does not exist")]
    UnknownModel(String),
    #[error("{0}")]
    Other(String),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SortOrder {
    Asc,
    Desc,
}

impl SortOrder {
    /// `"desc"` (any case) is descending; anything else ascending.
    pub fn parse(value: &str) -> Self {
        if value.eq_ignore_ascii_case("desc") {
            SortOrder::Desc
        } else {
            SortOrder::Asc
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Aggregate {
    Sum,
    Average,
    Max,
    Min,
}

/// One database operation's target.
#[derive(Clone, Debug)]
pub struct Query {
    pub model: Arc<DataModel>,
    pub filter: Filter,
    /// Fields in priority order.
    pub sort: Vec<(String, SortOrder)>,
    pub skip: u64,
    pub limit: Option<u64>,
}

impl Query {
    pub fn new(model: Arc<DataModel>, filter: Filter) -> Self {
        Self {
            model,
            filter,
            sort: Vec::new(),
            skip: 0,
            limit: None,
        }
    }

    pub fn collection(&self) -> &str {
        &self.model.collection
    }
}

/// Storage for records. Records are JSON objects keyed by a string `_id`;
/// dates are RFC 3339 strings and ids 24-hex-digit strings.
pub trait Backend: Send + Sync + 'static {
    /// Matching records, sorted, skipped and limited.
    fn find<'a>(&'a self, query: &'a Query) -> DbFuture<'a, Vec<DataMap>>;

    /// Number of matching records (ignores sort, skip and limit).
    fn count<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64>;

    /// Sum/average (numbers only, `0` when none), or max/min (`null` when none)
    /// of a field over the matching records.
    fn aggregate<'a>(
        &'a self,
        query: &'a Query,
        op: Aggregate,
        field: &'a str,
    ) -> DbFuture<'a, Value>;

    /// Stores new records, giving each an `_id` if it has none, and returns
    /// them as stored.
    fn insert<'a>(&'a self, query: &'a Query, records: Vec<DataMap>) -> DbFuture<'a, Vec<DataMap>>;

    /// Sets `fields` on the first matching record (in sort order), or on all
    /// of them when `many`, and returns the records as updated.
    fn update<'a>(
        &'a self,
        query: &'a Query,
        fields: DataMap,
        many: bool,
    ) -> DbFuture<'a, Vec<DataMap>>;

    /// Deletes the matching records, returning how many.
    fn delete<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64>;

    /// Creates the indexes the models need, returning how many exist
    /// afterwards. Backends without indexes do nothing.
    fn ensure_indexes<'a>(&'a self, _models: Vec<&'a DataModel>) -> DbFuture<'a, usize> {
        Box::pin(async { Ok(0) })
    }
}

/// `(field, unique)` for each single-field index a model's queries rely on,
/// sorted by field: every `unique` field (as a unique index), and fields
/// marked `index`, `tenantId` and foreign keys. `_id` is always indexed.
pub fn model_indexes(model: &DataModel) -> Vec<(String, bool)> {
    let mut indexes: Vec<(String, bool)> = model
        .fields
        .iter()
        .filter(|(name, field)| *name != "id" && *name != "_id" && !field.primary_key)
        .filter_map(|(name, field)| {
            if field.unique {
                Some((name.clone(), true))
            } else if field.index || name == TENANT_ID_KEY || field.foreign_key.is_some() {
                Some((name.clone(), false))
            } else {
                None
            }
        })
        .collect();

    indexes.sort();
    indexes
}

/// Stands in for a backend that isn't ported yet: every call fails.
pub struct UnsupportedBackend(pub String);

impl UnsupportedBackend {
    fn fail<'a, T: Send + 'a>(&self) -> DbFuture<'a, T> {
        let kind = self.0.clone();
        Box::pin(async move { Err(DbError::Unsupported(kind)) })
    }
}

impl Backend for UnsupportedBackend {
    fn find<'a>(&'a self, _: &'a Query) -> DbFuture<'a, Vec<DataMap>> {
        self.fail()
    }
    fn count<'a>(&'a self, _: &'a Query) -> DbFuture<'a, u64> {
        self.fail()
    }
    fn aggregate<'a>(&'a self, _: &'a Query, _: Aggregate, _: &'a str) -> DbFuture<'a, Value> {
        self.fail()
    }
    fn insert<'a>(&'a self, _: &'a Query, _: Vec<DataMap>) -> DbFuture<'a, Vec<DataMap>> {
        self.fail()
    }
    fn update<'a>(&'a self, _: &'a Query, _: DataMap, _: bool) -> DbFuture<'a, Vec<DataMap>> {
        self.fail()
    }
    fn delete<'a>(&'a self, _: &'a Query) -> DbFuture<'a, u64> {
        self.fail()
    }
}
