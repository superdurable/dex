// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(test)]

use super::prefix_successor;

#[test]
fn prefix_successor_handles_unicode_boundaries_and_carry() {
    assert_eq!(prefix_successor(""), None);
    assert_eq!(prefix_successor("\u{d7ff}"), Some("\u{e000}".into()));
    assert_eq!(prefix_successor("\u{10ffff}"), None);
    assert_eq!(prefix_successor("a\u{10ffff}"), Some("b".into()));
    assert_eq!(prefix_successor("ab\u{10ffff}\u{10ffff}"), Some("ac".into()));
}
