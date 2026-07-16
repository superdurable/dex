// Copyright (c) 2023 XDBLab Organization
// SPDX-License-Identifier: Apache-2.0

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
