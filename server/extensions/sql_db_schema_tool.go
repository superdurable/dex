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

package extensions

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"

	"github.com/urfave/cli/v2"
	"github.com/superdurable/dex/server/config"
)

// SetupSchemaByCli setup schema for a new database
func SetupSchemaByCli(cli *cli.Context, extensionName string) error {
	cfg, err := parseConnectConfig(cli, extensionName)
	if err != nil {
		panic(err)
	}
	filePath := cli.String(CLIFlagFile)
	return SetupSchema(cfg, filePath)
}

func SetupSchema(cfg *config.SQL, filePath string) error {
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("error reading contents of file %v:%v", filePath, err.Error())
	}

	adminSession, err := NewSQLAdminSession(cfg)
	if err != nil {
		return err
	}
	defer adminSession.Close()

	return adminSession.ExecuteSchemaDDL(context.Background(), string(content))
}

// CreateDatabaseByCli creates a sql database
func CreateDatabaseByCli(cli *cli.Context, extensionName string) error {
	cfg, err := parseConnectConfig(cli, extensionName)
	if err != nil {
		panic(err)
	}
	database := cli.String(CLIFlagDatabase)
	return CreateDatabase(*cfg, database)
}

func CreateDatabase(cfg config.SQL, name string) error {
	// cfg config.SQL will be modified as this cannot use pointer "cfg *config.SQL
	cfg.DatabaseName = ""
	// IMPORTATNT! set empty because the database is to be created(not exists yet). It's up to the extension to handle it
	// e.g.:
	// MySQL just use an account like root
	// Postgres will set it to postgres

	adminSession, err := NewSQLAdminSession(&cfg)
	if err != nil {
		return err
	}
	defer adminSession.Close()
	return adminSession.CreateDatabase(context.Background(), name)
}

func DropDatabase(cfg config.SQL, name string) error {
	cfg.DatabaseName = "" // similar as CreateDatabase, in Postgres, all connections must be closed before deleting a database
	adminSession, err := NewSQLAdminSession(&cfg)
	if err != nil {
		return err
	}
	defer adminSession.Close()
	return adminSession.DropDatabase(context.Background(), name)
}

func parseConnectConfig(cli *cli.Context, extensionName string) (*config.SQL, error) {
	cfg := new(config.SQL)

	host := cli.String(CLIFlagEndpoint)
	port := cli.Int(CLIFlagPort)
	cfg.ConnectAddr = fmt.Sprintf("%s:%v", host, port)
	cfg.User = cli.String(CLIFlagUser)
	cfg.Password = cli.String(CLIFlagPassword)
	cfg.DatabaseName = cli.String(CLIFlagDatabase)
	cfg.DBExtensionName = extensionName

	if err := ValidateConnectConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ValidateConnectConfig validates params
func ValidateConnectConfig(cfg *config.SQL) error {
	host, _, err := net.SplitHostPort(cfg.ConnectAddr)
	if err != nil {
		return fmt.Errorf("invalid host and port " + cfg.ConnectAddr)
	}
	if len(host) == 0 {
		return fmt.Errorf("missing sql endpoint argument " + flag(CLIFlagEndpoint))
	}
	if cfg.DatabaseName == "" {
		return fmt.Errorf("missing " + flag(CLIFlagDatabase) + " argument")
	}
	return nil
}

func flag(opt string) string {
	return "(-" + opt + ")"
}
