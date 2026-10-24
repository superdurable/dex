// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Durable credentials for strict signature verification.

use redb::{ReadableDatabase, ReadableTable};
use serde::{Deserialize, Serialize};

use super::{CREDENTIALS, Store, decode_json, encode_json};
use crate::error::StoreError;

/// Whether a stored access key can sign requests.
#[derive(Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum CredentialStatus {
    Enabled,
    Disabled,
}

/// A credential and its policy. The secret is persisted but must be omitted from API responses.
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CredentialRecord {
    pub access_key_id: String,
    #[serde(rename = "secret")]
    pub secret_access_key: String,
    pub parent: String,
    pub description: String,
    pub policy: serde_json::Value,
    pub status: CredentialStatus,
}

impl Store {
    /// Creates a credential without replacing an existing access key ID.
    pub fn create_credential(&self, credential: &CredentialRecord) -> Result<(), StoreError> {
        let transaction = self.begin_write()?;
        {
            let mut credentials = transaction.open_table(CREDENTIALS)?;
            if credentials
                .get(credential.access_key_id.as_str())?
                .is_some()
            {
                return Err(StoreError::CredentialAlreadyExists);
            }
            credentials.insert(
                credential.access_key_id.as_str(),
                encode_json(credential)?.as_slice(),
            )?;
        }
        transaction.commit()?;
        Ok(())
    }

    /// Reads a credential, including its signing secret for server-side verification.
    pub fn credential(&self, access_key_id: &str) -> Result<CredentialRecord, StoreError> {
        let transaction = self.database.begin_read()?;
        let credentials = transaction.open_table(CREDENTIALS)?;
        let stored = credentials
            .get(access_key_id)?
            .ok_or(StoreError::NoSuchCredential)?;
        decode_json(stored.value())
    }

    /// Lists credentials for one owner, including secrets for the caller to strip.
    pub fn credentials_by_parent(&self, parent: &str) -> Result<Vec<CredentialRecord>, StoreError> {
        let transaction = self.database.begin_read()?;
        let credentials = transaction.open_table(CREDENTIALS)?;
        let mut records = Vec::new();
        for entry in credentials.iter()? {
            let (_, value) = entry?;
            let record: CredentialRecord = decode_json(value.value())?;
            if record.parent == parent {
                records.push(record);
            }
        }
        Ok(records)
    }

    /// Changes the status of an existing credential.
    pub fn update_credential_status(
        &self,
        access_key_id: &str,
        status: CredentialStatus,
    ) -> Result<CredentialRecord, StoreError> {
        let transaction = self.begin_write()?;
        let credential = {
            let mut credentials = transaction.open_table(CREDENTIALS)?;
            let mut credential: CredentialRecord = {
                let stored = credentials
                    .get(access_key_id)?
                    .ok_or(StoreError::NoSuchCredential)?;
                decode_json(stored.value())?
            };
            credential.status = status;
            credentials.insert(access_key_id, encode_json(&credential)?.as_slice())?;
            credential
        };
        transaction.commit()?;
        Ok(credential)
    }

    /// Deletes a credential or reports that it is missing.
    pub fn delete_credential(&self, access_key_id: &str) -> Result<(), StoreError> {
        let transaction = self.begin_write()?;
        {
            let mut credentials = transaction.open_table(CREDENTIALS)?;
            if credentials.remove(access_key_id)?.is_none() {
                return Err(StoreError::NoSuchCredential);
            }
        }
        transaction.commit()?;
        Ok(())
    }
}
