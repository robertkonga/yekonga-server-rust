//! The server: route and middleware registry, the request pipeline and
//! startup (port of `yekonga/main.go` and the route setup in
//! `yekonga/initializer.go`).

use std::collections::{BTreeMap, HashMap};
use std::future::Future;
use std::net::SocketAddr;
use std::panic::AssertUnwindSafe;
use std::path::{Path, PathBuf};
use std::pin::Pin;
use std::sync::{Arc, OnceLock, RwLock};

use axum::body::Body;
use axum::extract::ConnectInfo;
use futures_util::FutureExt;
use http::header::{HeaderValue, CACHE_CONTROL};
use http::{Method, StatusCode};
use tower_http::compression::predicate::{NotForContentType, Predicate, SizeAbove};
use tower_http::compression::CompressionLayer;

use crate::config::{DatabaseKind, YekongaConfig};
use crate::db::{Backend, DbError, LocalBackend, UnsupportedBackend};
use crate::error::{Error, Result};
use crate::helper::{home_directory, home_directory_path, match_path, to_slug};
use crate::lookup::LookupCaches;
use crate::middleware::{self, Abort, MiddlewareKind, MiddlewareResult};
use crate::model::{build_system_models, DataModel};
use crate::query::ModelQuery;
use crate::request::{header_value, Request, RequestParts};
use crate::response::{self, Response, PAGE_404, PAGE_INDEX};
use crate::router::RoutePattern;
use crate::schema::DatabaseStructure;

/// Name of the cookie marking a browser that has talked to this server.
pub const COOKIE_ENABLED_KEY: &str = "YEKONGA_ENABLED";

/// Largest accepted request body unless `security.maxBodyBytes` is set (~310 MB).
const DEFAULT_MAX_BODY_BYTES: usize = 310 << 20;

/// Allowed static file extensions.
pub const DEFAULT_EXTENSIONS: &[&str] = &[
    // Web
    ".html",
    ".css",
    ".js",
    ".webmanifest",
    ".htm",
    ".json",
    ".xml",
    ".map",
    // Images
    ".png",
    ".jpg",
    ".jpeg",
    ".gif",
    ".svg",
    ".ico",
    ".bmp",
    ".webp",
    ".tiff",
    ".tif",
    ".avif",
    // Fonts
    ".ttf",
    ".otf",
    ".woff",
    ".woff2",
    ".eot",
    // Documents
    ".xlsx",
    ".pdf",
    ".doc",
    ".text",
    ".txt",
    ".csv",
    ".docx",
    ".odt",
    ".rtf",
    ".md",
    ".xls",
    ".ods",
    // Video
    ".mp4",
    ".webm",
    ".ogg",
    ".avi",
    ".mov",
    ".wmv",
    ".flv",
    ".mkv",
    // Audio
    ".mp3",
    ".wav",
    ".aac",
    ".flac",
    ".opus",
    ".m4a",
    // Archives
    ".zip",
    ".rar",
    ".tar.gz",
    ".7z",
];

pub type BoxFuture<T> = Pin<Box<dyn Future<Output = T> + Send>>;
type HandlerFn = Arc<dyn Fn(Request, Response) -> BoxFuture<()> + Send + Sync>;
type MiddlewareFn = Arc<dyn Fn(Request, Response) -> BoxFuture<MiddlewareResult> + Send + Sync>;

/// Static file serving settings (see [`Yekonga::serve_static`]).
#[derive(Clone, Debug)]
pub struct StaticConfig {
    /// Root directory of the files.
    pub directory: PathBuf,
    /// URL prefix the files are served under, e.g. `/public`.
    pub path_prefix: String,
    /// File served for a directory, `index.html` by default.
    pub index_file: String,
    /// Allowed extensions; [`DEFAULT_EXTENSIONS`] by default.
    pub extensions: Vec<String>,
    /// `Cache-Control: max-age` in seconds; 30 days by default.
    pub cache_max_age: u64,
}

impl StaticConfig {
    pub fn new(directory: impl Into<PathBuf>, path_prefix: impl Into<String>) -> Self {
        Self {
            directory: directory.into(),
            path_prefix: path_prefix.into(),
            index_file: String::new(),
            extensions: Vec::new(),
            cache_max_age: 0,
        }
    }
}

struct Route {
    pattern: RoutePattern,
    handler: HandlerFn,
}

#[derive(Default)]
struct Middlewares {
    global: Vec<MiddlewareFn>,
    init: Vec<MiddlewareFn>,
    preload: Vec<MiddlewareFn>,
    catch: Vec<MiddlewareFn>,
}

/// Paths where a missing or invalid access token isn't an error.
struct TokenPaths {
    exact: Vec<String>,
    patterns: Vec<String>,
}

/// A Yekonga server. Cheap to clone; clones share everything.
#[derive(Clone)]
pub struct Yekonga(Arc<Inner>);

struct Inner {
    config: YekongaConfig,
    database_structure: DatabaseStructure,
    models: BTreeMap<String, Arc<DataModel>>,
    root_path: PathBuf,
    is_dev: bool,
    routes: RwLock<HashMap<Method, Vec<Arc<Route>>>>,
    middlewares: RwLock<Middlewares>,
    static_configs: RwLock<Vec<Arc<StaticConfig>>>,
    public_routes: RwLock<Vec<String>>,
    when_ready: std::sync::Mutex<Vec<Box<dyn FnOnce() + Send>>>,
    token_paths: OnceLock<TokenPaths>,
    backend: Arc<dyn Backend>,
    caches: LookupCaches,
    graphql_schema: OnceLock<std::result::Result<async_graphql::dynamic::Schema, String>>,
    auth_schema: OnceLock<std::result::Result<async_graphql::dynamic::Schema, String>>,
    otp_sender: RwLock<Option<crate::auth::OtpSender>>,
    cloud: RwLock<crate::cloud::CloudRegistry>,
    cron_jobs: RwLock<Vec<crate::cron::CronJob>>,
    sockets: crate::socket::SocketServer,
    security: crate::security::Security,
}

impl Yekonga {
    /// Builds a server from a config and the app's own schema (the
    /// framework's built-in collections are added according to the config).
    ///
    /// The database is the one `database.kind` selects. Only `local` (the
    /// default) is ported so far; other kinds log an error and every query
    /// fails until their backends are ported.
    pub fn new(config: YekongaConfig, app_structure: DatabaseStructure) -> Self {
        let backend = default_backend(&config);
        Self::with_backend(config, app_structure, backend)
    }

    /// Like [`new`](Self::new), with a given database backend (e.g.
    /// [`LocalBackend::in_memory`] for tests).
    pub fn with_backend(
        config: YekongaConfig,
        app_structure: DatabaseStructure,
        backend: Arc<dyn Backend>,
    ) -> Self {
        let database_structure = DatabaseStructure::build(&app_structure, &config);
        let models = build_system_models(&config, &database_structure)
            .into_iter()
            .map(|(name, model)| (name, Arc::new(model)))
            .collect();

        let root_path = std::env::current_exe()
            .ok()
            .and_then(|exe| exe.parent().map(Path::to_path_buf))
            .unwrap_or_else(|| PathBuf::from("./"));

        let app = Yekonga(Arc::new(Inner {
            database_structure,
            models,
            root_path,
            is_dev: std::env::var("APP_ENV").as_deref() == Ok("development"),
            routes: RwLock::default(),
            middlewares: RwLock::default(),
            static_configs: RwLock::default(),
            public_routes: RwLock::default(),
            when_ready: std::sync::Mutex::default(),
            token_paths: OnceLock::new(),
            caches: LookupCaches::new(&config),
            graphql_schema: OnceLock::new(),
            auth_schema: OnceLock::new(),
            otp_sender: RwLock::default(),
            cloud: RwLock::default(),
            cron_jobs: RwLock::default(),
            sockets: crate::socket::SocketServer::new(),
            security: crate::security::Security::default(),
            backend,
            config,
        }));

        app.initialize();
        app
    }

    /// Builds a server from `config.json` and `database.json` files.
    pub fn from_files(
        config_file: impl AsRef<Path>,
        database_file: impl AsRef<Path>,
    ) -> Result<Self> {
        let config = YekongaConfig::from_file(config_file)?;
        let structure = DatabaseStructure::from_file(database_file)?;

        Ok(Self::new(config, structure))
    }

    pub fn config(&self) -> &YekongaConfig {
        &self.0.config
    }

    /// A query on a model by its singular PascalCase name, e.g. `"Order"`.
    pub fn query(&self, model: &str) -> std::result::Result<ModelQuery, DbError> {
        let found = self
            .model(model)
            .ok_or_else(|| DbError::UnknownModel(model.to_string()))?;
        Ok(ModelQuery::new(self.clone(), found))
    }

    /// The database backend.
    pub fn backend(&self) -> &Arc<dyn Backend> {
        &self.0.backend
    }

    /// Creates the database indexes the models need (MongoDB). `start` does
    /// this in the background unless `database.disableAutoIndexes` is set.
    pub async fn ensure_indexes(&self) -> std::result::Result<usize, DbError> {
        self.0
            .backend
            .ensure_indexes(self.0.models.values().map(|m| m.as_ref()).collect())
            .await
    }

    pub(crate) fn schema_cell(
        &self,
    ) -> &OnceLock<std::result::Result<async_graphql::dynamic::Schema, String>> {
        &self.0.graphql_schema
    }

    pub(crate) fn auth_schema_cell(
        &self,
    ) -> &OnceLock<std::result::Result<async_graphql::dynamic::Schema, String>> {
        &self.0.auth_schema
    }

    pub(crate) fn otp_sender_slot(&self) -> &RwLock<Option<crate::auth::OtpSender>> {
        &self.0.otp_sender
    }

    pub(crate) fn cloud_registry(&self) -> &RwLock<crate::cloud::CloudRegistry> {
        &self.0.cloud
    }

    pub(crate) fn cron_jobs(&self) -> &RwLock<Vec<crate::cron::CronJob>> {
        &self.0.cron_jobs
    }

    pub(crate) fn security_state(&self) -> &crate::security::Security {
        &self.0.security
    }

    /// The WebSocket server (change events and Socket.IO-style messaging).
    pub fn sockets(&self) -> &crate::socket::SocketServer {
        &self.0.sockets
    }

    /// Records a change on the request's audit trail, if it is enabled and
    /// the model isn't excluded (Go's `recordAuditChange` gate).
    pub(crate) fn record_audit_change(
        &self,
        request: Option<&Request>,
        change: crate::audit::AuditChange,
    ) {
        let config = &self.0.config;
        if !config.audit_trail.enabled || change.model == "AuditTrail" {
            return;
        }
        if config.audit_trail.exclude_models.contains(&change.model) {
            return;
        }
        if let Some(request) = request {
            request.add_audit_change(change);
        }
    }

    /// Persists a request's buffered audit changes as one batch, off the
    /// request path (Go's `flushAuditTrail`).
    pub(crate) fn flush_audit_trail(&self, request: &Request) {
        if !self.0.config.audit_trail.enabled {
            return;
        }
        let changes = request.take_audit_changes();
        if changes.is_empty() {
            return;
        }

        let app = self.clone();
        let auth = request.auth();
        let client = request.client();
        tokio::spawn(async move {
            let Ok(query) = app.query("AuditTrail") else {
                return;
            };
            for change in changes {
                let mut data = serde_json::json!({
                    "action": change.action,
                    "collection": change.collection,
                    "model": change.model,
                    "documentId": change.document_id,
                    "oldValues": change.old_values,
                    "newValues": change.new_values,
                });
                if let Some(auth) = &auth {
                    data["tenantId"] = serde_json::json!(auth.tenant_id);
                    data["profileId"] = serde_json::json!(auth.profile_id);
                    data["userId"] = serde_json::json!(auth.user_id);
                }
                if let Some(client) = &client {
                    data["ipAddress"] = serde_json::json!(client.ip_address);
                    data["userAgent"] = serde_json::json!(client.user_agent);
                    data["browser"] = serde_json::json!(client.user_agent);
                }
                let _ = query.clone().skip_before_commit().create(data).await;
            }
        });
    }

    pub(crate) fn caches(&self) -> &LookupCaches {
        &self.0.caches
    }

    /// The merged schema (built-in and app collections).
    pub fn database_structure(&self) -> &DatabaseStructure {
        &self.0.database_structure
    }

    /// A model by its singular PascalCase name, e.g. `"User"`.
    pub fn model(&self, name: &str) -> Option<Arc<DataModel>> {
        let model = self.0.models.get(name).cloned();
        if model.is_none() {
            tracing::error!("{name} is not available");
        }
        model
    }

    pub fn models(&self) -> &BTreeMap<String, Arc<DataModel>> {
        &self.0.models
    }

    /// Directory of the running executable.
    pub fn root_path(&self) -> &Path {
        &self.0.root_path
    }

    /// `APP_ENV=development`.
    pub fn is_dev(&self) -> bool {
        self.0.is_dev
    }

    /// `~/.yekonga-server/<app-name>`: local data and uploads.
    pub fn home_directory(&self) -> PathBuf {
        home_directory(&to_slug(&self.0.config.app_name))
    }

    // ----- routes --------------------------------------------------------

    pub fn get<H, F>(&self, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        self.route(Method::GET, path, handler);
    }

    pub fn post<H, F>(&self, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        self.route(Method::POST, path, handler);
    }

    pub fn put<H, F>(&self, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        self.route(Method::PUT, path, handler);
    }

    pub fn patch<H, F>(&self, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        self.route(Method::PATCH, path, handler);
    }

    pub fn delete<H, F>(&self, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        self.route(Method::DELETE, path, handler);
    }

    pub fn options<H, F>(&self, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        self.route(Method::OPTIONS, path, handler);
    }

    /// Registers the handler for GET, POST, PUT, PATCH, OPTIONS and DELETE.
    pub fn all<H, F>(&self, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        let handler = boxed_handler(handler);
        for method in [
            Method::GET,
            Method::POST,
            Method::PUT,
            Method::PATCH,
            Method::OPTIONS,
            Method::DELETE,
        ] {
            self.add_route(method, path, handler.clone());
        }
    }

    /// Registers a handler for one method. Routes are matched in
    /// registration order; the configured `baseURL` is prefixed.
    pub fn route<H, F>(&self, method: Method, path: &str, handler: H)
    where
        H: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = ()> + Send + 'static,
    {
        self.add_route(method, path, boxed_handler(handler));
    }

    fn add_route(&self, method: Method, path: &str, handler: HandlerFn) {
        let pattern = RoutePattern::new(&self.append_base_url(path));
        let mut routes = self.0.routes.write().unwrap_or_else(|e| e.into_inner());
        routes
            .entry(method)
            .or_default()
            .push(Arc::new(Route { pattern, handler }));
    }

    fn find_route(
        &self,
        method: &Method,
        path: &str,
    ) -> Option<(Arc<Route>, HashMap<String, String>)> {
        let routes = self.0.routes.read().unwrap_or_else(|e| e.into_inner());
        routes.get(method)?.iter().find_map(|route| {
            route
                .pattern
                .matches(path)
                .map(|params| (route.clone(), params))
        })
    }

    /// Prefixes the configured `baseURL`: `/users` -> `/v1/users`.
    pub fn append_base_url(&self, pattern: &str) -> String {
        let base_url = &self.0.config.base_url;
        let mut pattern = pattern.to_string();

        if !base_url.is_empty() && base_url != "/" {
            pattern = format!(
                "{}/{}",
                base_url.trim_end_matches('/'),
                pattern.trim_start_matches('/')
            );
        }

        let pattern = pattern.strip_suffix('/').unwrap_or(&pattern);
        format!("/{}", pattern.trim_start_matches('/'))
    }

    /// Marks a route pattern (a `path.Match` glob, e.g. `/shop/*`) as not
    /// needing an access token.
    pub fn set_public_route(&self, route: impl Into<String>) {
        self.0
            .public_routes
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .push(route.into());
    }

    pub fn public_routes(&self) -> Vec<String> {
        self.0
            .public_routes
            .read()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }

    // ----- middleware ----------------------------------------------------

    /// Adds a middleware at the given point of the chain (see
    /// [`crate::middleware`] for the order).
    pub fn middleware<M, F>(&self, kind: MiddlewareKind, middleware: M)
    where
        M: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = MiddlewareResult> + Send + 'static,
    {
        let middleware: MiddlewareFn = Arc::new(move |req, res| Box::pin(middleware(req, res)));
        let mut all = self
            .0
            .middlewares
            .write()
            .unwrap_or_else(|e| e.into_inner());

        match kind {
            MiddlewareKind::Global => all.global.push(middleware),
            MiddlewareKind::Init => all.init.push(middleware),
            MiddlewareKind::Preload => all.preload.push(middleware),
            MiddlewareKind::Catch => all.catch.push(middleware),
        }
    }

    /// Adds a global middleware (runs last, right before the handler).
    pub fn use_middleware<M, F>(&self, middleware: M)
    where
        M: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = MiddlewareResult> + Send + 'static,
    {
        self.middleware(MiddlewareKind::Global, middleware);
    }

    /// Adds a fallback for requests no route matched. Catch middleware run
    /// in order until one writes a response.
    pub fn catch<M, F>(&self, middleware: M)
    where
        M: Fn(Request, Response) -> F + Send + Sync + 'static,
        F: Future<Output = MiddlewareResult> + Send + 'static,
    {
        self.middleware(MiddlewareKind::Catch, middleware);
    }

    fn middleware_chain(&self, kind: MiddlewareKind) -> Vec<MiddlewareFn> {
        let all = self.0.middlewares.read().unwrap_or_else(|e| e.into_inner());
        match kind {
            MiddlewareKind::Global => all.global.clone(),
            MiddlewareKind::Init => all.init.clone(),
            MiddlewareKind::Preload => all.preload.clone(),
            MiddlewareKind::Catch => all.catch.clone(),
        }
    }

    pub(crate) fn is_token_optional_path(&self, path: &str) -> bool {
        let paths = self.0.token_paths.get_or_init(|| {
            let config = &self.0.config;
            let exact = [
                "me",
                "logout",
                "refresh",
                "languages",
                "excel-to-csv",
                "upload",
                "upload-files",
                "config/data",
                "config/report",
                "permissions",
                "config",
                "tenant",
                "tenant-config",
                "theme.css",
                "custom-style.css",
                &config.rest_api,
                &config.rest_auth_api,
                &config.graphql.api_route,
                &config.graphql.api_auth_route,
            ];
            let patterns = [
                "me/*",
                "refresh/*",
                "download/*",
                "translations/*",
                "image/*",
            ];

            TokenPaths {
                exact: exact.iter().map(|p| self.append_base_url(p)).collect(),
                patterns: patterns.iter().map(|p| self.append_base_url(p)).collect(),
            }
        });

        paths.exact.iter().any(|p| p == path) || paths.patterns.iter().any(|p| match_path(path, p))
    }

    // ----- static files --------------------------------------------------

    /// Serves files from a directory. Fails if the directory doesn't exist.
    pub fn serve_static(&self, mut config: StaticConfig) -> Result<()> {
        if config.index_file.is_empty() {
            config.index_file = "index.html".into();
        }
        if config.extensions.is_empty() {
            config.extensions = DEFAULT_EXTENSIONS.iter().map(|e| e.to_string()).collect();
        }
        if config.cache_max_age == 0 {
            config.cache_max_age = 2_592_000;
        }
        if !config.directory.is_dir() {
            return Err(Error::StaticDirectory(config.directory));
        }

        self.0
            .static_configs
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .push(Arc::new(config));
        Ok(())
    }

    pub fn static_configs(&self) -> Vec<Arc<StaticConfig>> {
        self.0
            .static_configs
            .read()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }

    fn is_static_path(&self, path: &str) -> bool {
        let ext = middleware::file_extension(path);
        !ext.is_empty()
            && self
                .static_configs()
                .iter()
                .any(|s| s.extensions.contains(&ext))
    }

    /// The file a static request maps to, if any.
    fn static_file(&self, path: &str) -> Option<(PathBuf, u64)> {
        // Never let a request climb out of a static directory.
        if path.split(['/', '\\']).any(|segment| segment == "..") {
            return None;
        }

        for config in self.static_configs() {
            let relative = path
                .strip_prefix(config.path_prefix.as_str())
                .unwrap_or(path)
                .trim_start_matches('/');
            let mut file = config.directory.join(relative);

            if file.is_dir() {
                file = file.join(&config.index_file);
            }
            if !file.is_file() {
                continue;
            }

            let ext = file
                .file_name()
                .map(|n| middleware::file_extension(&n.to_string_lossy()).to_lowercase())
                .unwrap_or_default();
            if config.extensions.contains(&ext) {
                let uploads = file
                    .to_string_lossy()
                    .replace('\\', "/")
                    .contains("/public/uploads/");
                let max_age = if uploads {
                    31_536_000
                } else {
                    config.cache_max_age
                };
                return Some((file, max_age));
            }
        }

        None
    }

    // ----- lifecycle -----------------------------------------------------

    /// Runs `f` once the server has started listening.
    pub fn when_ready(&self, f: impl FnOnce() + Send + 'static) {
        self.0
            .when_ready
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .push(Box::new(f));
    }

    /// Registers the framework's own routes and static directories.
    fn initialize(&self) {
        tracing::info!(app = %self.0.config.app_name, "initializing");

        self.all("/health", |_req, res| async move { res.text("Ok!") });
        self.all("/api-health", |_req, res| async move {
            res.json(&serde_json::json!({"status": "OK!"}))
        });
        self.all("/check-connection", |req, res| async move {
            let id = &req.app().config().connection_id;
            res.text(if id.is_empty() {
                "YEKONGA_CONNECTED"
            } else {
                id
            });
        });

        crate::auth::register_routes(self);
        crate::rest::register_graphql_route(self);
        crate::rest::register_rest_routes(self);

        for public in &self.0.config.public {
            match self.resolve_public_directory(public) {
                Some(directory) => {
                    let static_config = StaticConfig::new(directory, self.append_base_url("/"));
                    match self.serve_static(static_config) {
                        Ok(()) => {
                            tracing::info!(prefix = %self.append_base_url("/"), "static files enabled")
                        }
                        Err(err) => {
                            tracing::error!(%err, "failed to configure static file serving")
                        }
                    }
                }
                None => tracing::warn!(public, "public directory does not exist"),
            }
        }
    }

    /// Finds a `public` directory from the config: relative to the working
    /// directory, then to the executable's directory.
    fn resolve_public_directory(&self, public: &str) -> Option<PathBuf> {
        let relative = PathBuf::from(format!(
            "./{}",
            public.trim_start_matches("./").trim_start_matches('/')
        ));

        [relative.clone(), self.0.root_path.join(&relative)]
            .into_iter()
            .find(|p| p.is_dir())
    }

    /// The server as a tower service, ready for [`axum::serve`] or tests.
    /// Responses of 1 KB or more are gzip-compressed for clients that accept it.
    pub fn router(&self) -> axum::Router {
        let app = self.clone();
        let compress_when = SizeAbove::new(1024)
            .and(NotForContentType::GRPC)
            .and(NotForContentType::IMAGES)
            .and(NotForContentType::const_new("text/event-stream"));

        let socket_app = self.clone();
        let socket_path = self.append_base_url("/yekonga.io");
        let socket_handler = move || {
            let socket_app = socket_app.clone();
            axum::routing::get(
                move |ws: axum::extract::ws::WebSocketUpgrade, parts: http::request::Parts| {
                    let app = socket_app.clone();
                    async move { crate::socket::upgrade(app, ws, &parts).await }
                },
            )
        };

        axum::Router::new()
            .route(&socket_path, socket_handler())
            .route(&format!("{socket_path}/"), socket_handler())
            .fallback(move |request: http::Request<Body>| {
                let app = app.clone();
                let peer = request
                    .extensions()
                    .get::<ConnectInfo<SocketAddr>>()
                    .map(|c| c.0);
                async move { app.handle(request, peer).await }
            })
            .layer(
                CompressionLayer::new()
                    .gzip(true)
                    .compress_when(compress_when),
            )
    }

    /// Starts listening on `port`, or the configured `ports.server`, and
    /// serves until Ctrl-C.
    pub async fn start(&self, port: Option<u16>) -> Result<()> {
        let config = &self.0.config;
        if config.ports.secure {
            // TLS isn't ported yet; refusing is safer than silently serving plain HTTP.
            return Err(Error::Bind(
                format!("port {}", config.ports.ssl_server),
                std::io::Error::other(
                    "ports.secure (TLS) is not supported yet; terminate TLS at a reverse proxy",
                ),
            ));
        }
        if !config.database.disable_auto_indexes {
            // In the background: creating an existing index is a no-op, and
            // MongoDB builds new ones without blocking reads and writes.
            let app = self.clone();
            tokio::spawn(async move {
                match app.ensure_indexes().await {
                    Ok(0) => {}
                    Ok(n) => tracing::info!(indexes = n, "database indexes checked"),
                    Err(err) => tracing::error!(%err, "could not create database indexes"),
                }
            });
        }

        if config.has_cronjob {
            crate::cron::start(self.clone());
        }

        let port = port.unwrap_or(config.ports.server as u16);
        let address = format!("0.0.0.0:{port}");
        let listener = tokio::net::TcpListener::bind(&address)
            .await
            .map_err(|e| Error::Bind(address.clone(), e))?;

        tracing::info!("Server is running on {address}");
        for ready in
            std::mem::take(&mut *self.0.when_ready.lock().unwrap_or_else(|e| e.into_inner()))
        {
            tokio::task::spawn_blocking(ready);
        }

        let service = self
            .router()
            .into_make_service_with_connect_info::<SocketAddr>();
        axum::serve(listener, service)
            .with_graceful_shutdown(async {
                let _ = tokio::signal::ctrl_c().await;
            })
            .await
            .map_err(Error::Serve)
    }

    // ----- request pipeline ----------------------------------------------

    /// Handles one request (Go's `ServeHTTP`).
    async fn handle(
        &self,
        request: http::Request<Body>,
        peer: Option<SocketAddr>,
    ) -> http::Response<Body> {
        let (parts, body) = request.into_parts();
        let path = percent_encoding::percent_decode_str(parts.uri.path())
            .decode_utf8_lossy()
            .into_owned();

        // Abuse guards run first, before static files or body parsing, so a
        // blocked or throttled client is rejected as cheaply as possible.
        let client_key = crate::security::client_key(
            &parts.headers,
            peer,
            self.0.config.security.trust_proxy_headers,
        );
        if !self.allow_error_guard(&client_key).await {
            return text_response(StatusCode::FORBIDDEN, "forbidden");
        }
        if !self.allow_rate_limit(&client_key).await {
            let mut response = text_response(StatusCode::TOO_MANY_REQUESTS, "too many requests");
            response
                .headers_mut()
                .insert("retry-after", HeaderValue::from_static("1"));
            return response;
        }

        // Static files are served before any request parsing or middleware.
        if self.is_static_path(&path) {
            if let Some((file, max_age)) = self.static_file(&path) {
                let mut response =
                    response::serve_path(&parts.method, &parts.uri, &parts.headers, &file).await;
                if let Ok(value) = HeaderValue::from_str(&format!("max-age={max_age}")) {
                    response.headers_mut().insert(CACHE_CONTROL, value);
                }
                return response;
            }
        }

        let max_body = match self.0.config.security.max_body_bytes {
            n if n > 0 => n as usize,
            _ => DEFAULT_MAX_BODY_BYTES,
        };
        let raw_body = match axum::body::to_bytes(body, max_body).await {
            Ok(bytes) => bytes,
            Err(_) => {
                let mut response = http::Response::new(Body::from("request body too large"));
                *response.status_mut() = StatusCode::PAYLOAD_TOO_LARGE;
                return response;
            }
        };

        let origin = [
            header_value(&parts.headers, "origin"),
            header_value(&parts.headers, "referer"),
        ]
        .into_iter()
        .find(|v| !v.is_empty())
        .map(String::from)
        .unwrap_or_else(|| {
            let host = header_value(&parts.headers, "host");
            if host.is_empty() {
                "*".to_string()
            } else {
                host.to_string()
            }
        });
        let has_enabled_cookie = cookie_is_set(&parts.headers, COOKIE_ENABLED_KEY);

        let route = self.find_route(&parts.method, &path);
        let (route, params) = match route {
            Some((route, params)) => (Some(route), params),
            None => (None, HashMap::new()),
        };

        let req = Request::new(RequestParts {
            app: self.clone(),
            method: parts.method,
            uri: parts.uri,
            version: parts.version,
            headers: parts.headers,
            path,
            raw_body,
            params,
            peer,
        });
        let res = Response::new(req.clone());

        if !has_enabled_cookie {
            let value = match self.0.config.connection_id.as_str() {
                "" => "YEKONGA_CONNECTED",
                id => id,
            };
            let secure = if self.0.config.secure_only {
                "; Secure"
            } else {
                ""
            };
            res.append_header(
                "set-cookie",
                &format!("{COOKIE_ENABLED_KEY}={value}; Max-Age=2592000; HttpOnly{secure}"),
            );
        }

        if self.0.config.cors {
            res.set_header("access-control-allow-origin", &origin);
        }
        res.set_header(
            "access-control-allow-headers",
            "content-type, authorization, x-requested-with, x-csrf-token, timezone, upgrade-insecure-requests",
        );
        res.set_header("access-control-allow-credentials", "true");
        res.set_header(
            "access-control-allow-methods",
            "GET, POST, OPTIONS, PUT, PATCH, DELETE",
        );

        let pipeline =
            AssertUnwindSafe(self.run_pipeline(route, req.clone(), res.clone())).catch_unwind();
        match pipeline.await {
            Ok(Ok(())) => {}
            Ok(Err(abort)) => res.abort(abort.status, &abort.message),
            Err(_) => {
                tracing::error!(path = %req.path(), "handler panicked");
                res.abort(500, "");
            }
        }

        self.flush_audit_trail(&req);
        let response = res.finish().await;
        self.record_error_response(&req, &client_key, response.status().as_u16())
            .await;
        response
    }

    async fn run_pipeline(
        &self,
        route: Option<Arc<Route>>,
        req: Request,
        res: Response,
    ) -> Result<(), Abort> {
        middleware::run_builtin_head(&req, &res)?;
        for m in self.middleware_chain(MiddlewareKind::Preload) {
            m(req.clone(), res.clone()).await?;
        }

        middleware::run_builtin_auth(&req, &res).await?;
        for kind in [MiddlewareKind::Init, MiddlewareKind::Global] {
            for m in self.middleware_chain(kind) {
                m(req.clone(), res.clone()).await?;
            }
        }

        if let Some(route) = route {
            (route.handler)(req, res).await;
            return Ok(());
        }

        // No route: catch middleware run until one responds.
        for m in self.middleware_chain(MiddlewareKind::Catch) {
            m(req.clone(), res.clone()).await?;
            if res.is_written() {
                return Ok(());
            }
        }

        match req.path() {
            "" | "/" | "/index.html" => res.html(PAGE_INDEX),
            _ => {
                res.status(404).html(PAGE_404);
            }
        }

        Ok(())
    }
}

/// The backend `database.kind` selects.
fn default_backend(config: &YekongaConfig) -> Arc<dyn Backend> {
    match config.database.kind() {
        None | Some(DatabaseKind::Local) => {
            let dir = home_directory_path(&to_slug(&config.app_name)).join("database");
            tracing::info!(dir = %dir.display(), "using the local database");
            Arc::new(LocalBackend::open(dir))
        }
        #[cfg(feature = "mongodb")]
        Some(DatabaseKind::Mongodb) => {
            Arc::new(crate::db::MongoBackend::new(config.database.clone()))
        }
        #[cfg(feature = "mysql")]
        Some(DatabaseKind::Mysql | DatabaseKind::Sql) => {
            Arc::new(crate::db::SqlBackend::new(config.database.clone()))
        }
        #[allow(unreachable_patterns)]
        Some(kind) => {
            tracing::error!(
                kind = kind.as_str(),
                "this database kind is not enabled in this build (see the crate features); queries will fail"
            );
            Arc::new(UnsupportedBackend(kind.as_str().to_string()))
        }
    }
}

fn boxed_handler<H, F>(handler: H) -> HandlerFn
where
    H: Fn(Request, Response) -> F + Send + Sync + 'static,
    F: Future<Output = ()> + Send + 'static,
{
    Arc::new(move |req, res| Box::pin(handler(req, res)))
}

/// A plain-text response with a status (for the abuse guards).
fn text_response(status: StatusCode, body: &'static str) -> http::Response<Body> {
    let mut response = http::Response::new(Body::from(body));
    *response.status_mut() = status;
    response
}

fn cookie_is_set(headers: &http::HeaderMap, name: &str) -> bool {
    headers
        .get_all(http::header::COOKIE)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(';'))
        .filter_map(|pair| pair.trim().split_once('='))
        .any(|(k, v)| k == name && !v.is_empty())
}
