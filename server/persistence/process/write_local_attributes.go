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

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/log/tag"
	"github.com/xcherryio/xcherry/server/common/uuid"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) handleInitialLocalAttributesWrite(
	ctx context.Context,
	tx extensions2.SQLTransaction,
	req xcapi.ProcessExecutionStartRequest,
	resp data_models2.StartProcessResponse,
) error {
	if req.ProcessStartConfig == nil || req.ProcessStartConfig.LocalAttributeConfig == nil ||
		len(req.ProcessStartConfig.LocalAttributeConfig.InitialWrite) == 0 {
		return nil
	}

	attributes := req.ProcessStartConfig.LocalAttributeConfig.InitialWrite
	for i := range attributes {
		valueBytes, err := data_models2.FromEncodedObjectIntoBytes(&attributes[i].Value)
		if err != nil {
			return err
		}
		row := extensions2.LocalAttributeRow{
			ProcessExecutionId: resp.ProcessExecutionId,
			Key:                attributes[i].Key,
			Value:              valueBytes,
		}

		err = tx.InsertLocalAttribute(ctx, row)
		if err != nil {
			p.logger.Error("error on inserting local attribute", tag.Error(err))
			return err
		}
	}

	return nil
}

func (p sqlProcessStoreImpl) updateLocalAttributesIfNeeded(
	ctx context.Context, tx extensions2.SQLTransaction,
	processExecutionId uuid.UUID,
	localAttributeToUpdate []xcapi.KeyValue,
) error {
	if len(localAttributeToUpdate) > 0 {
		for _, kv := range localAttributeToUpdate {
			valueBytes, err := data_models2.FromEncodedObjectIntoBytes(&kv.Value)
			if err != nil {
				return err
			}
			row := extensions2.LocalAttributeRow{
				ProcessExecutionId: processExecutionId,
				Key:                kv.Key,
				Value:              valueBytes,
			}
			err = tx.UpsertLocalAttribute(ctx, row)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
