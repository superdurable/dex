// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"database/sql"

	"github.com/xcherryio/xcherry/server/common/log"
	"github.com/xcherryio/xcherry/server/config"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	"github.com/xcherryio/xcherry/server/persistence"
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
