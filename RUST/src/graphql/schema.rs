//! Builds the dynamic schema with the Go server's names (port of the type and
//! field builders in `yekonga/graphql.go`).

use std::sync::Arc;

use async_graphql::dynamic::{
    Enum, Field, FieldFuture, FieldValue, InputObject, InputValue, Object, Scalar, Schema,
    SchemaError, TypeRef,
};
use serde_json::Value;

use super::resolve::{self, Kind};
use super::Node;
use crate::app::Yekonga;
use crate::helper::{pluralize, singularize, to_camel_case, to_variable};
use crate::model::{DataModel, FieldKind};

const WHERE_OPERATIONS: &[&str] = &[
    "equalTo",
    "notEqualTo",
    "lessThan",
    "notLessThan",
    "lessThanOrEqualTo",
    "notLessThanOrEqualTo",
    "greaterThan",
    "notGreaterThan",
    "greaterThanOrEqualTo",
    "notGreaterThanOrEqualTo",
    "matchesRegex",
    "options",
];
const WHERE_LIST_OPERATIONS: &[&str] = &["in", "all", "notIn"];

// ----- names (as the Go server derives them) -----------------------------------------

pub(crate) fn enum_fields_name(m: &DataModel) -> String {
    to_camel_case(&format!("{}_enum_fields", m.variable_single))
}
fn structured_enum_name(m: &DataModel) -> String {
    to_camel_case(&format!("{}_structured_enum_fields", m.variable_single))
}
fn where_name(m: &DataModel) -> String {
    to_camel_case(&format!("where_{}_input", m.variable_single))
}
fn dimension_where_name(m: &DataModel) -> String {
    to_camel_case(&format!("dimension_where_{}_input", m.variable_single))
}
fn order_by_name(m: &DataModel) -> String {
    to_camel_case(&format!("order_by_{}_input", m.variable_single))
}
fn input_name(m: &DataModel) -> String {
    to_camel_case(&format!("{}_input", m.variable_single))
}
fn result_name(action: &str, m: &DataModel) -> String {
    to_camel_case(&format!(
        "{action}_{}_input_result_output",
        m.variable_single
    ))
}
fn summary_type_name(m: &DataModel) -> String {
    to_camel_case(&format!("{}_summary", m.name))
}
fn paginate_type_name(m: &DataModel) -> String {
    to_camel_case(&format!("{}_paginate", m.name))
}
fn download_type_name(m: &DataModel) -> String {
    to_camel_case(&format!("download_{}", pluralize(&m.name)))
}

/// The output scalar for a field kind.
fn output_scalar(kind: FieldKind) -> &'static str {
    match kind {
        FieldKind::Bool => TypeRef::BOOLEAN,
        FieldKind::Id => TypeRef::ID,
        FieldKind::Date => "Date",
        FieldKind::Float => TypeRef::FLOAT,
        FieldKind::Number => TypeRef::INT,
        FieldKind::Object | FieldKind::Any => "Any",
        FieldKind::Array => "Array",
        FieldKind::String | FieldKind::File => TypeRef::STRING,
    }
}

/// The scalar for a field in where inputs (IDs and strings take `CustomString`).
fn where_scalar(kind: FieldKind) -> &'static str {
    match kind {
        FieldKind::Id | FieldKind::String => "CustomString",
        other => output_scalar(other),
    }
}

/// Whether a relation's model exists (Go's `vi.Model != nil`).
fn related<'a>(app: &'a Yekonga, model_name: &str) -> Option<&'a Arc<DataModel>> {
    app.models().get(model_name)
}

fn has_valid_relation(app: &Yekonga, m: &DataModel) -> bool {
    m.parent_fields
        .values()
        .chain(m.children_fields.values())
        .any(|r| related(app, &r.model_name).is_some())
}

// ----- builder ---------------------------------------------------------------------------

pub fn build_schema(app: &Yekonga) -> Result<Schema, SchemaError> {
    let mut query = Object::new("Query");
    let mut mutation = Object::new("Mutation");
    let mut builder_types: Vec<async_graphql::dynamic::Type> = Vec::new();

    builder_types.extend(shared_types());

    for model in app.models().values() {
        let m = model.as_ref();
        let (enums, inputs, objects) = model_types(app, model);
        builder_types.extend(enums.into_iter().map(Into::into));
        builder_types.extend(inputs.into_iter().map(Into::into));
        builder_types.extend(objects.into_iter().map(Into::into));

        let k = to_variable(&singularize(&m.name));
        query = query
            .field(single_field(app, &k, model, None))
            .field(list_field(app, &pluralize(&k), model, None))
            .field(paginate_field(
                app,
                &to_variable(&format!("{k}_paginate")),
                model,
                None,
            ))
            .field(summary_field(
                &to_variable(&format!("{k}_summary")),
                model,
                None,
            ))
            .field(download_field(
                &to_variable(&format!("download_{}", pluralize(&k))),
                model,
            ));

        mutation = mutation
            .field(import_field(
                app,
                &to_variable(&format!("import_{}", pluralize(&k))),
                model,
            ))
            .field(create_field(
                app,
                &to_variable(&format!("create_{k}")),
                model,
            ))
            .field(update_field(
                app,
                &to_variable(&format!("update_{k}")),
                model,
            ))
            .field(delete_field(
                app,
                &to_variable(&format!("delete_{k}")),
                model,
            ))
            .field(action_field(
                app,
                &to_variable(&format!("{k}_action")),
                model,
            ));
    }

    let mut schema = Schema::build("Query", Some("Mutation"), None)
        .register(query)
        .register(mutation);
    for ty in builder_types {
        schema = schema.register(ty);
    }

    schema.finish()
}

fn shared_types() -> Vec<async_graphql::dynamic::Type> {
    let scalar =
        |name: &str| Scalar::new(name).description(format!("Custom scalar type for {name}"));
    let enumeration = |name: &str, items: &[&str]| Enum::new(name).items(items.iter().copied());

    vec![
        Scalar::new("Date")
            .description("Custom scalar type for Date")
            .into(),
        scalar("Any").into(),
        scalar("Array").into(),
        Scalar::new("CustomString")
            .description("Custom scalar type for String")
            .into(),
        enumeration("GeneralOrderOptionsEnum", &["ASC", "DESC"]).into(),
        enumeration(
            "DownloadTypeOptionsEnum",
            &["PDF", "EXCEL", "CSV", "PRINT", "IMAGE", "PNG", "JPG"],
        )
        .into(),
        enumeration("OrientationOptionsEnum", &["PORTRAIT", "LANDSCAPE"]).into(),
        enumeration("GeneralGraphOptions", &["PIE", "DOUGHNUT", "LINE", "BAR"]).into(),
        enumeration(
            "GeneralTotalOptions",
            &["COUNT", "MIN", "MAX", "SUM", "AVG", "AVERAGE"],
        )
        .into(),
        enumeration(
            "GeneralPeriodicity",
            &[
                "HOURLY",
                "DAILY",
                "DAY_HOURS",
                "WEEKLY",
                "WEEK_DAYS",
                "MONTHLY",
                "MONTH_DAYS",
                "QUARTERLY",
                "SEMIANNUALLY",
                "YEARLY",
                "YEAR_MONTHS",
                "NONE",
            ],
        )
        .into(),
        json_object(
            "GraphDataType",
            &[
                ("labels", TypeRef::named_list(TypeRef::STRING)),
                ("datasets", TypeRef::named_list("GraphDataset")),
            ],
        )
        .into(),
        json_object(
            "GraphDataset",
            &[
                ("label", TypeRef::named(TypeRef::STRING)),
                ("color", TypeRef::named_list(TypeRef::STRING)),
                ("backgroundColor", TypeRef::named_list(TypeRef::STRING)),
                ("data", TypeRef::named_list(TypeRef::INT)),
            ],
        )
        .into(),
        InputObject::new("GeneralDownloadTypeInput")
            .field(InputValue::new(
                "operationName",
                TypeRef::named(TypeRef::STRING),
            ))
            .field(InputValue::new(
                "variables",
                TypeRef::named("GeneralDownloadVariablesTypeInput"),
            ))
            .field(InputValue::new("query", TypeRef::named(TypeRef::STRING)))
            .into(),
        InputObject::new("GeneralDownloadVariablesTypeInput")
            .field(InputValue::new("input", TypeRef::named("Any")))
            .into(),
    ]
}

/// An object whose fields read keys of a JSON parent.
pub(crate) fn json_object(name: &str, fields: &[(&str, TypeRef)]) -> Object {
    fields
        .iter()
        .fold(Object::new(name), |object, (field, ty)| {
            object.field(json_field(field, ty.clone()))
        })
}

fn json_field(name: &str, ty: TypeRef) -> Field {
    let key = name.to_string();
    let kind = Kind::for_scalar(ty.to_string().trim_end_matches('!'));

    Field::new(name, ty, move |ctx| {
        let value = match ctx.parent_value.downcast_ref::<Node>() {
            Some(Node::Json(Value::Object(map))) => map.get(&key).cloned().unwrap_or(Value::Null),
            _ => Value::Null,
        };
        FieldFuture::Value(resolve::coerce(kind, value).map(FieldValue::value))
    })
}

type Types = (Vec<Enum>, Vec<InputObject>, Vec<Object>);

/// Every type a model contributes.
fn model_types(app: &Yekonga, model: &Arc<DataModel>) -> Types {
    let m = model.as_ref();
    let (mut enums, mut inputs, mut objects) = (Vec::new(), Vec::new(), Vec::new());

    // Enums of the model's fields.
    let mut fields_enum = Enum::new(enum_fields_name(m));
    let mut structured = Vec::new();
    for (name, field) in &m.fields {
        fields_enum = fields_enum.item(name.as_str());
        if name == "id"
            || !field.options.is_empty()
            || field.kind == FieldKind::Date
            || m.parent_keys.contains(name)
        {
            structured.push(name.as_str());
        }
    }
    if structured.is_empty() {
        structured = m.fields.keys().map(String::as_str).collect();
    }
    enums.push(fields_enum);
    enums.push(Enum::new(structured_enum_name(m)).items(structured));

    // Where input, with one operator input per field.
    let mut where_input = InputObject::new(where_name(m));
    for (name, field) in &m.fields {
        let field_type = to_camel_case(&format!("where_{}_input_{name}_field", m.name));
        let scalar = where_scalar(field.kind);
        let mut operators = InputObject::new(&field_type);
        for op in WHERE_OPERATIONS {
            operators = operators.field(InputValue::new(*op, TypeRef::named(scalar)));
        }
        for op in WHERE_LIST_OPERATIONS {
            operators = operators.field(InputValue::new(*op, TypeRef::named_list(scalar)));
        }
        operators = operators.field(InputValue::new("exists", TypeRef::named(TypeRef::BOOLEAN)));
        inputs.push(operators);

        where_input = where_input.field(InputValue::new(name, TypeRef::named(field_type)));
    }
    for logical in ["AND", "OR", "NOR"] {
        where_input =
            where_input.field(InputValue::new(logical, TypeRef::named_list(where_name(m))));
    }

    let mut dimension_where =
        has_valid_relation(app, m).then(|| InputObject::new(dimension_where_name(m)));
    for (relation_name, relation) in m.parent_fields.iter().chain(&m.children_fields) {
        if let Some(target) = related(app, &relation.model_name) {
            where_input = where_input.field(InputValue::new(
                relation_name,
                TypeRef::named(where_name(target)),
            ));
            if let Some(dimension) = dimension_where.take() {
                dimension_where = Some(dimension.field(InputValue::new(
                    relation_name,
                    TypeRef::named(where_name(target)),
                )));
            }
        }
    }
    inputs.push(where_input);
    inputs.extend(dimension_where);

    // Order-by input.
    inputs.push(
        m.fields
            .keys()
            .fold(InputObject::new(order_by_name(m)), |input, name| {
                input.field(InputValue::new(
                    name,
                    TypeRef::named("GeneralOrderOptionsEnum"),
                ))
            }),
    );

    // Model input, with its children as nested lists.
    let mut input = InputObject::new(input_name(m));
    for (name, field) in &m.fields {
        let scalar = output_scalar(field.kind);
        let ty = if field.required {
            TypeRef::named_nn(scalar)
        } else {
            TypeRef::named(scalar)
        };
        input = input.field(InputValue::new(name, ty));
    }
    for (relation_name, relation) in &m.children_fields {
        if let Some(child) = related(app, &relation.model_name) {
            input = input.field(InputValue::new(
                to_variable(&pluralize(relation_name)),
                TypeRef::named_list(input_name(child)),
            ));
        }
    }
    inputs.push(input);

    // The model's object type.
    let mut object = Object::new(&m.name);
    for (name, field) in &m.fields {
        object = object.field(record_field(model, name, output_scalar(field.kind)));
    }
    if m.name == "CustomForm" {
        object = object.field(Field::new("result", TypeRef::named("Any"), |_| {
            FieldFuture::new(async {
                Err::<Option<FieldValue>, _>(resolve::not_ported("CustomForm.result"))
            })
        }));
    }
    for (relation_name, relation) in &m.parent_fields {
        if let Some(target) = related(app, &relation.model_name) {
            let rel = RelationKeys::new(&relation.foreign_key, &relation.primary_key, true);
            object = object.field(single_field(app, relation_name, target, Some(rel)));
        }
    }
    for (relation_name, relation) in &m.children_fields {
        if let Some(child) = related(app, &relation.model_name) {
            let rel = RelationKeys::new(&relation.foreign_key, &relation.primary_key, false);
            let singular = singularize(relation_name);
            object = object
                .field(list_field(app, relation_name, child, Some(rel.clone())))
                .field(paginate_field(
                    app,
                    &to_variable(&format!("{singular}_paginate")),
                    child,
                    Some(rel.clone()),
                ))
                .field(summary_field(
                    &to_variable(&format!("{singular}_summary")),
                    child,
                    Some(rel),
                ));
        }
    }
    let own = RelationKeys::new("id", "id", false);
    object = object.field(summary_field(
        &to_variable(&format!("{}_summary", singularize(&m.name))),
        model,
        Some(own),
    ));
    objects.push(object);

    // Summary, paginate and download types.
    objects.push(summary_object(app, model));
    objects.push(
        Object::new(paginate_type_name(m))
            .field(json_field("total", TypeRef::named(TypeRef::INT)))
            .field(json_field("perPage", TypeRef::named(TypeRef::INT)))
            .field(json_field("currentPage", TypeRef::named(TypeRef::INT)))
            .field(json_field("lastPage", TypeRef::named(TypeRef::INT)))
            .field(json_field("from", TypeRef::named(TypeRef::INT)))
            .field(json_field("to", TypeRef::named(TypeRef::INT)))
            .field(records_field("data", model)),
    );
    objects.push(json_object(
        &download_type_name(m),
        &[
            ("filename", TypeRef::named(TypeRef::STRING)),
            ("url", TypeRef::named(TypeRef::STRING)),
            ("type", TypeRef::named(TypeRef::STRING)),
            ("size", TypeRef::named(TypeRef::FLOAT)),
        ],
    ));

    // Mutation results.
    let status_fields = |name: String| {
        Object::new(name)
            .field(json_field("status", TypeRef::named(TypeRef::BOOLEAN)))
            .field(json_field("success", TypeRef::named(TypeRef::BOOLEAN)))
            .field(json_field("message", TypeRef::named(TypeRef::STRING)))
    };
    objects.push(status_fields(result_name("create", m)).field(record_data_field(model)));
    objects.push(status_fields(result_name("update", m)).field(record_data_field(model)));
    objects.push(
        status_fields(result_name("delete", m)).field(json_field("data", TypeRef::named("Any"))),
    );
    objects.push(
        status_fields(result_name("action", m)).field(json_field("data", TypeRef::named("Any"))),
    );
    let mut import = status_fields(result_name("import", m));
    for name in ["imported", "updated", "deleted", "ignored", "errors"] {
        import = import.field(json_field(name, TypeRef::named(TypeRef::INT)));
    }
    objects.push(import);

    (enums, inputs, objects)
}

/// A model field, read from the parent record and formatted for output.
fn record_field(model: &Arc<DataModel>, name: &str, scalar: &'static str) -> Field {
    let key = name.to_string();
    let model = model.clone();

    Field::new(name, TypeRef::named(scalar), move |ctx| {
        let value = match ctx.parent_value.downcast_ref::<Node>() {
            Some(Node::Record(record)) => resolve::output_value(&ctx, &model, &key, record),
            _ => Value::Null,
        };
        FieldFuture::Value(resolve::coerce(Kind::for_scalar(scalar), value).map(FieldValue::value))
    })
}

/// A list of records in a JSON parent (`data` of a page).
fn records_field(name: &str, model: &Arc<DataModel>) -> Field {
    let key = name.to_string();
    let model = model.clone();

    Field::new(name, TypeRef::named_list(model.name.clone()), move |ctx| {
        let records = match ctx.parent_value.downcast_ref::<Node>() {
            Some(Node::Json(Value::Object(map))) => map
                .get(&key)
                .and_then(Value::as_array)
                .cloned()
                .unwrap_or_default(),
            _ => Vec::new(),
        };
        let list = records
            .into_iter()
            .filter_map(|r| r.as_object().cloned())
            .map(|r| FieldValue::owned_any(Node::Record(r)));
        FieldFuture::Value(Some(FieldValue::list(list)))
    })
}

/// The `data` record of a create/update result.
fn record_data_field(model: &Arc<DataModel>) -> Field {
    let model = model.clone();

    Field::new("data", TypeRef::named(model.name.clone()), move |ctx| {
        let record = match ctx.parent_value.downcast_ref::<Node>() {
            Some(Node::Json(Value::Object(map))) => {
                map.get("data").and_then(Value::as_object).cloned()
            }
            _ => None,
        };
        FieldFuture::Value(record.map(|r| FieldValue::owned_any(Node::Record(r))))
    })
}

// ----- query fields --------------------------------------------------------------------------

/// Keys linking a relation field to its parent record.
#[derive(Clone)]
pub(crate) struct RelationKeys {
    pub foreign_key: String,
    pub target_key: String,
    pub is_parent: bool,
}

impl RelationKeys {
    fn new(foreign_key: &str, target_key: &str, is_parent: bool) -> Self {
        Self {
            foreign_key: foreign_key.into(),
            target_key: target_key.into(),
            is_parent,
        }
    }
}

fn common_args(field: Field, m: &DataModel) -> Field {
    field
        .argument(InputValue::new("where", TypeRef::named(where_name(m))))
        .argument(InputValue::new("orderBy", TypeRef::named(order_by_name(m))))
        .argument(InputValue::new(
            "groupBy",
            TypeRef::named_list(enum_fields_name(m)),
        ))
        .argument(InputValue::new(
            "accessRole",
            TypeRef::named(TypeRef::STRING),
        ))
        .argument(InputValue::new("route", TypeRef::named(TypeRef::STRING)))
}

fn list_args(field: Field, m: &DataModel) -> Field {
    common_args(field, m)
        .argument(InputValue::new("limit", TypeRef::named(TypeRef::INT)))
        .argument(InputValue::new("page", TypeRef::named(TypeRef::INT)))
        .argument(InputValue::new(
            "distinct",
            TypeRef::named_list(enum_fields_name(m)),
        ))
}

fn single_field(
    app: &Yekonga,
    name: &str,
    model: &Arc<DataModel>,
    relation: Option<RelationKeys>,
) -> Field {
    let (app, target) = (app.clone(), model.clone());
    let field = Field::new(name, TypeRef::named(model.name.clone()), move |ctx| {
        let (app, target, relation) = (app.clone(), target.clone(), relation.clone());
        FieldFuture::new(async move { resolve::single(&ctx, &app, &target, relation).await })
    })
    .description(format!("Get {}", model.name.to_uppercase()));

    common_args(field, model)
}

fn list_field(
    app: &Yekonga,
    name: &str,
    model: &Arc<DataModel>,
    relation: Option<RelationKeys>,
) -> Field {
    let (app, target) = (app.clone(), model.clone());
    let field = Field::new(name, TypeRef::named_list(model.name.clone()), move |ctx| {
        let (app, target, relation) = (app.clone(), target.clone(), relation.clone());
        FieldFuture::new(async move { resolve::list(&ctx, &app, &target, relation).await })
    })
    .description(format!("List of {}", pluralize(&model.name).to_uppercase()));

    list_args(field, model)
}

fn paginate_field(
    app: &Yekonga,
    name: &str,
    model: &Arc<DataModel>,
    relation: Option<RelationKeys>,
) -> Field {
    let (app, target) = (app.clone(), model.clone());
    let field = Field::new(
        name,
        TypeRef::named(paginate_type_name(model)),
        move |ctx| {
            let (app, target, relation) = (app.clone(), target.clone(), relation.clone());
            FieldFuture::new(async move { resolve::paginate(&ctx, &app, &target, relation).await })
        },
    )
    .description(format!("Get {}", model.name));

    list_args(field, model)
}

fn summary_field(name: &str, model: &Arc<DataModel>, relation: Option<RelationKeys>) -> Field {
    Field::new(name, TypeRef::named(summary_type_name(model)), move |ctx| {
        FieldFuture::Value(resolve::summary(&ctx, relation.as_ref()))
    })
    .description(format!("Get {}", model.name))
    .argument(InputValue::new("where", TypeRef::named(where_name(model))))
    .argument(InputValue::new(
        "orderBy",
        TypeRef::named(order_by_name(model)),
    ))
    .argument(InputValue::new(
        "groupBy",
        TypeRef::named_list(enum_fields_name(model)),
    ))
    .argument(InputValue::new(
        "distinct",
        TypeRef::named_list(enum_fields_name(model)),
    ))
    .argument(InputValue::new(
        "accessRole",
        TypeRef::named(TypeRef::STRING),
    ))
    .argument(InputValue::new("route", TypeRef::named(TypeRef::STRING)))
}

/// The summary object: count, sum, max, min, average and graph.
fn summary_object(app: &Yekonga, model: &Arc<DataModel>) -> Object {
    let enum_name = enum_fields_name(model);
    let aggregate = |name: &str, ty: &str, target: bool| {
        let op = name.to_string();
        let mut field = Field::new(name, TypeRef::named(ty), {
            let (app, model) = (app.clone(), model.clone());
            move |ctx| {
                let (app, model, op) = (app.clone(), model.clone(), op.clone());
                FieldFuture::new(async move { resolve::aggregate(&ctx, &app, &model, &op).await })
            }
        })
        .description(format!("Get {}", model.name));

        if target {
            field = field.argument(InputValue::new("targetKey", TypeRef::named(&enum_name)));
        }
        field
            .argument(InputValue::new("distinct", TypeRef::named_list(&enum_name)))
            .argument(InputValue::new("where", TypeRef::named(where_name(model))))
            .argument(InputValue::new(
                "accessRole",
                TypeRef::named(TypeRef::STRING),
            ))
            .argument(InputValue::new("route", TypeRef::named(TypeRef::STRING)))
    };

    Object::new(summary_type_name(model))
        .field(aggregate("count", TypeRef::FLOAT, false))
        .field(
            aggregate("sum", TypeRef::FLOAT, true)
                .argument(InputValue::new(
                    "productOf",
                    TypeRef::named_list(&enum_name),
                ))
                .argument(InputValue::new("formula", TypeRef::named(TypeRef::STRING))),
        )
        .field(aggregate("max", "Any", true))
        .field(aggregate("min", "Any", true))
        .field(aggregate("average", TypeRef::FLOAT, true))
        .field(graph_field(app, model))
}

fn graph_field(app: &Yekonga, model: &Arc<DataModel>) -> Field {
    let structured = structured_enum_name(model);
    let order_by = order_by_name(model);
    let named = |ty: &str| TypeRef::named(ty.to_string());

    let mut field = Field::new("graph", TypeRef::named("GraphDataType"), |_| {
        FieldFuture::new(async {
            Err::<Option<FieldValue>, _>(resolve::not_ported("summary graph"))
        })
    })
    .description(format!("Get {}", model.name))
    .argument(InputValue::new("where", named(&where_name(model))))
    .argument(InputValue::new("orderBy", named(&order_by)))
    .argument(InputValue::new(
        "targetKey",
        named(&enum_fields_name(model)),
    ))
    .argument(InputValue::new("type", named("GeneralGraphOptions")))
    .argument(InputValue::new("total", named("GeneralTotalOptions")))
    .argument(InputValue::new(
        "runningCalculation",
        named("GeneralTotalOptions"),
    ))
    .argument(InputValue::new("periodicity", named("GeneralPeriodicity")))
    .argument(InputValue::new(
        "dimension",
        TypeRef::named_nn(structured.clone()),
    ))
    .argument(InputValue::new(
        "dimensionSort",
        TypeRef::named_list(order_by.clone()),
    ))
    .argument(InputValue::new("dimensionBreakdown", named(&structured)))
    .argument(InputValue::new(
        "dimensionBreakdownSort",
        TypeRef::named_list(order_by),
    ))
    .argument(InputValue::new(
        "dimensionBreakdownPeriodicity",
        named("GeneralPeriodicity"),
    ))
    .argument(InputValue::new(
        "dimensionPeriodicity",
        named("GeneralPeriodicity"),
    ))
    .argument(InputValue::new("metric", named(&structured)))
    .argument(InputValue::new("metrics", TypeRef::named_list(structured)))
    .argument(InputValue::new("from", named("Date")))
    .argument(InputValue::new("to", named("Date")))
    .argument(InputValue::new("accessRole", named(TypeRef::STRING)))
    .argument(InputValue::new("route", named(TypeRef::STRING)));

    // Only when a related model exists, as in Go.
    if has_valid_relation(app, model) {
        let dimension = dimension_where_name(model);
        field = field
            .argument(InputValue::new("dimensionWhere", named(&dimension)))
            .argument(InputValue::new(
                "dimensionBreakdownWhere",
                named(&dimension),
            ));
    }
    field
}

fn download_field(name: &str, model: &Arc<DataModel>) -> Field {
    Field::new(name, TypeRef::named(download_type_name(model)), |_| {
        FieldFuture::new(async {
            Err::<Option<FieldValue>, _>(resolve::not_ported("download queries"))
        })
    })
    .description(format!("Download {}", model.name))
    .argument(InputValue::new(
        "download",
        TypeRef::named("GeneralDownloadTypeInput"),
    ))
    .argument(InputValue::new(
        "downloadType",
        TypeRef::named("DownloadTypeOptionsEnum"),
    ))
    .argument(InputValue::new(
        "orientation",
        TypeRef::named("OrientationOptionsEnum"),
    ))
    .argument(InputValue::new(
        "accessRole",
        TypeRef::named(TypeRef::STRING),
    ))
    .argument(InputValue::new("route", TypeRef::named(TypeRef::STRING)))
    .argument(InputValue::new(
        "flatKeys",
        TypeRef::named_list(TypeRef::STRING),
    ))
}

// ----- mutation fields --------------------------------------------------------------------------

fn role_args(field: Field) -> Field {
    field
        .argument(InputValue::new(
            "accessRole",
            TypeRef::named(TypeRef::STRING),
        ))
        .argument(InputValue::new("route", TypeRef::named(TypeRef::STRING)))
}

fn create_field(app: &Yekonga, name: &str, model: &Arc<DataModel>) -> Field {
    let (app, target) = (app.clone(), model.clone());
    role_args(
        Field::new(
            name,
            TypeRef::named(result_name("create", model)),
            move |ctx| {
                let (app, target) = (app.clone(), target.clone());
                FieldFuture::new(async move { resolve::create(&ctx, &app, &target).await })
            },
        )
        .argument(InputValue::new("input", TypeRef::named(input_name(model)))),
    )
}

fn update_field(app: &Yekonga, name: &str, model: &Arc<DataModel>) -> Field {
    let (app, target) = (app.clone(), model.clone());
    role_args(
        Field::new(
            name,
            TypeRef::named(result_name("update", model)),
            move |ctx| {
                let (app, target) = (app.clone(), target.clone());
                FieldFuture::new(async move { resolve::update(&ctx, &app, &target).await })
            },
        )
        .argument(InputValue::new("input", TypeRef::named(input_name(model))))
        .argument(InputValue::new("where", TypeRef::named(where_name(model)))),
    )
}

fn delete_field(app: &Yekonga, name: &str, model: &Arc<DataModel>) -> Field {
    let (app, target) = (app.clone(), model.clone());
    role_args(
        Field::new(
            name,
            TypeRef::named(result_name("delete", model)),
            move |ctx| {
                let (app, target) = (app.clone(), target.clone());
                FieldFuture::new(async move { resolve::delete(&ctx, &app, &target).await })
            },
        )
        .argument(InputValue::new("where", TypeRef::named(where_name(model)))),
    )
}

fn import_field(app: &Yekonga, name: &str, model: &Arc<DataModel>) -> Field {
    let (app, target) = (app.clone(), model.clone());
    role_args(
        Field::new(
            name,
            TypeRef::named(result_name("import", model)),
            move |ctx| {
                let (app, target) = (app.clone(), target.clone());
                FieldFuture::new(async move { resolve::import(&ctx, &app, &target).await })
            },
        )
        .argument(InputValue::new(
            "input",
            TypeRef::named_list(input_name(model)),
        ))
        .argument(InputValue::new(
            "uniqueKeys",
            TypeRef::named_list(enum_fields_name(model)),
        )),
    )
}

fn action_field(app: &Yekonga, name: &str, model: &Arc<DataModel>) -> Field {
    let (app, target) = (app.clone(), model.clone());
    role_args(
        Field::new(
            name,
            TypeRef::named(result_name("action", model)),
            move |ctx| {
                let (app, target) = (app.clone(), target.clone());
                FieldFuture::new(async move { resolve::action(&ctx, &app, &target).await })
            },
        )
        .argument(InputValue::new("where", TypeRef::named(where_name(model))))
        .argument(InputValue::new("input", TypeRef::named(input_name(model))))
        .argument(InputValue::new("inputData", TypeRef::named("Any")))
        .argument(InputValue::new("inputRaw", TypeRef::named("Any"))),
    )
    .argument(InputValue::new("action", TypeRef::named(TypeRef::STRING)))
}
