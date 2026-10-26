// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::SdkResult;

use super::invalid;

pub(super) fn generate_app_worker_id() -> String {
    let identity =
        super::super::worker_options::WorkerEnvironmentConfig::from_environment().identity;
    let mut cleaned = String::new();
    for character in identity.to_ascii_lowercase().chars() {
        if cleaned.len() >= 54 {
            break;
        }
        if character.is_ascii_lowercase() || character.is_ascii_digit() || character == '-' {
            cleaned.push(character);
        } else if !cleaned.is_empty() {
            cleaned.push('-');
        }
    }
    let cleaned = cleaned.trim_matches('-');
    let identity = if cleaned.is_empty() {
        "worker"
    } else {
        cleaned
    };
    let random = uuid::Uuid::new_v4();
    let mut bits = 0u64;
    for byte in &random.as_bytes()[..5] {
        bits = (bits << 8) | u64::from(*byte);
    }
    let alphabet = b"abcdefghijklmnopqrstuvwxyz234567";
    let suffix: String = (0..8)
        .rev()
        .map(|index| alphabet[((bits >> (index * 5)) & 31) as usize] as char)
        .collect();
    format!("{identity}-{suffix}")
}

pub(super) fn validate_app_worker_id(value: &str) -> SdkResult<()> {
    if value.is_empty() || value.len() > 63 {
        return Err(invalid("app Worker ID must contain 1-63 characters"));
    }
    if let Some(character) = value.chars().find(|character| {
        !character.is_ascii_alphanumeric() && !matches!(character, '.' | '_' | '-')
    }) {
        return Err(invalid(format!(
            "app Worker ID contains invalid character {character:?}"
        )));
    }
    Ok(())
}
