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
