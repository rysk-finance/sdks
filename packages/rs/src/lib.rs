//! Rust sdk for Rysk v12.
//!
//! Like the typescript and python sdks, this one does not talk to the api
//! itself: it builds argument lists for the `ryskV12` cli and spawns it. The
//! cli owns the signing key handling, the websocket and the eip712 signing, so
//! all three sdks stay thin and agree on flags by construction.

mod client;
mod error;
mod models;
mod nonce;

pub use client::{Env, Rysk, RELEASES_URL};
pub use error::Error;
pub use models::{
    ChainId, ErrorData, JsonRpcResponse, Quote, QuoteNotification, Request, Transfer,
    TypedDataDomain,
};
pub use nonce::NonceCounter;
