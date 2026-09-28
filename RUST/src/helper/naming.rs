//! Name conversions used to derive model, collection and relation names from
//! `database.json`. These mirror `helper/helpers.go` exactly, including its
//! quirks (e.g. acronyms are not split: `HTTPServer` -> `httpserver`), because
//! the derived names end up in database collection names and GraphQL fields.

use std::sync::LazyLock;

use regex::Regex;

static RE_SLUG_INVALID: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"[^a-z0-9]+").unwrap());
static RE_SEPARATOR: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"[\s-]+").unwrap());
static RE_MULTIPLE_UNDERSCORES: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"_+").unwrap());

static PLURAL_RULES: LazyLock<Vec<(Regex, &'static str)>> = LazyLock::new(|| {
    vec![
        (Regex::new(r"([^aeiou])y$").unwrap(), "${1}ies"), // city -> cities
        (Regex::new(r"(f|fe)$").unwrap(), "ves"),          // knife -> knives
        (Regex::new(r"(s|sh|ch|x|z)$").unwrap(), "${1}es"), // box -> boxes
        (Regex::new(r"$").unwrap(), "s"),                  // default: add "s"
    ]
});

static SINGULAR_RULES: LazyLock<Vec<(Regex, &'static str)>> = LazyLock::new(|| {
    vec![
        (Regex::new(r"ies$").unwrap(), "y"),      // cities -> city
        (Regex::new(r"ves$").unwrap(), "f"),      // knives -> knife
        (Regex::new(r"eases$").unwrap(), "ease"), // releases -> release
        (Regex::new(r"(s|sh|ch|x|z)es$").unwrap(), "${1}"), // boxes -> box
        (Regex::new(r"s$").unwrap(), ""),         // default: remove "s"
    ]
});

/// camelCase / PascalCase to snake_case. Acronyms stay together, and digits
/// are split from letters on both sides.
pub fn camel_to_snake(s: &str) -> String {
    let mut result = String::with_capacity(s.len() + 4);
    let (mut prev_lower, mut prev_upper, mut prev_digit) = (false, false, false);

    for r in s.chars() {
        let is_lower = r.is_lowercase();
        let is_upper = r.is_uppercase();
        let is_digit = r.is_ascii_digit();

        if (prev_lower && is_upper)
            || (prev_digit && (is_lower || is_upper))
            || (is_digit && (prev_lower || prev_upper))
        {
            result.push('_');
        }
        result.extend(r.to_lowercase());

        prev_lower = is_lower;
        prev_upper = is_upper;
        prev_digit = is_digit;
    }

    result
}

/// Converts camelCase, PascalCase, kebab-case and spaced text to snake_case.
pub fn to_underscore(text: &str) -> String {
    if text.is_empty() {
        return String::new();
    }

    let t = camel_to_snake(text).to_lowercase();
    let t = RE_SEPARATOR.replace_all(&t, "_");
    let t = RE_MULTIPLE_UNDERSCORES.replace_all(&t, "_");

    t.trim_matches('_').to_string()
}

/// Converts a string to PascalCase (Go's `ToCamelCase`).
pub fn to_camel_case(s: &str) -> String {
    to_underscore(s)
        .split(|c: char| !c.is_alphabetic() && !c.is_numeric())
        .filter(|w| !w.is_empty())
        .map(|w| upper_first(&w.to_lowercase()))
        .collect()
}

/// PascalCase with a lowercase first character: `user_profiles` -> `userProfiles`.
pub fn to_variable(s: &str) -> String {
    let s = to_camel_case(s);
    let mut chars = s.chars();

    match chars.next() {
        Some(first) => first.to_lowercase().chain(chars).collect(),
        None => String::new(),
    }
}

/// URL-friendly slug: `My App` -> `my-app`.
pub fn to_slug(s: &str) -> String {
    let s = to_underscore(s);
    RE_SLUG_INVALID
        .replace_all(&s, "-")
        .trim_matches('-')
        .to_string()
}

/// Human title: `firstName` -> `First Name`.
pub fn to_title(s: &str) -> String {
    if s.is_empty() {
        return String::new();
    }

    let s = to_underscore(s);
    let s = RE_MULTIPLE_UNDERSCORES.replace_all(&s, " ");

    s.trim()
        .split(' ')
        .filter(|w| !w.is_empty())
        .map(upper_first)
        .collect::<Vec<_>>()
        .join(" ")
}

/// Converts a singular noun to its plural form.
pub fn pluralize(word: &str) -> String {
    let word = singularize(word);

    for (pattern, replace) in PLURAL_RULES.iter() {
        if pattern.is_match(&word) {
            return pattern.replace_all(&word, *replace).into_owned();
        }
    }

    word
}

/// Converts a plural noun to its singular form.
pub fn singularize(word: &str) -> String {
    for (pattern, replace) in SINGULAR_RULES.iter() {
        if pattern.is_match(word) {
            return pattern.replace_all(word, *replace).into_owned();
        }
    }

    word.to_string()
}

/// Name of the field a child collection is exposed under on its parent,
/// e.g. `Order.userId -> User` gives `orders` on `User`.
pub fn get_child_relative_name(
    parent: &str,
    collection: &str,
    _primary_key: &str,
    foreign_key: &str,
) -> String {
    let test_key = to_underscore(foreign_key);

    let class_variable = match test_key.strip_suffix("_id") {
        Some(stem) => {
            let new_variable = to_variable(stem);

            if to_variable(parent) != to_variable(&new_variable) {
                to_variable(&format!("{new_variable}_{collection}"))
            } else {
                to_variable(collection)
            }
        }
        None => to_variable(&format!("{test_key}_{collection}")),
    };

    pluralize(&class_variable)
}

/// Name of the field a parent record is exposed under on its child,
/// e.g. `userId` gives `user`.
pub fn get_parent_relative_name(
    _collection: &str,
    _primary_key: &str,
    foreign_key: &str,
) -> String {
    let test_key = to_underscore(foreign_key);

    let class_variable = match test_key.strip_suffix("_id") {
        Some(stem) => to_variable(stem),
        None => to_variable(foreign_key),
    };

    if class_variable == foreign_key || class_variable == singularize(foreign_key) {
        return to_variable(&format!("{}_info", to_underscore(foreign_key)));
    }

    class_variable
}

fn upper_first(word: &str) -> String {
    let mut chars = word.chars();

    match chars.next() {
        Some(first) => first.to_uppercase().chain(chars).collect(),
        None => String::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn underscore() {
        assert_eq!(to_underscore("firstName"), "first_name");
        assert_eq!(to_underscore("UserProfiles"), "user_profiles");
        assert_eq!(to_underscore("hello-world thing"), "hello_world_thing");
        assert_eq!(to_underscore("HTTPServer"), "httpserver");
        assert_eq!(to_underscore("address2Line"), "address_2_line");
        assert_eq!(to_underscore("__a__b__"), "a_b");
    }

    #[test]
    fn camel_and_variable() {
        assert_eq!(to_camel_case("user_profiles"), "UserProfiles");
        assert_eq!(to_camel_case("tenantConfigs"), "TenantConfigs");
        assert_eq!(to_variable("TenantConfigs"), "tenantConfigs");
        assert_eq!(to_variable("user_id"), "userId");
        assert_eq!(to_variable(""), "");
    }

    #[test]
    fn slug_and_title() {
        assert_eq!(to_slug("My Yekonga App"), "my-yekonga-app");
        assert_eq!(to_title("firstName"), "First Name");
        assert_eq!(to_title("per_user"), "Per User");
        assert_eq!(to_title("NGO"), "Ngo");
    }

    #[test]
    fn plural_singular() {
        assert_eq!(pluralize("User"), "Users");
        assert_eq!(pluralize("Users"), "Users");
        assert_eq!(pluralize("city"), "cities");
        assert_eq!(pluralize("box"), "boxes");
        assert_eq!(pluralize("knife"), "knives");
        assert_eq!(singularize("TenantCatches"), "TenantCatch");
        assert_eq!(singularize("releases"), "release");
        assert_eq!(singularize("Categories"), "Category");
        // Faithful to the Go rules, including their limits.
        assert_eq!(singularize("status"), "statu");
    }

    #[test]
    fn relation_names() {
        assert_eq!(get_parent_relative_name("User", "_id", "userId"), "user");
        assert_eq!(
            get_parent_relative_name("User", "_id", "owner"),
            "ownerInfo"
        );
        assert_eq!(
            get_child_relative_name("User", "Order", "_id", "userId"),
            "orders"
        );
        assert_eq!(
            get_child_relative_name("User", "Order", "_id", "createdById"),
            "createdByOrders"
        );
        assert_eq!(
            get_child_relative_name("User", "Order", "_id", "owner"),
            "ownerOrders"
        );
    }
}
