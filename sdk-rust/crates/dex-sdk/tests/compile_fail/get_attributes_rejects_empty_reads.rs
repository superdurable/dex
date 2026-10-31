// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_sdk::Client;

fn read_nothing(client: &Client) {
    let _values = client.get_attributes("order-1", ());
}

fn main() {}
