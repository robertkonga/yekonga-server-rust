//! Utility functions shared across the framework (the Rust counterpart of the
//! Go `helper` package; only what the ported modules need so far).

mod contact;
pub mod jwt;
pub mod lenient;
mod naming;
mod web;

pub use contact::*;
pub use naming::*;
pub use web::*;
