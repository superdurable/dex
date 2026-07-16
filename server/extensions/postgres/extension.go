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
	"fmt"
	"net"
	"net/url"

	"github.com/xcherryio/xcherry/server/config"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"

	"github.com/iancoleman/strcase"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq" // load the SQL driver for postgres
)

const (
	dsnFmt = "postgres://%s@%s:%s/%s"
)

type extension struct{}

var _ extensions2.SQLDBExtension = (*extension)(nil)

func init() {
	extensions2.RegisterSQLDBExtension(ExtensionName, &extension{})
}

func (d *extension) StartDBSession(cfg *config.SQL) (extensions2.SQLDBSession, error) {
	sqlxdb, err := d.createSingleDBConn(cfg)
	if err != nil {
		return nil, err
	}
	return newDBSession(sqlxdb), nil
}

func (d *extension) StartAdminDBSession(cfg *config.SQL) (extensions2.SQLAdminDBSession, error) {
	sqlxdb, err := d.createSingleDBConn(cfg)
	if err != nil {
		return nil, err
	}
	return newAdminDBSession(sqlxdb), nil
}

// CreateDBConnection returns a reference to a logical connection to the
// underlying SQL database. The returned object is tied to a single
// SQL database and the object can be used to perform CRUD operations on
// the tables in the database
func (d *extension) createSingleDBConn(cfg *config.SQL) (*sqlx.DB, error) {
	host, port, err := net.SplitHostPort(cfg.ConnectAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid connect address, it must be in host:port format, %v, err: %w", cfg.ConnectAddr, err)
	}

	// TODO there are a lot more config we need to support like in Cadence
	// https://github.com/uber/cadence/blob/2df19da3d4c6fdfd74a54a6df43447883e3d3567/common/persistence/sql/sqlplugin/postgres/plugin.go#L138
	sslParams := url.Values{}
	sslParams.Set("sslmode", "disable")
	db, err := sqlx.Connect(ExtensionName, buildDSN(cfg, host, port, sslParams))
	if err != nil {
		return nil, err
	}

	// Maps struct names in CamelCase to snake without need for db struct tags.
	db.MapperFunc(strcase.ToSnake)
	return db, nil
}

func buildDSN(cfg *config.SQL, host string, port string, params url.Values) string {
	dbName := cfg.DatabaseName
	//NOTE: postgres doesn't allow to connect with empty dbName, the admin dbName is "postgres"
	if dbName == "" {
		dbName = ExtensionName
	}

	credentialString := generateCredentialString(cfg.User, cfg.Password)
	dsn := fmt.Sprintf(dsnFmt, credentialString, host, port, dbName)
	if attrs := params.Encode(); attrs != "" {
		dsn += "?" + attrs
	}
	return dsn
}

func generateCredentialString(user string, password string) string {
	userPass := url.PathEscape(user)
	if password != "" {
		userPass += ":" + url.PathEscape(password)
	}
	return userPass
}
