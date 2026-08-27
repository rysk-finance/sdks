use std::fmt;

/// What can go wrong before the cli is ever spawned.
///
/// The other sdks raise; rust returns. Every variant here is a refusal to hand
/// the cli something it would sign wrongly or reject, caught while the args are
/// still being built.
#[derive(Debug)]
pub enum Error {
    /// A domain the cli could not sign: a salt, a chain that is not the quote's,
    /// or a missing field.
    Domain(String),
    /// The cli is older than this sdk needs, and strict_version was set.
    CliVersion(String),
    /// Spawning or downloading the cli failed.
    Io(std::io::Error),
    /// A batch could not be serialised.
    Json(serde_json::Error),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Domain(m) => write!(f, "{m}"),
            Error::CliVersion(m) => write!(f, "{m}"),
            Error::Io(e) => write!(f, "{e}"),
            Error::Json(e) => write!(f, "{e}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Io(e) => Some(e),
            Error::Json(e) => Some(e),
            _ => None,
        }
    }
}

impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Error::Io(e)
    }
}

impl From<serde_json::Error> for Error {
    fn from(e: serde_json::Error) -> Self {
        Error::Json(e)
    }
}
