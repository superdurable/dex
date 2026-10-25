// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use rustls::client::WebPkiServerVerifier;
use rustls::client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier};
use rustls::crypto::{WebPkiSupportedAlgorithms, verify_tls12_signature, verify_tls13_signature};
use rustls::pki_types::pem::PemObject;
use rustls::pki_types::{CertificateDer, ServerName, UnixTime};
use rustls::{
    CertificateError, ClientConfig, DigitallySignedStruct, Error, RootCertStore, SignatureScheme,
};

/// How the server certificate is checked, following pgx's `sslmode` semantics.
#[derive(Debug)]
pub(crate) enum CertificateVerification {
    /// `allow`, `prefer`, and `require` without `sslrootcert`: encrypt without checking the certificate.
    None,
    /// `verify-ca`, and `require` with `sslrootcert`: check the chain but not the host name.
    ChainOnly(RootCertStore),
    /// `verify-full`: check the chain and the host name.
    Full(RootCertStore),
}

/// Builds the rustls client configuration; ALPN `postgresql` is offered for direct TLS negotiation.
pub(crate) fn client_config(
    verification: CertificateVerification,
    is_direct_negotiation: bool,
    is_sni_enabled: bool,
) -> Result<ClientConfig, String> {
    let provider = Arc::new(rustls::crypto::aws_lc_rs::default_provider());
    let builder = ClientConfig::builder_with_provider(Arc::clone(&provider))
        .with_safe_default_protocol_versions()
        .map_err(|error| format!("configure TLS protocol versions: {error}"))?;
    let mut config = match verification {
        CertificateVerification::Full(roots) => {
            builder.with_root_certificates(roots).with_no_client_auth()
        }
        CertificateVerification::ChainOnly(roots) => {
            let verifier =
                WebPkiServerVerifier::builder_with_provider(Arc::new(roots), Arc::clone(&provider))
                    .build()
                    .map_err(|error| format!("build TLS certificate verifier: {error}"))?;
            builder
                .dangerous()
                .with_custom_certificate_verifier(Arc::new(ChainOnlyVerifier { inner: verifier }))
                .with_no_client_auth()
        }
        CertificateVerification::None => builder
            .dangerous()
            .with_custom_certificate_verifier(Arc::new(UnverifiedCertificate {
                algorithms: provider.signature_verification_algorithms,
            }))
            .with_no_client_auth(),
    };
    if is_direct_negotiation {
        config.alpn_protocols = vec![b"postgresql".to_vec()];
    }
    config.enable_sni = is_sni_enabled;
    Ok(config)
}

/// Loads a PEM CA bundle, or the system roots for `system`.
pub(crate) fn load_root_certificates(ssl_root_cert: &str) -> Result<RootCertStore, String> {
    let certificates = if ssl_root_cert == "system" {
        let loaded = rustls_native_certs::load_native_certs();
        if loaded.certs.is_empty() {
            return Err(format!(
                "load system certificate roots: {}",
                loaded
                    .errors
                    .iter()
                    .map(ToString::to_string)
                    .collect::<Vec<_>>()
                    .join("; ")
            ));
        }
        loaded.certs
    } else {
        CertificateDer::pem_file_iter(ssl_root_cert)
            .map_err(|error| format!("read the sslrootcert file: {error}"))?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| "the sslrootcert file is not a valid PEM bundle".to_owned())?
    };
    let mut roots = RootCertStore::empty();
    let (added, _) = roots.add_parsable_certificates(certificates);
    if added == 0 {
        return Err("the sslrootcert file contains no usable CA certificate".to_owned());
    }
    Ok(roots)
}

/// pgx's `verify-ca`: the chain must verify against the roots; the host name is not checked.
#[derive(Debug)]
struct ChainOnlyVerifier {
    inner: Arc<WebPkiServerVerifier>,
}

impl ServerCertVerifier for ChainOnlyVerifier {
    fn verify_server_cert(
        &self,
        end_entity: &CertificateDer<'_>,
        intermediates: &[CertificateDer<'_>],
        server_name: &ServerName<'_>,
        ocsp_response: &[u8],
        now: UnixTime,
    ) -> Result<ServerCertVerified, Error> {
        match self.inner.verify_server_cert(
            end_entity,
            intermediates,
            server_name,
            ocsp_response,
            now,
        ) {
            Err(Error::InvalidCertificate(
                CertificateError::NotValidForName | CertificateError::NotValidForNameContext { .. },
            )) => Ok(ServerCertVerified::assertion()),
            result => result,
        }
    }

    fn verify_tls12_signature(
        &self,
        message: &[u8],
        certificate: &CertificateDer<'_>,
        signature: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, Error> {
        self.inner
            .verify_tls12_signature(message, certificate, signature)
    }

    fn verify_tls13_signature(
        &self,
        message: &[u8],
        certificate: &CertificateDer<'_>,
        signature: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, Error> {
        self.inner
            .verify_tls13_signature(message, certificate, signature)
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        self.inner.supported_verify_schemes()
    }
}

/// libpq's `require` without a root certificate: any certificate, but handshake signatures still verify.
#[derive(Debug)]
struct UnverifiedCertificate {
    algorithms: WebPkiSupportedAlgorithms,
}

impl ServerCertVerifier for UnverifiedCertificate {
    fn verify_server_cert(
        &self,
        _end_entity: &CertificateDer<'_>,
        _intermediates: &[CertificateDer<'_>],
        _server_name: &ServerName<'_>,
        _ocsp_response: &[u8],
        _now: UnixTime,
    ) -> Result<ServerCertVerified, Error> {
        Ok(ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        message: &[u8],
        certificate: &CertificateDer<'_>,
        signature: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, Error> {
        verify_tls12_signature(message, certificate, signature, &self.algorithms)
    }

    fn verify_tls13_signature(
        &self,
        message: &[u8],
        certificate: &CertificateDer<'_>,
        signature: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, Error> {
        verify_tls13_signature(message, certificate, signature, &self.algorithms)
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        self.algorithms.supported_schemes()
    }
}
