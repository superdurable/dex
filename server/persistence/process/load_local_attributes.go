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

package process

import (
	"context"
	"fmt"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/ptr"
	"github.com/superdurable/dex/server/extensions"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) LoadLocalAttributes(
	ctx context.Context,
	request data_models2.LoadLocalAttributesRequest,
) (*data_models2.LoadLocalAttributesResponse, error) {
	if len(request.Request.KeysToLoadWithLock) != 0 &&
		request.Request.LockType != ptr.Any(xcapi.NO_LOCKING) {
		return nil, fmt.Errorf("locking type %v is not supported", request.Request.LockType)
	}

	var noLockRows []extensions.LocalAttributeRow
	var err error
	if len(request.Request.KeysToLoadNoLock) > 0 {
		noLockRows, err = p.session.SelectLocalAttributes(
			ctx, request.ProcessExecutionId, request.Request.KeysToLoadNoLock)
		if err != nil {
			return nil, err
		}
	}

	var attributes []xcapi.KeyValue
	for _, row := range noLockRows {
		value, err := data_models2.BytesToEncodedObject(row.Value)
		if err != nil {
			return nil, err
		}
		attributes = append(attributes, xcapi.KeyValue{
			Key:   row.Key,
			Value: value,
		})
	}

	return &data_models2.LoadLocalAttributesResponse{
		Response: xcapi.LoadLocalAttributesResponse{
			Attributes: attributes,
		},
	}, nil
}
