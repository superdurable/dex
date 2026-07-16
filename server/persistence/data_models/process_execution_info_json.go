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

type ProcessExecutionInfoJson struct {
	ProcessType       string                     `json:"processType"`
	WorkerURL         string                     `json:"workerURL"`
	AppDatabaseConfig *InternalAppDatabaseConfig `json:"appDatabaseConfig"`
}

func FromStartRequestToProcessInfoBytes(req xcapi.ProcessExecutionStartRequest) ([]byte, error) {
	info := ProcessExecutionInfoJson{
		ProcessType:       req.GetProcessType(),
		WorkerURL:         req.GetWorkerUrl(),
		AppDatabaseConfig: getInternalAppDatabaseConfig(req),
	}
	return json.Marshal(info)
}

func BytesToProcessExecutionInfo(bytes []byte) (ProcessExecutionInfoJson, error) {
	var info ProcessExecutionInfoJson
	err := json.Unmarshal(bytes, &info)
	return info, err
}
