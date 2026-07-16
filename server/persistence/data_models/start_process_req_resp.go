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
	"github.com/xcherryio/xcherry/server/common/uuid"
)

type (
	StartProcessRequest struct {
		Request                xcapi.ProcessExecutionStartRequest
		NewTaskShardId         int32
		TimeoutTimeUnixSeconds int64
	}

	StartProcessResponse struct {
		ProcessExecutionId         uuid.UUID
		AlreadyStarted             bool
		HasNewImmediateTask        bool
		FailedAtWritingAppDatabase bool
		AppDatabaseWritingError    error
	}
)
