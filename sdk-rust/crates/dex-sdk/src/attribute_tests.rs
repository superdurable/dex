// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::{Attribute, AttributeMap};
use crate::attribute_lock_requirement::LockRequirement;

#[test]
fn sync_configuration_is_opt_in_and_survives_clone() {
    let plain = Attribute::<String>::new("plain");
    let synced = Attribute::<String>::new("synced").sync_to_attribute_store();
    let synced_map = AttributeMap::<String>::new("synced_map").sync_to_attribute_store();

    assert!(plain.sync_config().is_none());
    assert_eq!(
        synced.sync_config().map(|config| config.enabled),
        Some(true)
    );
    assert_eq!(
        synced_map
            .clone()
            .sync_config()
            .map(|config| config.enabled),
        Some(true)
    );
}

#[test]
fn write_lock_requirement_is_opt_in_and_survives_clone() {
    let coordination = Attribute::<bool>::new("coordination");
    let plain = Attribute::<u32>::new("plain");
    let own_key = Attribute::<u32>::new("own-key").writes_require_lock();
    let shared_map =
        AttributeMap::<u32>::new("shared-map").writes_require_attribute_lock(&coordination);

    assert_eq!(None, plain.write_lock());
    assert_eq!(Some(&LockRequirement::OwnKey), own_key.clone().write_lock());
    assert_eq!(
        Some(&LockRequirement::Attribute("coordination".to_owned())),
        shared_map.clone().write_lock()
    );
}
