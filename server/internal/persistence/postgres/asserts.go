// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import p "github.com/superdurable/dex/server/internal/persistence"

// Compile-time assertions that each Postgres store satisfies its interface.
var (
	_ p.RunStore        = (*pgRunStore)(nil)
	_ p.ShardStore      = (*pgShardStore)(nil)
	_ p.BlobStore       = (*pgBlobStore)(nil)
	_ p.DLQStore        = (*pgDLQStore)(nil)
	_ p.TasklistStore   = (*pgTasklistStore)(nil)
	_ p.VisibilityStore = (*pgVisibilityStore)(nil)
	_ p.HistoryStore    = (*pgHistoryStore)(nil)
)
