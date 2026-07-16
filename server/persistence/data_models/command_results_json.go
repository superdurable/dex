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

type CommandResultsJson struct {
	// if value is true, the timer was fired. Otherwise, the timer was skipped.
	TimerResults      map[int]bool                            `json:"timerResults"`
	LocalQueueResults map[int][]xcapi.LocalQueueMessageResult `json:"localQueueResults"`
}

func NewCommandResultsJson() CommandResultsJson {
	return CommandResultsJson{
		TimerResults:      map[int]bool{},
		LocalQueueResults: map[int][]xcapi.LocalQueueMessageResult{},
	}
}

func FromCommandResultsJsonToBytes(result CommandResultsJson) ([]byte, error) {
	return json.Marshal(result)
}

func BytesToCommandResultsJson(bytes []byte) (CommandResultsJson, error) {
	var result CommandResultsJson
	err := json.Unmarshal(bytes, &result)
	return result, err
}
