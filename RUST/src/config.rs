//! `config.json`: server, database, auth, GraphQL and gateway settings.
//! Field-for-field port of `config/config.go`; see the Go README
//! "Configuration" section for what each setting does.

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

use serde_json::Value;

use crate::error::{Error, Result};
use crate::helper::lenient::go_json_struct;

/// Database backend selected by `database.kind`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DatabaseKind {
    Mongodb,
    Sql,
    Mysql,
    Local,
}

impl DatabaseKind {
    pub fn parse(kind: &str) -> Option<Self> {
        match kind {
            "mongodb" => Some(Self::Mongodb),
            "sql" => Some(Self::Sql),
            "mysql" => Some(Self::Mysql),
            "local" => Some(Self::Local),
            _ => None,
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Mongodb => "mongodb",
            Self::Sql => "sql",
            Self::Mysql => "mysql",
            Self::Local => "local",
        }
    }
}

go_json_struct! {
    /// SMS gateway configuration.
    pub struct SmsGatewayConfig {
        /// beem, infobip, alibaba
        pub provider: String,
        #[serde(rename(serialize = "baseURL"))]
        pub base_url: String,
        pub sender: String,
        pub api_key: String,
        pub secret_key: String,
        pub username: String,
        pub password: String,
    }
}

go_json_struct! {
    /// WhatsApp gateway configuration.
    pub struct WhatsappGatewayConfig {
        pub provider: String,
        pub sender: String,
        pub sandbox: String,
        #[serde(rename(serialize = "baseURL"))]
        pub base_url: String,
        pub api_key: String,
    }
}

go_json_struct! {
    /// One payment gateway.
    pub struct PaymentProviderConfig {
        /// flutterwave, paypal, pesapal, selcom, clickpesa, azampay, stripe, twocheckout
        pub provider: String,
        pub sandbox: bool,
        #[serde(rename(serialize = "baseURL"))]
        pub base_url: String,
        /// Empty = derived from the request host and `webhookRoute`.
        #[serde(rename(serialize = "webhookURL"))]
        pub webhook_url: String,
        /// Gateway credentials keyed by name (secret_key, client_id, ...).
        pub credentials: BTreeMap<String, String>,
    }
}

go_json_struct! {
    pub struct PaymentGatewayConfig {
        /// Served as `<webhookRoute>/:provider`; empty = `/payment/webhook`.
        pub webhook_route: String,
        pub providers: Vec<PaymentProviderConfig>,
    }
}

go_json_struct! {
    pub struct SmtpConfig {
        pub service: String,
        pub host: String,
        pub port: i64,
        pub secure: bool,
        pub from: String,
        pub domain: String,
        pub username: String,
        pub password: String,
        pub api_key: String,
    }
}

go_json_struct! {
    pub struct Branding {
        pub logo_url: String,
        pub favicon_url: String,
        pub primary_color: String,
        pub secondary_color: String,
        pub dark_background_color: String,
    }
}

go_json_struct! {
    pub struct PermissionsConfig {
        pub auth_actions: Vec<String>,
        pub guest_actions: Vec<String>,
    }
}

go_json_struct! {
    pub struct GraphqlAuthQuery {
        pub user: Value,
        pub account: Value,
    }
}

go_json_struct! {
    pub struct GraphqlConfig {
        pub active: bool,
        pub api_route: String,
        pub api_auth_route: String,
        pub custom_types: String,
        pub custom_resolvers: String,
        pub custom_auth_types: String,
        pub custom_auth_resolvers: String,
        pub enabled_for_classes: Value,
        pub disabled_for_classes: Value,
        pub auth_resolvers: Value,
        pub auth_classes: Value,
        pub guest_resolvers: Value,
        pub guest_classes: Value,
        pub auth_query: GraphqlAuthQuery,
    }
}

go_json_struct! {
    pub struct AuditTrailConfig {
        pub enabled: bool,
        pub exclude_models: Vec<String>,
    }
}

go_json_struct! {
    /// In-memory caches for per-request lookups.
    pub struct CacheConfig {
        /// Tenant lookup TTL; 0 = default (30s), negative = disabled.
        pub tenant_seconds: i64,
        /// User lookup TTL on an authorization server; 0 = default (10s), negative = disabled.
        pub user_seconds: i64,
    }
}

go_json_struct! {
    pub struct RateLimitConfig {
        pub enabled: bool,
        /// 0 = default (300).
        pub requests_per_minute: i64,
        /// 0 = requestsPerMinute.
        pub burst: i64,
    }
}

go_json_struct! {
    /// Blocks clients that flood the server with error responses.
    pub struct ErrorGuardConfig {
        pub enabled: bool,
        /// Empty = [400, 401, 403, 404].
        pub status_codes: Vec<u16>,
        /// 0 = default (20).
        pub requests_per_second: i64,
        /// 0 = permanent block.
        pub block_hours: i64,
    }
}

go_json_struct! {
    /// Protection against volumetric/resource-exhaustion attacks.
    pub struct SecurityConfig {
        pub read_timeout_seconds: i64,
        pub read_header_timeout_seconds: i64,
        pub write_timeout_seconds: i64,
        pub idle_timeout_seconds: i64,
        pub max_header_bytes: i64,
        /// 0 = default (310 MB).
        pub max_body_bytes: i64,
        /// Trust X-Forwarded-For / X-Real-Ip (only behind a trusted proxy).
        pub trust_proxy_headers: bool,
        pub rate_limit: RateLimitConfig,
        pub error_guard: ErrorGuardConfig,
    }
}

go_json_struct! {
    pub struct DatabaseConfig {
        /// mongodb, sql, mysql or local; see [`DatabaseConfig::kind`].
        pub kind: String,
        pub srv: bool,
        pub host: String,
        pub port: String,
        pub database_name: String,
        pub username: Value,
        pub password: Value,
        pub prefix: String,
        #[serde(rename(serialize = "generateID"))]
        pub generate_id: bool,
        #[serde(rename(serialize = "generateIDLength"))]
        pub generate_id_length: i64,
        pub max_pool_size: i64,
        pub min_pool_size: i64,
        pub max_conn_idle_time_seconds: i64,
        pub connect_timeout_seconds: i64,
        pub server_selection_timeout_seconds: i64,
        pub query_timeout_seconds: i64,
        pub disable_auto_indexes: bool,
        pub disable_auto_migrate: bool,
    }
}

impl DatabaseConfig {
    pub fn kind(&self) -> Option<DatabaseKind> {
        DatabaseKind::parse(&self.kind)
    }
}

go_json_struct! {
    pub struct AuthenticationConfig {
        pub salt_round: i64,
        pub algorithm: String,
        /// Secret used to sign access tokens.
        pub secret_token: String,
        pub crypto_js_key: String,
        pub crypto_js_iv: String,
    }
}

go_json_struct! {
    pub struct PortsConfig {
        pub secure: bool,
        pub server: i64,
        pub ssl_server: i64,
        pub redis: i64,
    }
}

go_json_struct! {
    pub struct MailConfig {
        pub smtp: SmtpConfig,
    }
}

go_json_struct! {
    pub struct ApiGatewayConfig {
        pub sms: SmsGatewayConfig,
        pub whatsapp: WhatsappGatewayConfig,
        pub payment: PaymentGatewayConfig,
    }
}

go_json_struct! {
    pub struct AdminCredential {
        pub username: Value,
        pub password: Value,
    }
}

go_json_struct! {
    /// The whole `config.json`.
    pub struct YekongaConfig {
        pub app_name: String,
        pub version: String,
        pub description: String,
        pub app_key: String,
        pub master_key: String,
        pub enable_app_key: bool,
        #[serde(rename(serialize = "connectionID"))]
        pub connection_id: String,
        pub user_identifiers: Vec<String>,
        pub domain: String,
        pub protocol: String,
        pub domain_alias: Vec<String>,
        pub address: String,
        #[serde(rename(serialize = "baseURL"))]
        pub base_url: String,
        pub rest_api_enabled: bool,
        #[serde(rename(serialize = "restAPI"))]
        pub rest_api: String,
        #[serde(rename(serialize = "restAuthAPI"))]
        pub rest_auth_api: String,
        pub token_key: String,
        pub pdf_instances: i64,
        /// Minutes.
        pub access_token_expire_time: i64,
        /// Minutes.
        pub refresh_token_expire_time: i64,
        pub secure_only: bool,
        pub debug: bool,
        pub cors: bool,
        #[serde(rename(serialize = "resetOTP"))]
        pub reset_otp: bool,
        pub environment: String,
        pub has_tenant: bool,
        pub tenant_only: bool,
        pub has_tenant_billing: bool,
        pub has_payment_module: bool,
        pub has_tenant_catch: bool,
        pub secure_authentication: bool,
        pub is_authorization_server: bool,
        pub authorize_tenant_user_only: bool,
        pub authorized_only: bool,
        pub has_cronjob: bool,
        pub register_user_on_otp: bool,
        pub send_otp_to_sms_and_whatsapp: bool,
        pub end_to_end_encryption: bool,
        pub auth_playground_enable: bool,
        pub api_playground_enable: bool,
        pub enable_dashboard: bool,
        pub allow_create_frontend: bool,
        pub naming_convention: String,
        pub column_naming_convention: String,
        pub naming_convention_options: Vec<String>,
        /// Directories served as static files.
        pub public: Vec<String>,
        pub cloud: String,
        pub log_file: String,
        pub index_template: String,
        pub email_template: String,
        pub google_api_key: String,
        pub google_api_key_alt: String,
        pub google_client_id: String,
        pub google_client_secret: String,
        pub global_password: String,
        pub branding: Branding,
        pub permissions: PermissionsConfig,
        pub graphql: GraphqlConfig,
        pub audit_trail: AuditTrailConfig,
        pub cache: CacheConfig,
        pub security: SecurityConfig,
        pub database: DatabaseConfig,
        pub authentication: AuthenticationConfig,
        pub ports: PortsConfig,
        pub mail: MailConfig,
        pub api_gateway: ApiGatewayConfig,
        pub admin_credential: AdminCredential,
    }
}

impl YekongaConfig {
    /// Loads `config.json`. A relative path that doesn't exist from the
    /// working directory is also looked up next to the executable.
    pub fn from_file(path: impl AsRef<Path>) -> Result<Self> {
        let path = resolve_path(path.as_ref());
        let text = std::fs::read_to_string(&path).map_err(|e| Error::Io(path.clone(), e))?;

        Self::from_json(&text).map_err(|e| Error::Json(path, e))
    }

    pub fn from_json(text: &str) -> serde_json::Result<Self> {
        serde_json::from_str(text)
    }
}

/// `path` as given if it exists, else relative to the executable's directory
/// (Go's `getPath`).
pub fn resolve_path(path: &Path) -> PathBuf {
    if path.exists() || path.is_absolute() {
        return path.to_path_buf();
    }

    std::env::current_exe()
        .ok()
        .and_then(|exe| exe.parent().map(|dir| dir.join(path)))
        .filter(|p| p.exists())
        .unwrap_or_else(|| path.to_path_buf())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn readme_example_config() {
        // Trimmed from the Go README "Getting Started" config.
        let config = YekongaConfig::from_json(
            r#"{
                "appName": "My YekongaServer App",
                "appKey": "APP", "masterKey": "MASTER", "enableAppKey": true,
                "connectionId": "default",
                "restApi": "/api", "restAuthApi": "/api/auth",
                "accessTokenExpireTime": 15,
                "public": ["/api/auth/login"],
                "permissions": {"authActions": ["read"], "guestActions": ["read"]},
                "graphql": {"apiRoute": "/graphql", "apiAuthRoute": "/graphql/auth", "authQuery": {"user": {}}},
                "database": {"kind": "mongodb", "port": 27017, "generateId": true, "generateIdLength": 24, "prefix": null},
                "authentication": {"secretToken": "s3cret"},
                "ports": {"secure": false, "server": 8080, "sslServer": "8443"},
                "mail": {"smtp": {"port": 587}},
                "apiGateway": {"payment": {"providers": [{"provider": "stripe", "credentials": {"secret_key": "sk"}}]}},
                "adminCredential": {"username": "admin"}
            }"#,
        )
        .unwrap();

        assert_eq!(config.connection_id, "default");
        assert_eq!(config.rest_auth_api, "/api/auth");
        assert!(config.enable_app_key);
        assert_eq!(config.access_token_expire_time, 15);
        assert_eq!(config.permissions.auth_actions, vec!["read"]);
        assert_eq!(config.graphql.api_auth_route, "/graphql/auth");
        assert_eq!(config.database.kind(), Some(DatabaseKind::Mongodb));
        assert_eq!(config.database.port, "27017");
        assert!(config.database.generate_id);
        assert_eq!(config.database.generate_id_length, 24);
        assert_eq!(config.authentication.secret_token, "s3cret");
        assert_eq!(config.ports.server, 8080);
        assert_eq!(config.ports.ssl_server, 8443);
        assert_eq!(config.mail.smtp.port, 587);
        assert_eq!(
            config.api_gateway.payment.providers[0].credentials["secret_key"],
            "sk"
        );
        assert_eq!(config.admin_credential.username, "admin");
    }
}
