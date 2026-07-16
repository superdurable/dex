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

package postgres

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/xcherryio/xcherry/server/extensions"
)

// NOTE we have to use %v because somehow postgres doesn't work with ? here
// It's a small bug in sqlx library
const createDatabaseQuery = "CREATE database %v"

const dropDatabaseQuery = "Drop database %v"

type adminDBSession struct {
	db *sqlx.DB
}

var _ extensions.SQLAdminDBSession = (*adminDBSession)(nil)

func newAdminDBSession(db *sqlx.DB) *adminDBSession {
	return &adminDBSession{
		db: db,
	}
}

func (a adminDBSession) DropDatabase(ctx context.Context, database string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(dropDatabaseQuery, database))
	return err
}

func (a adminDBSession) ExecuteSchemaDDL(ctx context.Context, ddlQuery string) error {
	_, err := a.db.ExecContext(ctx, ddlQuery)
	return err
}

func (a adminDBSession) CreateDatabase(ctx context.Context, database string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(createDatabaseQuery, database))
	return err
}

func (a adminDBSession) Close() error {
	return a.db.Close()
}
