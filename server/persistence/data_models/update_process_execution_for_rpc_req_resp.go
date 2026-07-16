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

package data_models

import (
	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/uuid"
)

type (
	UpdateProcessExecutionForRpcRequest struct {
		Namespace          string
		ProcessId          string
		ProcessType        string
		ProcessExecutionId uuid.UUID

		StateDecision       xcapi.StateDecision
		PublishToLocalQueue []xcapi.LocalQueueMessage

		AppDatabaseConfig *InternalAppDatabaseConfig
		AppDatabaseWrite  *xcapi.AppDatabaseWrite

		WorkerUrl   string
		TaskShardId int32
	}

	UpdateProcessExecutionForRpcResponse struct {
		HasNewImmediateTask      bool
		ProcessNotExists         bool
		FailAtWritingAppDatabase bool
		WritingAppDatabaseError  error
	}
)
