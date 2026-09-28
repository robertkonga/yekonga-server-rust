//! `database.kind = "local"`: records kept in memory and saved as one JSON
//! file per collection. Meant for development and tests.
//!
//! The Go server uses an embedded document database (tiedot) here; its data
//! files are not readable by this backend. Unlike that backend, this one
//! applies sorting, and count/sum/max/min/average respect the filter.

use std::collections::{BTreeMap, HashMap};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, RwLock};

use serde_json::Value;

use super::filter::{lookup, sort_cmp};
use super::values::{float_value, new_object_id};
use super::{Aggregate, Backend, DataMap, DbError, DbFuture, Query, SortOrder};

type Records = BTreeMap<String, DataMap>;

pub struct LocalBackend {
    /// `None` keeps everything in memory.
    dir: Option<PathBuf>,
    collections: Mutex<HashMap<String, Arc<RwLock<Records>>>>,
}

impl LocalBackend {
    /// Stores collections as `<dir>/<collection>.json`. Nothing is written
    /// until the first change.
    pub fn open(dir: impl Into<PathBuf>) -> Self {
        Self {
            dir: Some(dir.into()),
            collections: Mutex::default(),
        }
    }

    /// Keeps everything in memory (for tests).
    pub fn in_memory() -> Self {
        Self {
            dir: None,
            collections: Mutex::default(),
        }
    }

    pub fn directory(&self) -> Option<&Path> {
        self.dir.as_deref()
    }

    fn collection(&self, name: &str) -> Result<Arc<RwLock<Records>>, DbError> {
        let mut collections = self.collections.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(records) = collections.get(name) {
            return Ok(records.clone());
        }

        let records = Arc::new(RwLock::new(self.load(name)?));
        collections.insert(name.to_string(), records.clone());
        Ok(records)
    }

    fn file(&self, name: &str) -> Option<PathBuf> {
        self.dir
            .as_ref()
            .map(|dir| dir.join(format!("{name}.json")))
    }

    fn load(&self, name: &str) -> Result<Records, DbError> {
        let Some(path) = self.file(name) else {
            return Ok(Records::new());
        };

        let text = match std::fs::read_to_string(&path) {
            Ok(text) => text,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Records::new()),
            Err(e) => return Err(DbError::Io(format!("{}: {e}", path.display()))),
        };

        let docs: Vec<DataMap> = serde_json::from_str(&text)
            .map_err(|e| DbError::Io(format!("{}: {e}", path.display())))?;

        Ok(docs
            .into_iter()
            .filter_map(|doc| Some((doc.get("_id")?.as_str()?.to_string(), doc)))
            .collect())
    }

    /// Writes a collection atomically (temporary file, then rename).
    fn save(&self, name: &str, records: &Records) -> Result<(), DbError> {
        let (Some(dir), Some(path)) = (&self.dir, self.file(name)) else {
            return Ok(());
        };
        let io = |e: std::io::Error| DbError::Io(format!("{}: {e}", path.display()));

        std::fs::create_dir_all(dir).map_err(io)?;
        let docs: Vec<&DataMap> = records.values().collect();
        let text = serde_json::to_string(&docs).map_err(|e| DbError::Io(e.to_string()))?;

        let tmp = path.with_extension("json.tmp");
        std::fs::write(&tmp, text).map_err(io)?;
        std::fs::rename(&tmp, &path).map_err(io)
    }

    /// Ids of matching records, in the query's sort order (insertion order
    /// when unsorted), before skip/limit.
    fn matching_ids(records: &Records, query: &Query) -> Vec<String> {
        let mut matched: Vec<(&String, &DataMap)> = records
            .iter()
            .filter(|(_, doc)| query.filter.matches(doc))
            .collect();

        if !query.sort.is_empty() {
            matched.sort_by(|(_, a), (_, b)| {
                query
                    .sort
                    .iter()
                    .map(|(field, order)| {
                        let ordering = sort_cmp(lookup(a, field), lookup(b, field));
                        if *order == SortOrder::Desc {
                            ordering.reverse()
                        } else {
                            ordering
                        }
                    })
                    .find(|o| o.is_ne())
                    .unwrap_or(std::cmp::Ordering::Equal)
            });
        }

        matched.into_iter().map(|(id, _)| id.clone()).collect()
    }

    fn find_sync(&self, query: &Query) -> Result<Vec<DataMap>, DbError> {
        let collection = self.collection(query.collection())?;
        let records = collection.read().unwrap_or_else(|e| e.into_inner());
        let limit = query.limit.map_or(usize::MAX, |l| l as usize);

        Ok(Self::matching_ids(&records, query)
            .iter()
            .skip(query.skip as usize)
            .take(limit)
            .filter_map(|id| records.get(id).cloned())
            .collect())
    }
}

fn ready<'a, T: Send + 'a>(result: Result<T, DbError>) -> DbFuture<'a, T> {
    Box::pin(std::future::ready(result))
}

impl Backend for LocalBackend {
    fn find<'a>(&'a self, query: &'a Query) -> DbFuture<'a, Vec<DataMap>> {
        ready(self.find_sync(query))
    }

    fn count<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64> {
        ready((|| {
            let collection = self.collection(query.collection())?;
            let records = collection.read().unwrap_or_else(|e| e.into_inner());
            Ok(records
                .values()
                .filter(|doc| query.filter.matches(doc))
                .count() as u64)
        })())
    }

    fn aggregate<'a>(
        &'a self,
        query: &'a Query,
        op: Aggregate,
        field: &'a str,
    ) -> DbFuture<'a, Value> {
        ready((|| {
            let collection = self.collection(query.collection())?;
            let records = collection.read().unwrap_or_else(|e| e.into_inner());
            let values = records
                .values()
                .filter(|doc| query.filter.matches(doc))
                .filter_map(|doc| lookup(doc, field))
                .filter(|v| !v.is_null());

            Ok(match op {
                Aggregate::Sum | Aggregate::Average => {
                    let numbers: Vec<f64> = values.filter_map(Value::as_f64).collect();
                    let sum: f64 = numbers.iter().sum();
                    match op {
                        Aggregate::Sum => float_value(sum),
                        _ if numbers.is_empty() => float_value(0.0),
                        _ => float_value(sum / numbers.len() as f64),
                    }
                }
                Aggregate::Max => values
                    .max_by(|a, b| sort_cmp(Some(a), Some(b)))
                    .cloned()
                    .unwrap_or(Value::Null),
                Aggregate::Min => values
                    .min_by(|a, b| sort_cmp(Some(a), Some(b)))
                    .cloned()
                    .unwrap_or(Value::Null),
            })
        })())
    }

    fn insert<'a>(&'a self, query: &'a Query, docs: Vec<DataMap>) -> DbFuture<'a, Vec<DataMap>> {
        ready((|| {
            let collection = self.collection(query.collection())?;
            let mut records = collection.write().unwrap_or_else(|e| e.into_inner());
            let mut added = Vec::with_capacity(docs.len());

            for mut doc in docs {
                let id = match doc.get("_id").and_then(Value::as_str) {
                    Some(id) if !id.is_empty() => id.to_string(),
                    _ => new_object_id(),
                };
                if records.contains_key(&id)
                    || added.iter().any(|d: &DataMap| d["_id"] == id.as_str())
                {
                    return Err(DbError::DuplicateId(id, query.collection().to_string()));
                }

                doc.insert("_id".into(), Value::String(id));
                added.push(doc);
            }

            for doc in &added {
                records.insert(doc["_id"].as_str().unwrap().to_string(), doc.clone());
            }
            self.save(query.collection(), &records)?;

            Ok(added)
        })())
    }

    fn update<'a>(
        &'a self,
        query: &'a Query,
        fields: DataMap,
        many: bool,
    ) -> DbFuture<'a, Vec<DataMap>> {
        ready((|| {
            let collection = self.collection(query.collection())?;
            let mut records = collection.write().unwrap_or_else(|e| e.into_inner());
            let mut ids = Self::matching_ids(&records, query);
            if !many {
                ids.truncate(1);
            }

            let mut updated = Vec::with_capacity(ids.len());
            for id in &ids {
                if let Some(doc) = records.get_mut(id) {
                    for (key, value) in &fields {
                        if key != "_id" {
                            doc.insert(key.clone(), value.clone());
                        }
                    }
                    updated.push(doc.clone());
                }
            }

            if !updated.is_empty() {
                self.save(query.collection(), &records)?;
            }
            Ok(updated)
        })())
    }

    fn delete<'a>(&'a self, query: &'a Query) -> DbFuture<'a, u64> {
        ready((|| {
            if query.filter.is_all() {
                return Err(DbError::EmptyDeleteFilter);
            }

            let collection = self.collection(query.collection())?;
            let mut records = collection.write().unwrap_or_else(|e| e.into_inner());
            let before = records.len();
            records.retain(|_, doc| !query.filter.matches(doc));
            let deleted = (before - records.len()) as u64;

            if deleted > 0 {
                self.save(query.collection(), &records)?;
            }
            Ok(deleted)
        })())
    }
}
