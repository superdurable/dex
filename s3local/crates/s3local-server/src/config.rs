// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Command-line and environment configuration of the s3local server.

use std::net::SocketAddr;
use std::path::PathBuf;

use s3local::Durability;

/// Configuration of one s3local server process; every flag also reads an `S3LOCAL_*` environment variable.
#[derive(Clone, clap::Parser)]
#[command(name = "s3local", about = "S3-compatible object store on local disk")]
pub struct ServerConfig {
    /// TCP address the HTTP endpoint binds to. Default `127.0.0.1:9000`. Clients use path-style URLs
    /// against this address, such as `http://127.0.0.1:9000/<bucket>/<key>`; port 0 picks a free port.
    #[arg(long, env = "S3LOCAL_LISTEN_ADDRESS", default_value = "127.0.0.1:9000")]
    pub listen_address: SocketAddr,
    /// Directory that holds the metadata database and the data files. Default `s3local-data` in the
    /// working directory; created when missing. Only one process may use a directory at a time.
    #[arg(long, env = "S3LOCAL_DATA_DIRECTORY", default_value = "s3local-data")]
    pub data_directory: PathBuf,
    /// Write durability. Default `strict`: every acknowledged write is flushed with fsync and survives a
    /// crash. `relaxed` skips fsync for speed; a crash may lose acknowledged writes, so never use it for
    /// crash-recovery tests.
    #[arg(long, env = "S3LOCAL_DURABILITY", value_enum, default_value_t = DurabilityFlag::Strict)]
    pub durability: DurabilityFlag,
    /// Default 0; deterministic seed for probabilistic fault selection. Rule replacement restarts its sequence.
    #[arg(long, env = "S3LOCAL_FAULT_SEED", default_value_t = 0)]
    pub fault_seed: u64,
    /// Default 1; slash-separated key components grouped together in prefix counters. Zero groups all keys.
    #[arg(long, env = "S3LOCAL_COUNTER_PREFIX_DEPTH", default_value_t = 1)]
    pub counter_prefix_depth: usize,
    /// Default 1024; maximum recent requests retained in memory. Zero disables the request ring buffer.
    #[arg(long, env = "S3LOCAL_REQUEST_LOG_CAPACITY", default_value_t = 1024)]
    pub request_log_capacity: usize,
    /// Default md5; opaque gives every stored version a stable ETag unrelated to its content hash.
    #[arg(long, env = "S3LOCAL_ETAG_MODE", value_enum, default_value_t = EtagModeFlag::Md5)]
    pub etag_mode: EtagModeFlag,
    /// Default off; strict validates SigV4 headers, payloads, signed chunks, and signed trailers before dispatch.
    #[arg(long, env = "S3LOCAL_SIGNATURE_VERIFICATION", value_enum, default_value_t = SignatureVerification::Off)]
    pub signature_verification: SignatureVerification,
    /// Default off; root requires a root SigV4 signature on test controls when signature verification is strict.
    #[arg(long, env = "S3LOCAL_CONTROL_AUTHENTICATION", value_enum, default_value_t = ControlAuthentication::Off)]
    pub control_authentication: ControlAuthentication,
    /// Default s3local; root access key in strict mode. Only this key can manage credentials.
    #[arg(long, env = "S3LOCAL_ACCESS_KEY_ID", default_value = "s3local")]
    pub access_key_id: String,
    /// Default s3local; HMAC secret used in strict mode. Use test credentials only; never production credentials.
    #[arg(long, env = "S3LOCAL_SECRET_ACCESS_KEY", default_value = "s3local")]
    pub secret_access_key: String,
    /// Seconds between lifecycle passes. Default `60`; `0` disables the background pass. Each pass
    /// removes everything due at the lifecycle clock, so expiration happens within one interval of
    /// its due time rather than on S3's daily schedule.
    #[arg(
        long,
        env = "S3LOCAL_LIFECYCLE_SCAN_INTERVAL_SECONDS",
        default_value_t = 60
    )]
    pub lifecycle_scan_interval_seconds: u64,
    /// Request dialect the endpoint emulates. Default `amazon-s3`. `aliyun-oss` emulates the S3-compatible
    /// endpoint of Aliyun OSS: PutObject and CompleteMultipartUpload reject `If-None-Match` and `If-Match`
    /// with 400 NotImplemented and honor `x-oss-forbid-overwrite: true` with 409 FileAlreadyExists,
    /// UploadPart responses omit checksum headers, and DeleteObjects requires `Content-MD5`. As in OSS,
    /// a bucket whose versioning was ever enabled or suspended ignores `x-oss-forbid-overwrite`.
    #[arg(long, env = "S3LOCAL_API_DIALECT", value_enum, default_value_t = S3ApiDialect::AmazonS3)]
    pub api_dialect: S3ApiDialect,
}

/// Command-line spelling of [`Durability`].
#[derive(Clone, Copy, Debug, PartialEq, Eq, clap::ValueEnum)]
pub enum DurabilityFlag {
    Strict,
    ProcessCrashSafe,
    Relaxed,
}

impl From<DurabilityFlag> for Durability {
    fn from(flag: DurabilityFlag) -> Durability {
        match flag {
            DurabilityFlag::Strict => Durability::Strict,
            DurabilityFlag::ProcessCrashSafe => Durability::ProcessCrashSafe,
            DurabilityFlag::Relaxed => Durability::Relaxed,
        }
    }
}

/// Signature verification policy for S3 requests.
#[derive(Clone, Copy, Debug, PartialEq, Eq, clap::ValueEnum)]
pub enum SignatureVerification {
    Off,
    Strict,
}

/// Authentication policy for test-control endpoints.
#[derive(Clone, Copy, Debug, PartialEq, Eq, clap::ValueEnum)]
pub enum ControlAuthentication {
    Off,
    Root,
}

/// S3 request dialect served by the endpoint.
#[derive(Clone, Copy, Debug, PartialEq, Eq, clap::ValueEnum)]
#[non_exhaustive]
pub enum S3ApiDialect {
    AmazonS3,
    AliyunOss,
}

/// Command-line ETag representation.
#[derive(Clone, Copy, Debug, PartialEq, Eq, clap::ValueEnum)]
pub enum EtagModeFlag {
    Md5,
    Opaque,
}

impl From<EtagModeFlag> for s3local::EtagMode {
    fn from(mode: EtagModeFlag) -> Self {
        match mode {
            EtagModeFlag::Md5 => Self::Md5,
            EtagModeFlag::Opaque => Self::Opaque,
        }
    }
}

impl Default for ServerConfig {
    fn default() -> Self {
        let command = <Self as clap::CommandFactory>::command()
            .mut_args(|argument| argument.env(None::<&str>));
        <Self as clap::FromArgMatches>::from_arg_matches(&command.get_matches_from(["s3local"]))
            .expect("clap defaults form valid server configuration")
    }
}

impl std::fmt::Debug for ServerConfig {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("ServerConfig")
            .field("listen_address", &self.listen_address)
            .field("data_directory", &self.data_directory)
            .field("durability", &self.durability)
            .field("fault_seed", &self.fault_seed)
            .field("counter_prefix_depth", &self.counter_prefix_depth)
            .field("request_log_capacity", &self.request_log_capacity)
            .field("etag_mode", &self.etag_mode)
            .field("signature_verification", &self.signature_verification)
            .field("control_authentication", &self.control_authentication)
            .field("access_key_id", &self.access_key_id)
            .field("secret_access_key", &"<redacted>")
            .field(
                "lifecycle_scan_interval_seconds",
                &self.lifecycle_scan_interval_seconds,
            )
            .field("api_dialect", &self.api_dialect)
            .finish()
    }
}
