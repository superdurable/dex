// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

#[test]
fn flow_start_input_is_checked_at_compile_time() {
    trybuild::TestCases::new().compile_fail("tests/compile_fail/wrong_flow_input.rs");
}

#[test]
fn attribute_store_sync_builders_compile() {
    let _attribute = dex_sdk::Attribute::<String>::new("email").sync_to_attribute_store();
    let _attribute_map =
        dex_sdk::AttributeMap::<String>::new("email_by_tenant").sync_to_attribute_store();
    let _config = dex_sdk::FlowConfig::new()
        .attribute_store_names(vec!["profiles".to_owned(), "audit".to_owned()]);
    let _disabled = dex_sdk::FlowConfig::new().attribute_store_names(vec![]);
}
