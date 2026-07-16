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

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/urfave/cli/v2"
	"github.com/xcherryio/xcherry/server/cmd/server/bootstrap"

	_ "github.com/xcherryio/xcherry/extensions/postgres" // import postgres extension
)

func main() {
	app := &cli.App{
		Name:  "xCherry server",
		Usage: "start the xCherry server",
		Action: func(c *cli.Context) error {
			bootstrap.StartXCherryServerCli(c)
			return nil
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  bootstrap.FlagConfig,
				Value: "./config/development-postgres.yaml",
				Usage: "the config to start xCherry server",
			},
			&cli.StringFlag{
				Name:  bootstrap.FlagService,
				Value: fmt.Sprintf("%v,%v", bootstrap.ApiServiceName, bootstrap.AsyncServiceName),
				Usage: "the services to start, separated by comma",
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
