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
	"database/sql"

	"github.com/jmoiron/sqlx"
	"github.com/xcherryio/xcherry/server/extensions"
)

type dbSession struct {
	db *sqlx.DB
}

type dbTx struct {
	tx *sqlx.Tx
}

var _ extensions.SQLDBSession = (*dbSession)(nil)
var _ extensions.SQLTransaction = (*dbTx)(nil)

func newDBSession(db *sqlx.DB) *dbSession {
	return &dbSession{
		db: db,
	}
}

func (d dbSession) StartTransaction(ctx context.Context, opts *sql.TxOptions) (extensions.SQLTransaction, error) {
	tx, err := d.db.BeginTxx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return dbTx{
		tx: tx,
	}, nil
}

func (d dbSession) Close() error {
	return d.db.Close()
}

func (d dbTx) Commit() error {
	return d.tx.Commit()
}

func (d dbTx) Rollback() error {
	return d.tx.Rollback()
}
