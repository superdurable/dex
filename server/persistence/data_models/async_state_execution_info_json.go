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
	"encoding/json"

	"github.com/xcherryio/apis/goapi/xcapi"
)

type AsyncStateExecutionInfoJson struct {
	Namespace                   string                     `json:"namespace"`
	ProcessId                   string                     `json:"processId"`
	ProcessType                 string                     `json:"processType"`
	WorkerURL                   string                     `json:"workerURL"`
	StateConfig                 *xcapi.AsyncStateConfig    `json:"stateConfig"`
	RecoverFromStateExecutionId *string                    `json:"recoverFromStateExecutionId,omitempty"`
	RecoverFromApi              *xcapi.WorkerApiType       `json:"recoverFromApi,omitempty"`
	AppDatabaseConfig           *InternalAppDatabaseConfig `json:"appDatabaseConfig"`
}

func FromStartRequestToStateInfoBytes(req xcapi.ProcessExecutionStartRequest) ([]byte, error) {
	infoJson := AsyncStateExecutionInfoJson{
		Namespace:         req.Namespace,
		ProcessId:         req.ProcessId,
		ProcessType:       req.GetProcessType(),
		WorkerURL:         req.GetWorkerUrl(),
		StateConfig:       req.StartStateConfig,
		AppDatabaseConfig: getInternalAppDatabaseConfig(req),
	}

	return infoJson.ToBytes()
}

func (j *AsyncStateExecutionInfoJson) ToBytes() ([]byte, error) {
	return json.Marshal(j)
}

func FromAsyncStateExecutionInfoToBytesForStateRecovery(
	info AsyncStateExecutionInfoJson,
	stateExeId string,
	api xcapi.WorkerApiType,
) ([]byte, error) {
	info.RecoverFromStateExecutionId = &stateExeId
	info.RecoverFromApi = &api
	// TODO we need to clean up for the next state execution otherwise it will be carried over forever
	return json.Marshal(info)
}

func BytesToAsyncStateExecutionInfo(bytes []byte) (AsyncStateExecutionInfoJson, error) {
	var info AsyncStateExecutionInfoJson
	err := json.Unmarshal(bytes, &info)
	return info, err
}
