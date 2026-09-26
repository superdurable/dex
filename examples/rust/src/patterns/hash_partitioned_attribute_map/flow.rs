// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::collections::BTreeMap;
use std::sync::LazyLock;

use dex_sdk::{
    AttributeMap, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc, RpcList,
    RpcResult, StepList,
};
use serde::{Deserialize, Serialize};

pub const FLOW_ID: &str = "customer-directory";
pub const PARTITION_COUNT: u32 = 1000;
const FNV_OFFSET_BASIS_32: u32 = 2_166_136_261;
const FNV_PRIME_32: u32 = 16_777_619;

pub const UPSERT_CUSTOMER_PROFILE: Rpc<CustomerProfile, CustomerProfile> =
    Rpc::new("UpsertCustomerProfile");
pub const GET_CUSTOMER_PROFILE_BY_EMAIL: Rpc<String, CustomerProfile> =
    Rpc::new("GetCustomerProfileByEmail");

pub static CUSTOMER_PROFILES_BY_EMAIL_PARTITION: LazyLock<AttributeMap<CustomerProfilePartition>> =
    LazyLock::new(|| AttributeMap::new("customer_profiles_by_email_partition"));

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CustomerProfile {
    pub email_address: String,
    pub full_name: String,
    pub company_name: String,
    pub customer_tier: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CustomerProfilePartition {
    pub profiles_by_canonical_email: BTreeMap<String, CustomerProfile>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EmailPartition {
    pub canonical_email: String,
    pub hash: u32,
    pub partition_name: String,
}

#[derive(Default)]
pub struct CustomerDirectoryFlow;

impl CustomerDirectoryFlow {
    fn upsert_customer_profile(
        &self,
        context: &mut Context,
        mut profile: CustomerProfile,
    ) -> HandlerResult<RpcResult<CustomerProfile>> {
        let partition = email_partition(&profile.email_address)?;
        profile.email_address = partition.canonical_email.clone();
        let mut profiles = CUSTOMER_PROFILES_BY_EMAIL_PARTITION
            .get(context, &partition.partition_name)?
            .unwrap_or_default();
        profiles
            .profiles_by_canonical_email
            .insert(partition.canonical_email, profile.clone());
        CUSTOMER_PROFILES_BY_EMAIL_PARTITION.set(context, &partition.partition_name, profiles)?;
        Ok(RpcResult::new(profile))
    }

    fn get_customer_profile_by_email(
        &self,
        context: &mut Context,
        email_address: String,
    ) -> HandlerResult<RpcResult<CustomerProfile>> {
        let partition = email_partition(&email_address)?;
        let profile = CUSTOMER_PROFILES_BY_EMAIL_PARTITION
            .get(context, &partition.partition_name)?
            .and_then(|profiles| {
                profiles
                    .profiles_by_canonical_email
                    .get(&partition.canonical_email)
                    .cloned()
            })
            .ok_or_else(|| {
                HandlerError::new(
                    "CustomerDirectoryFlow",
                    format!(
                        "customer profile \"{}\" not found",
                        partition.canonical_email
                    ),
                )
            })?;
        Ok(RpcResult::new(profile))
    }
}

impl Flow for CustomerDirectoryFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute_map(&CUSTOMER_PROFILES_BY_EMAIL_PARTITION)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(UPSERT_CUSTOMER_PROFILE, Self::upsert_customer_profile)
            .function(
                GET_CUSTOMER_PROFILE_BY_EMAIL,
                Self::get_customer_profile_by_email,
            )
    }
}

pub fn email_partition(email_address: &str) -> HandlerResult<EmailPartition> {
    let canonical_email = canonical_email_address(email_address)?;
    let hash = fnv1a32(canonical_email.as_bytes());
    Ok(EmailPartition {
        canonical_email,
        hash,
        partition_name: format!("partition-{:03}", hash % PARTITION_COUNT),
    })
}

pub fn canonical_email_address(email_address: &str) -> HandlerResult<String> {
    if !email_address.is_ascii() {
        return Err(HandlerError::new(
            "CustomerDirectoryFlow",
            "emailAddress must contain only ASCII characters",
        ));
    }
    let bytes = email_address.as_bytes();
    let mut start = 0;
    while start < bytes.len() && bytes[start].is_ascii_whitespace() {
        start += 1;
    }
    let mut end = bytes.len();
    while end > start && bytes[end - 1].is_ascii_whitespace() {
        end -= 1;
    }
    if start == end {
        return Err(HandlerError::new(
            "CustomerDirectoryFlow",
            "emailAddress is required",
        ));
    }
    let mut canonical = bytes[start..end].to_vec();
    canonical.make_ascii_lowercase();
    Ok(String::from_utf8(canonical).expect("ASCII email remains valid UTF-8"))
}

pub fn fnv1a32(value: &[u8]) -> u32 {
    let mut hash = FNV_OFFSET_BASIS_32;
    for octet in value {
        hash ^= u32::from(*octet);
        hash = hash.wrapping_mul(FNV_PRIME_32);
    }
    hash
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn uses_shared_email_partition_golden_vectors() {
        let cases = [
            (
                " Alice@Example.COM ",
                "alice@example.com",
                2_493_822_278,
                "partition-278",
            ),
            (
                "bob@example.com",
                "bob@example.com",
                3_055_529_145,
                "partition-145",
            ),
            (
                "support+west@example.org",
                "support+west@example.org",
                2_156_001_632,
                "partition-632",
            ),
        ];
        for (input, canonical_email, hash, partition_name) in cases {
            let actual = email_partition(input).expect("valid email");
            assert_eq!(actual.canonical_email, canonical_email);
            assert_eq!(actual.hash, hash);
            assert_eq!(actual.partition_name, partition_name);
        }
    }

    #[test]
    fn rejects_invalid_email_addresses() {
        for email_address in ["", " \t\r\n", "josé@example.com"] {
            assert!(canonical_email_address(email_address).is_err());
        }
    }
}
