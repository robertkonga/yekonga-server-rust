//! Backend-neutral query filters.
//!
//! The query builder compiles a `where` map into a [`Filter`]. Its meaning is
//! MongoDB's (the Go server's main backend): `Eq` on an array field matches
//! when any element is equal, `Eq(null)` also matches a missing field,
//! comparisons only match values of the same type, and so on. The local
//! backend evaluates filters with [`Filter::matches`]; other backends
//! translate them into their own query language.

use std::cmp::Ordering;

use chrono::{DateTime, Utc};
use regex::Regex;
use serde_json::{Map, Value};

use super::values::parse_datetime;

/// A value in a condition, typed where the backend needs to know.
#[derive(Clone, Debug, PartialEq)]
pub enum Operand {
    Value(Value),
    /// A point in time (a date field, or a date the filter computed).
    Date(DateTime<Utc>),
    /// A record id (24 hex digits).
    ObjectId(String),
}

impl Operand {
    pub fn null() -> Self {
        Operand::Value(Value::Null)
    }

    pub fn is_null(&self) -> bool {
        matches!(self, Operand::Value(Value::Null))
    }
}

/// A condition on one field.
#[derive(Clone, Debug)]
pub enum Cond {
    Eq(Operand),
    Ne(Operand),
    Lt(Operand),
    Lte(Operand),
    Gt(Operand),
    Gte(Operand),
    In(Vec<Operand>),
    Nin(Vec<Operand>),
    /// Every operand is in the field (an array).
    All(Vec<Operand>),
    /// `true`: present and not null. `false`: missing or null.
    Exists(bool),
    /// Case-insensitive regular expression on string values.
    Regex(Regex),
    Not(Box<Cond>),
}

#[derive(Clone, Debug)]
pub enum Filter {
    /// Matches every record.
    All,
    And(Vec<Filter>),
    Or(Vec<Filter>),
    Nor(Vec<Filter>),
    Field(String, Cond),
}

impl Filter {
    /// `And` of `filters`, simplified when there are none or one.
    pub fn and(mut filters: Vec<Filter>) -> Filter {
        filters.retain(|f| !matches!(f, Filter::All));
        match filters.len() {
            0 => Filter::All,
            1 => filters.pop().unwrap(),
            _ => Filter::And(filters),
        }
    }

    pub fn is_all(&self) -> bool {
        matches!(self, Filter::All)
    }

    /// Whether a stored record matches.
    pub fn matches(&self, doc: &Map<String, Value>) -> bool {
        match self {
            Filter::All => true,
            Filter::And(filters) => filters.iter().all(|f| f.matches(doc)),
            Filter::Or(filters) => filters.iter().any(|f| f.matches(doc)),
            Filter::Nor(filters) => !filters.iter().any(|f| f.matches(doc)),
            Filter::Field(name, cond) => cond.matches(lookup(doc, name)),
        }
    }
}

impl Cond {
    pub fn matches(&self, field: Option<&Value>) -> bool {
        match self {
            Cond::Eq(op) => eq(field, op),
            Cond::Ne(op) => !eq(field, op),
            Cond::Lt(op) => compare(field, op, |o| o == Ordering::Less),
            Cond::Lte(op) => compare(field, op, |o| o != Ordering::Greater),
            Cond::Gt(op) => compare(field, op, |o| o == Ordering::Greater),
            Cond::Gte(op) => compare(field, op, |o| o != Ordering::Less),
            Cond::In(ops) => ops.iter().any(|op| eq(field, op)),
            Cond::Nin(ops) => !ops.iter().any(|op| eq(field, op)),
            Cond::All(ops) => !ops.is_empty() && ops.iter().all(|op| eq(field, op)),
            Cond::Exists(true) => !matches!(field, None | Some(Value::Null)),
            Cond::Exists(false) => matches!(field, None | Some(Value::Null)),
            Cond::Regex(re) => {
                candidates(field).any(|v| v.as_str().is_some_and(|s| re.is_match(s)))
            }
            Cond::Not(cond) => !cond.matches(field),
        }
    }
}

/// A field by name; `a.b` reaches into nested objects.
pub fn lookup<'a>(doc: &'a Map<String, Value>, name: &str) -> Option<&'a Value> {
    let mut parts = name.split('.');
    let mut value = doc.get(parts.next()?)?;

    for part in parts {
        value = value.as_object()?.get(part)?;
    }

    Some(value)
}

/// The value itself, and its elements when it's an array.
fn candidates(field: Option<&Value>) -> impl Iterator<Item = &Value> {
    let (single, many) = match field {
        Some(Value::Array(items)) => (field, items.as_slice()),
        other => (other, &[][..]),
    };

    single.into_iter().chain(many.iter())
}

fn eq(field: Option<&Value>, op: &Operand) -> bool {
    if op.is_null() {
        return match field {
            None | Some(Value::Null) => true,
            Some(Value::Array(items)) => items.iter().any(Value::is_null),
            _ => false,
        };
    }

    candidates(field).any(|v| operand_eq(v, op))
}

fn operand_eq(value: &Value, op: &Operand) -> bool {
    match op {
        Operand::Value(expected) => json_eq(value, expected),
        Operand::Date(date) => value
            .as_str()
            .and_then(parse_datetime)
            .is_some_and(|d| d == *date),
        Operand::ObjectId(id) => value.as_str().is_some_and(|s| s.eq_ignore_ascii_case(id)),
    }
}

/// JSON equality where `1` equals `1.0`.
pub fn json_eq(a: &Value, b: &Value) -> bool {
    match (a, b) {
        (Value::Number(x), Value::Number(y)) => x.as_f64() == y.as_f64(),
        (Value::Array(x), Value::Array(y)) => {
            x.len() == y.len() && x.iter().zip(y).all(|(a, b)| json_eq(a, b))
        }
        (Value::Object(x), Value::Object(y)) => {
            x.len() == y.len()
                && x.iter()
                    .all(|(k, v)| y.get(k).is_some_and(|w| json_eq(v, w)))
        }
        _ => a == b,
    }
}

fn compare(field: Option<&Value>, op: &Operand, accept: impl Fn(Ordering) -> bool) -> bool {
    candidates(field).any(|v| compare_same_type(v, op).is_some_and(&accept))
}

/// Orders a stored value against an operand of the same type; `None` when
/// the types differ (MongoDB's type bracketing).
fn compare_same_type(value: &Value, op: &Operand) -> Option<Ordering> {
    match (value, op) {
        (Value::Number(a), Operand::Value(Value::Number(b))) => {
            a.as_f64()?.partial_cmp(&b.as_f64()?)
        }
        (Value::String(a), Operand::Value(Value::String(b))) => Some(a.as_str().cmp(b.as_str())),
        (Value::Bool(a), Operand::Value(Value::Bool(b))) => Some(a.cmp(b)),
        (Value::String(a), Operand::Date(b)) => Some(parse_datetime(a)?.cmp(b)),
        (Value::String(a), Operand::ObjectId(b)) => Some(a.to_lowercase().cmp(&b.to_lowercase())),
        _ => None,
    }
}

/// Sort order across types, as MongoDB orders them: missing/null, numbers,
/// strings, objects, arrays, booleans.
pub fn sort_cmp(a: Option<&Value>, b: Option<&Value>) -> Ordering {
    fn rank(v: Option<&Value>) -> u8 {
        match v {
            None | Some(Value::Null) => 0,
            Some(Value::Number(_)) => 1,
            Some(Value::String(_)) => 2,
            Some(Value::Object(_)) => 3,
            Some(Value::Array(_)) => 4,
            Some(Value::Bool(_)) => 5,
        }
    }

    match (a, b) {
        (Some(Value::Number(x)), Some(Value::Number(y))) => x
            .as_f64()
            .partial_cmp(&y.as_f64())
            .unwrap_or(Ordering::Equal),
        (Some(Value::String(x)), Some(Value::String(y))) => x.cmp(y),
        (Some(Value::Bool(x)), Some(Value::Bool(y))) => x.cmp(y),
        (Some(Value::Array(x)), Some(Value::Array(y))) => x
            .iter()
            .zip(y)
            .map(|(a, b)| sort_cmp(Some(a), Some(b)))
            .find(|o| *o != Ordering::Equal)
            .unwrap_or_else(|| x.len().cmp(&y.len())),
        (Some(Value::Object(x)), Some(Value::Object(y))) => Value::Object(x.clone())
            .to_string()
            .cmp(&Value::Object(y.clone()).to_string()),
        _ => rank(a).cmp(&rank(b)),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn doc(v: Value) -> Map<String, Value> {
        v.as_object().unwrap().clone()
    }

    fn val(v: Value) -> Operand {
        Operand::Value(v)
    }

    #[test]
    fn equality_follows_mongodb() {
        let d = doc(json!({"n": 1, "tags": ["a", "b"], "gone": null, "nested": {"x": 5}}));

        assert!(Cond::Eq(val(json!(1.0))).matches(lookup(&d, "n")));
        assert!(
            Cond::Eq(val(json!("a"))).matches(lookup(&d, "tags")),
            "array contains"
        );
        assert!(
            Cond::Eq(val(json!(["a", "b"]))).matches(lookup(&d, "tags")),
            "whole array"
        );
        assert!(Cond::Eq(Operand::null()).matches(lookup(&d, "missing")));
        assert!(Cond::Eq(Operand::null()).matches(lookup(&d, "gone")));
        assert!(!Cond::Eq(Operand::null()).matches(lookup(&d, "n")));
        assert!(Cond::Ne(val(json!("c"))).matches(lookup(&d, "tags")));
        assert!(Filter::Field("nested.x".into(), Cond::Eq(val(json!(5)))).matches(&d));
    }

    #[test]
    fn comparisons_are_type_bracketed() {
        let d = doc(json!({"n": 5, "s": "m", "at": "2026-01-02T00:00:00.000Z"}));

        assert!(Cond::Gt(val(json!(4))).matches(lookup(&d, "n")));
        assert!(
            !Cond::Gt(val(json!("4"))).matches(lookup(&d, "n")),
            "string vs number never matches"
        );
        assert!(Cond::Lt(val(json!("z"))).matches(lookup(&d, "s")));
        let jan1 = parse_datetime("2026-01-01").unwrap();
        assert!(Cond::Gt(Operand::Date(jan1)).matches(lookup(&d, "at")));
        assert!(!Cond::Gt(Operand::Date(jan1)).matches(lookup(&d, "s")));
        assert!(Cond::Not(Box::new(Cond::Lt(val(json!(1))))).matches(lookup(&d, "missing")));
    }

    #[test]
    fn sets_existence_and_regex() {
        let d = doc(json!({"tags": ["a", "b"], "name": "Asha Konga", "gone": null}));

        assert!(Cond::In(vec![val(json!("x")), val(json!("b"))]).matches(lookup(&d, "tags")));
        assert!(Cond::Nin(vec![val(json!("x"))]).matches(lookup(&d, "tags")));
        assert!(Cond::All(vec![val(json!("a")), val(json!("b"))]).matches(lookup(&d, "tags")));
        assert!(!Cond::All(vec![]).matches(lookup(&d, "tags")));
        assert!(Cond::Exists(true).matches(lookup(&d, "name")));
        assert!(!Cond::Exists(true).matches(lookup(&d, "gone")));
        assert!(Cond::Exists(false).matches(lookup(&d, "gone")));
        assert!(Cond::Regex(Regex::new("(?i)asha.*konga").unwrap()).matches(lookup(&d, "name")));
    }

    #[test]
    fn logic() {
        let d = doc(json!({"a": 1, "b": 2}));
        let a1 = Filter::Field("a".into(), Cond::Eq(val(json!(1))));
        let b3 = Filter::Field("b".into(), Cond::Eq(val(json!(3))));

        assert!(Filter::Or(vec![a1.clone(), b3.clone()]).matches(&d));
        assert!(!Filter::And(vec![a1.clone(), b3.clone()]).matches(&d));
        assert!(!Filter::Nor(vec![a1, b3]).matches(&d));
        assert!(Filter::and(vec![]).is_all());
    }

    #[test]
    fn sorting() {
        let mut values = [
            json!("b"),
            json!(2),
            Value::Null,
            json!(true),
            json!("a"),
            json!(1.5),
        ];
        values.sort_by(|a, b| sort_cmp(Some(a), Some(b)));
        assert_eq!(
            values.to_vec(),
            vec![
                Value::Null,
                json!(1.5),
                json!(2),
                json!("a"),
                json!("b"),
                json!(true)
            ]
        );
    }
}
