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
	"database/sql"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	extensions2 "github.com/superdurable/dex/server/extensions"
	"github.com/superdurable/dex/server/persistence"
)

type sqlProcessStoreImpl struct {
	session extensions2.SQLDBSession
	logger  log.Logger
}

var defaultTxOpts *sql.TxOptions = &sql.TxOptions{
	Isolation: sql.LevelReadCommitted,
}

func NewSQLProcessStore(sqlConfig config.SQL, logger log.Logger) (persistence.ProcessStore, error) {
	session, err := extensions2.NewSQLSession(&sqlConfig)
	return &sqlProcessStoreImpl{
		session: session,
		logger:  logger,
	}, err
}

func (p sqlProcessStoreImpl) Close() error {
	return p.session.Close()
}
