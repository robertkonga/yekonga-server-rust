use std::path::PathBuf;

/// Errors from loading config/schema files and starting the server.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("cannot read {0}: {1}")]
    Io(PathBuf, #[source] std::io::Error),

    #[error("invalid JSON in {0}: {1}")]
    Json(PathBuf, #[source] serde_json::Error),

    #[error("static directory {0} does not exist")]
    StaticDirectory(PathBuf),

    #[error("cannot listen on {0}: {1}")]
    Bind(String, #[source] std::io::Error),

    #[error("server error: {0}")]
    Serve(#[source] std::io::Error),
}

pub type Result<T, E = Error> = std::result::Result<T, E>;
