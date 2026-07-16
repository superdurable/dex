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

type RecoverFromStateExecutionFailureRequest struct {
	Namespace                    string
	ProcessExecutionId           uuid.UUID
	Prepare                      PrepareStateExecutionResponse
	SourceStateExecutionId       StateExecutionId
	SourceFailedStateApi         xcapi.WorkerApiType
	LastFailureStatus            int32
	LastFailureDetails           string
	LastFailureCompletedAttempts int32
	DestinationStateId           string
	DestinationStateConfig       *xcapi.AsyncStateConfig
	DestinationStateInput        xcapi.EncodedObject
	ShardId                      int32
}
