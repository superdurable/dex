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
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	"github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) writeToAppDatabase(
	ctx context.Context, tx extensions2.SQLTransaction, req xcapi.ProcessExecutionStartRequest,
) error {
	if req.ProcessStartConfig == nil || req.ProcessStartConfig.AppDatabaseConfig == nil {
		return nil
	}

	for _, tableConfig := range req.ProcessStartConfig.AppDatabaseConfig.Tables {
		for _, row := range tableConfig.Rows {
			writeMode := xcapi.RETURN_ERROR_ON_CONFLICT
			if row.ConflictMode != nil {
				writeMode = *row.ConflictMode
			}

			primaryKeyColumnToValue := map[string]string{}
			otherColumnToValue := map[string]string{}

			for _, primaryKeyColumn := range row.GetPrimaryKey() {
				primaryKeyColumnToValue[primaryKeyColumn.GetColumn()] = primaryKeyColumn.GetQueryValue()
			}

			for _, otherColumn := range row.GetInitialWrite() {
				otherColumnToValue[otherColumn.GetColumn()] = otherColumn.GetQueryValue()
			}

			rows := extensions2.AppDatabaseTableRow{
				TableName:               tableConfig.TableName,
				PrimaryKeyColumnToValue: primaryKeyColumnToValue,
				OtherColumnToValue:      otherColumnToValue,
			}

			err := tx.InsertAppDatabaseTable(ctx, rows, writeMode)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (p sqlProcessStoreImpl) writeToAppDatabaseIfNeeded(
	ctx context.Context, tx extensions2.SQLTransaction, tableConfig *data_models.InternalAppDatabaseConfig,
	appDatabaseWrite *xcapi.AppDatabaseWrite,
) error {
	for _, tableWrite := range appDatabaseWrite.GetTables() {
		allPrimaryKeys, ok := tableConfig.TablePrimaryKeys[tableWrite.GetTableName()]
		if !ok {
			return fmt.Errorf("table %s is not configured properly with primary key", tableWrite.GetTableName())
		}

		allPrimaryKeysColumnToValue := []map[string]string{}
		for _, primaryKey := range allPrimaryKeys {
			primaryKeyMap := map[string]string{}
			for _, pk := range primaryKey {
				primaryKeyMap[pk.GetColumn()] = pk.GetQueryValue()
			}
			allPrimaryKeysColumnToValue = append(allPrimaryKeysColumnToValue, primaryKeyMap)
		}

		for _, rowWrite := range tableWrite.GetRows() {
			primaryKeyColumnToValue := map[string]string{}
			otherColumnToValue := map[string]string{}

			for _, pk := range rowWrite.GetPrimaryKey() {
				primaryKeyColumnToValue[pk.GetColumn()] = pk.GetQueryValue()
			}

			if !isValidPrimaryKey(allPrimaryKeysColumnToValue, primaryKeyColumnToValue) {
				return fmt.Errorf("table %s row %v is not configured properly with primary key", tableWrite.GetTableName(), rowWrite)
			}

			for _, other := range rowWrite.GetWriteColumns() {
				otherColumnToValue[other.GetColumn()] = other.GetQueryValue()
			}

			err := tx.UpsertAppDatabaseTableByPK(ctx, extensions2.AppDatabaseTableRow{
				TableName:               tableWrite.GetTableName(),
				PrimaryKeyColumnToValue: primaryKeyColumnToValue,
				OtherColumnToValue:      otherColumnToValue,
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func isValidPrimaryKey(
	allPrimaryKeysColumnToValue []map[string]string, targetPrimaryKeyColumnToValue map[string]string,
) bool {
	for _, primaryKeyColumnToValue := range allPrimaryKeysColumnToValue {
		isValidPK := true
		for k, v := range primaryKeyColumnToValue {
			vv, ok := targetPrimaryKeyColumnToValue[k]
			if !ok || vv != v {
				isValidPK = false
				break
			}
		}

		if isValidPK {
			return true
		}
	}

	return false
}
