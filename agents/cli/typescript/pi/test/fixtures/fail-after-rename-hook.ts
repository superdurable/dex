// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Loaded with --import: every write's check after its rename fails, as if its directory moved right after it.

import { WorkspaceFiles } from "@superdurable/dex-agents-cli";

(WorkspaceFiles.prototype as unknown as { isOpenedFileAt: () => Promise<boolean> }).isOpenedFileAt = () =>
  Promise.resolve(false);
