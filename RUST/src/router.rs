//! Route patterns (port of `parseRoute` / `parseSegment` in `yekonga/main.go`).
//!
//! Patterns support named parameters anywhere in a segment and optional
//! parameters:
//!
//! * `/api/:model/:id` - `model` and `id` match `[a-zA-Z0-9_-]+`
//! * `/me/:moduleName?` - matches `/me` and `/me/admin`
//! * `/user-:name-:action/:id.svg` - several parameters in one segment

use std::collections::HashMap;
use std::sync::LazyLock;

use regex::Regex;

static PARAM_RE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r":([a-zA-Z0-9_]+)(\?)?").unwrap());

const CAPTURE: &str = "([a-zA-Z0-9_-]+)";

/// A compiled route pattern.
#[derive(Clone, Debug)]
pub struct RoutePattern {
    /// The regular expression the pattern compiles to.
    pub regex: Regex,
    pub param_names: Vec<String>,
}

impl RoutePattern {
    pub fn new(pattern: &str) -> Self {
        let (param_names, regex) = parse_route(pattern);

        Self {
            regex: Regex::new(&regex).expect("route patterns always compile"),
            param_names,
        }
    }

    /// Matches a request path, returning its parameters. Optional parameters
    /// that are absent map to an empty string.
    pub fn matches(&self, path: &str) -> Option<HashMap<String, String>> {
        let caps = self.regex.captures(path)?;

        if caps.len() - 1 != self.param_names.len() {
            return None;
        }

        Some(
            self.param_names
                .iter()
                .enumerate()
                .map(|(i, name)| {
                    (
                        name.clone(),
                        caps.get(i + 1).map_or("", |m| m.as_str()).to_string(),
                    )
                })
                .collect(),
        )
    }
}

/// Returns the parameter names and the anchored regex for a route pattern.
pub fn parse_route(pattern: &str) -> (Vec<String>, String) {
    let mut params = Vec::new();
    let mut out = String::from("^");

    for (i, part) in pattern.split('/').enumerate() {
        if !part.contains(':') {
            if i > 0 {
                out.push('/');
            }
            out.push_str(&regex::escape(part));
            continue;
        }

        let segment = parse_segment(part);
        params.extend(segment.params);

        if segment.fully_optional && i > 0 {
            // The leading slash is optional together with the segment.
            out.push_str("(?:/");
            out.push_str(&segment.pattern);
            out.push_str(")?");
        } else {
            if i > 0 {
                out.push('/');
            }
            out.push_str(&segment.pattern);
        }
    }

    out.push('$');
    (params, out)
}

struct ParsedSegment {
    params: Vec<String>,
    pattern: String,
    /// The whole segment is a single `:param?`.
    fully_optional: bool,
}

fn parse_segment(part: &str) -> ParsedSegment {
    let mut params = Vec::new();
    let mut pattern = String::new();
    let mut remaining = part;

    // A ':' not followed by a valid name is kept as a literal.
    while let Some(caps) = PARAM_RE.captures(remaining) {
        let whole = caps.get(0).unwrap();
        let leading = &remaining[..whole.start()];
        let optional = caps.get(2).is_some();

        params.push(caps[1].to_string());

        if optional {
            pattern.push_str("(?:");
            pattern.push_str(&regex::escape(leading));
            pattern.push_str(CAPTURE);
            pattern.push_str(")?");
        } else {
            pattern.push_str(&regex::escape(leading));
            pattern.push_str(CAPTURE);
        }

        remaining = &remaining[whole.end()..];
    }
    pattern.push_str(&regex::escape(remaining));

    let fully_optional = params.len() == 1
        && part
            .strip_prefix(':')
            .and_then(|rest| rest.strip_prefix(params[0].as_str()))
            .map(|rest| rest.strip_suffix('?').unwrap_or(rest).is_empty())
            .unwrap_or(false);

    ParsedSegment {
        params,
        pattern,
        fully_optional,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn compiles_like_go() {
        assert_eq!(parse_route("/graphql"), (vec![], "^/graphql$".to_string()));
        assert_eq!(parse_route("/"), (vec![], "^/$".to_string()));
        assert_eq!(
            parse_route("/me/:moduleName?"),
            (
                vec!["moduleName".to_string()],
                "^/me(?:/(?:([a-zA-Z0-9_-]+))?)?$".to_string()
            )
        );
        assert_eq!(
            parse_route("/user-:name-:action/:id.svg").1,
            r"^/user\-([a-zA-Z0-9_-]+)\-([a-zA-Z0-9_-]+)/([a-zA-Z0-9_-]+)\.svg$"
        );
    }

    #[test]
    fn matching() {
        type Case<'a> = (&'a str, &'a str, &'a [(&'a str, &'a str)]);
        let cases: &[Case] = &[
            ("/api/:model", "/api/orders", &[("model", "orders")]),
            (
                "/api/:model/:id",
                "/api/orders/abc123",
                &[("model", "orders"), ("id", "abc123")],
            ),
            ("/me/:moduleName?", "/me", &[("moduleName", "")]),
            ("/me/:moduleName?", "/me/admin", &[("moduleName", "admin")]),
            (
                "/image/:w/:h/:file",
                "/image/100/200/logo",
                &[("w", "100"), ("h", "200"), ("file", "logo")],
            ),
            (
                "/user-:name-:action/:id.svg",
                "/user-bob-edit/7.svg",
                &[("name", "bob"), ("action", "edit"), ("id", "7")],
            ),
        ];

        for (pattern, path, expected) in cases {
            let params = RoutePattern::new(pattern)
                .matches(path)
                .unwrap_or_else(|| panic!("{path} !~ {pattern}"));
            for (k, v) in *expected {
                assert_eq!(params[*k], *v, "{pattern} {path} {k}");
            }
        }

        let api = RoutePattern::new("/api/:model/:id");
        assert!(api.matches("/api/orders/abc/extra").is_none());
        assert!(api.matches("/api/orders/a.b").is_none());
        assert!(RoutePattern::new("/graphql").matches("/graphqlx").is_none());
    }
}
