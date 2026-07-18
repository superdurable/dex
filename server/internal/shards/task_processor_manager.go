// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package shards

import (
	p "github.com/superdurable/dex/server/internal/persistence"
)

// ShardTaskProcessorManager is the interface to manage all the taskProcessors for a shard.
// Using a separate interface to avoid circular dependencies.
type ShardTaskProcessorManager interface {
	StartAll(shardID int32, rangeID int32)
	StopAll(shardID int32)
	GetShardMetadata(shardID int32) *p.ShardMetadata
}
