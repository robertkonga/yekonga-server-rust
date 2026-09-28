//! `database.json`: the data schema that drives models, REST and GraphQL.
//!
//! The file maps collection names to field definitions:
//!
//! ```json
//! { "Orders": { "userId": { "type": "ID", "foreignKey": "User.id" },
//!               "total":  { "type": "Float", "default": 0 } } }
//! ```
//!
//! [`DatabaseStructure::build`] merges it with the framework's built-in
//! collections (users, tenants, billing, payments, notifications, ...) the
//! same way `NewDatabaseStructure` does in `yekonga/initializer.go`.

use std::collections::BTreeMap;
use std::path::Path;
use std::sync::LazyLock;

use serde_json::{Map, Value};

use crate::config::{resolve_path, YekongaConfig};
use crate::error::{Error, Result};
use crate::helper::{pluralize, to_camel_case};

/// Fields of one collection, by field name.
pub type CollectionStructure = BTreeMap<String, CollectionFieldConfig>;

/// A field reference to another collection (`"foreignKey": "User.id"`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ForeignKeyConfig {
    pub model: String,
    pub key: String,
}

/// One field definition from `database.json`.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct CollectionFieldConfig {
    pub primary_key: bool,
    /// Raw type name (`String`, `ID`, `Date`, `[String]`, ...).
    pub kind: String,
    pub default_value: Value,
    pub required: bool,
    pub protected: bool,
    /// Create a database index on this field.
    pub index: bool,
    /// Create a unique index on this field.
    pub unique: bool,
    pub options: Vec<String>,
    pub foreign_key: Option<ForeignKeyConfig>,
}

impl CollectionFieldConfig {
    /// Parses the JSON shape used in `database.json`. Unknown keys and values
    /// of the wrong type are ignored, as in the Go loader.
    pub fn from_value(value: &Value) -> Self {
        let mut result = Self::default();
        let Some(field) = value.as_object() else {
            return result;
        };

        if let Some(kind) = field.get("type").and_then(Value::as_str) {
            result.kind = kind.to_string();
        }

        if let Some(default) = field.get("default").or_else(|| field.get("defaultValue")) {
            result.default_value = default.clone();
        }

        let flag = |name: &str| field.get(name).and_then(Value::as_bool).unwrap_or(false);
        result.required = flag("required");
        result.protected = flag("protected");
        result.index = flag("index");
        result.unique = flag("unique");
        result.primary_key = flag("primaryKey");

        if let Some(options) = field.get("options").and_then(Value::as_array) {
            result.options = options
                .iter()
                .map(|o| match o {
                    Value::String(s) => s.clone(),
                    other => other.to_string(),
                })
                .collect();
        }

        // The first of these keys that is present wins, even if its value isn't usable.
        let relation = field
            .get("foreignKey")
            .or_else(|| field.get("relation"))
            .or_else(|| field.get("source"));

        if let Some(relation) = relation.and_then(Value::as_str).filter(|s| !s.is_empty()) {
            let (model, key) = relation.split_once('.').unwrap_or((relation, "id"));
            result.foreign_key = Some(ForeignKeyConfig {
                model: model.to_string(),
                key: key.to_string(),
            });
        }

        result
    }
}

/// Collections by name.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct DatabaseStructure(pub BTreeMap<String, CollectionStructure>);

impl DatabaseStructure {
    /// Parses a `database.json` document.
    pub fn from_value(value: &Value) -> Self {
        let collections = value
            .as_object()
            .map(|collections| {
                collections
                    .iter()
                    .map(|(name, fields)| (name.clone(), parse_fields(fields)))
                    .collect()
            })
            .unwrap_or_default();

        Self(collections)
    }

    /// Reads a `database.json` file (the app's own collections only).
    pub fn from_file(path: impl AsRef<Path>) -> Result<Self> {
        let path = resolve_path(path.as_ref());
        let text = std::fs::read_to_string(&path).map_err(|e| Error::Io(path.clone(), e))?;
        let value: Value = serde_json::from_str(&text).map_err(|e| Error::Json(path, e))?;

        Ok(Self::from_value(&value))
    }

    /// Merges the app's collections with the built-in ones the config enables.
    ///
    /// Built-in collection names are normalized to plural PascalCase. When an
    /// app collection has the same name as a built-in one, its fields replace
    /// the built-in fields of the same name and add new ones.
    pub fn build(extra: &DatabaseStructure, config: &YekongaConfig) -> DatabaseStructure {
        let mut structure: BTreeMap<String, CollectionStructure> = DEFAULTS.extra.0.clone();

        // Adds a built-in group; a collection that already exists instead
        // takes the app's fields for it (the Go code does exactly this).
        let mut add_group = |group: &DatabaseStructure| {
            for (name, fields) in &group.0 {
                let name = to_camel_case(&pluralize(name));
                match structure.get_mut(&name) {
                    Some(existing) => merge_fields(existing, extra.0.get(&name)),
                    None => {
                        structure.insert(name, fields.clone());
                    }
                }
            }
        };

        if config.is_authorization_server {
            add_group(&DEFAULTS.auth);
        }

        if config.has_tenant_catch {
            add_group(&DEFAULTS.tenant_catch);
        }

        if config.has_tenant {
            add_group(&DEFAULTS.tenant);

            if config.has_tenant_billing {
                add_group(&DEFAULTS.billing);
                add_group(&DEFAULTS.payment);
            }
        }

        if (!config.has_tenant || !config.has_tenant_billing) && config.has_payment_module {
            add_group(&DEFAULTS.payment);
        }

        for (name, fields) in &extra.0 {
            let name = to_camel_case(&pluralize(name));
            match structure.get_mut(&name) {
                Some(existing) => merge_fields(existing, Some(fields)),
                None => {
                    structure.insert(name, fields.clone());
                }
            }
        }

        DatabaseStructure(structure)
    }
}

fn parse_fields(fields: &Value) -> CollectionStructure {
    fields
        .as_object()
        .map(|fields| {
            fields
                .iter()
                .map(|(name, field)| (name.clone(), CollectionFieldConfig::from_value(field)))
                .collect()
        })
        .unwrap_or_default()
}

fn merge_fields(existing: &mut CollectionStructure, extra: Option<&CollectionStructure>) {
    for (name, field) in extra.into_iter().flatten() {
        existing.insert(name.clone(), field.clone());
    }
}

/// The framework's built-in collections (`yekonga/database_json.go`).
pub struct DefaultStructures {
    pub tenant: DatabaseStructure,
    pub tenant_catch: DatabaseStructure,
    pub auth: DatabaseStructure,
    pub billing: DatabaseStructure,
    pub payment: DatabaseStructure,
    /// Always included (notifications, audit trail, chats, ...).
    pub extra: DatabaseStructure,
}

pub static DEFAULTS: LazyLock<DefaultStructures> = LazyLock::new(|| {
    let all: Map<String, Value> = serde_json::from_str(include_str!("defaults.json"))
        .expect("built-in defaults.json is valid");
    let group = |name: &str| DatabaseStructure::from_value(&all[name]);

    DefaultStructures {
        tenant: group("tenant"),
        tenant_catch: group("tenantCatch"),
        auth: group("auth"),
        billing: group("billing"),
        payment: group("payment"),
        extra: group("extra"),
    }
});

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn field_parsing() {
        let field = CollectionFieldConfig::from_value(&json!({
            "type": "String", "defaultValue": "x", "required": true, "index": "yes",
            "options": ["a", 1], "relation": "User"
        }));

        assert_eq!(field.kind, "String");
        assert_eq!(field.default_value, json!("x"));
        assert!(field.required);
        assert!(!field.index);
        assert_eq!(field.options, vec!["a", "1"]);
        assert_eq!(
            field.foreign_key,
            Some(ForeignKeyConfig {
                model: "User".into(),
                key: "id".into()
            })
        );

        // A present-but-unusable foreignKey hides "relation", as in Go.
        let field =
            CollectionFieldConfig::from_value(&json!({"foreignKey": null, "relation": "User.id"}));
        assert_eq!(field.foreign_key, None);
    }

    #[test]
    fn defaults_are_complete() {
        let count = |s: &DatabaseStructure| s.0.values().map(|c| c.len()).sum::<usize>();
        let d = &*DEFAULTS;

        // Field counts of the Go Default*DatabaseStructure vars.
        assert_eq!(count(&d.tenant), 43);
        assert_eq!(count(&d.tenant_catch), 3);
        assert_eq!(count(&d.auth), 159);
        assert_eq!(count(&d.billing), 63);
        assert_eq!(count(&d.payment), 53);
        assert_eq!(count(&d.extra), 202);

        let collections: usize = [
            &d.tenant,
            &d.tenant_catch,
            &d.auth,
            &d.billing,
            &d.payment,
            &d.extra,
        ]
        .iter()
        .map(|s| s.0.len())
        .sum();
        assert_eq!(collections, 46);

        let user_id = &d.tenant.0["Tenants"]["userId"];
        assert_eq!(user_id.kind, "ID");
        assert_eq!(
            user_id.foreign_key,
            Some(ForeignKeyConfig {
                model: "User".into(),
                key: "id".into()
            })
        );
    }

    #[test]
    fn build_merges_by_config() {
        let extra = DatabaseStructure::from_value(&json!({
            "Order": {"total": {"type": "Float"}},
            "Users": {"nickname": {"type": "String"}, "email": {"type": "Text"}}
        }));

        let plain = DatabaseStructure::build(&extra, &YekongaConfig::default());
        assert!(
            plain.0.contains_key("Orders"),
            "app collections are pluralized"
        );
        assert!(
            plain.0.contains_key("Notifications"),
            "extra built-ins always load"
        );
        assert!(!plain.0.contains_key("Tenants"));
        assert_eq!(
            plain.0["Users"].len(),
            2,
            "no auth built-ins without isAuthorizationServer"
        );

        let config = YekongaConfig {
            is_authorization_server: true,
            has_tenant: true,
            has_tenant_billing: true,
            ..Default::default()
        };
        let full = DatabaseStructure::build(&extra, &config);
        assert!(full.0.contains_key("Tenants"));
        assert!(full.0.contains_key("PricingPlans"));
        assert!(full.0.contains_key("Payments"));

        let users = &full.0["Users"];
        assert_eq!(
            users["email"].kind, "Text",
            "app fields replace built-in ones"
        );
        assert!(users.contains_key("nickname"));
        assert!(users.contains_key("password"));
    }
}
