//! Data models built from the merged schema (port of `yekonga/model.go`).
//!
//! Models are schema-driven: there is no Rust type per model. A
//! [`DataModel`] describes one collection's fields, naming and relations, and
//! is shared by the REST layer, GraphQL and the database backends.

use std::collections::{BTreeMap, BTreeSet};

use serde_json::Value;

use crate::config::{DatabaseKind, YekongaConfig};
use crate::helper::{
    get_child_relative_name, get_parent_relative_name, pluralize, singularize, to_camel_case,
    to_title, to_underscore, to_variable,
};
use crate::schema::{CollectionFieldConfig, CollectionStructure, DatabaseStructure};

pub const TENANT_ID_KEY: &str = "tenantId";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum FieldKind {
    Id,
    String,
    Number,
    Float,
    Date,
    Bool,
    Object,
    Any,
    Array,
    File,
}

impl FieldKind {
    /// The name used in the Go code and generated schemas.
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Id => "id",
            Self::String => "string",
            Self::Number => "number",
            Self::Float => "float",
            Self::Date => "date",
            Self::Bool => "bool",
            Self::Object => "object",
            Self::Any => "any",
            Self::Array => "array",
            Self::File => "file",
        }
    }
}

/// One allowed value of an enum-like field.
#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize)]
pub struct FieldOption {
    pub value: String,
    pub label: String,
}

/// A relation between two models, seen from one side.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ForeignKey {
    /// The model on the other side of the relation.
    pub model_name: String,
    /// Key on the parent model (`_id` for `id`).
    pub primary_key: String,
    /// Field on the child model holding the parent's key.
    pub foreign_key: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct DataModelField {
    pub primary_key: bool,
    pub name: String,
    pub kind: FieldKind,
    pub required: bool,
    pub protected: bool,
    pub is_array: bool,
    pub default_value: Value,
    pub foreign_key: Option<ForeignKey>,
    pub options: Vec<FieldOption>,
    pub id: bool,
    pub index: bool,
    pub unique: bool,
}

impl DataModelField {
    /// Resolves a raw schema field into its typed form.
    pub fn new(name: &str, field: &CollectionFieldConfig) -> Self {
        let mut vi = field.kind.trim().to_lowercase();
        let mut is_array = false;

        if vi.contains('[') && vi.contains(']') {
            is_array = true;
            vi = vi.replace(['[', ']'], "").trim().to_string();
        }

        let kind = match vi.as_str() {
            "id" => FieldKind::Id,
            "date" | "time" | "datetime" | "timestamp" => FieldKind::Date,
            "bool" | "boolean" => FieldKind::Bool,
            "float" | "double" | "decimal" => FieldKind::Float,
            "int" | "number" | "integer" | "digit" => FieldKind::Number,
            "array" => {
                is_array = true;
                FieldKind::Array
            }
            "any" => FieldKind::Any,
            "object" => FieldKind::Object,
            "url" | "file" => FieldKind::File,
            _ => FieldKind::String,
        };

        let default_value = if is_array {
            match &field.default_value {
                Value::Array(_) => field.default_value.clone(),
                _ => Value::Array(Vec::new()),
            }
        } else {
            field.default_value.clone()
        };

        let options = field
            .options
            .iter()
            .map(|opt| FieldOption {
                value: opt.clone(),
                label: to_title(opt),
            })
            .collect();

        let foreign_key = field
            .foreign_key
            .as_ref()
            .filter(|fk| !fk.model.is_empty())
            .and_then(|fk| {
                let parent_collection = singularize(&fk.model);
                let primary_key = match fk.key.as_str() {
                    "" | "id" => "_id".to_string(),
                    key => key.to_string(),
                };

                (!parent_collection.is_empty()).then(|| ForeignKey {
                    model_name: to_camel_case(&parent_collection),
                    primary_key,
                    foreign_key: name.to_string(),
                })
            });

        Self {
            primary_key: field.primary_key,
            name: name.to_string(),
            kind,
            required: field.required,
            protected: field.protected,
            is_array,
            default_value,
            foreign_key,
            options,
            id: kind == FieldKind::Id,
            index: field.index,
            unique: field.unique,
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct DataModel {
    /// Singular PascalCase name, e.g. `OrderItem`.
    pub name: String,
    /// PascalCase name as written in the schema, e.g. `OrderItems`.
    pub class: String,
    /// Database collection/table, e.g. `order_items`.
    pub collection: String,
    pub variable: String,
    pub variable_single: String,
    pub variable_plural: String,
    pub primary_key: String,
    /// Field used as the record's display name.
    pub primary_name: String,
    pub has_tenant: bool,
    pub database_kind: Option<DatabaseKind>,

    pub fields: BTreeMap<String, DataModelField>,
    /// All field names, sorted.
    pub valid_fields: Vec<String>,
    pub required: Vec<String>,
    pub protected: Vec<String>,
    pub date_fields: Vec<String>,
    pub option_fields: Vec<String>,
    pub file_fields: Vec<String>,
    pub boolean_fields: Vec<String>,
    pub number_fields: Vec<String>,
    pub float_fields: Vec<String>,
    /// Fields that reference another model.
    pub parent_keys: Vec<String>,
    pub relative_keys: Vec<String>,
    pub id_keys: BTreeSet<String>,

    /// Parent records reachable from this model, by relation name (`user`).
    pub parent_fields: BTreeMap<String, ForeignKey>,
    /// Child collections reachable from this model, by relation name (`orders`).
    pub children_fields: BTreeMap<String, ForeignKey>,
}

impl DataModel {
    pub fn new(config: &YekongaConfig, collection: &str, fields: &CollectionStructure) -> Self {
        let mut model = DataModel {
            name: to_camel_case(&singularize(collection)),
            class: to_camel_case(collection),
            collection: to_underscore(&pluralize(collection)),
            variable: to_variable(collection),
            variable_single: to_variable(&singularize(collection)),
            variable_plural: to_variable(&pluralize(collection)),
            primary_key: "_id".into(),
            primary_name: String::new(),
            has_tenant: false,
            database_kind: config.database.kind(),
            fields: BTreeMap::new(),
            valid_fields: Vec::new(),
            required: Vec::new(),
            protected: Vec::new(),
            date_fields: Vec::new(),
            option_fields: Vec::new(),
            file_fields: Vec::new(),
            boolean_fields: Vec::new(),
            number_fields: Vec::new(),
            float_fields: Vec::new(),
            parent_keys: Vec::new(),
            relative_keys: Vec::new(),
            id_keys: BTreeSet::new(),
            parent_fields: BTreeMap::new(),
            children_fields: BTreeMap::new(),
        };

        let mut has_primary_name = false;

        for (key, config) in fields {
            if key == "id" {
                continue;
            }

            if key == TENANT_ID_KEY {
                model.has_tenant = true;
            }

            let field = DataModelField::new(key, config);

            // An exact name/title/label field wins; otherwise the first
            // field whose name contains "name" or "title"; otherwise the first field.
            let snake = to_underscore(&field.name);
            let exact = ["name", "title", "label"].contains(&field.name.as_str());
            let similar = snake.contains("name") || snake.contains("title");

            if exact || (!has_primary_name && similar) {
                model.primary_name = field.name.clone();
                has_primary_name = true;
            } else if model.primary_name.is_empty() && field.name != "_id" {
                model.primary_name = field.name.clone();
            }

            model.valid_fields.push(key.clone());

            if field.required {
                model.required.push(key.clone());
            }
            if field.protected {
                model.protected.push(key.clone());
            }

            match field.kind {
                FieldKind::Date => model.date_fields.push(key.clone()),
                FieldKind::File => model.file_fields.push(key.clone()),
                FieldKind::Bool => model.boolean_fields.push(key.clone()),
                FieldKind::Number => model.number_fields.push(key.clone()),
                FieldKind::Float => model.float_fields.push(key.clone()),
                _ => {}
            }

            if !field.options.is_empty() {
                model.option_fields.push(key.clone());
            }
            if field.id {
                model.id_keys.insert(key.clone());
            }
            if field.foreign_key.is_some() {
                model.parent_keys.push(key.clone());
                model.relative_keys.push(key.clone());
            }

            model.fields.insert(key.clone(), field);
        }

        let id = CollectionFieldConfig {
            kind: "ID".into(),
            ..Default::default()
        };
        model
            .fields
            .insert("id".into(), DataModelField::new("id", &id));
        model.valid_fields.push("id".into());
        model.valid_fields.sort();

        model
    }

    pub fn field(&self, name: &str) -> Option<&DataModelField> {
        self.fields.get(name)
    }
}

/// Builds every model from the merged schema and links parent/child relations
/// between them (`NewSystemModels`).
pub fn build_system_models(
    config: &YekongaConfig,
    structure: &DatabaseStructure,
) -> BTreeMap<String, DataModel> {
    let mut models: BTreeMap<String, DataModel> = structure
        .0
        .iter()
        .map(|(collection, fields)| {
            let model = DataModel::new(config, collection, fields);
            (model.name.clone(), model)
        })
        .collect();

    let mut children: Vec<(String, String, ForeignKey)> = Vec::new();

    for model in models.values_mut() {
        for key in model.parent_keys.clone() {
            let Some(parent) = model.fields[&key].foreign_key.clone() else {
                continue;
            };

            let parent_name = get_parent_relative_name(
                &parent.model_name,
                &parent.primary_key,
                &parent.foreign_key,
            );
            model
                .parent_fields
                .entry(parent_name)
                .or_insert_with(|| parent.clone());

            let child_name = get_child_relative_name(
                &parent.model_name,
                &model.name,
                &parent.primary_key,
                &parent.foreign_key,
            );
            let child = ForeignKey {
                model_name: model.name.clone(),
                ..parent.clone()
            };
            children.push((parent.model_name, child_name, child));
        }
    }

    for (parent_model, child_name, child) in children {
        if let Some(parent) = models.get_mut(&parent_model) {
            parent.children_fields.insert(child_name, child);
        }
    }

    models
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn structure() -> DatabaseStructure {
        DatabaseStructure::from_value(&json!({
            "Users": {
                "firstName": {"type": "String"},
                "email": {"type": "String", "required": true},
                "createdAt": {"type": "Date", "default": "now"}
            },
            "OrderItems": {
                "orderId": {"type": "ID", "foreignKey": "Order.id"},
                "quantity": {"type": "Integer"},
                "tags": {"type": "[String]", "default": "bad"},
                "status": {"type": "String", "options": ["pending", "per_unit"]}
            },
            "Orders": {
                "id": {"type": "ID"},
                "userId": {"type": "ID", "foreignKey": "Users"},
                "tenantId": {"type": "ID"},
                "title": {"type": "String"},
                "total": {"type": "Double", "protected": true},
                "paid": {"type": "Boolean"},
                "receipt": {"type": "URL"}
            }
        }))
    }

    #[test]
    fn model_naming_and_fields() {
        let models = build_system_models(&YekongaConfig::default(), &structure());
        let order = &models["Order"];

        assert_eq!(order.class, "Orders");
        assert_eq!(order.collection, "orders");
        assert_eq!(order.variable_plural, "orders");
        assert_eq!(order.variable_single, "order");
        assert_eq!(order.primary_name, "title");
        assert!(order.has_tenant);
        assert_eq!(order.float_fields, vec!["total"]);
        assert_eq!(order.protected, vec!["total"]);
        assert_eq!(order.boolean_fields, vec!["paid"]);
        assert_eq!(order.file_fields, vec!["receipt"]);
        assert!(order.id_keys.contains("userId"));
        assert_eq!(order.valid_fields.first().map(String::as_str), Some("id"));
        assert!(order.valid_fields.windows(2).all(|w| w[0] <= w[1]));

        let item = &models["OrderItem"];
        assert_eq!(item.collection, "order_items");
        assert_eq!(item.fields["quantity"].kind, FieldKind::Number);
        assert!(item.fields["tags"].is_array);
        assert_eq!(item.fields["tags"].default_value, json!([]));
        assert_eq!(
            item.fields["status"].options[1],
            FieldOption {
                value: "per_unit".into(),
                label: "Per Unit".into()
            }
        );

        let user = &models["User"];
        assert_eq!(user.required, vec!["email"]);
        assert_eq!(user.primary_name, "firstName");
    }

    #[test]
    fn relations() {
        let models = build_system_models(&YekongaConfig::default(), &structure());

        let user_fk = ForeignKey {
            model_name: "User".into(),
            primary_key: "_id".into(),
            foreign_key: "userId".into(),
        };
        assert_eq!(
            models["Order"].fields["userId"].foreign_key,
            Some(user_fk.clone())
        );
        assert_eq!(models["Order"].parent_fields["user"], user_fk);
        assert_eq!(
            models["OrderItem"].parent_fields["order"].model_name,
            "Order"
        );

        assert_eq!(
            models["User"].children_fields["orders"],
            ForeignKey {
                model_name: "Order".into(),
                ..user_fk
            }
        );
        assert_eq!(
            models["Order"].children_fields["orderItems"].model_name,
            "OrderItem"
        );
    }
}
