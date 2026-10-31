// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_sdk::{AttributeMap, Client};

fn read_with_load(client: &Client, notes: &AttributeMap<String>) {
    let _values = client.get_attributes("order-1", (&notes.load("n1"),));
}

fn main() {}
