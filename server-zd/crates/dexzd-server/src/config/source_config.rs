// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fs;
use std::path::{Path, PathBuf};
use std::time::Duration;

use super::{
    AttributeStoreConfig, Config, CredentialSource, DevConfig, ObjectStoreCredentials,
    SearchBackend, StreamStoreBackend,
};
use crate::DevCommandOptions;

const EMBEDDED_S3_ENDPOINT_PENDING: &str = "http://127.0.0.1:0";
/// Dev has no standby, so a longer lease rides out host stalls; only crash restarts wait longer.
const DEV_LEASE_DURATION: Duration = Duration::from_secs(30);

/// Reports a YAML, filesystem, environment, or field validation failure.
#[derive(Debug, thiserror::Error)]
pub enum ConfigError {
    /// Configuration file could not be read.
    #[error("cannot read configuration file {}: {source}", path.display())]
    Read {
        /// Path that failed to load.
        path: PathBuf,
        /// Underlying filesystem error.
        #[source]
        source: std::io::Error,
    },
    /// YAML could not be decoded into the typed sections.
    #[error("invalid configuration YAML: {source}")]
    Yaml {
        /// YAML syntax, type, or unknown-key error.
        #[source]
        source: serde_yaml_ng::Error,
    },
    /// A documented environment credential is absent or empty.
    #[error("environment variable {name} is required by {field}")]
    MissingEnvironmentCredential {
        /// Object Store configuration field selecting environment credentials.
        field: &'static str,
        /// Name of the missing variable, never its value.
        name: &'static str,
    },
    /// A documented environment credential contains bytes that are not Unicode.
    #[error("environment variable {name} for {field} must be Unicode")]
    NonUnicodeEnvironmentCredential {
        /// Object Store configuration field selecting environment credentials.
        field: &'static str,
        /// Name of the variable, never its value.
        name: &'static str,
    },
    /// A decoded field violates an operational constraint.
    #[error("{field}: {message}")]
    Invalid {
        /// CamelCase YAML field path that failed validation.
        field: &'static str,
        /// Constraint and valid range, without secret values.
        message: String,
    },
    /// A field of one `attributeStore.stores` entry violates its constraint.
    #[error("attributeStore.stores.{store_name}.{field}: {message}")]
    InvalidAttributeStore {
        /// Configured Attribute Store name.
        store_name: String,
        /// CamelCase YAML field of the store entry.
        field: &'static str,
        /// Constraint, without secret values.
        message: String,
    },
}

impl Config {
    /// Parses YAML with per-section defaults and rejects unknown keys without reading environment variables.
    pub fn parse_yaml(contents: &str) -> Result<Self, ConfigError> {
        serde_yaml_ng::from_str(contents).map_err(|source| ConfigError::Yaml { source })
    }

    /// Loads, resolves documented credentials, and validates a production configuration file.
    pub fn load_file(path: &Path) -> Result<Self, ConfigError> {
        let contents = fs::read_to_string(path).map_err(|source| ConfigError::Read {
            path: path.to_path_buf(),
            source,
        })?;
        let mut configuration = Self::parse_yaml(&contents)?;
        if configuration.dev != DevConfig::default() {
            return Err(ConfigError::Invalid {
                field: "dev",
                message: "applies only to dex-server-zd dev; remove it from a serve configuration"
                    .to_owned(),
            });
        }
        configuration.resolve_environment_credentials()?;
        configuration.validate()?;
        Ok(configuration)
    }

    /// Loads optional YAML, applies the fixed dev flags, then resolves credentials and validates the result.
    pub fn load_dev(options: &DevCommandOptions) -> Result<Self, ConfigError> {
        if options.data_dir.as_os_str().is_empty() {
            return Err(ConfigError::Invalid {
                field: "dev.dataDirectory",
                message: "--data-dir must be non-empty".to_owned(),
            });
        }
        let (mut configuration, override_document) = if let Some(path) = &options.config {
            let contents = fs::read_to_string(path).map_err(|source| ConfigError::Read {
                path: path.clone(),
                source,
            })?;
            let override_document: serde_yaml_ng::Value = serde_yaml_ng::from_str(&contents)
                .map_err(|source| ConfigError::Yaml { source })?;
            (Self::parse_yaml(&contents)?, Some(override_document))
        } else {
            (Self::default(), None)
        };
        let has_stream_store_backend_override =
            has_yaml_key(override_document.as_ref(), &["streamStore", "backend"]);
        let has_attribute_store_override =
            has_yaml_key(override_document.as_ref(), &["attributeStore"]);
        let has_lease_duration_override =
            has_yaml_key(override_document.as_ref(), &["lease", "duration"]);
        if let Some(path) = &options.attribute_store_config {
            if has_attribute_store_override {
                return Err(ConfigError::Invalid {
                    field: "attributeStore",
                    message: "set it in --config or in --attribute-store-config, not both"
                        .to_owned(),
                });
            }
            configuration.attribute_store = read_go_attribute_store_section(path)?;
        }
        if configuration.object_store.hard_delete_store.is_some() {
            return Err(ConfigError::Invalid {
                field: "objectStore.hardDeleteStore",
                message: "dev mode uses the embedded object store for hard-delete values"
                    .to_owned(),
            });
        }
        configuration.cluster.node_id = "dev".to_owned();
        configuration.api.listen = options.listen;
        configuration
            .dev
            .data_directory
            .clone_from(&options.data_dir);
        configuration.dev.s3_data_directory = options.data_dir.join("s3");
        configuration.dev.log_directory.clone_from(&options.log_dir);
        configuration.local_storage.directory = options.data_dir.join("store");
        if !has_stream_store_backend_override {
            configuration.stream_store.backend = StreamStoreBackend::Memory;
        }
        if !has_lease_duration_override {
            configuration.lease.duration = DEV_LEASE_DURATION;
        }
        if configuration.object_store.bucket.is_empty() {
            configuration.object_store.bucket = "dex-dev".to_owned();
        }
        configuration.object_store.endpoint = EMBEDDED_S3_ENDPOINT_PENDING.to_owned();
        configuration.object_store.region = "us-east-1".to_owned();
        configuration.object_store.force_path_style = true;
        configuration.object_store.credentials_source = CredentialSource::Anonymous;
        configuration.resolve_environment_credentials()?;
        configuration.validate()?;
        Ok(configuration)
    }

    fn resolve_environment_credentials(&mut self) -> Result<(), ConfigError> {
        self.attribute_store.resolve_environment_dsns()?;
        if self.search.backend == SearchBackend::Paradedb
            && self.search.parade_db_connection_string.is_empty()
            && let Some(connection_string) = read_environment_credential(
                "DEX_ZD_PARADEDB_CONNECTION_STRING",
                "search.paradeDbConnectionString",
            )?
        {
            self.search.parade_db_connection_string = connection_string;
        }
        self.object_store.credentials = credentials_from_lookup(
            self.object_store.credentials_source,
            "objectStore.credentialsSource",
            "DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID",
            "DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY",
            "DEX_ZD_OBJECT_STORE_SESSION_TOKEN",
            |name| read_environment_credential(name, "objectStore.credentialsSource"),
        )?;
        if let Some(hard_delete_store) = &mut self.object_store.hard_delete_store {
            hard_delete_store.credentials = credentials_from_lookup(
                hard_delete_store.credentials_source,
                "objectStore.hardDeleteStore.credentialsSource",
                "DEX_ZD_HARD_DELETE_STORE_ACCESS_KEY_ID",
                "DEX_ZD_HARD_DELETE_STORE_SECRET_ACCESS_KEY",
                "DEX_ZD_HARD_DELETE_STORE_SESSION_TOKEN",
                |name| {
                    read_environment_credential(
                        name,
                        "objectStore.hardDeleteStore.credentialsSource",
                    )
                },
            )?;
        }
        Ok(())
    }
}

/// Returns whether `document` sets the nested key `path`, including to an explicit null.
fn has_yaml_key(document: Option<&serde_yaml_ng::Value>, path: &[&str]) -> bool {
    path.iter()
        .fold(document, |value, key| {
            value.and_then(|section| section.get(*key))
        })
        .is_some()
}

/// Reads the `attributeStore` section of a Go Dex Server YAML file and ignores its other sections.
fn read_go_attribute_store_section(path: &Path) -> Result<AttributeStoreConfig, ConfigError> {
    let contents = fs::read_to_string(path).map_err(|source| ConfigError::Read {
        path: path.to_path_buf(),
        source,
    })?;
    let document: serde_yaml_ng::Value =
        serde_yaml_ng::from_str(&contents).map_err(|source| ConfigError::Yaml { source })?;
    let section = document
        .get("attributeStore")
        .cloned()
        .unwrap_or(serde_yaml_ng::Value::Null);
    let attribute_store: Option<AttributeStoreConfig> =
        serde_yaml_ng::from_value(section).map_err(|source| ConfigError::Yaml { source })?;
    match attribute_store {
        Some(attribute_store) if !attribute_store.stores.is_empty() => Ok(attribute_store),
        _ => Err(ConfigError::Invalid {
            field: "attributeStore.stores",
            message: format!(
                "--attribute-store-config file {} does not define any stores",
                path.display()
            ),
        }),
    }
}

fn read_environment_credential(
    name: &'static str,
    field: &'static str,
) -> Result<Option<String>, ConfigError> {
    match std::env::var(name) {
        Ok(value) => Ok(Some(value)),
        Err(std::env::VarError::NotPresent) => Ok(None),
        Err(std::env::VarError::NotUnicode(_)) => {
            Err(ConfigError::NonUnicodeEnvironmentCredential { field, name })
        }
    }
}

fn credentials_from_lookup(
    source: CredentialSource,
    field: &'static str,
    access_key_name: &'static str,
    secret_key_name: &'static str,
    session_token_name: &'static str,
    lookup: impl Fn(&'static str) -> Result<Option<String>, ConfigError>,
) -> Result<Option<ObjectStoreCredentials>, ConfigError> {
    if source != CredentialSource::Environment {
        return Ok(None);
    }
    let access_key_id = lookup(access_key_name)?
        .filter(|value| !value.is_empty())
        .ok_or(ConfigError::MissingEnvironmentCredential {
            field,
            name: access_key_name,
        })?;
    let secret_access_key = lookup(secret_key_name)?
        .filter(|value| !value.is_empty())
        .ok_or(ConfigError::MissingEnvironmentCredential {
            field,
            name: secret_key_name,
        })?;
    let session_token = lookup(session_token_name)?.filter(|value| !value.is_empty());
    Ok(Some(ObjectStoreCredentials {
        access_key_id,
        secret_access_key,
        session_token,
    }))
}

#[cfg(test)]
mod credential_tests {
    use super::*;

    #[test]
    fn credential_source_only_reads_required_environment_names() {
        let names = (
            "DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID",
            "DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY",
            "DEX_ZD_OBJECT_STORE_SESSION_TOKEN",
        );
        let credentials = credentials_from_lookup(
            CredentialSource::Environment,
            "objectStore.credentialsSource",
            names.0,
            names.1,
            names.2,
            |name| match name {
                "DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID" => Ok(Some("test-id".to_owned())),
                "DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY" => Ok(Some("test-secret".to_owned())),
                _ => Ok(None),
            },
        )
        .expect("provided test credentials")
        .expect("environment credentials");
        assert_eq!(credentials.access_key_id, "test-id");
        assert_eq!(credentials.secret_access_key, "test-secret");
        assert!(credentials.session_token.is_none());
        assert!(!format!("{credentials:?}").contains("test-secret"));
        let error = credentials_from_lookup(
            CredentialSource::Environment,
            "objectStore.credentialsSource",
            names.0,
            names.1,
            names.2,
            |_| Ok(None),
        )
        .expect_err("missing access key must fail");
        assert!(matches!(
            error,
            ConfigError::MissingEnvironmentCredential {
                name: "DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID",
                ..
            }
        ));
    }
}
