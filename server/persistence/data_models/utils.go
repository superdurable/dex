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

func FromEncodedObjectIntoBytes(obj *xcapi.EncodedObject) ([]byte, error) {
	if obj == nil {
		// set this as default for
		// https://github.com/xcherryio/xcherry/issues/100
		return json.Marshal(xcapi.NewEncodedObject("", ""))
	}
	return json.Marshal(obj)
}

func BytesToEncodedObject(bytes []byte) (xcapi.EncodedObject, error) {
	var obj xcapi.EncodedObject
	err := json.Unmarshal(bytes, &obj)
	return obj, err
}

func FromCommandRequestToBytes(request xcapi.CommandRequest) ([]byte, error) {
	return json.Marshal(request)
}

func BytesToCommandRequest(bytes []byte) (xcapi.CommandRequest, error) {
	if len(bytes) == 0 {
		return xcapi.CommandRequest{}, nil
	}

	var request xcapi.CommandRequest
	err := json.Unmarshal(bytes, &request)
	return request, err
}
