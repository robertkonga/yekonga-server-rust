//! Sign-in for authorization servers (`isAuthorizationServer`): OTP codes,
//! password and OTP login, access and refresh tokens, auth cookies and
//! permissions. Ports of the functions in `yekonga/main_function.go` and the
//! `/me`, `/logout` and `/refresh` handlers in `yekonga/initializer.go`.
//!
//! The GraphQL side (`graphql.apiAuthRoute`) is in `graphql/auth.rs`.

use std::sync::Arc;

use chrono::{Duration, Utc};
use serde_json::{json, Map, Value};

use crate::app::{Yekonga, COOKIE_ENABLED_KEY};
use crate::db::values::{format_datetime, is_empty, parse_datetime, ZERO_OBJECT_ID};
use crate::db::{DataMap, DbError};
use crate::helper::{
    format_phone, get_base_url, hash_refresh_token, is_email, is_phone, jwt, random_digits,
    random_string,
};
use crate::payload::{ClientPayload, TokenPayload};
use crate::query::ModelQuery;
use crate::request::{keys, Request};
use crate::response::Response;

const USER_MODEL: &str = "User";
const PROFILE_MODEL: &str = "Profile";
const PROFILE_USER_MODEL: &str = "ProfileUser";
const TENANT_MODEL: &str = "Tenant";
const TENANT_USER_MODEL: &str = "TenantUser";
const USER_VERIFICATION_MODEL: &str = "UserVerification";
const LOGIN_ATTEMPT_MODEL: &str = "LoginAttempt";
const REFRESH_TOKEN_MODEL: &str = "RefreshToken";
const PERMISSION_MODEL: &str = "AuthPermission";
const USER_PERMISSION_MODEL: &str = "AuthUserPermission";

/// The user fields a login returns (Go's `publicKeys` in `GetLoginData`).
const PUBLIC_KEYS: &[&str] = &[
    "id",
    "dateOfBirth",
    "email",
    "firstName",
    "gender",
    "isActive",
    "isBanned",
    "isEmailVerified",
    "isPhoneVerified",
    "isWhatsappVerified",
    "lastName",
    "phone",
    "profileUrl",
    "role",
    "secondName",
    "status",
    "tenantId",
    "token",
    "userType",
    "username",
    "usernameType",
    "whatsapp",
    "owner",
    "profileRole",
    "profileName",
    "profileId",
    "isAdmin",
    "isManager",
    "additionalFields",
    "permissions",
];

/// An OTP code to deliver to a user.
#[derive(Clone, Debug)]
pub struct OtpMessage {
    pub user_id: String,
    pub username: String,
    /// `"phone"`, `"whatsapp"` or `"email"`, or `""` when the username is
    /// neither a phone number nor an email address.
    pub channel: String,
    pub code: String,
    /// The message text (Go's default `otp` text template).
    pub text: String,
}

pub(crate) type OtpSender = Arc<dyn Fn(OtpMessage) + Send + Sync>;

/// A login request (Go's `AttemptData`).
#[derive(Clone, Debug, Default)]
pub struct LoginAttempt {
    pub username: String,
    pub username_type: String,
    pub password: String,
    /// `password`, `otp`, `registration` or `normal`.
    pub login_type: String,
    pub remember_me: bool,
    pub module_name: String,
    pub user_id: String,
    pub profile_id: String,
}

/// Whose login data to build (Go's `LoginData`).
#[derive(Clone, Debug, Default)]
pub struct LoginData {
    pub user_id: String,
    pub profile_id: String,
    pub module_name: String,
}

/// The result of checking an OTP code.
#[derive(Clone, Debug, Default)]
pub struct OtpCheck {
    /// The UserVerification record for the username.
    pub record: Option<DataMap>,
    /// The code matched.
    pub matched: bool,
    /// The user the code signs in (found or created) when it matched.
    pub user: Option<DataMap>,
}

impl OtpCheck {
    /// What Go's `OTPVerification` returns: the user when the code matched,
    /// otherwise the verification record.
    pub(crate) fn go_result(&self) -> Option<&DataMap> {
        if self.matched {
            self.user.as_ref()
        } else {
            self.record.as_ref()
        }
    }
}

// ----- small helpers ---------------------------------------------------------------------

/// Go's `GetValueOfString`.
pub(crate) fn string_of(value: Option<&Value>) -> String {
    match value {
        None | Some(Value::Null) => String::new(),
        Some(Value::String(s)) => s.clone(),
        Some(Value::Bool(b)) => b.to_string(),
        Some(Value::Number(n)) => n.to_string(),
        Some(other) => other.to_string(),
    }
}

/// Go's `GetValueOfBoolean`.
pub(crate) fn bool_of(value: Option<&Value>) -> bool {
    match value {
        Some(Value::Bool(b)) => *b,
        Some(Value::String(s)) => matches!(s.as_str(), "true" | "1"),
        Some(Value::Number(n)) => n.as_f64().is_some_and(|n| n != 0.0),
        _ => false,
    }
}

fn get(map: &DataMap, key: &str) -> String {
    string_of(map.get(key))
}

/// `value`, or null when it's empty (Go stores `nil` for empty ids).
fn or_null(value: &str) -> Value {
    if value.is_empty() {
        Value::Null
    } else {
        json!(value)
    }
}

/// Logs a failed database call and carries on with an empty result, as the
/// Go functions do.
fn logged<T: Default>(result: Result<T, DbError>, what: &str) -> T {
    result.unwrap_or_else(|err| {
        tracing::error!(%err, "{what} failed");
        T::default()
    })
}

/// A time as the database stores it.
fn time_value(at: chrono::DateTime<Utc>) -> Value {
    json!(format_datetime(at))
}

fn domain_of(req: &Request) -> String {
    req.client().map(|c| c.origin_domain()).unwrap_or_default()
}

impl Yekonga {
    /// A query on a model the auth functions use, or `None` when the model
    /// isn't part of the schema.
    fn auth_query(&self, model: &str) -> Option<ModelQuery> {
        self.query(model).ok().map(ModelQuery::skip_before_commit)
    }

    /// Delivers OTP codes. The Go server sends them through its SMS,
    /// WhatsApp and mail gateways, which are not ported yet; without a
    /// sender, codes are stored but not delivered.
    pub fn set_otp_sender(&self, sender: impl Fn(OtpMessage) + Send + Sync + 'static) {
        *self
            .otp_sender_slot()
            .write()
            .unwrap_or_else(|e| e.into_inner()) = Some(Arc::new(sender));
    }

    fn send_otp(&self, message: OtpMessage) {
        let sender = self
            .otp_sender_slot()
            .read()
            .unwrap_or_else(|e| e.into_inner())
            .clone();
        match sender {
            Some(sender) => sender(message),
            None => tracing::warn!(
                username = %message.username,
                "OTP code created but not sent: no OTP sender is set (Yekonga::set_otp_sender)"
            ),
        }
    }

    /// The access token lifetime, in minutes (default 15).
    fn access_token_minutes(&self) -> i64 {
        match self.config().access_token_expire_time {
            n if n > 0 => n,
            _ => 15,
        }
    }

    /// The refresh token lifetime, in days (default 7).
    fn refresh_token_days(&self) -> i64 {
        match self.config().refresh_token_expire_time {
            n if n > 0 => n,
            _ => 7,
        }
    }

    // ----- OTP ---------------------------------------------------------------------------

    /// Checks an OTP code for `username` (Go's `OTPVerification`). A matching
    /// code is marked verified (and cleared when `resetOTP` is set), and
    /// signs in the verification's user, who is created when `can_create`
    /// is set or the user owns the request's tenant.
    pub async fn otp_verification(
        &self,
        username: &str,
        password: &str,
        can_create: bool,
        req: &Request,
    ) -> OtpCheck {
        let mut check = OtpCheck::default();
        let Some(verifications) = self.auth_query(USER_VERIFICATION_MODEL) else {
            return check;
        };

        let tenant_owner = req
            .tenant_config()
            .map(|c| get(&c, "userId"))
            .unwrap_or_default();
        let mut is_owner = false;
        if let Some(users) = self.auth_query(USER_MODEL) {
            let default_user = logged(
                users
                    .skip_tenant()
                    .set_request(req)
                    .where_("username", username)
                    .find_one()
                    .await,
                "user lookup",
            );
            if let Some(user) = default_user {
                is_owner = get(&user, "_id") == tenant_owner;
            }
        }

        let tenant_id = req.tenant_id().unwrap_or(json!(ZERO_OBJECT_ID));
        check.record = logged(
            verifications
                .clone()
                .skip_tenant()
                .set_request(req)
                .where_("tenantId", tenant_id)
                .where_("username", username)
                .find_one()
                .await,
            "OTP lookup",
        );

        let Some(record) = &check.record else {
            return check;
        };
        let otp_code = get(record, "otpCode");
        if password.is_empty() || otp_code != password {
            return check;
        }

        check.matched = true;
        let now = time_value(Utc::now());
        let code = if self.config().reset_otp {
            Value::Null
        } else {
            json!(otp_code)
        };
        logged(
            verifications
                .set_request(req)
                .where_("username", username)
                .update(json!({"otpCode": code, "otpVerifiedAt": now.clone(), "updatedAt": now}))
                .await,
            "OTP update",
        );

        let user = self
            .get_user(&Value::Object(record.clone()), is_owner || can_create)
            .await;
        check.user = (!user.is_empty()).then_some(user);
        check
    }

    /// Creates or refreshes the OTP code for a username and sends it (Go's
    /// `SetOTPVerification`). `value` is the username, or a map with
    /// `username`, `phone` or `email`. A new code is only created for
    /// existing tenant users, the tenant's owner, or when `can_create` is
    /// set or the request has no tenant. Returns the UserVerification record.
    pub async fn set_otp_verification(
        &self,
        value: &Value,
        username_type: &str,
        can_create: bool,
        target: &str,
        req: &Request,
    ) -> Option<DataMap> {
        let mut username_type = username_type.to_string();
        let username = match value {
            Value::String(s) => s.clone(),
            Value::Object(map) => {
                let mut username = map
                    .get("username")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                for key in ["phone", "email"] {
                    if let Some(Value::String(v)) = map.get(key) {
                        username = v.clone();
                        username_type = key.into();
                    }
                }
                username
            }
            _ => String::new(),
        };
        if username.is_empty() {
            return None;
        }

        let verifications = self.auth_query(USER_VERIFICATION_MODEL)?;
        let default_user = match self.auth_query(USER_MODEL) {
            Some(users) => logged(
                users
                    .skip_tenant()
                    .set_request(req)
                    .where_("username", username.as_str())
                    .find_one()
                    .await,
                "user lookup",
            ),
            None => None,
        };

        let no_tenant = req.tenant_id().is_none();
        let tenant_id = req.tenant_id().unwrap_or(json!(ZERO_OBJECT_ID));
        let tenant_owner = req
            .tenant_config()
            .map(|c| get(&c, "userId"))
            .unwrap_or_default();
        let user_id = default_user
            .as_ref()
            .map(|u| get(u, "_id"))
            .unwrap_or_default();
        let is_owner = default_user.is_some() && user_id == tenant_owner;

        let existing = logged(
            verifications
                .clone()
                .skip_tenant()
                .set_request(req)
                .where_("username", username.as_str())
                .where_("tenantId", tenant_id.clone())
                .find_one()
                .await,
            "OTP lookup",
        );
        let is_tenant_user = match self.auth_query(TENANT_USER_MODEL) {
            Some(q) if !user_id.is_empty() => logged(
                q.skip_tenant()
                    .set_request(req)
                    .where_("tenantId", tenant_id.clone())
                    .where_("userId", user_id.as_str())
                    .exists()
                    .await,
                "tenant user lookup",
            ),
            _ => false,
        };

        let now = time_value(Utc::now());
        let record = if let Some(existing) = existing {
            let mut code = get(&existing, "otpCode");
            let mut created_at = existing.get("otpCreatedAt").cloned().unwrap_or(Value::Null);
            if code.is_empty() {
                code = random_digits(4);
                created_at = now.clone();
            }

            let mut body = json!({
                "userId": or_null(&user_id),
                "usernameType": username_type,
                "target": target,
                "otpCode": code,
                "otpCreatedAt": created_at,
                "updatedAt": now,
            });
            if !no_tenant {
                body["tenantId"] = tenant_id;
            }
            logged(
                verifications
                    .skip_tenant()
                    .set_request(req)
                    .where_("id", get(&existing, "_id"))
                    .update(body)
                    .await,
                "OTP update",
            )
        } else if is_owner || is_tenant_user || can_create || no_tenant {
            let body = json!({
                "tenantId": tenant_id,
                "userId": or_null(&user_id),
                "username": username,
                "usernameType": username_type,
                "target": target,
                "otpCode": random_digits(4),
                "otpCreatedAt": now.clone(),
                "updatedAt": now.clone(),
                "createdAt": now,
            });
            logged(
                verifications
                    .skip_tenant()
                    .set_request(req)
                    .create(body)
                    .await
                    .map(Some),
                "OTP create",
            )
        } else {
            None
        };

        if let Some(record) = &record {
            let code = get(record, "otpCode");
            let channel = if is_phone(&username) {
                if username_type == "whatsapp" {
                    "whatsapp"
                } else {
                    "phone"
                }
            } else if is_email(&username) {
                "email"
            } else {
                ""
            };
            self.send_otp(OtpMessage {
                user_id: get(record, "userId"),
                username: username.clone(),
                channel: channel.into(),
                text: format!(
                    "{code} is your verification code. For security, do not share this code."
                ),
                code,
            });
        }

        record
    }

    // ----- users -------------------------------------------------------------------------

    /// Finds a user (Go's `GetUser`) by `userId`, or by `username` (a phone
    /// number is normalized first), creating it when `can_create` is set.
    /// `value` is a username or a map with `userId`, `username`,
    /// `usernameType`, names, `phone`, `email`, and optionally `tenantId`,
    /// `moduleName` and `permissions` to grant. Missing names, email and
    /// phone are filled in, and the user gets a profile if they have none.
    /// Returns an empty map when there is no such user.
    pub async fn get_user(&self, value: &Value, can_create: bool) -> DataMap {
        let Some(users) = self.auth_query(USER_MODEL) else {
            return DataMap::new();
        };

        let mut username_type = "phone".to_string();
        let (mut username, mut user_id, mut tenant_id, mut module_name) =
            (String::new(), String::new(), String::new(), String::new());
        let mut fields: [(&str, String); 5] = [
            ("firstName", String::new()),
            ("secondName", String::new()),
            ("lastName", String::new()),
            ("email", String::new()),
            ("phone", String::new()),
        ];
        let mut permissions: Option<Vec<Value>> = None;

        match value {
            Value::String(s) => username = s.clone(),
            Value::Object(map) => {
                if let Some(Value::Array(list)) = map.get("permissions") {
                    if !list.is_empty() {
                        permissions = Some(list.clone());
                    }
                }
                user_id = get(map, "userId");
                tenant_id = get(map, "tenantId");
                username = get(map, "username");
                username_type = get(map, "usernameType");
                module_name = get(map, "moduleName");
                for (key, field) in fields.iter_mut() {
                    *field = get(map, key);
                }
            }
            _ => {}
        }

        let mut user = None;
        if !user_id.is_empty() {
            user = logged(
                users
                    .clone()
                    .where_("id", user_id.as_str())
                    .find_one()
                    .await,
                "user lookup",
            );
        } else if !username.is_empty() {
            if is_email(&username) {
                username_type = "email".into();
            } else if is_phone(&username) {
                username_type = "phone".into();
                username = format_phone(&username);
            }

            user = logged(
                users
                    .clone()
                    .where_("username", username.as_str())
                    .find_one()
                    .await,
                "user lookup",
            );

            if user.is_none() && can_create {
                let now = time_value(Utc::now());
                let mut data = json!({
                    "usernameType": username_type,
                    "username": username,
                    "role": "user",
                    "status": "active",
                    "isActive": true,
                    "userType": "individual",
                    "updatedAt": now.clone(),
                    "createdAt": now,
                });
                for (key, field) in &fields {
                    data[*key] = json!(field);
                }
                user = logged(users.clone().create(data).await.map(Some), "user create");
            }
        }

        let Some(user) = user else {
            return DataMap::new();
        };
        let user_id = get(&user, "id");

        if let Some(permissions) = permissions {
            self.set_user_permission(&json!(tenant_id), &user_id, &module_name, &permissions)
                .await;
        }

        let missing: Map<String, Value> = fields
            .iter()
            .filter(|(key, field)| !field.is_empty() && get(&user, key).is_empty())
            .map(|(key, field)| (key.to_string(), json!(field)))
            .collect();
        if !missing.is_empty() {
            logged(
                users
                    .where_("id", user_id.as_str())
                    .update(Value::Object(missing))
                    .await,
                "user update",
            );
        }

        if let Some(profiles) = self.auth_query(PROFILE_MODEL) {
            let profile = logged(
                profiles
                    .clone()
                    .where_("userId", user_id.as_str())
                    .find_one()
                    .await,
                "profile lookup",
            );
            if profile.is_none() {
                let now = time_value(Utc::now());
                logged(
                    profiles
                        .create(json!({
                            "userId": user_id,
                            "name": "Private Profile",
                            "updatedAt": now.clone(),
                            "createdAt": now,
                        }))
                        .await
                        .map(Some),
                    "profile create",
                );
            }
        }

        user
    }

    /// The ids of the profiles a user owns or belongs to.
    async fn profile_ids(&self, user_id: &str) -> Vec<String> {
        let mut ids = Vec::new();
        for (model, key) in [(PROFILE_MODEL, "id"), (PROFILE_USER_MODEL, "profileId")] {
            if let Some(q) = self.auth_query(model) {
                let records = logged(q.where_("userId", user_id).find().await, "profile lookup");
                ids.extend(records.iter().map(|r| get(r, key)));
            }
        }
        ids
    }

    /// Records a login attempt (`status` is `otp`, `success` or `fail`).
    pub(crate) async fn record_login_attempt(
        &self,
        status: &str,
        req: &Request,
        attempt: &LoginAttempt,
    ) {
        if let Some(q) = self.auth_query(LOGIN_ATTEMPT_MODEL) {
            logged(
                q.create(json!({
                    "domain": domain_of(req),
                    "profileId": or_null(&attempt.profile_id),
                    "userId": or_null(&attempt.user_id),
                    "username": attempt.username,
                    "status": status,
                    "timestamp": time_value(Utc::now()),
                }))
                .await
                .map(Some),
                "login attempt",
            );
        }
    }

    /// Checks a login (Go's `AttemptLogin`) and returns the login data.
    ///
    /// OTP logins use `otp`, the result of checking the code. Other logins
    /// look the user up among active users and check the password against
    /// its bcrypt hash, or the configured `globalPassword`. `registration`
    /// logins need the OTP code or the password, and return no access token.
    /// `Ok(None)` means the credentials didn't match.
    pub async fn attempt_login(
        &self,
        req: &Request,
        attempt: &LoginAttempt,
        otp: &OtpCheck,
    ) -> Result<Option<DataMap>, String> {
        let config = self.config();
        let mut username = attempt.username.clone();
        let mut username_type = attempt.username_type.clone();

        if matches!(username_type.as_str(), "phone" | "whatsapp") {
            username = format_phone(&username);
        }
        let identifiers = &config.user_identifiers;
        if identifiers.is_empty() || identifiers.iter().any(|i| i == "username") {
            username_type = "username".into();
            if is_phone(&username) {
                username = format_phone(&username);
            }
        }
        if username_type.is_empty() {
            username_type = "username".into();
        }

        let (user, password_ok) = if attempt.login_type == "otp" {
            let user = if otp.matched { otp.user.clone() } else { None };
            if otp.record.is_none() || (otp.matched && user.is_none()) {
                (None, false)
            } else if let Some(user) = user {
                self.mark_otp_login(&user, &username_type).await;
                (Some(user), true)
            } else {
                return Ok(None);
            }
        } else {
            let user = match self.auth_query(USER_MODEL) {
                Some(q) => logged(
                    q.where_("status", json!({"in": [1, "active"]}))
                        .where_(&username_type, username.as_str())
                        .find_one()
                        .await,
                    "user lookup",
                ),
                None => None,
            };

            let password_ok = user.as_ref().is_some_and(|user| {
                let global = &config.global_password;
                let password_matches = !attempt.password.is_empty()
                    && bcrypt::verify(&attempt.password, &get(user, "password")).unwrap_or(false);

                (!global.is_empty() && attempt.password == *global)
                    || password_matches
                    || (attempt.login_type == "registration" && otp.matched)
            });
            (user, password_ok)
        };

        let Some(user) = user else {
            tracing::info!(username = %username, "login failed: user does not exist");
            return Err("User does not exists".into());
        };
        if bool_of(user.get("isBanned")) {
            return Err(format!("You are banned from accessing {}", config.app_name));
        }
        if !password_ok {
            return Ok(None);
        }

        let mut result = self
            .get_login_data(
                req,
                &LoginData {
                    user_id: get(&user, "id"),
                    module_name: attempt.module_name.clone(),
                    ..Default::default()
                },
            )
            .await;
        if result.is_empty() {
            return Ok(None);
        }
        if attempt.login_type == "registration" {
            result.insert("token".into(), Value::Null);
        }
        Ok(Some(result))
    }

    /// After an OTP login: the phone or email is verified, and the code is
    /// cleared when `resetOTP` is set.
    async fn mark_otp_login(&self, user: &DataMap, username_type: &str) {
        let now = time_value(Utc::now());
        let mut body = Map::new();
        if self.config().reset_otp {
            body.insert("otpCode".into(), Value::Null);
            body.insert("otpCreatedAt".into(), Value::Null);
        }
        for (kind, flag, at) in [
            ("phone", "isPhoneVerified", "phoneVerifiedAt"),
            ("email", "isEmailVerified", "emailVerifiedAt"),
        ] {
            if username_type == kind && !bool_of(user.get(flag)) {
                body.insert(at.into(), now.clone());
                body.insert(flag.into(), json!(true));
            }
        }

        if let (false, Some(q)) = (body.is_empty(), self.auth_query(USER_MODEL)) {
            logged(
                q.where_("id", get(user, "id"))
                    .update(Value::Object(body))
                    .await,
                "user update",
            );
        }
    }

    /// The signed-in user's data and a new access token (Go's
    /// `GetLoginData`): the public user fields plus the profile, role and
    /// permission fields. Empty when the user doesn't exist.
    pub async fn get_login_data(&self, req: &Request, input: &LoginData) -> DataMap {
        let client = req.client().unwrap_or_default();
        let domain = client.origin_domain();
        let config = self.config();

        let mut user = self
            .get_user(&json!({"userId": input.user_id}), false)
            .await;
        if user.is_empty() {
            return user;
        }

        let mut profile = None;
        if !input.profile_id.is_empty()
            && self
                .profile_ids(&input.user_id)
                .await
                .contains(&input.profile_id)
        {
            if let Some(q) = self.auth_query(PROFILE_MODEL) {
                profile = logged(
                    q.where_("id", input.profile_id.as_str()).find_one().await,
                    "profile lookup",
                );
            }
        }
        for model in [PROFILE_MODEL, PROFILE_USER_MODEL] {
            if profile.is_some() {
                break;
            }
            if let Some(q) = self.auth_query(model) {
                profile = logged(
                    q.where_("userId", input.user_id.as_str()).find_one().await,
                    "profile lookup",
                );
            }
        }

        let tenant_id = req.tenant_id().unwrap_or(Value::Null);
        let permissions = self
            .get_user_permission(&tenant_id, &input.user_id, &input.module_name)
            .await;

        let mut payload = TokenPayload {
            tenant_id,
            domain: domain.clone(),
            user_id: input.user_id.clone(),
            username: get(&user, "username"),
            username_type: get(&user, "usernameType"),
            phone: get(&user, "phone"),
            email: get(&user, "email"),
            whatsapp: get(&user, "whatsapp"),
            module_name: input.module_name.clone(),
            roles: Vec::new(),
            permissions: permissions.clone(),
            expires_at: Some(Utc::now() + Duration::minutes(self.access_token_minutes())),
            ..Default::default()
        };

        let role = user.get("role").cloned().unwrap_or(Value::Null);
        let is_admin = role == json!("1") || role == json!("admin");

        if !is_empty(&client.tenant_id) {
            payload.tenant_id = client.tenant_id.clone();
            user.insert("tenantId".into(), client.tenant_id.clone());
        }

        if let Some(profile) = &profile {
            let profile_id = get(profile, "_id");
            user.insert("owner".into(), json!(false));
            user.insert("profileRole".into(), json!("member"));
            user.insert("profileName".into(), json!(get(profile, "name")));
            user.insert("profileId".into(), json!(profile_id));
            payload.profile_id = profile_id;
        }
        if is_admin {
            payload.admin_id = input.user_id.clone();
        }

        let token = jwt::encode(payload.to_map(), &config.authentication.secret_token);
        let owner = json!(input.user_id) == user.get("id").cloned().unwrap_or(Value::Null);
        user.insert("token".into(), json!(token));
        user.insert("uuid".into(), json!(input.user_id));
        user.insert("isAdmin".into(), json!(is_admin));
        user.insert(
            "isManager".into(),
            json!(role == json!("2") || role == json!("manager")),
        );
        user.insert("owner".into(), json!(owner));
        user.insert("permissions".into(), json!(permissions));

        let role = if role == json!("1") {
            json!("admin")
        } else if role != json!("admin") && role != json!("manager") {
            json!("user")
        } else {
            role
        };
        user.insert("role".into(), role.clone());
        user.insert(
            "profileRole".into(),
            if owner { json!("admin") } else { role },
        );

        if let (Some(profile), Some(keys)) = (
            &profile,
            config
                .graphql
                .auth_query
                .user
                .get("profile")
                .and_then(Value::as_array),
        ) {
            let extra: Map<String, Value> = keys
                .iter()
                .filter_map(Value::as_str)
                .map(|k| {
                    (
                        k.to_string(),
                        profile.get(k).cloned().unwrap_or(Value::Null),
                    )
                })
                .collect();
            user.insert("additionalFields".into(), Value::Object(extra));
        }

        let protected = self
            .model(USER_MODEL)
            .map(|m| m.protected.clone())
            .unwrap_or_default();
        let mut data: DataMap = PUBLIC_KEYS
            .iter()
            .filter(|key| **key == "token" || !protected.iter().any(|p| p == *key))
            .map(|key| {
                (
                    key.to_string(),
                    user.get(*key).cloned().unwrap_or(Value::Null),
                )
            })
            .collect();

        let url = get(&data, "profileUrl");
        let url = if url.is_empty() {
            "/image/profile.png".to_string()
        } else {
            url
        };
        data.insert(
            "profileUrl".into(),
            json!(get_base_url(
                &url,
                &domain,
                &config.base_url,
                config.ports.server as u16
            )),
        );

        data
    }

    // ----- permissions -------------------------------------------------------------------

    /// The permission codes a user has for `module_name` (and the `access`
    /// module) in a tenant (Go's `GetUserPermission`). The tenant's owner
    /// has every permission. Empty without a module name.
    pub async fn get_user_permission(
        &self,
        tenant_id: &Value,
        user_id: &str,
        module_name: &str,
    ) -> Vec<String> {
        if module_name.is_empty() {
            return Vec::new();
        }

        let is_admin = match self.auth_query(TENANT_MODEL) {
            Some(q) => logged(
                q.where_("_id", tenant_id.clone())
                    .where_("userId", user_id)
                    .exists()
                    .await,
                "tenant lookup",
            ),
            None => false,
        };
        let modules = json!({"in": ["access", module_name]});

        let permissions = if is_admin {
            self.auth_query(PERMISSION_MODEL)
                .map(|q| q.where_("moduleName", modules))
        } else {
            self.auth_query(USER_PERMISSION_MODEL).map(|q| {
                q.where_("tenantId", tenant_id.clone())
                    .where_("userId", user_id)
                    .where_("moduleName", modules)
            })
        };

        match permissions {
            Some(q) => logged(q.find().await, "permission lookup")
                .iter()
                .map(|p| get(p, "code"))
                .collect(),
            None => Vec::new(),
        }
    }

    /// Replaces a user's permissions in a tenant (Go's `SetUserPermission`).
    /// Each permission is a code (`"payroll:read"`, whose module is the part
    /// before the colon) or a map with `code` and optionally `moduleName`,
    /// `userId` and `authGroupId`.
    pub async fn set_user_permission(
        &self,
        tenant_id: &Value,
        user_id: &str,
        module_name: &str,
        permissions: &[Value],
    ) {
        let Some(q) = self.auth_query(USER_PERMISSION_MODEL) else {
            return;
        };
        let q = q.skip_tenant();

        logged(
            q.clone()
                .where_("tenantId", tenant_id.clone())
                .where_("userId", user_id)
                .delete()
                .await,
            "permission delete",
        );

        for permission in permissions {
            let mut input = Map::new();
            let (mut module_name, mut user_id) = (module_name.to_string(), user_id.to_string());
            let code = match permission {
                Value::String(code) => {
                    module_name = code.split(':').next().unwrap_or_default().to_string();
                    code.clone()
                }
                Value::Object(map) => {
                    if !is_empty(map.get("authGroupId").unwrap_or(&Value::Null)) {
                        input.insert("authGroupId".into(), map["authGroupId"].clone());
                    }
                    for (key, target) in
                        [("moduleName", &mut module_name), ("userId", &mut user_id)]
                    {
                        let value = get(map, key);
                        if !value.is_empty() {
                            *target = value;
                        }
                    }
                    get(map, "code")
                }
                _ => continue,
            };

            input.insert("tenantId".into(), tenant_id.clone());
            input.insert("userId".into(), json!(user_id));
            input.insert("code".into(), json!(code));
            input.insert("moduleName".into(), json!(module_name));

            let exists = logged(
                q.clone()
                    .where_many(Value::Object(input.clone()))
                    .exists()
                    .await,
                "permission lookup",
            );
            if !exists {
                logged(
                    q.clone().create(Value::Object(input)).await.map(Some),
                    "permission create",
                );
            }
        }
    }

    // ----- tokens and cookies ------------------------------------------------------------

    /// Creates a refresh token for `payload`'s user and stores its hash (Go's
    /// `getRefreshToken`). It lasts `refreshTokenExpireTime` days, or 30 with
    /// `remember_me`.
    pub(crate) async fn create_refresh_token(
        &self,
        client: &ClientPayload,
        payload: &TokenPayload,
        remember_me: bool,
    ) -> String {
        let token = random_string(64, "");
        let days = if remember_me {
            30
        } else {
            self.refresh_token_days()
        };
        let tenant_id = if is_empty(&payload.tenant_id) {
            Value::Null
        } else {
            payload.tenant_id.clone()
        };

        if let Some(q) = self.auth_query(REFRESH_TOKEN_MODEL) {
            logged(
                q.create(json!({
                    "domain": payload.domain,
                    "tenantId": tenant_id,
                    "profileId": or_null(&payload.profile_id),
                    "userId": or_null(&payload.user_id),
                    "adminId": or_null(&payload.admin_id),
                    "tokenHash": hash_refresh_token(&token),
                    "userAgent": client.user_agent,
                    "ipAddress": client.ip_address,
                    "revoked": false,
                    "expiresAt": time_value(Utc::now() + Duration::days(days)),
                }))
                .await
                .map(Some),
                "refresh token create",
            );
        }

        token
    }

    /// Sets the access and refresh token cookies (Go's `setAuthCookies`).
    /// The access token cookie is sent everywhere; the refresh token cookie
    /// only to `/refresh` and the auth GraphQL route.
    pub(crate) fn set_auth_cookies(
        &self,
        req: &Request,
        res: &Response,
        access_token: &str,
        refresh_token: &str,
    ) {
        if access_token.is_empty() || refresh_token.is_empty() {
            return;
        }
        let domain = domain_of(req);
        let secure = self.config().secure_only;
        let access_age = self.access_token_minutes() * 60;
        let refresh_age = self.refresh_token_days() * 24 * 60 * 60;

        for path in ["/".to_string(), self.append_base_url("/")] {
            let cookie = go_cookie(
                keys::ACCESS_TOKEN,
                access_token,
                &path,
                &domain,
                access_age,
                secure,
            );
            res.append_header("set-cookie", &cookie);
        }
        for path in self.refresh_cookie_paths() {
            let cookie = go_cookie(
                keys::REFRESH_TOKEN,
                refresh_token,
                &path,
                &domain,
                refresh_age,
                secure,
            );
            res.append_header("set-cookie", &cookie);
        }
    }

    /// Expires the token cookies (Go's `clearAuthCookies`).
    pub(crate) fn clear_auth_cookies(&self, res: &Response, domain: &str) {
        for path in ["/".to_string(), self.append_base_url("/")] {
            res.append_header(
                "set-cookie",
                &go_cookie(keys::ACCESS_TOKEN, "", &path, domain, -1, true),
            );
        }
        for path in self.refresh_cookie_paths() {
            res.append_header(
                "set-cookie",
                &go_cookie(keys::REFRESH_TOKEN, "", &path, domain, -1, true),
            );
        }
    }

    fn refresh_cookie_paths(&self) -> [String; 2] {
        [
            self.append_base_url("/refresh"),
            self.append_base_url(&self.config().graphql.api_auth_route),
        ]
    }

    /// Exchanges a refresh token for a new access token and refresh token,
    /// and revokes the old one (Go's `refreshTokenProcess`). The token comes
    /// from `refresh_token`, else the refresh cookie, the `X-Refresh-Token`
    /// header or the `refresh_token` URL parameter. Returns the result and
    /// its HTTP status (401 with an `error` when refused).
    pub(crate) async fn refresh_token_process(
        &self,
        req: &Request,
        res: &Response,
        refresh_token: &str,
        module_name: &str,
    ) -> (DataMap, u16) {
        let refuse = |message: &str| {
            let mut result = DataMap::new();
            result.insert("error".into(), json!(message));
            (result, 401)
        };

        let mut token = refresh_token.to_string();
        for fallback in [
            req.context_string(keys::REFRESH_TOKEN),
            req.header("x-refresh-token"),
            req.query("refresh_token"),
        ] {
            if !token.is_empty() {
                break;
            }
            token = fallback;
        }
        if token.is_empty() {
            return refuse("Empty Refresh Token");
        }

        let hash = hash_refresh_token(&token);
        let Some(tokens) = self.auth_query(REFRESH_TOKEN_MODEL) else {
            return refuse("Invalid Refresh Token");
        };
        let Some(record) = logged(
            tokens
                .clone()
                .where_("tokenHash", hash.as_str())
                .find_one()
                .await,
            "refresh token lookup",
        ) else {
            return refuse("Invalid Refresh Token");
        };

        if bool_of(record.get("revoked")) {
            return refuse("refresh_token is revoked");
        }
        let now = Utc::now();
        let expires_at = parse_datetime(&get(&record, "expiresAt"));
        if expires_at.is_none_or(|at| at <= now) {
            return refuse("Refresh Token expired");
        }

        let client = req.client().unwrap_or_default();
        let domain = client.origin_domain();
        if get(&record, "domain") != domain {
            return refuse("Domain mismatch");
        }

        let user_id = get(&record, "userId");
        let tenant_id = get(&record, "tenantId");
        let permissions = self
            .get_user_permission(&json!(tenant_id), &user_id, module_name)
            .await;
        let payload = TokenPayload {
            domain,
            tenant_id: json!(tenant_id),
            profile_id: get(&record, "profileId"),
            user_id,
            admin_id: get(&record, "adminId"),
            username: get(&record, "username"),
            username_type: get(&record, "usernameType"),
            phone: get(&record, "phone"),
            email: get(&record, "email"),
            whatsapp: get(&record, "whatsapp"),
            module_name: module_name.to_string(),
            roles: Vec::new(),
            permissions,
            expires_at: Some(now + Duration::minutes(self.access_token_minutes())),
        };

        let access_token =
            jwt::encode(payload.to_map(), &self.config().authentication.secret_token);
        let new_refresh_token = self.create_refresh_token(&client, &payload, false).await;

        let mut result = DataMap::new();

        if req
            .cookie(COOKIE_ENABLED_KEY)
            .unwrap_or_default()
            .is_empty()
        {
            result.insert("accessToken".into(), json!(access_token));
            result.insert("refreshToken".into(), json!(new_refresh_token));
        } else {
            result.insert("accessToken".into(), json!("Cookie is set"));
            result.insert("refreshToken".into(), json!("Cookie is set"));
        }
        self.set_auth_cookies(req, res, &access_token, &new_refresh_token);

        logged(
            tokens
                .where_("tokenHash", hash.as_str())
                .update(json!({"revoked": true}))
                .await,
            "refresh token revoke",
        );

        (result, 200)
    }
}

/// A `Set-Cookie` value as Go's `http.Cookie.String` writes it (HttpOnly,
/// default SameSite). Go leaves out a domain it considers invalid, such as
/// one with a port.
fn go_cookie(
    name: &str,
    value: &str,
    path: &str,
    domain: &str,
    max_age: i64,
    secure: bool,
) -> String {
    let mut cookie = format!("{name}={value}; Path={path}");
    let domain = domain.strip_prefix('.').unwrap_or(domain);
    if is_cookie_domain(domain) {
        cookie.push_str(&format!("; Domain={domain}"));
    }
    if max_age > 0 {
        cookie.push_str(&format!("; Max-Age={max_age}"));
    } else if max_age < 0 {
        cookie.push_str("; Max-Age=0");
    }
    cookie.push_str("; HttpOnly");
    if secure {
        cookie.push_str("; Secure");
    }
    cookie
}

/// Go's `validCookieDomain`: a host name with a letter, or an IPv4 address.
fn is_cookie_domain(domain: &str) -> bool {
    if domain.is_empty() || domain.len() > 255 {
        return false;
    }
    if domain.parse::<std::net::Ipv4Addr>().is_ok() {
        return true;
    }

    let mut has_letter = false;
    for part in domain.split('.') {
        if part.is_empty()
            || part.len() > 63
            || part.starts_with('-')
            || part.ends_with('-')
            || !part.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-')
        {
            return false;
        }
        has_letter |= part.bytes().any(|c| c.is_ascii_alphabetic());
    }
    has_letter
}

// ----- routes ------------------------------------------------------------------------------

/// `/me`, `/logout` and `/refresh` (each with an optional `/:moduleName`),
/// on authorization servers.
pub(crate) fn register_routes(app: &Yekonga) {
    if !app.config().is_authorization_server {
        return;
    }

    for method in ["GET", "POST"] {
        let method: http::Method = method.parse().unwrap();
        app.route(method.clone(), "/me/:moduleName?", |req, res| async move {
            me(req, res).await
        });
        app.route(
            method.clone(),
            "/logout/:moduleName?",
            |req, res| async move { logout(req, res) },
        );
        app.route(method, "/refresh/:moduleName?", |req, res| async move {
            refresh(req, res).await
        });
    }
}

async fn me(req: Request, res: Response) {
    let Some(auth) = req.auth().filter(|a| !a.id.is_empty()) else {
        res.status(401)
            .json(&json!({"error": "Missing or Invalid token"}));
        return;
    };

    let mut user = req
        .app()
        .get_login_data(
            &req,
            &LoginData {
                user_id: auth.id,
                profile_id: auth.profile_id,
                module_name: req.param("moduleName"),
            },
        )
        .await;
    if !user.is_empty() {
        user.insert("token".into(), Value::Null);
    }
    res.json(&user);
}

fn is_json_request(req: &Request) -> bool {
    req.header("content-type").contains("json") || req.header("accept").contains("json")
}

fn logout(req: Request, res: Response) {
    let app = req.app();
    let domain = domain_of(&req);
    app.clear_auth_cookies(&res, &domain);

    if !is_json_request(&req) {
        let config = app.config();
        res.status(307).redirect(&get_base_url(
            "/",
            &domain,
            &config.base_url,
            config.ports.server as u16,
        ));
        return;
    }
    res.json(&json!({"status": "SUCCESS"}));
}

async fn refresh(req: Request, res: Response) {
    let app = req.app();
    let (mut result, status) = app
        .refresh_token_process(&req, &res, "", &req.param("moduleName"))
        .await;
    if !app.config().secure_authentication {
        result.insert("token".into(), Value::Null);
    }

    if !is_json_request(&req) {
        let origin = req.client().map(|c| c.origin).unwrap_or_default();
        res.status(307).redirect(&origin);
        return;
    }
    res.status(status).json(&result);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cookies_like_go() {
        assert_eq!(
            go_cookie("access_token", "t", "/", "shop.tz", 900, false),
            "access_token=t; Path=/; Domain=shop.tz; Max-Age=900; HttpOnly"
        );
        assert_eq!(
            go_cookie("refresh_token", "", "/refresh", "localhost:8080", -1, true),
            "refresh_token=; Path=/refresh; Max-Age=0; HttpOnly; Secure"
        );
        assert!(is_cookie_domain("localhost"));
        assert!(is_cookie_domain("10.0.0.1"));
        assert!(!is_cookie_domain("a..b"));
        assert!(!is_cookie_domain("-a.tz"));
        assert!(!is_cookie_domain("123"));
    }
}
