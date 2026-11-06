// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::error::Error;
use std::net::{IpAddr, Ipv6Addr};

use rustls::RootCertStore;
use rustls::pki_types::{CertificateDer, pem::PemObject};
use tonic::transport::{ClientTlsConfig, Endpoint};

use crate::{SdkError, SdkResult, ServiceError};

/// Enables verified TLS for a Client or Worker's outbound FlowService connection.
///
/// Pass independently to [`crate::ClientOptions::flow_service_tls`] and
/// [`crate::WorkerOptions::flow_service_tls`]. Omitting these settings keeps plaintext gRPC.
/// An empty options value enables TLS 1.2 or newer with native system roots. Additional roots
/// extend that trust store. The endpoint must negotiate HTTP/2 through ALPN h2.
/// The address must be `host:port`, `dns:///host:port`, or `dns://authority/host:port`.
/// DNS authority syntax is accepted, but resolution uses the operating system resolver.
/// Credentials remain bearer call metadata. These settings do not change an API-key token endpoint.
///
/// [`crate::Client::try_new`] and [`crate::Worker::try_new`] return
/// [`SdkError::InvalidArgument`] for an invalid address, nonempty bundle without a valid root,
/// or invalid server name. Native-root loading errors are logged; successfully loaded roots
/// remain usable. An empty trust store constructs successfully but cannot verify a server.
/// Connections open lazily: certificate, hostname, and handshake failures appear on the first
/// Client call or [`crate::Worker::start`] as service errors.
///
/// ```no_run
/// use std::sync::Arc;
/// use dex_sdk::{BlobCache, BlobCacheConfig, Client, ClientOptions, FileCredential,
///     FlowServiceTlsOptions, Registry, Worker, WorkerOptions};
/// let cache = Arc::new(BlobCache::open(BlobCacheConfig::new(
///     "/tmp/dex-tls-blobs", 64 * 1024 * 1024, 0,
/// )?)?);
/// let tls = FlowServiceTlsOptions::new()
///     .additional_root_cas_pem(std::fs::read("/etc/dex/root-ca.pem")?);
/// let credential = Arc::new(FileCredential::new("/var/run/secrets/dex/token")?);
/// let registry = Registry::new();
/// let worker = Worker::try_new(registry.clone(), cache.clone(), WorkerOptions::new()
///     .server_address("dex.example.com:443").flow_service_tls(tls.clone())
///     .credential(credential.clone()))?;
/// let client = Client::try_new(registry, cache, ClientOptions::new()
///     .server_address("dex.example.com:443").flow_service_tls(tls)
///     .credential(credential))?;
/// client.health_check()?;
/// worker.start()?;
/// # Ok::<(), Box<dyn std::error::Error>>(())
/// ```
#[derive(Clone, Default)]
pub struct FlowServiceTlsOptions {
    additional_root_cas_pem: Option<Vec<u8>>,
    server_name: Option<String>,
}

impl FlowServiceTlsOptions {
    /// Creates verified TLS options using native system roots and the address host.
    ///
    /// Equivalent to [`Self::default`]. No additional roots or server name override are set.
    /// Pass these options to a Client or Worker as shown in the enclosing type's example.
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds PEM-encoded CA certificates to the native system trust store.
    ///
    /// Defaults to no additional certificates. Empty bytes have the same effect.
    /// Takes ownership of `value` and returns these options with that bundle. Construction reads
    /// the bytes once; file changes are not watched. A nonempty bundle must contain at least
    /// one parsable certificate, or construction returns [`SdkError::InvalidArgument`].
    /// Other blocks and malformed certificates are skipped when another certificate parses.
    /// Additional roots remain usable when the native store is empty or fails to load.
    /// See the enclosing type's example.
    pub fn additional_root_cas_pem(mut self, value: impl Into<Vec<u8>>) -> Self {
        self.additional_root_cas_pem = Some(value.into());
        self
    }

    /// Overrides the address host for certificate verification, SNI, and HTTP/2 authority.
    ///
    /// Defaults to verifying the address host and retaining its host and port as authority.
    /// Takes ownership of `value` and returns these options with that override. Use a DNS name
    /// or unbracketed IP address without a port. Empty or invalid names cause construction to
    /// return [`SdkError::InvalidArgument`]. An IP address sends no SNI and requires a matching
    /// IP SAN. An override becomes the authority without a port; a gateway must accept that name.
    /// An IPv6 override is bracketed in `:authority` (for example, `[::1]`), while certificate
    /// verification uses the bare address. See the enclosing type's example, adding
    /// `.server_name("dex.example.com")` to the TLS options when a gateway requires that name.
    pub fn server_name(mut self, value: impl Into<String>) -> Self {
        self.server_name = Some(value.into());
        self
    }
}

impl std::fmt::Debug for FlowServiceTlsOptions {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("FlowServiceTlsOptions")
            .field(
                "additional_root_cas_pem_bytes",
                &self.additional_root_cas_pem.as_ref().map(Vec::len),
            )
            .field("server_name", &self.server_name)
            .finish()
    }
}

pub(crate) fn flow_service_endpoint(
    address: &str,
    options: Option<&FlowServiceTlsOptions>,
    operation: &'static str,
) -> SdkResult<Endpoint> {
    let Some(options) = options else {
        let address = if address.contains("://") {
            address.to_string()
        } else {
            format!("http://{address}")
        };
        return Endpoint::from_shared(address)
            .map_err(|error| transport_error(operation, "build FlowService endpoint", error));
    };
    let endpoint_address = tls_endpoint_address(address)?;
    let (host, _) = endpoint_address
        .rsplit_once(':')
        .expect("validated TLS endpoint has a port");
    let host = host.trim_start_matches('[').trim_end_matches(']');
    let mut tls = ClientTlsConfig::new().domain_name(host);
    let mut roots = RootCertStore::empty();
    if let Some(server_name) = &options.server_name {
        if !is_valid_tls_name(server_name)
            || rustls::pki_types::ServerName::try_from(server_name.as_str()).is_err()
        {
            return Err(invalid_tls(
                "flow_service_tls.server_name must be a nonempty DNS name or IP address without a port",
            ));
        }
        tls = tls.domain_name(server_name);
    }
    let mut skipped_added_count = 0;
    if let Some(bundle) = &options.additional_root_cas_pem {
        let (added_count, skipped_count) = add_additional_certificates(&mut roots, bundle);
        skipped_added_count = skipped_count;
        if !bundle.is_empty() && added_count == 0 {
            return Err(invalid_tls(
                "flow_service_tls.additional_root_cas_pem contains no PEM certificate",
            ));
        }
    }
    let mut endpoint = Endpoint::from_shared(format!("https://{endpoint_address}"))
        .map_err(|error| transport_error(operation, "build FlowService endpoint", error))?;
    if let Some(server_name) = &options.server_name {
        let authority = if server_name.parse::<Ipv6Addr>().is_ok() {
            format!("[{server_name}]")
        } else {
            server_name.clone()
        };
        endpoint = endpoint.origin(format!("https://{authority}").parse().map_err(|_| {
            invalid_tls("flow_service_tls.server_name is not a valid HTTP/2 authority")
        })?);
    }
    let native = rustls_native_certs::load_native_certs();
    if !native.errors.is_empty() {
        tracing::warn!(
            error_count = native.errors.len(),
            "failed to load some native root CAs"
        );
    }
    let (_, skipped_count) = roots.add_parsable_certificates(native.certs);
    if skipped_count > 0 {
        tracing::debug!(skipped_count, "skipped invalid native root CAs");
    }
    if skipped_added_count > 0 {
        tracing::warn!(
            skipped_count = skipped_added_count,
            "skipped invalid additional root CA PEM blocks"
        );
    }
    if roots.is_empty() {
        tracing::warn!("FlowService TLS trust store is empty; server verification will fail");
    }
    endpoint
        .tls_config(tls.trust_anchors(roots.roots))
        .map_err(|error| transport_error(operation, "build FlowService TLS connector", error))
}

fn tls_endpoint_address(address: &str) -> SdkResult<&str> {
    let unsupported = || {
        invalid_tls(
            "flow_service_tls requires server_address as host:port, dns:///host:port, or dns://authority/host:port",
        )
    };
    let endpoint = if let Some((scheme, target)) = address.split_once("://") {
        if !scheme.eq_ignore_ascii_case("dns") {
            return Err(unsupported());
        }
        let (authority, endpoint) = target.split_once('/').ok_or_else(unsupported)?;
        if authority.contains([' ', '\t', '\r', '\n', '?', '#', '@']) {
            return Err(unsupported());
        }
        endpoint
    } else {
        address
    };
    if endpoint.contains(['/', '?', '#']) {
        return Err(unsupported());
    }
    let (host, port) = endpoint.rsplit_once(':').ok_or_else(unsupported)?;
    let host = if let Some(bracketed_host) = host.strip_prefix('[') {
        let ipv6 = bracketed_host.strip_suffix(']').ok_or_else(unsupported)?;
        ipv6.parse::<Ipv6Addr>().map_err(|_| unsupported())?;
        ipv6
    } else {
        if host.contains([':', '[', ']']) {
            return Err(unsupported());
        }
        host
    };
    if rustls::pki_types::ServerName::try_from(host).is_err() {
        return Err(invalid_tls(
            "flow_service_tls server_address host is not a valid DNS name or IP address",
        ));
    }
    if !is_valid_tls_name(host)
        || [
            "dns",
            "unix",
            "unix-abstract",
            "passthrough",
            "ipv4",
            "ipv6",
        ]
        .contains(&host.to_ascii_lowercase().as_str())
    {
        return Err(unsupported());
    }
    if port.is_empty()
        || !port.bytes().all(|value| value.is_ascii_digit())
        || !port.parse::<u16>().is_ok_and(|value| value > 0)
    {
        return Err(invalid_tls(
            "flow_service_tls requires server_address port 1-65535",
        ));
    }
    Ok(endpoint)
}

fn is_valid_tls_name(name: &str) -> bool {
    !name.is_empty()
        && !name.contains([' ', '\t', '\r', '\n', '/', '#', '?', '@', '[', ']'])
        && (!name.contains(':') || name.parse::<IpAddr>().is_ok())
}

fn add_additional_certificates(roots: &mut RootCertStore, bundle: &[u8]) -> (usize, usize) {
    let bundle = String::from_utf8_lossy(bundle);
    let mut added_count = 0;
    let mut skipped_count = 0;
    for block in bundle.split("-----BEGIN CERTIFICATE-----").skip(1) {
        let Some((body, _)) = block.split_once("-----END CERTIFICATE-----") else {
            skipped_count += 1;
            continue;
        };
        let certificate = format!("-----BEGIN CERTIFICATE-----{body}-----END CERTIFICATE-----");
        match CertificateDer::from_pem_slice(certificate.as_bytes()) {
            Ok(parsed) => match roots.add(parsed.into_owned()) {
                Ok(()) => added_count += 1,
                Err(_) => skipped_count += 1,
            },
            Err(_) => skipped_count += 1,
        }
    }
    (added_count, skipped_count)
}

fn invalid_tls(message: &str) -> SdkError {
    SdkError::InvalidArgument {
        message: message.to_string(),
    }
}

fn transport_error(operation: &'static str, object: &str, error: impl Error + 'static) -> SdkError {
    let mut detail = format!("{object}: {error}");
    let mut source = error.source();
    while let Some(cause) = source {
        detail.push_str(": ");
        detail.push_str(&cause.to_string());
        source = cause.source();
    }
    SdkError::Service {
        service: ServiceError::local(operation, detail),
    }
}
