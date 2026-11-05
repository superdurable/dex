// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Environment configuration with deprecated variable fallbacks.

use std::collections::BTreeSet;
use std::ffi::OsString;
use std::sync::Mutex;

static WARNED_DEPRECATED_VARIABLES: Mutex<BTreeSet<String>> = Mutex::new(BTreeSet::new());

/// Reads current settings before deprecated names; returns None when absent and warns once per deprecated variable.
pub fn read_environment_setting(variable_name: &str) -> Option<OsString> {
    if let Some(value) = std::env::var_os(variable_name) {
        return Some(value);
    }
    let suffix = variable_name.strip_prefix("S3LOCAL_")?;
    let deprecated_variable_name = format!("S3MOCK_{suffix}");
    let value = std::env::var_os(&deprecated_variable_name)?;
    let should_warn = WARNED_DEPRECATED_VARIABLES
        .lock()
        .expect("the environment warning set is never poisoned")
        .insert(deprecated_variable_name.clone());
    if should_warn {
        tracing::warn!(
            deprecated_variable = %deprecated_variable_name,
            replacement_variable = variable_name,
            "deprecated environment variable; use the replacement variable"
        );
    }
    Some(value)
}
