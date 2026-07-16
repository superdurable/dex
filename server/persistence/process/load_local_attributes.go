// Copyright (c) 2023 XDBLab Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"
	"fmt"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/ptr"
	"github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
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
