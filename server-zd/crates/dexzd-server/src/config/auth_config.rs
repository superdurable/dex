// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::path::{Path, PathBuf};

use dexzd_api::{CredentialAuthenticatorConfig, JwtAuthenticatorConfig, check_endpoint_url};
use serde::{Deserialize, Serialize};

use super::ConfigError;

/// Authenticates callers of the public gRPC API with the Go Server's `auth` section.
///
/// Every FlowService and WorkerTaskService call needs an `authorization: Bearer <credential>`
/// header that one authenticator accepts; gRPC health needs none. Rejected calls end with
/// UNAUTHENTICATED. A credential is checked when a call starts, so a Worker stream that started
/// with a valid credential stays open after the credential expires or is revoked; reconnecting
/// needs a currently valid credential. `api.listen` serves plaintext gRPC, so bearer credentials
/// cross the network in clear text: terminate TLS in front of the server or restrict the network.
#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct AuthConfig {
    /// Defaults to empty, which disables public API authentication; otherwise the authenticators
    /// are tried in order. Immutable after startup.
    pub authenticators: Vec<AuthenticatorConfig>,
}

/// Selects the credential format of one authenticator.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub enum AuthenticatorKind {
    /// Verifies signed JSON Web Tokens of one issuer.
    #[serde(rename = "jwt")]
    Jwt,
    /// Accepts bearer tokens whose SHA-256 hashes a file lists.
    #[serde(rename = "staticTokens")]
    StaticTokens,
}

/// One authenticator: a JWT issuer or a static token file.
///
/// Fields that belong to the other kind must stay unset.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct AuthenticatorConfig {
    /// Required; `jwt` or `staticTokens`. Immutable after startup.
    pub kind: AuthenticatorKind,
    /// Defaults to empty, which is invalid for jwt; the exact `iss` claim, an HTTPS URL or plain
    /// HTTP on `localhost` or a loopback IP. Each issuer may appear once. Without `jwksURL`, keys
    /// are discovered from `<issuer>/.well-known/openid-configuration`, whose `issuer` must match and
    /// whose `jwks_uri` must follow the same HTTPS rule. Immutable after startup.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub issuer: String,
    /// Defaults to empty, which is invalid for jwt; a token's `aud` must name at least one of them.
    /// Immutable after startup.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub audiences: Vec<String>,
    /// Defaults to empty, which discovers the key set through the issuer's OpenID configuration.
    /// HTTPS is required except on loopback unless `allowInsecureJWKSURL` is set. Keys refresh at
    /// most once a minute; a token naming an unknown `kid` refreshes them at most every ten seconds,
    /// and a request waits at most five seconds for such a refresh. A failed refresh keeps the previous
    /// keys, and an unreachable key server never fails startup. Redirects are not followed and proxy
    /// environment variables are ignored. Immutable after startup.
    #[serde(default, rename = "jwksURL", skip_serializing_if = "String::is_empty")]
    pub jwks_url: String,
    /// Defaults to false; true permits a plain `http://` `jwksURL` on any host, such as a key server
    /// inside the cluster network. It requires `jwksURL` and relaxes neither `issuer` nor a discovered
    /// `jwks_uri`. Anyone who can alter that network path can forge tokens, so enable it only on a
    /// trusted network. server-zd only; the Go Server has no such key. Immutable after startup.
    #[serde(
        default,
        rename = "allowInsecureJWKSURL",
        skip_serializing_if = "std::ops::Not::not"
    )]
    pub allow_insecure_jwks_url: bool,
    /// Defaults to empty, which trusts only the system roots; otherwise a PEM bundle added to them
    /// for discovery and key set requests. It must hold at least one certificate. Read once at
    /// startup; immutable after startup.
    #[serde(default, rename = "caFile", skip_serializing_if = "is_empty_path")]
    pub ca_file: PathBuf,
    /// Defaults to empty, which is invalid for staticTokens; a YAML file
    /// `tokens: [{sha256: <64 hexadecimal characters>}]` listing token hashes, never plaintext tokens.
    /// It must exist and be valid at startup. Changes are picked up within a second; a malformed update
    /// keeps the last valid content, while an empty or missing file revokes every token. The path is
    /// immutable after startup.
    #[serde(default, skip_serializing_if = "is_empty_path")]
    pub file: PathBuf,
}

fn is_empty_path(path: &Path) -> bool {
    path.as_os_str().is_empty()
}

impl AuthConfig {
    /// Checks every authenticator like the Go Server's `ValidateGeneralAccess`.
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        let mut issuers = BTreeSet::new();
        for (index, authenticator) in self.authenticators.iter().enumerate() {
            authenticator
                .validate(&mut issuers)
                .map_err(|message| ConfigError::Invalid {
                    field: "auth.authenticators",
                    message: format!("[{index}]: {message}"),
                })?;
        }
        Ok(())
    }

    /// Converts the authenticators into their component configurations.
    pub(crate) fn credential_authenticators(&self) -> Vec<CredentialAuthenticatorConfig> {
        self.authenticators
            .iter()
            .map(AuthenticatorConfig::credential_authenticator)
            .collect()
    }
}

impl AuthenticatorConfig {
    fn validate<'config>(
        &'config self,
        issuers: &mut BTreeSet<&'config str>,
    ) -> Result<(), String> {
        match self.kind {
            AuthenticatorKind::Jwt => {
                if !is_empty_path(&self.file) {
                    return Err("file applies only to staticTokens".to_owned());
                }
                check_endpoint_url(&self.issuer, false)
                    .map_err(|error| format!("issuer {error}"))?;
                if self.jwks_url.is_empty() {
                    if self.allow_insecure_jwks_url {
                        return Err("allowInsecureJWKSURL requires jwksURL".to_owned());
                    }
                } else {
                    check_endpoint_url(&self.jwks_url, self.allow_insecure_jwks_url)
                        .map_err(|error| format!("jwksURL {error}"))?;
                }
                if self.audiences.is_empty() {
                    return Err("audiences must not be empty".to_owned());
                }
                if !issuers.insert(&self.issuer) {
                    return Err("issuer is configured twice".to_owned());
                }
            }
            AuthenticatorKind::StaticTokens => {
                if !self.issuer.is_empty()
                    || !self.audiences.is_empty()
                    || !self.jwks_url.is_empty()
                    || self.allow_insecure_jwks_url
                    || !is_empty_path(&self.ca_file)
                {
                    return Err(
                        "issuer, audiences, jwksURL, allowInsecureJWKSURL, and caFile apply only to jwt"
                            .to_owned(),
                    );
                }
                if is_empty_path(&self.file) {
                    return Err("file is required".to_owned());
                }
            }
        }
        Ok(())
    }

    fn credential_authenticator(&self) -> CredentialAuthenticatorConfig {
        match self.kind {
            AuthenticatorKind::Jwt => CredentialAuthenticatorConfig::Jwt(JwtAuthenticatorConfig {
                issuer: self.issuer.clone(),
                audiences: self.audiences.clone(),
                jwks_url: Some(self.jwks_url.clone()).filter(|jwks_url| !jwks_url.is_empty()),
                allow_insecure_jwks_url: self.allow_insecure_jwks_url,
                ca_file: Some(self.ca_file.clone()).filter(|ca_file| !is_empty_path(ca_file)),
            }),
            AuthenticatorKind::StaticTokens => CredentialAuthenticatorConfig::StaticTokens {
                file: self.file.clone(),
            },
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::Config;

    const OPERATOR_AUTH_SECTION: &str = r#"
auth:
  authenticators:
    - kind: jwt
      issuer: https://issuer.example.com
      audiences: ["dex"]
      jwksURL: https://issuer.example.com/.well-known/jwks.json
      allowInsecureJWKSURL: false
      caFile: ""
    - kind: staticTokens
      file: /etc/dex-server-zd/tokens.yaml
"#;

    fn auth_section(yaml: &str) -> AuthConfig {
        Config::parse_yaml(yaml)
            .unwrap_or_else(|error| panic!("{yaml} parses: {error}"))
            .auth
    }

    fn validation_message(yaml: &str) -> String {
        match auth_section(yaml).validate() {
            Err(ConfigError::Invalid { field, message }) => {
                assert_eq!(field, "auth.authenticators");
                message
            }
            other => panic!("{yaml} must fail validation, got {other:?}"),
        }
    }

    fn single_authenticator(entry: &str) -> String {
        format!("auth:\n  authenticators:\n    - {entry}\n")
    }

    #[test]
    fn the_go_server_section_parses_validates_and_converts() {
        let auth = auth_section(OPERATOR_AUTH_SECTION);
        auth.validate().expect("the operator section is valid");
        assert_eq!(
            auth.credential_authenticators(),
            vec![
                CredentialAuthenticatorConfig::Jwt(JwtAuthenticatorConfig {
                    issuer: "https://issuer.example.com".to_owned(),
                    audiences: vec!["dex".to_owned()],
                    jwks_url: Some("https://issuer.example.com/.well-known/jwks.json".to_owned()),
                    allow_insecure_jwks_url: false,
                    ca_file: None,
                }),
                CredentialAuthenticatorConfig::StaticTokens {
                    file: PathBuf::from("/etc/dex-server-zd/tokens.yaml"),
                },
            ]
        );
        let discovered = auth_section(&single_authenticator(
            "{kind: jwt, issuer: \"https://issuer.example.com/\", audiences: [a, b], caFile: /etc/ca.pem}",
        ));
        discovered.validate().expect("discovery needs no jwksURL");
        let CredentialAuthenticatorConfig::Jwt(jwt) = &discovered.credential_authenticators()[0]
        else {
            panic!("a jwt authenticator");
        };
        assert_eq!(jwt.jwks_url, None);
        assert_eq!(jwt.ca_file, Some(PathBuf::from("/etc/ca.pem")));
        assert!(AuthConfig::default().credential_authenticators().is_empty());
    }

    #[test]
    fn plain_http_is_limited_to_loopback_unless_the_key_set_url_opts_in() {
        for issuer in [
            "http://127.0.0.1:8080",
            "http://localhost/realm",
            "http://[::1]:9000",
        ] {
            auth_section(&single_authenticator(&format!(
                "{{kind: jwt, issuer: \"{issuer}\", audiences: [dex]}}"
            )))
            .validate()
            .unwrap_or_else(|error| panic!("{issuer} is loopback: {error}"));
        }
        assert_eq!(
            validation_message(&single_authenticator(
                "{kind: jwt, issuer: \"http://issuer.example.com\", audiences: [dex]}"
            )),
            "[0]: issuer must use https unless its host is loopback"
        );
        assert_eq!(
            validation_message(&single_authenticator(
                "{kind: jwt, issuer: \"https://issuer.example.com\", audiences: [dex], jwksURL: \"http://keys.internal/jwks\"}"
            )),
            "[0]: jwksURL must use https unless its host is loopback"
        );
        auth_section(&single_authenticator(
            "{kind: jwt, issuer: \"https://issuer.example.com\", audiences: [dex], jwksURL: \"http://keys.internal/jwks\", allowInsecureJWKSURL: true}",
        ))
        .validate()
        .expect("the opt-in permits a plain HTTP key set URL");
        assert_eq!(
            validation_message(&single_authenticator(
                "{kind: jwt, issuer: \"http://issuer.example.com\", audiences: [dex], jwksURL: \"http://keys.internal/jwks\", allowInsecureJWKSURL: true}"
            )),
            "[0]: issuer must use https unless its host is loopback"
        );
        assert_eq!(
            validation_message(&single_authenticator(
                "{kind: jwt, issuer: \"https://issuer.example.com\", audiences: [dex], allowInsecureJWKSURL: true}"
            )),
            "[0]: allowInsecureJWKSURL requires jwksURL"
        );
    }

    #[test]
    fn invalid_authenticators_name_their_index_and_rule() {
        let jwt = "{kind: jwt, issuer: \"https://issuer.example.com\", audiences: [dex]}";
        let cases = [
            (
                single_authenticator("{kind: jwt, issuer: \"\", audiences: [dex]}"),
                "[0]: issuer must be an absolute URL",
            ),
            (
                single_authenticator("{kind: jwt, issuer: \"https://issuer.example.com\"}"),
                "[0]: audiences must not be empty",
            ),
            (
                format!("auth:\n  authenticators:\n    - {jwt}\n    - {jwt}\n"),
                "[1]: issuer is configured twice",
            ),
            (
                single_authenticator(
                    "{kind: jwt, issuer: \"https://issuer.example.com\", audiences: [dex], file: /tokens.yaml}",
                ),
                "[0]: file applies only to staticTokens",
            ),
            (
                single_authenticator("{kind: staticTokens}"),
                "[0]: file is required",
            ),
            (
                single_authenticator(
                    "{kind: staticTokens, file: /tokens.yaml, issuer: \"https://issuer.example.com\"}",
                ),
                "[0]: issuer, audiences, jwksURL, allowInsecureJWKSURL, and caFile apply only to jwt",
            ),
        ];
        for (yaml, expected) in cases {
            assert_eq!(validation_message(&yaml), expected, "{yaml}");
        }
        for yaml in [
            single_authenticator("{kind: oauth, issuer: \"https://issuer.example.com\"}"),
            single_authenticator("{issuer: \"https://issuer.example.com\", audiences: [dex]}"),
            single_authenticator("{kind: staticTokens, file: /tokens.yaml, token: secret}"),
            single_authenticator("{kind: jwt, issuer: x, audiences: [dex], jwksUrl: x}"),
            "auth:\n  authenticator: []\n".to_owned(),
        ] {
            assert!(
                matches!(Config::parse_yaml(&yaml), Err(ConfigError::Yaml { .. })),
                "{yaml}"
            );
        }
    }
}
