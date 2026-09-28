//! Cloud functions and database triggers (port of `yekonga/cloud_functions.go`).
//!
//! - **Cloud functions** are named async functions registered with
//!   [`Yekonga::define`] and run with [`Yekonga::run`].
//! - **Triggers** run around database operations: before/after find, create,
//!   update and delete, per model or for every model (`*_all`). A `before`
//!   trigger can change the data or reject the operation.
//! - **Auth triggers** run around login, OTP and registration.
//! - [`Yekonga::set_fetch_tenant_by_domain`] resolves a tenant for a host on
//!   a tenant-catch server.

use std::collections::HashMap;
use std::sync::Arc;

use serde_json::Value;

use crate::app::{BoxFuture, Yekonga};
use crate::request::Request;

/// When a trigger runs, relative to the database operation.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum TriggerAction {
    BeforeFind,
    AfterFind,
    BeforeCreate,
    AfterCreate,
    BeforeUpdate,
    AfterUpdate,
    BeforeDelete,
    AfterDelete,
    BeforeOtp,
    AfterOtp,
    BeforeLogin,
    AfterLogin,
    BeforeRegister,
    AfterRegister,
}

/// What a trigger decides (the meaning of Go's `interface{}` return):
/// carry on, replace the data, or reject the operation.
pub enum TriggerReturn {
    /// Leave the data as it is.
    Continue,
    /// Use this value instead (a `before` trigger changes the input or
    /// filters; an `after` trigger changes the result).
    Replace(Value),
    /// Stop the operation (a `before` trigger only).
    Reject,
}

/// Passed to a trigger: the request it runs for and the data it acts on.
#[derive(Clone)]
pub struct TriggerContext {
    pub app: Yekonga,
    pub request: Option<Request>,
    pub model: String,
    pub action: TriggerAction,
    /// A `before` trigger gets the input (create/update) or filters
    /// (find/delete); an `after` trigger gets the result.
    pub data: Value,
    pub access_role: String,
    pub route: String,
}

/// Passed to a cloud function.
#[derive(Clone)]
pub struct CloudContext {
    pub app: Yekonga,
    pub request: Option<Request>,
}

pub type TriggerFn = Arc<dyn Fn(TriggerContext) -> BoxFuture<TriggerReturn> + Send + Sync>;
pub type CloudFn =
    Arc<dyn Fn(Value, CloudContext) -> BoxFuture<Result<Value, String>> + Send + Sync>;

/// The cloud function `set_fetch_tenant_by_domain` registers under.
pub(crate) const FETCH_TENANT_BY_DOMAIN: &str = "__SET_FETCH_TENANT_BY_DOMAIN__";

/// Every registered function and trigger.
#[derive(Default, Clone)]
pub(crate) struct CloudRegistry {
    functions: HashMap<String, CloudFn>,
    /// Keyed by model, action and the access slug (role/route), as in Go.
    triggers: HashMap<(String, TriggerAction, String), TriggerFn>,
    trigger_all: HashMap<TriggerAction, TriggerFn>,
    auth_triggers: HashMap<TriggerAction, TriggerFn>,
}

/// The access slug Go builds from a role and route (`ToSlug(role_route)`).
fn access_slug(role: &str, route: &str) -> String {
    let combined = match (role.is_empty(), route.is_empty()) {
        (true, _) => route.to_string(),
        (false, true) => role.to_string(),
        (false, false) => format!("{role}_{route}"),
    };
    crate::helper::to_slug(&combined)
}

impl Yekonga {
    // ----- cloud functions ---------------------------------------------------------------

    /// Registers a named cloud function. Returns an error if one already
    /// exists with that name (Go's `Define`).
    pub fn define(
        &self,
        name: &str,
        function: impl Fn(Value, CloudContext) -> BoxFuture<Result<Value, String>>
            + Send
            + Sync
            + 'static,
    ) -> Result<(), String> {
        let mut registry = self
            .cloud_registry()
            .write()
            .unwrap_or_else(|e| e.into_inner());
        if registry.functions.contains_key(name) {
            return Err(format!("cloud function {name} already exists"));
        }
        registry
            .functions
            .insert(name.to_string(), Arc::new(function));
        Ok(())
    }

    /// Runs a named cloud function, or returns `Ok(Null)` if none is
    /// registered (Go's `Run`).
    pub async fn run(
        &self,
        name: &str,
        data: Value,
        request: Option<&Request>,
    ) -> Result<Value, String> {
        let function = self
            .cloud_registry()
            .read()
            .unwrap_or_else(|e| e.into_inner())
            .functions
            .get(name)
            .cloned();
        match function {
            Some(function) => {
                let ctx = CloudContext {
                    app: self.clone(),
                    request: request.cloned(),
                };
                function(data, ctx).await
            }
            None => Ok(Value::Null),
        }
    }

    fn has_function(&self, name: &str) -> bool {
        self.cloud_registry()
            .read()
            .unwrap_or_else(|e| e.into_inner())
            .functions
            .contains_key(name)
    }

    // ----- triggers ----------------------------------------------------------------------

    /// Registers a trigger for one model. `access_role` and `route` narrow it
    /// to a GraphQL role and route (empty matches any).
    pub fn set_trigger(
        &self,
        model: &str,
        action: TriggerAction,
        access_role: &str,
        route: &str,
        function: impl Fn(TriggerContext) -> BoxFuture<TriggerReturn> + Send + Sync + 'static,
    ) {
        let key = (model.to_string(), action, access_slug(access_role, route));
        self.cloud_registry()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .triggers
            .insert(key, Arc::new(function));
    }

    /// Registers a trigger that runs for every model.
    pub fn set_trigger_all(
        &self,
        action: TriggerAction,
        function: impl Fn(TriggerContext) -> BoxFuture<TriggerReturn> + Send + Sync + 'static,
    ) {
        self.cloud_registry()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .trigger_all
            .insert(action, Arc::new(function));
    }

    /// Registers an auth trigger (login, OTP, registration).
    pub fn set_auth_trigger(
        &self,
        action: TriggerAction,
        function: impl Fn(TriggerContext) -> BoxFuture<TriggerReturn> + Send + Sync + 'static,
    ) {
        self.cloud_registry()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .auth_triggers
            .insert(action, Arc::new(function));
    }

    /// Runs a per-model trigger and the matching `*_all` trigger in turn,
    /// threading the data through any that replace it (Go's
    /// `runTriggerAction`). Returns `None` when a trigger rejects.
    pub(crate) async fn run_trigger(
        &self,
        model: &str,
        action: TriggerAction,
        request: Option<&Request>,
        mut data: Value,
        access_role: &str,
        route: &str,
    ) -> Option<Value> {
        let (all, one) = {
            let registry = self
                .cloud_registry()
                .read()
                .unwrap_or_else(|e| e.into_inner());
            let one = registry
                .triggers
                .get(&(model.to_string(), action, access_slug(access_role, route)))
                .cloned();
            (registry.trigger_all.get(&action).cloned(), one)
        };

        for function in [all, one].into_iter().flatten() {
            let ctx = TriggerContext {
                app: self.clone(),
                request: request.cloned(),
                model: model.to_string(),
                action,
                data: data.clone(),
                access_role: access_role.to_string(),
                route: route.to_string(),
            };
            match function(ctx).await {
                TriggerReturn::Continue => {}
                TriggerReturn::Replace(value) => data = value,
                TriggerReturn::Reject => return None,
            }
        }

        Some(data)
    }

    /// Runs an auth trigger (Go's `authTriggerCallback`). Returns `None` when
    /// none is registered, otherwise what it decided.
    pub(crate) async fn run_auth_trigger(
        &self,
        action: TriggerAction,
        request: Option<&Request>,
        data: Value,
    ) -> Option<TriggerReturn> {
        let function = self
            .cloud_registry()
            .read()
            .unwrap_or_else(|e| e.into_inner())
            .auth_triggers
            .get(&action)
            .cloned()?;
        let ctx = TriggerContext {
            app: self.clone(),
            request: request.cloned(),
            model: String::new(),
            action,
            data,
            access_role: String::new(),
            route: String::new(),
        };
        Some(function(ctx).await)
    }

    // ----- tenant fetch ------------------------------------------------------------------

    /// Registers the function that resolves a tenant for a host on a
    /// tenant-catch server (Go's `SetFetchTenantByDomain`).
    pub fn set_fetch_tenant_by_domain(
        &self,
        function: impl Fn(Value, CloudContext) -> BoxFuture<Result<Value, String>>
            + Send
            + Sync
            + 'static,
    ) -> Result<(), String> {
        self.define(FETCH_TENANT_BY_DOMAIN, function)
    }

    /// Resolves `{domain, tenantId}` for a host (Go's `FetchTenantByDomain`):
    /// the TenantCatch record if there is one, otherwise the registered
    /// function, whose result is cached as a new TenantCatch record.
    pub(crate) async fn fetch_tenant_by_domain(
        &self,
        domain: &str,
        request: &Request,
    ) -> Option<Value> {
        if domain.is_empty() {
            return None;
        }

        let mut tenant_id = Value::Null;
        if self.config().has_tenant_catch {
            if let Some(record) = self.tenant_catch_by_domain(domain).await {
                tenant_id = record.get("tenantId").cloned().unwrap_or(Value::Null);
            }
        }

        let mut domain = domain.to_string();
        if crate::db::values::is_empty(&tenant_id) && self.has_function(FETCH_TENANT_BY_DOMAIN) {
            if let Ok(response) = self
                .run(
                    FETCH_TENANT_BY_DOMAIN,
                    serde_json::json!(domain),
                    Some(request),
                )
                .await
            {
                let new_domain = response.get("domain").cloned().unwrap_or(Value::Null);
                let new_tenant = response.get("tenantId").cloned().unwrap_or(Value::Null);
                if !crate::db::values::is_empty(&new_domain) {
                    domain = new_domain.as_str().unwrap_or(&domain).to_string();
                }
                tenant_id = new_tenant;

                if self.config().has_tenant_catch
                    && !domain.is_empty()
                    && !crate::db::values::is_empty(&tenant_id)
                {
                    if let Ok(q) = self.query(crate::lookup::TENANT_CATCH_MODEL) {
                        let _ = q
                            .create(serde_json::json!({"domain": domain, "tenantId": tenant_id}))
                            .await;
                        self.caches().tenant_catch.clear();
                    }
                }
            }
        }

        Some(serde_json::json!({"domain": domain, "tenantId": tenant_id}))
    }
}

/// Convenience registration methods named like Go's (`before_create`, …).
macro_rules! trigger_methods {
    ($($method:ident => $action:ident),* $(,)?) => {
        impl Yekonga {
            $(
                #[doc = concat!("Registers a `", stringify!($action), "` trigger for a model.")]
                pub fn $method(
                    &self,
                    model: &str,
                    function: impl Fn(TriggerContext) -> BoxFuture<TriggerReturn> + Send + Sync + 'static,
                ) {
                    self.set_trigger(model, TriggerAction::$action, "", "", function);
                }
            )*
        }
    };
}

trigger_methods! {
    before_find => BeforeFind,
    after_find => AfterFind,
    before_create => BeforeCreate,
    after_create => AfterCreate,
    before_update => BeforeUpdate,
    after_update => AfterUpdate,
    before_delete => BeforeDelete,
    after_delete => AfterDelete,
}
