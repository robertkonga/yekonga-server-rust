//! The audit trail: one recorded data change (port of `AuditTrailChange` in
//! `yekonga/request.go`). Changes are buffered on the request and flushed as
//! a batch to the `AuditTrail` collection once the request ends.

use crate::db::DataMap;

/// One create, update or delete recorded for the audit trail.
#[derive(Clone, Debug)]
pub struct AuditChange {
    pub action: String,
    pub collection: String,
    pub model: String,
    pub document_id: String,
    pub old_values: DataMap,
    pub new_values: DataMap,
}
