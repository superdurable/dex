// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Loaded with node --import: the command then sees Windows as its platform, as on a Windows machine.

Object.defineProperty(process, "platform", { value: "win32" });
