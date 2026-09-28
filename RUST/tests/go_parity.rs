//! Checks the ported helpers against outputs recorded from the Go
//! implementation (`tests/fixtures/go_helper_vectors.json`, produced by
//! calling the `helper` package functions on the same inputs).

use serde_json::Value;
use yekonga::helper::*;

fn vectors() -> serde_json::Map<String, Value> {
    serde_json::from_str(include_str!("fixtures/go_helper_vectors.json")).unwrap()
}

#[test]
fn naming_matches_go() {
    let mut mismatches = Vec::new();

    for (word, expected) in vectors().iter().filter(|(k, _)| !k.starts_with("__")) {
        let actual = [
            ("underscore", to_underscore(word)),
            ("camel", to_camel_case(word)),
            ("variable", to_variable(word)),
            ("slug", to_slug(word)),
            ("title", to_title(word)),
            ("plural", pluralize(word)),
            ("singular", singularize(word)),
            ("parentRel", get_parent_relative_name("X", "_id", word)),
            (
                "childRel",
                get_child_relative_name("User", "Order", "_id", word),
            ),
        ];

        for (name, actual) in actual {
            if expected[name] != actual.as_str() {
                mismatches.push(format!(
                    "{name}({word:?}): go={} rust={actual:?}",
                    expected[name]
                ));
            }
        }
    }

    assert!(mismatches.is_empty(), "{}", mismatches.join("\n"));
}

#[test]
fn path_match_matches_go() {
    let paths = [
        ("/me/admin", "/me/*"),
        ("/me/admin/x", "/me/*"),
        ("/me/", "/me/*"),
        ("/me", "/me/*"),
        ("/a/b.css", "/a/*.css"),
        ("/x/c", "/x/[a-c]"),
        ("/x/d", "/x/[^a-c]"),
        ("/x", "/[x"),
        ("*", "\\*"),
        ("/ab", "/a*b*"),
        ("/abc/d", "/*/?"),
        ("/a]", "/[]a]"),
        ("/-", "/[a-]"),
        ("abc", "a*c"),
        ("a/c", "a*c"),
    ];
    let expected = vectors()["__paths"].clone();

    for (i, (route, pattern)) in paths.iter().enumerate() {
        assert_eq!(
            Value::Bool(match_path(route, pattern)),
            expected[i],
            "match_path({route:?}, {pattern:?})"
        );
    }
}

#[test]
fn domains_match_go() {
    for (input, expected) in vectors()["__domains"].as_object().unwrap() {
        assert_eq!(
            extract_domain(input),
            expected["extract"].as_str().unwrap(),
            "extract_domain({input:?})"
        );
        assert_eq!(
            get_main_domain(input).as_deref(),
            expected["main"].as_str(),
            "get_main_domain({input:?})"
        );
    }
}

#[test]
fn contacts_match_go() {
    for case in vectors()["__contacts"].as_array().unwrap() {
        let input = case["input"].as_str().unwrap();
        assert_eq!(
            format_phone(input),
            case["formatPhone"].as_str().unwrap(),
            "format_phone({input:?})"
        );
        assert_eq!(is_phone(input), case["isPhone"], "is_phone({input:?})");
        assert_eq!(is_email(input), case["isEmail"], "is_email({input:?})");
    }
}

#[test]
fn models_match_go() {
    use yekonga::model::build_system_models;
    use yekonga::{DatabaseStructure, YekongaConfig};

    let config = YekongaConfig::from_json(
        r#"{"isAuthorizationServer": true, "hasTenant": true, "hasTenantBilling": true,
            "hasTenantCatch": true, "database": {"kind": "local"}}"#,
    )
    .unwrap();
    let app_structure = DatabaseStructure::from_value(
        &serde_json::from_str(include_str!("fixtures/database.json")).unwrap(),
    );
    let structure = DatabaseStructure::build(&app_structure, &config);
    let models = build_system_models(&config, &structure);

    let expected: serde_json::Map<String, Value> =
        serde_json::from_str(include_str!("fixtures/go_models.json")).unwrap();

    let mut names: Vec<_> = models.keys().cloned().collect();
    let mut expected_names: Vec<_> = expected.keys().cloned().collect();
    names.sort();
    expected_names.sort();
    assert_eq!(names, expected_names);

    let mut mismatches = Vec::new();
    let mut check = |what: String, go: &Value, rust: Value| {
        if *go != rust {
            mismatches.push(format!("{what}: go={go} rust={rust}"));
        }
    };

    for (name, model) in &models {
        let go = &expected[name];
        let rel = |r: &std::collections::BTreeMap<String, yekonga::model::ForeignKey>| {
            Value::Object(
                r.iter()
                    .map(|(k, v)| {
                        (
                            k.clone(),
                            format!("{}|{}|{}", v.model_name, v.primary_key, v.foreign_key).into(),
                        )
                    })
                    .collect(),
            )
        };
        let sorted = |v: &[String]| {
            let mut v = v.to_vec();
            v.sort();
            serde_json::json!(v)
        };

        check(
            format!("{name}.class"),
            &go["class"],
            model.class.clone().into(),
        );
        check(
            format!("{name}.collection"),
            &go["collection"],
            model.collection.clone().into(),
        );
        check(
            format!("{name}.variable"),
            &go["variable"],
            model.variable.clone().into(),
        );
        check(
            format!("{name}.single"),
            &go["single"],
            model.variable_single.clone().into(),
        );
        check(
            format!("{name}.plural"),
            &go["plural"],
            model.variable_plural.clone().into(),
        );
        check(
            format!("{name}.hasTenant"),
            &go["hasTenant"],
            model.has_tenant.into(),
        );
        check(
            format!("{name}.validFields"),
            &go["validFields"],
            serde_json::json!(model.valid_fields),
        );
        check(
            format!("{name}.required"),
            &go["required"],
            sorted(&model.required),
        );
        check(
            format!("{name}.dateFields"),
            &go["dateFields"],
            sorted(&model.date_fields),
        );
        check(
            format!("{name}.parents"),
            &go["parents"],
            rel(&model.parent_fields),
        );
        check(
            format!("{name}.children"),
            &go["children"],
            rel(&model.children_fields),
        );

        for (field_name, field) in &model.fields {
            let gf = &go["fields"][field_name];
            let fk = match &field.foreign_key {
                Some(fk) => {
                    serde_json::json!({"model": fk.model_name, "primary": fk.primary_key, "foreign": fk.foreign_key})
                }
                None => serde_json::json!({}),
            };
            let actual = serde_json::json!({
                "kind": field.kind.as_str(), "isArray": field.is_array, "required": field.required,
                "protected": field.protected, "default": field.default_value, "fk": fk, "options": field.options.len(),
            });
            check(format!("{name}.fields.{field_name}"), gf, actual);
        }
    }

    assert!(
        mismatches.is_empty(),
        "{} mismatches:\n{}",
        mismatches.len(),
        mismatches.join("\n")
    );
}

/// The auto-generated GraphQL schema, flattened to sorted lines (type,
/// field with argument types, input field, enum value), as the fixtures
/// were recorded from the Go server's introspection output.
fn normalized_schema(introspection: &Value) -> String {
    fn type_name(t: &Value) -> String {
        match t["kind"].as_str() {
            Some("NON_NULL") => format!("{}!", type_name(&t["ofType"])),
            Some("LIST") => format!("[{}]", type_name(&t["ofType"])),
            _ => t["name"].as_str().unwrap_or("?").to_string(),
        }
    }

    let mut lines = Vec::new();
    for ty in introspection["data"]["__schema"]["types"]
        .as_array()
        .unwrap()
    {
        let name = ty["name"].as_str().unwrap();
        if name.starts_with("__") {
            continue;
        }
        lines.push(format!("{} {name}", ty["kind"].as_str().unwrap()));

        for field in ty["fields"].as_array().into_iter().flatten() {
            let mut args: Vec<String> = field["args"]
                .as_array()
                .unwrap()
                .iter()
                .map(|a| format!("{}:{}", a["name"].as_str().unwrap(), type_name(&a["type"])))
                .collect();
            args.sort();
            lines.push(format!(
                "  {name}.{}({}): {}",
                field["name"].as_str().unwrap(),
                args.join(","),
                type_name(&field["type"])
            ));
        }
        for field in ty["inputFields"].as_array().into_iter().flatten() {
            lines.push(format!(
                "  {name}.{}: {}",
                field["name"].as_str().unwrap(),
                type_name(&field["type"])
            ));
        }
        for value in ty["enumValues"].as_array().into_iter().flatten() {
            lines.push(format!("  {name} = {}", value["name"].as_str().unwrap()));
        }
    }

    lines.sort();
    lines.join("\n")
}

async fn introspect(config: Value) -> String {
    introspect_schema(config, false).await
}

/// The auto-generated schema, or the auth schema (`graphql.apiAuthRoute`).
async fn introspect_schema(config: Value, auth: bool) -> String {
    use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

    let config: YekongaConfig = serde_json::from_value(config).unwrap();
    let structure = DatabaseStructure::from_value(
        &serde_json::from_str(include_str!("fixtures/database.json")).unwrap(),
    );
    let app = Yekonga::with_backend(
        config,
        structure,
        std::sync::Arc::new(LocalBackend::in_memory()),
    );

    let query = include_str!("fixtures/introspection.graphql");
    let result = if auth {
        app.auth_graphql(query, serde_json::json!({}), "", None, None)
            .await
    } else {
        app.graphql(query, serde_json::json!({}), "", None).await
    };
    assert!(result.get("errors").is_none(), "{result}");
    normalized_schema(&result)
}

fn assert_same_schema(rust: &str, go: &str) {
    let go = go.trim_end();
    if rust != go {
        let missing: Vec<&str> = go
            .lines()
            .filter(|l| !rust.lines().any(|r| r == *l))
            .take(20)
            .collect();
        let extra: Vec<&str> = rust
            .lines()
            .filter(|l| !go.lines().any(|g| g == *l))
            .take(20)
            .collect();
        panic!(
            "schema differs from Go\nonly in Go:\n{}\nonly in Rust:\n{}",
            missing.join("\n"),
            extra.join("\n")
        );
    }
}

#[tokio::test]
async fn graphql_schema_matches_go() {
    let rust = introspect(serde_json::json!({})).await;
    assert_same_schema(&rust, include_str!("fixtures/go_graphql_schema.txt"));
}

#[tokio::test]
async fn auth_schema_matches_go() {
    // The GraphQL library always has the built-in Float scalar, which no
    // auth field uses; Go's schema leaves it out.
    let without_float = |schema: String| {
        schema
            .lines()
            .filter(|l| *l != "SCALAR Float")
            .collect::<Vec<_>>()
            .join("\n")
    };

    let rust = without_float(introspect_schema(serde_json::json!({}), true).await);
    assert_same_schema(&rust, include_str!("fixtures/go_auth_schema.txt"));

    let secure = serde_json::json!({"secureAuthentication": true});
    let rust = without_float(introspect_schema(secure, true).await);
    assert_same_schema(&rust, include_str!("fixtures/go_auth_schema_secure.txt"));
}

#[tokio::test]
async fn graphql_schema_matches_go_with_every_module() {
    use sha2::{Digest, Sha256};

    let rust = introspect(serde_json::json!({
        "isAuthorizationServer": true, "hasTenant": true, "hasTenantBilling": true,
        "hasTenantCatch": true, "hasPaymentModule": true
    }))
    .await;

    let hash: String = Sha256::digest(format!("{rust}\n"))
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    assert_eq!(
        hash,
        include_str!("fixtures/go_graphql_schema_full.sha256").trim(),
        "{} lines",
        rust.lines().count()
    );
}
