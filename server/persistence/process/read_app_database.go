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
	"github.com/superdurable/dex/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) ReadAppDatabase(
	ctx context.Context, request data_models.AppDatabaseReadRequest,
) (*data_models.AppDatabaseReadResponse, error) {
	var tableResponses []xcapi.AppDatabaseTableReadResponse
	config := request.AppDatabaseConfig

	for _, tableReq := range request.Request.Tables {
		if tableReq.GetLockType() != xcapi.NO_LOCKING {
			// TODO support other locking types
			return nil, fmt.Errorf("locking type %v is not supported", tableReq.GetLockType())
		}

		pk, ok := config.TablePrimaryKeys[tableReq.GetTableName()]
		if !ok {
			return nil, fmt.Errorf("table %s is not configured properly with primary key", tableReq.GetTableName())
		}

		rows, err := p.session.SelectAppDatabaseTableByPK(ctx, tableReq.GetTableName(), pk, tableReq.GetColumns())
		if err != nil {
			return nil, err
		}

		var rowReadResponse []xcapi.AppDatabaseRowReadResponse

		for _, row := range rows {
			var colsOut []xcapi.AppDatabaseColumnValue

			for fname, fvalue := range row.ColumnToValue {
				colsOut = append(colsOut, xcapi.AppDatabaseColumnValue{
					Column:     fname,
					QueryValue: fvalue,
				})
			}

			rowReadResponse = append(rowReadResponse, xcapi.AppDatabaseRowReadResponse{
				Columns: colsOut,
			})
		}

		tableResponses = append(tableResponses, xcapi.AppDatabaseTableReadResponse{
			TableName: tableReq.TableName,
			Rows:      rowReadResponse,
		})
	}

	return &data_models.AppDatabaseReadResponse{
		Response: xcapi.AppDatabaseReadResponse{
			Tables: tableResponses,
		},
	}, nil
}
