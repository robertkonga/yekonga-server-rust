//! The auth GraphQL API at `graphql.apiAuthRoute` (port of
//! `yekonga/graphql_auth.go` and the auth types in `graphql_types.go`), with
//! the same types and fields as the Go server.
//!
//! Ported: `profile`, `refreshToken`, `tenantAvailability`, `otp`, `login`
//! and `register`. The other mutations (`socialLogin`, `contactOTP`,
//! `contactVerify`, `resetPassword`, `confirmToken`, `changePassword`,
//! `switchAccount`) return "not supported by the Rust port yet"; in the Go
//! server they only look up a user and return it.

use std::future::Future;
use std::pin::Pin;

use async_graphql::dynamic::{
    Enum, Field, FieldFuture, FieldValue, InputObject, InputValue, Object, Scalar, Schema,
    SchemaError, TypeRef,
};
use async_graphql::{Error, Value as GqlValue};
use serde_json::{json, Map, Value};

use super::resolve::{args, not_ported};
use super::schema::json_object;
use super::{ExecutionData, Node};
use crate::app::{Yekonga, COOKIE_ENABLED_KEY};
use crate::auth::{bool_of, string_of, LoginAttempt, LoginData, OtpCheck};
use crate::db::values::is_empty;
use crate::helper::{format_phone, is_phone};
use crate::payload::TokenPayload;
use crate::request::Request;
use crate::response::Response;

/// What a resolver gets: its arguments and the HTTP exchange.
struct Call {
    app: Yekonga,
    args: Map<String, Value>,
    request: Option<Request>,
    response: Option<Response>,
}

impl Call {
    /// The `input` argument.
    fn input(&self) -> Map<String, Value> {
        match self.args.get("input") {
            Some(Value::Object(map)) => map.clone(),
            _ => Map::new(),
        }
    }

    fn arg(&self, name: &str) -> String {
        string_of(self.args.get(name))
    }

    fn request(&self) -> Result<&Request, Error> {
        self.request
            .as_ref()
            .ok_or_else(|| Error::new("this operation needs an HTTP request"))
    }
}

type Resolved = Pin<Box<dyn Future<Output = Result<Value, Error>> + Send>>;

/// A field whose resolver returns JSON: an object for the object types, or
/// a scalar.
fn field(
    app: &Yekonga,
    name: &str,
    ty: &str,
    arguments: &[(&str, &str)],
    resolve: fn(Call) -> Resolved,
) -> Field {
    let app = app.clone();
    let scalar = matches!(ty, "Boolean" | "String");
    let mut field = Field::new(name, TypeRef::named(ty), move |ctx| {
        let execution = ctx.data_opt::<ExecutionData>().cloned().unwrap_or_default();
        let call = Call {
            app: app.clone(),
            args: args(&ctx),
            request: execution.request,
            response: execution.response,
        };
        FieldFuture::new(async move {
            let value = resolve(call).await?;
            Ok(match value {
                Value::Null => None,
                value if scalar => Some(FieldValue::value(
                    GqlValue::from_json(value).unwrap_or(GqlValue::Null),
                )),
                value => Some(FieldValue::owned_any(Node::Json(value))),
            })
        })
    });
    for (arg, arg_type) in arguments {
        field = field.argument(InputValue::new(*arg, TypeRef::named(*arg_type)));
    }
    field
}

fn stub(what: &'static str) -> fn(Call) -> Resolved {
    // One function per stubbed field keeps `field` taking plain fn pointers.
    macro_rules! stubs {
        ($($name:literal),*) => {
            match what {
                $($name => |_| Box::pin(async { Err(not_ported($name)) }),)*
                _ => unreachable!(),
            }
        };
    }
    stubs!(
        "socialLogin",
        "contactOTP",
        "contactVerify",
        "resetPassword",
        "confirmToken",
        "changePassword",
        "switchAccount"
    )
}

fn input(name: &str, fields: &[(&str, &str)]) -> InputObject {
    fields
        .iter()
        .fold(InputObject::new(name), |object, (f, ty)| {
            object.field(InputValue::new(*f, TypeRef::named(*ty)))
        })
}

const PROFILE_FIELDS: &[(&str, &str)] = &[
    ("id", "ID"),
    ("userId", "ID"),
    ("username", "String"),
    ("usernameType", "String"),
    ("phone", "String"),
    ("phoneVerifiedAt", "Date"),
    ("phoneVerifyCode", "String"),
    ("email", "String"),
    ("emailVerifiedAt", "Date"),
    ("emailVerifyCode", "Boolean"),
    ("whatsapp", "String"),
    ("whatsappVerifiedAt", "Date"),
    ("whatsappVerifyCode", "String"),
    ("profileUrl", "String"),
    ("firstName", "String"),
    ("secondName", "String"),
    ("lastName", "String"),
    ("userType", "String"),
    ("dateOfBirth", "Date"),
    ("deviceToken", "String"),
    ("gender", "String"),
    ("googleToken", "String"),
    ("isEmailVerified", "Boolean"),
    ("isPhoneVerified", "Boolean"),
    ("isWhatsappVerified", "Boolean"),
    ("lastActive", "Date"),
    ("role", "String"),
    ("status", "String"),
    ("token", "String"),
    ("countryId", "String"),
    ("regionId", "String"),
    ("districtId", "String"),
    ("wardId", "String"),
    ("isActive", "Boolean"),
    ("isBanned", "Boolean"),
    ("isAdmin", "Boolean"),
    ("isManager", "Boolean"),
    ("owner", "Boolean"),
    ("profileId", "String"),
    ("profileRole", "String"),
    ("profileName", "String"),
    ("updatedAt", "Date"),
    ("createdAt", "Date"),
    ("deletedAt", "Date"),
];

fn object(name: &str, fields: &[(&str, &str)]) -> Object {
    let fields: Vec<(&str, TypeRef)> = fields
        .iter()
        .map(|(f, ty)| (*f, TypeRef::named(*ty)))
        .collect();
    json_object(name, &fields)
}

/// Builds the auth schema. `login` and `refreshToken` return a
/// `CredentialToken` when `secureAuthentication` is set, otherwise a
/// `Profile`.
pub fn build_auth_schema(app: &Yekonga) -> Result<Schema, SchemaError> {
    let token_type = if app.config().secure_authentication {
        "CredentialToken"
    } else {
        "Profile"
    };
    let username_type = &[("input", "OtpInput"), ("type", "UsernameIdentifier")];

    let query = Object::new("Query")
        .field(field(
            app,
            "profile",
            "Profile",
            &[("input", "ProfileInput")],
            |c| Box::pin(profile(c)),
        ))
        .field(field(
            app,
            "refreshToken",
            token_type,
            &[("refreshToken", "String"), ("moduleName", "String")],
            |c| Box::pin(refresh_token(c)),
        ))
        .field(field(
            app,
            "tenantAvailability",
            "Boolean",
            &[
                ("id", "String"),
                ("domain", "String"),
                ("subdomain", "String"),
                ("customDomain", "String"),
                ("customSubdomain", "String"),
            ],
            |c| Box::pin(tenant_availability(c)),
        ));

    let mutation = Object::new("Mutation")
        .field(field(app, "otp", "ActionResponse", username_type, |c| {
            Box::pin(otp(c))
        }))
        .field(field(
            app,
            "login",
            token_type,
            &[
                ("input", "LoginInput"),
                ("moduleName", "String"),
                ("type", "UsernameIdentifier"),
            ],
            |c| Box::pin(login(c)),
        ))
        .field(field(
            app,
            "register",
            "ActionResponse",
            &[("input", "RegistrationInput")],
            |c| Box::pin(register(c)),
        ))
        .field(field(
            app,
            "socialLogin",
            "Profile",
            &[("input", "SocialLoginInput")],
            stub("socialLogin"),
        ))
        .field(field(
            app,
            "contactOTP",
            "ActionResponse",
            &[("input", "ConcatOtpInput"), ("type", "UsernameIdentifier")],
            stub("contactOTP"),
        ))
        .field(field(
            app,
            "contactVerify",
            "ActionResponse",
            &[
                ("input", "ContactVerifyInput"),
                ("type", "UsernameIdentifier"),
            ],
            stub("contactVerify"),
        ))
        .field(field(
            app,
            "resetPassword",
            "ActionResponse",
            &[("input", "ResetPasswordInput")],
            stub("resetPassword"),
        ))
        .field(field(
            app,
            "confirmToken",
            "ConfirmTokenResponse",
            &[("input", "ConfirmTokenInput")],
            stub("confirmToken"),
        ))
        .field(field(
            app,
            "changePassword",
            "ActionResponse",
            &[("input", "ChangePasswordInput")],
            stub("changePassword"),
        ))
        .field(field(
            app,
            "switchAccount",
            "Profile",
            &[
                ("profileId", "String"),
                ("userId", "String"),
                ("token", "String"),
            ],
            stub("switchAccount"),
        ));

    let mut builder = Schema::build("Query", Some("Mutation"), None)
        .register(query)
        .register(mutation)
        .register(Scalar::new("Date").description("Custom scalar type for Date"))
        .register(Scalar::new("Any").description("Custom scalar type for Any"))
        .register(Enum::new("UsernameIdentifier").items(["phone", "email", "whatsapp"]))
        .register(Enum::new("LoginTypeEnum").items(["PASSWORD", "OTP", "REGISTRATION", "NORMAL"]))
        .register(Enum::new("OrganizationTypeEnum").items(["NGO", "COMPANY", "PRIVATE"]))
        .register(object(
            "ActionResponse",
            &[
                ("status", "Boolean"),
                ("message", "String"),
                ("data", "Any"),
            ],
        ))
        .register(object(
            "ConfirmTokenResponse",
            &[
                ("status", "Boolean"),
                ("email", "String"),
                ("token", "String"),
            ],
        ))
        .register(object("Profile", PROFILE_FIELDS))
        .register(input(
            "ProfileInput",
            &[
                ("id", "String"),
                ("username", "String"),
                ("usernameType", "String"),
                ("phone", "String"),
                ("email", "String"),
            ],
        ))
        .register(input(
            "OtpInput",
            &[
                ("usernameType", "UsernameIdentifier"),
                ("username", "String"),
            ],
        ))
        .register(input(
            "LoginInput",
            &[
                ("usernameType", "UsernameIdentifier"),
                ("username", "String"),
                ("password", "String"),
                ("type", "LoginTypeEnum"),
                ("moduleName", "String"),
                ("rememberMe", "Boolean"),
            ],
        ))
        .register(input(
            "SocialLoginInput",
            &[
                ("authUser", "Any"),
                ("expiresIn", "Int"),
                ("prompt", "String"),
                ("scope", "String"),
                ("accessToken", "String"),
                ("tokenType", "String"),
            ],
        ))
        .register(input(
            "ConcatOtpInput",
            &[
                ("userId", "String"),
                ("phone", "String"),
                ("email", "String"),
                ("whatsapp", "String"),
            ],
        ))
        .register(input(
            "ContactVerifyInput",
            &[
                ("userId", "String"),
                ("phone", "String"),
                ("email", "String"),
                ("whatsapp", "String"),
                ("password", "String"),
                ("type", "String"),
            ],
        ))
        .register(input("ResetPasswordInput", &[("username", "String")]))
        .register(input("ConfirmTokenInput", &[("token", "String")]))
        .register(input(
            "ChangePasswordInput",
            &[
                ("token", "String"),
                ("password", "String"),
                ("passwordConfirmation", "String"),
            ],
        ))
        .register(input(
            "RegistrationInput",
            &[
                ("userId", "String"),
                ("firstName", "String"),
                ("lastName", "String"),
                ("organization", "String"),
                ("description", "String"),
                ("logoUrl", "String"),
                ("type", "OrganizationTypeEnum"),
                ("address", "String"),
                ("email", "String"),
                ("phone", "String"),
                ("website", "String"),
                ("domain", "String"),
                ("subdomain", "String"),
                ("language", "String"),
                ("isApproved", "Boolean"),
                ("status", "String"),
            ],
        ));

    if app.config().secure_authentication {
        builder = builder.register(object(
            "CredentialToken",
            &[("accessToken", "ID"), ("refreshToken", "String")],
        ));
    }

    builder.finish()
}

// ----- resolvers -----------------------------------------------------------------------------

/// The signed-in user's login data, with a new access token.
async fn profile(call: Call) -> Result<Value, Error> {
    let req = call.request()?;
    let Some(auth) = req.auth().filter(|a| !a.id.is_empty()) else {
        return Err(Error::new("Not authorized"));
    };

    let user = call
        .app
        .get_login_data(
            req,
            &LoginData {
                user_id: auth.id,
                profile_id: auth.profile_id,
                ..Default::default()
            },
        )
        .await;
    Ok(Value::Object(user))
}

/// Exchanges a refresh token for new tokens (only with
/// `secureAuthentication`; otherwise null).
async fn refresh_token(call: Call) -> Result<Value, Error> {
    if !call.app.config().secure_authentication {
        return Ok(Value::Null);
    }
    let req = call.request()?;
    let res = call
        .response
        .clone()
        .ok_or_else(|| Error::new("this operation needs an HTTP request"))?;

    let (result, status) = call
        .app
        .refresh_token_process(
            req,
            &res,
            &call.arg("refreshToken"),
            &call.arg("moduleName"),
        )
        .await;
    match (status, result.get("error")) {
        (200, _) => Ok(Value::Object(result)),
        (_, Some(error)) => Err(Error::new(string_of(Some(error)))),
        _ => Ok(Value::Null),
    }
}

/// Whether a tenant with any of the given id, domain or subdomains exists.
async fn tenant_availability(call: Call) -> Result<Value, Error> {
    let Ok(query) = call.app.query("Tenant") else {
        return Ok(json!(false));
    };
    let mut query = query.skip_before_commit();
    let mut valid = false;

    let id = call.arg("id");
    if !id.is_empty() {
        query = query.where_("id", id);
        valid = true;
    }

    let any: Vec<Value> = ["domain", "subdomain", "customDomain", "customSubdomain"]
        .iter()
        .map(|key| (key, call.arg(key)))
        .filter(|(_, value)| !value.is_empty())
        .map(|(key, value)| json!({ *key: {"equalTo": value} }))
        .collect();
    if !any.is_empty() {
        query = query.where_many(json!({"OR": any}));
        valid = true;
    }

    if !valid {
        return Ok(json!(false));
    }
    let found = query
        .find_one()
        .await
        .map_err(|e| Error::new(e.to_string()))?;
    Ok(json!(found.is_some()))
}

/// Sends an OTP code (Go's `otp`). With a tenant, only its users and owner
/// get one, unless the tenant's `publicCanRegister` is set.
async fn otp(call: Call) -> Result<Value, Error> {
    let req = call.request()?;
    let app = &call.app;
    let input = call.input();
    let tenant_config = req.tenant_config().unwrap_or_default();
    let public_can_register = bool_of(tenant_config.get("publicCanRegister"));
    let tenant_owner = string_of(tenant_config.get("userId"));

    let mut username = string_of(input.get("username"));
    let username_type = string_of(input.get("usernameType"));
    if is_phone(&username) {
        username = format_phone(&username);
    }

    let mut user = None;
    let mut user_id = String::new();
    if let Some(tenant_id) = req.tenant_id() {
        if !username.is_empty() {
            user = app
                .set_otp_verification(
                    &json!(username),
                    &username_type,
                    public_can_register,
                    "login",
                    req,
                )
                .await;
            user_id = user
                .as_ref()
                .map(|u| string_of(u.get("userId")))
                .unwrap_or_default();
        }

        if !user_id.is_empty() {
            let mut tenant_user = user_id == tenant_owner;
            if !tenant_user {
                if let Ok(q) = app.query("TenantUser") {
                    tenant_user = q
                        .skip_tenant()
                        .skip_before_commit()
                        .where_("tenantId", tenant_id)
                        .where_("userId", user_id.as_str())
                        .exists()
                        .await
                        .unwrap_or(false);
                }
            }
            if !tenant_user && !public_can_register {
                return Err(Error::new("User does not exist"));
            }
        } else if !public_can_register {
            return Err(Error::new("User does not exist at all"));
        }
    } else if !username.is_empty() {
        user = app
            .set_otp_verification(&json!(username), &username_type, true, "login", req)
            .await;
        user_id = user
            .as_ref()
            .map(|u| string_of(u.get("userId")))
            .unwrap_or_default();
    }

    app.record_login_attempt(
        "otp",
        req,
        &LoginAttempt {
            user_id,
            username,
            ..Default::default()
        },
    )
    .await;

    Ok(match user {
        Some(_) => json!({"status": true, "message": "Success", "data": null}),
        None => json!({"status": false, "message": "You have no access", "data": null}),
    })
}

/// Go's enum values for `LoginTypeEnum` and `OrganizationTypeEnum` are the
/// names in lower case.
fn enum_value(value: Option<&Value>) -> String {
    string_of(value).to_lowercase()
}

/// Signs in with a password or an OTP code (Go's `login`), and returns the
/// user's login data with an access token and a refresh token, which are
/// also set as cookies.
async fn login(call: Call) -> Result<Value, Error> {
    let req = call.request()?;
    let app = &call.app;
    let input = call.input();

    let mut attempt = LoginAttempt {
        username: string_of(input.get("username")),
        username_type: string_of(input.get("usernameType")),
        password: string_of(input.get("password")),
        login_type: enum_value(input.get("type")),
        remember_me: bool_of(input.get("rememberMe")),
        module_name: string_of(input.get("moduleName")),
        ..Default::default()
    };
    if is_phone(&attempt.username) {
        attempt.username = format_phone(&attempt.username);
    }

    // The OTP code is checked once, here: Go checks it again in
    // AttemptLogin, after the first check cleared it when resetOTP is set.
    let otp = if attempt.username.is_empty() {
        OtpCheck::default()
    } else {
        app.otp_verification(&attempt.username, &attempt.password, true, req)
            .await
    };

    let failed = LoginAttempt {
        username: attempt.username.clone(),
        username_type: attempt.username_type.clone(),
        login_type: attempt.login_type.clone(),
        ..Default::default()
    };

    if otp.go_result().is_some() {
        match app.attempt_login(req, &attempt, &otp).await {
            Ok(Some(mut user)) => {
                let user_id = string_of(user.get("id"));
                attempt.user_id = user_id.clone();
                attempt.profile_id = string_of(user.get("profileId"));
                app.record_login_attempt("success", req, &attempt).await;

                let access_token = string_of(user.get("token"));
                let tenant_id = string_of(user.get("tenantId"));
                let client = req.client().unwrap_or_default();
                let payload = TokenPayload {
                    domain: client.origin_domain(),
                    tenant_id: json!(tenant_id),
                    profile_id: attempt.profile_id.clone(),
                    user_id: user_id.clone(),
                    admin_id: string_of(user.get("adminId")),
                    username: string_of(user.get("username")),
                    username_type: string_of(user.get("usernameType")),
                    phone: string_of(user.get("phone")),
                    email: string_of(user.get("email")),
                    whatsapp: string_of(user.get("whatsapp")),
                    module_name: attempt.module_name.clone(),
                    roles: Vec::new(),
                    permissions: app
                        .get_user_permission(&json!(tenant_id), &user_id, &attempt.module_name)
                        .await,
                    expires_at: None,
                };
                let refresh_token = app
                    .create_refresh_token(&client, &payload, attempt.remember_me)
                    .await;

                if req
                    .cookie(COOKIE_ENABLED_KEY)
                    .unwrap_or_default()
                    .is_empty()
                {
                    user.insert("accessToken".into(), json!(access_token));
                    user.insert("refreshToken".into(), json!(refresh_token));
                } else {
                    user.insert("accessToken".into(), json!("Cookie is set"));
                    user.insert("refreshToken".into(), json!("Cookie is set"));
                }
                if let Some(res) = &call.response {
                    app.set_auth_cookies(req, res, &access_token, &refresh_token);
                }

                return Ok(Value::Object(user));
            }
            Err(message) => {
                app.record_login_attempt("fail", req, &failed).await;
                return Err(Error::new(message));
            }
            Ok(None) => {}
        }
    }

    app.record_login_attempt("fail", req, &failed).await;
    Err(Error::new("Wrong credential"))
}

/// Registers an organization (a Tenant) for the signed-in user (Go's
/// `register`), and fills in the user's names.
async fn register(call: Call) -> Result<Value, Error> {
    let req = call.request()?;
    let app = &call.app;
    let Some(auth) = req.auth().filter(|a| !a.id.is_empty()) else {
        return Err(Error::new("Not authorized"));
    };
    let tenants = app
        .query("Tenant")
        .map_err(|e| Error::new(e.to_string()))?
        .skip_before_commit()
        .set_request(req);

    let mut input = call.input();
    input.insert("userId".into(), json!(auth.id));
    input.insert(
        "name".into(),
        input.get("organization").cloned().unwrap_or(Value::Null),
    );
    if let Some(kind) = input.get("type").filter(|v| !v.is_null()) {
        input.insert("type".into(), json!(enum_value(Some(kind))));
    }
    if is_empty(input.get("language").unwrap_or(&Value::Null)) {
        input.insert("language".into(), json!("en"));
    }

    let tenant = tenants
        .create(Value::Object(input.clone()))
        .await
        .map_err(|e| Error::new(e.to_string()))?;

    if let Ok(users) = app.query("User") {
        let names = json!({"firstName": input.get("firstName"), "lastName": input.get("lastName")});
        if let Err(err) = users
            .skip_before_commit()
            .where_("id", auth.id.as_str())
            .update(names)
            .await
        {
            tracing::error!(%err, "user update failed");
        }
    }

    let data: Map<String, Value> = [
        ("id", "_id"),
        ("name", "name"),
        ("description", "description"),
        ("logoUrl", "logoUrl"),
        ("email", "email"),
        ("phone", "phone"),
        ("whatsapp", "whatsapp"),
        ("type", "type"),
        ("domain", "domain"),
        ("subdomain", "subdomain"),
        ("status", "status"),
    ]
    .iter()
    .map(|(key, from)| {
        (
            key.to_string(),
            tenant.get(*from).cloned().unwrap_or(Value::Null),
        )
    })
    .collect();

    Ok(json!({"status": true, "message": "SUCCESS", "data": data}))
}
