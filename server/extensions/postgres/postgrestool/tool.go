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

package postgrestool

import (
	"github.com/urfave/cli/v2"
	extensions2 "github.com/superdurable/dex/server/extensions"
	"github.com/superdurable/dex/server/extensions/postgres"
)

const DefaultEndpoint = "127.0.0.1"
const DefaultPort = 5432
const DefaultUserName = "dex"
const DefaultPassword = "superdurable"
const DefaultDatabaseName = "dex"
const DefaultSchemaFilePath = "./extensions/postgres/schema/dex_sys_schema.sql"
const SampleTablesSchemaFilePath = "./extensions/postgres/schema/sample_tables.sql"

// BuildCLIOptions builds the options for cli
func BuildCLIOptions() *cli.App {

	app := cli.NewApp()

	app.Name = "dex postgres tool"
	app.Usage = "tool for Dex operation on postgres"

	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    extensions2.CLIFlagEndpoint,
			Aliases: []string{"e"},
			Value:   DefaultEndpoint,
			Usage:   "hostname or ip address of sql host to connect to postgres",
		},
		&cli.IntFlag{
			Name:    extensions2.CLIFlagPort,
			Aliases: []string{"p"},
			Value:   DefaultPort,
			Usage:   "port of sql host to connect to postgres",
		},
		&cli.StringFlag{
			Name:    extensions2.CLIFlagUser,
			Aliases: []string{"u"},
			Value:   DefaultUserName,
			Usage:   "user name used for authentication when connecting to postgres",
		},
		&cli.StringFlag{
			Name:    extensions2.CLIFlagPassword,
			Aliases: []string{"pw"},
			Value:   DefaultPassword,
			Usage:   "password used for authentication when connecting to postgres",
		},
		&cli.StringFlag{
			Name:    extensions2.CLIFlagDatabase,
			Aliases: []string{"db"},
			Value:   DefaultDatabaseName,
			Usage:   "name of the postgres database",
		},
	}

	app.Commands = []*cli.Command{
		{
			Name:    "create-database",
			Aliases: []string{"create"},
			Usage:   "creates a database",
			Action: func(c *cli.Context) error {
				return extensions2.CreateDatabaseByCli(c, postgres.ExtensionName)
			},
		},
		{
			Name:    "install-schema",
			Aliases: []string{"install"},
			Usage:   "install schema into a database",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    extensions2.CLIFlagFile,
					Aliases: []string{"f"},
					Value:   DefaultSchemaFilePath,
					Usage:   "file path of the schema file to install",
				},
			},
			Action: func(c *cli.Context) error {
				return extensions2.SetupSchemaByCli(c, postgres.ExtensionName)
			},
		},
	}

	return app
}
