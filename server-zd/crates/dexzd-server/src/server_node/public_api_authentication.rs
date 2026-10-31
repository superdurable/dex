// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_api::{AuthenticatedService, CredentialAuthenticatorConfig, PublicApiAuthenticator};
use dexzd_clock::SystemWallClock;

use crate::ServerError;

/// Builds the authenticator of the public services, or `None` when no authenticator is configured.
pub(super) fn public_api_authenticator(
    configs: &[CredentialAuthenticatorConfig],
) -> Result<Option<Arc<PublicApiAuthenticator>>, ServerError> {
    if configs.is_empty() {
        return Ok(None);
    }
    let authenticator = PublicApiAuthenticator::new(configs, Arc::new(SystemWallClock))
        .map_err(|source| ServerError::Authentication { source })?;
    tracing::info!(
        authenticators = configs.len(),
        "public API authentication is enabled"
    );
    Ok(Some(Arc::new(authenticator)))
}

/// Requires credentials on `service`; gRPC health stays unwrapped and open.
pub(super) fn authenticated<S>(
    service: S,
    authenticator: Option<&Arc<PublicApiAuthenticator>>,
) -> AuthenticatedService<S> {
    AuthenticatedService::new(service, authenticator.cloned())
}

/// Builds JWT-only admin authorization without changing normal API audiences.
pub(super) fn admin_api_authenticator(
    config: &crate::config::AuthConfig,
) -> Result<Option<Arc<PublicApiAuthenticator>>, ServerError> {
    if config.authenticators.is_empty() {
        return Ok(None);
    }
    let authenticators: Vec<_> = config
        .credential_authenticators()
        .into_iter()
        .filter_map(|authenticator| match authenticator {
            CredentialAuthenticatorConfig::Jwt(mut jwt) => {
                jwt.audiences = config.admin_audiences.clone();
                Some(CredentialAuthenticatorConfig::Jwt(jwt))
            }
            CredentialAuthenticatorConfig::StaticTokens { .. } => None,
        })
        .collect();
    PublicApiAuthenticator::new(&authenticators, Arc::new(SystemWallClock))
        .map(|authenticator| Some(Arc::new(authenticator)))
        .map_err(|source| ServerError::Authentication { source })
}
