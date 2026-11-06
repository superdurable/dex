// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::net::{IpAddr, Ipv6Addr};

use rustls::RootCertStore;
use rustls::pki_types::{CertificateDer, pem::PemObject};
use tonic::transport::{Certificate, ClientTlsConfig, Endpoint};

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
/// or invalid server name. Native-root loading errors return [`SdkError::Service`].
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
/// let tls = FlowServiceTlsOptions {
///     additional_root_cas_pem: Some(std::fs::read("/etc/dex/root-ca.pem")?),
///     server_name: None,
/// };
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
    /// Adds PEM-encoded CA certificates to the native system trust store.
    ///
    /// Defaults to `None`, using native roots alone. Empty bytes have the same effect.
    /// Construction reads the owned bytes once; file changes are not watched. A nonempty bundle
    /// must contain at least one parsable certificate. Other blocks and malformed certificates
    /// are skipped when another certificate parses. See the enclosing type's example.
    pub additional_root_cas_pem: Option<Vec<u8>>,
    /// Overrides the address host for certificate verification, SNI, and HTTP/2 authority.
    ///
    /// Defaults to `None`, verifying the address host and retaining its host and port as authority.
    /// Use a DNS name or unbracketed IP address without a port. `Some("")` is invalid.
    /// An IP address sends no SNI and requires a matching IP SAN. An override becomes the
    /// authority without a port; a gateway must accept that name. See the enclosing type's example.
    pub server_name: Option<String>,
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
        return Endpoint::from_shared(address).map_err(|error| transport_error(operation, error));
    };
    let endpoint_address = tls_endpoint_address(address)?;
    let (host, _) = endpoint_address
        .rsplit_once(':')
        .expect("validated TLS endpoint has a port");
    let host = host.trim_start_matches('[').trim_end_matches(']');
    let mut tls = ClientTlsConfig::new().with_native_roots().domain_name(host);
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
    if let Some(bundle) = &options.additional_root_cas_pem {
        let certificates = additional_certificates(bundle);
        if !bundle.is_empty() && certificates.is_empty() {
            return Err(invalid_tls(
                "flow_service_tls.additional_root_cas_pem contains no PEM certificate",
            ));
        }
        for certificate in certificates {
            tls = tls.ca_certificate(Certificate::from_pem(certificate));
        }
    }
    let mut endpoint = Endpoint::from_shared(format!("https://{endpoint_address}"))
        .map_err(|error| transport_error(operation, error))?;
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
    endpoint
        .tls_config(tls)
        .map_err(|error| transport_error(operation, error))
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
    if !is_valid_tls_name(host)
        || rustls::pki_types::ServerName::try_from(host).is_err()
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

fn additional_certificates(bundle: &[u8]) -> Vec<Vec<u8>> {
    let bundle = String::from_utf8_lossy(bundle);
    bundle
        .split("-----BEGIN CERTIFICATE-----")
        .skip(1)
        .filter_map(|block| {
            let (body, _) = block.split_once("-----END CERTIFICATE-----")?;
            let certificate =
                format!("-----BEGIN CERTIFICATE-----{body}-----END CERTIFICATE-----").into_bytes();
            let parsed = match CertificateDer::from_pem_slice(&certificate) {
                Ok(parsed) => parsed,
                Err(_) => return None,
            };
            match RootCertStore::empty().add(parsed) {
                Ok(()) => Some(certificate),
                Err(_) => None,
            }
        })
        .collect()
}

fn invalid_tls(message: &str) -> SdkError {
    SdkError::InvalidArgument {
        message: message.to_string(),
    }
}

fn transport_error(operation: &'static str, error: impl std::fmt::Display) -> SdkError {
    SdkError::Service {
        service: ServiceError::local(operation, error.to_string()),
    }
}
