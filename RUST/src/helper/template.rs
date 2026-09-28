//! Text templating (port of `helper.TextTemplate` in `helper/template.go`):
//! replaces `{{ path }}` placeholders with values looked up by dotted path.

use std::sync::LazyLock;

use regex::Regex;
use serde_json::Value;

static PLACEHOLDER: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"\{\{\s*([a-zA-Z0-9_]+(?:\.[a-zA-Z0-9_]+)*)\s*\}\}").unwrap());

/// Replaces every `{{ path }}` in `template` with the value at that dotted
/// path in `data`; a missing value becomes an empty string.
pub fn text_template(template: &str, data: &Value) -> String {
    PLACEHOLDER
        .replace_all(template, |caps: &regex::Captures| {
            match nested_value(data, &caps[1]) {
                Some(Value::String(s)) => s.clone(),
                Some(Value::Null) | None => String::new(),
                Some(other) => other.to_string(),
            }
        })
        .into_owned()
}

/// The value at a dotted path (`user.name`, `items.0.price`).
fn nested_value<'a>(data: &'a Value, path: &str) -> Option<&'a Value> {
    let mut current = data;
    for key in path.split('.') {
        current = match current {
            Value::Object(map) => map.get(key)?,
            Value::Array(items) => items.get(key.parse::<usize>().ok()?)?,
            _ => return None,
        };
    }
    Some(current)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn fills_placeholders() {
        let data = json!({"otpCode": "1234", "user": {"name": "Asha"}, "items": [{"price": 5}]});
        assert_eq!(
            text_template("{{ otpCode }} for {{user.name}}", &data),
            "1234 for Asha"
        );
        assert_eq!(text_template("p={{items.0.price}}", &data), "p=5");
        assert_eq!(text_template("{{ missing }}x", &data), "x");
    }
}
