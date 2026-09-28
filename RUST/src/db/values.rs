//! Value conversions used when writing and filtering records (ports of
//! `ObjectID`, `GetTimestamp`, `ToInt`, `ToFloat`, `ConvertCalculatedValue`
//! and `CreateFuzzyRegex` from the Go `helper` package).

use std::collections::hash_map::RandomState;
use std::hash::{BuildHasher, Hasher};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::LazyLock;

use chrono::{DateTime, NaiveDate, NaiveDateTime, NaiveTime, SecondsFormat, TimeZone, Utc};
use serde_json::Value;

/// The id Go's `ObjectID` gives a string that isn't valid hex.
pub const ZERO_OBJECT_ID: &str = "000000000000000000000000";

static PROCESS_RANDOM: LazyLock<[u8; 5]> = LazyLock::new(|| {
    let mut hasher = RandomState::new().build_hasher();
    hasher.write_u32(std::process::id());
    let bytes = hasher.finish().to_be_bytes();
    [bytes[0], bytes[1], bytes[2], bytes[3], bytes[4]]
});

static COUNTER: LazyLock<AtomicU32> = LazyLock::new(|| {
    let mut hasher = RandomState::new().build_hasher();
    hasher.write_u8(0);
    AtomicU32::new(hasher.finish() as u32)
});

/// A new MongoDB-style ObjectId: 4-byte timestamp, 5 random bytes fixed per
/// process and a 3-byte counter, as 24 hex digits. Ids made later sort later.
pub fn new_object_id() -> String {
    let seconds = Utc::now().timestamp() as u32;
    let count = COUNTER.fetch_add(1, Ordering::Relaxed) & 0x00ff_ffff;

    let mut bytes = [0u8; 12];
    bytes[..4].copy_from_slice(&seconds.to_be_bytes());
    bytes[4..9].copy_from_slice(&*PROCESS_RANDOM);
    bytes[9..].copy_from_slice(&count.to_be_bytes()[1..]);

    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

pub fn is_object_id(value: &str) -> bool {
    value.len() == 24 && value.bytes().all(|b| b.is_ascii_hexdigit())
}

/// Go's `helper.ObjectID` for a string: valid hex is kept (lowercased),
/// anything else becomes the all-zero id.
pub fn object_id_from_str(value: &str) -> String {
    if is_object_id(value) {
        value.to_ascii_lowercase()
    } else {
        ZERO_OBJECT_ID.to_string()
    }
}

/// Go's `helper.IsEmpty` for JSON values.
pub fn is_empty(value: &Value) -> bool {
    match value {
        Value::Null => true,
        Value::String(s) => s.is_empty(),
        Value::Array(a) => a.is_empty(),
        Value::Object(o) => o.is_empty(),
        Value::Bool(b) => !b,
        Value::Number(n) => n.as_f64() == Some(0.0),
    }
}

/// Stored form of a point in time: RFC 3339, UTC, milliseconds
/// (`2026-09-28T16:22:25.148Z`). Strings in this form sort by time.
pub fn format_datetime(at: DateTime<Utc>) -> String {
    at.to_rfc3339_opts(SecondsFormat::Millis, true)
}

pub fn now_string() -> String {
    format_datetime(Utc::now())
}

/// Parses the date formats Go's `StringToDatetime` accepts: `2006-01-02`,
/// `2006-01-02 15:04:05`, RFC 3339 and `15:04:05`.
pub fn parse_datetime(value: &str) -> Option<DateTime<Utc>> {
    let value = value.trim();

    if let Ok(at) = DateTime::parse_from_rfc3339(value) {
        return Some(at.with_timezone(&Utc));
    }
    if let Ok(date) = NaiveDate::parse_from_str(value, "%Y-%m-%d") {
        return Some(Utc.from_utc_datetime(&date.and_hms_opt(0, 0, 0)?));
    }
    if let Ok(at) = NaiveDateTime::parse_from_str(value, "%Y-%m-%d %H:%M:%S") {
        return Some(Utc.from_utc_datetime(&at));
    }
    if let Ok(time) = NaiveTime::parse_from_str(value, "%H:%M:%S") {
        return Some(Utc.from_utc_datetime(&NaiveDate::from_ymd_opt(0, 1, 1)?.and_time(time)));
    }

    None
}

/// Go's `helper.GetTimestamp`: the date a value holds, or *now* when it
/// doesn't hold one.
pub fn get_timestamp(value: &Value) -> DateTime<Utc> {
    value
        .as_str()
        .and_then(parse_datetime)
        .unwrap_or_else(Utc::now)
}

/// Integer value of a number field. JSON numbers are truncated toward zero;
/// strings must hold an integer. Anything else is 0.
///
/// The Go `helper.ToInt` parses JSON numbers in base 32 (12 becomes 34);
/// this deliberately doesn't.
pub fn to_int(value: &Value) -> i64 {
    match value {
        Value::Number(n) => n
            .as_i64()
            .or_else(|| n.as_f64().map(|f| f.trunc() as i64))
            .unwrap_or(0),
        Value::String(s) => s.trim().parse().unwrap_or(0),
        _ => 0,
    }
}

/// Float value of a float field: numbers, or strings holding one; else 0.
pub fn to_float(value: &Value) -> f64 {
    match value {
        Value::Number(n) => n.as_f64().unwrap_or(0.0),
        Value::String(s) => s.trim().parse().unwrap_or(0.0),
        _ => 0.0,
    }
}

pub fn float_value(f: f64) -> Value {
    serde_json::Number::from_f64(f)
        .map(Value::Number)
        .unwrap_or(Value::from(0))
}

/// Whether Go's `helper.IsNumeric` accepts the value.
pub fn is_numeric(value: &Value) -> bool {
    match value {
        Value::Number(_) => true,
        Value::String(s) => s.trim().parse::<f64>().is_ok(),
        _ => false,
    }
}

/// `(?i)` + the words of `input`, escaped and joined by `.*`: "R F Konga"
/// matches "Robert Fred Konga".
pub fn fuzzy_regex(input: &str) -> String {
    let parts: Vec<String> = input.split_whitespace().map(regex::escape).collect();
    format!("(?i){}", parts.join(".*"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn object_ids() {
        let a = new_object_id();
        let b = new_object_id();
        assert!(is_object_id(&a));
        assert_ne!(a, b);
        assert!(a[..8] <= b[..8]);

        assert_eq!(
            object_id_from_str("5F1A2B3C4D5E6F7A8B9C0D1E"),
            "5f1a2b3c4d5e6f7a8b9c0d1e"
        );
        assert_eq!(object_id_from_str("bad"), ZERO_OBJECT_ID);
    }

    #[test]
    fn numbers() {
        assert_eq!(to_int(&json!(12)), 12);
        assert_eq!(to_int(&json!(100)), 100);
        assert_eq!(to_int(&json!(12.9)), 12);
        assert_eq!(to_int(&json!("12")), 12);
        assert_eq!(to_int(&json!("12.5")), 0);
        assert_eq!(to_float(&json!("2.5")), 2.5);
        assert_eq!(to_float(&json!(true)), 0.0);
    }

    #[test]
    fn dates() {
        let at = parse_datetime("2026-09-28").unwrap();
        assert_eq!(format_datetime(at), "2026-09-28T00:00:00.000Z");
        assert_eq!(
            format_datetime(parse_datetime("2026-09-28 10:11:12").unwrap()),
            "2026-09-28T10:11:12.000Z"
        );
        assert_eq!(
            format_datetime(parse_datetime("2026-09-28T13:00:00+03:00").unwrap()),
            "2026-09-28T10:00:00.000Z"
        );
        assert!(parse_datetime("garbage").is_none());
        assert!(
            (Utc::now() - get_timestamp(&json!("garbage"))).num_seconds() < 5,
            "Go falls back to now"
        );
    }

    #[test]
    fn regex_and_emptiness() {
        assert_eq!(fuzzy_regex("R F. Konga"), r"(?i)R.*F\..*Konga");
        assert!(is_empty(&json!(0)) && is_empty(&json!("")) && is_empty(&json!(false)));
        assert!(!is_empty(&json!("x")));
    }
}
