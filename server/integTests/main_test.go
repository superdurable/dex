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

package integTests

import (
	"context"
	"flag"
	"fmt"

	"github.com/xcherryio/sdk-go/xc"
	"github.com/xcherryio/xcherry/server/cmd/server/bootstrap"
	config2 "github.com/xcherryio/xcherry/server/config"
	"github.com/xcherryio/xcherry/server/extensions"
	"github.com/xcherryio/xcherry/server/extensions/postgres"
	"github.com/xcherryio/xcherry/server/extensions/postgres/postgrestool"

	"testing"
	"time"

	"github.com/xcherryio/sdk-go/integTests/worker"
)

func TestMain(m *testing.M) {
	flag.Parse()
	testDBName := fmt.Sprintf("test%v", time.Now().UnixNano())
	fmt.Printf("start running integ test, "+
		"testDBName: %v, useLocalServer:%v, createServerWithPostgres: %v \n",
		testDBName, *useLocalServer, *createServerWithPostgres)

	worker.StartGinWorker(workerService)

	var resultCode int
	var shutdownFunc bootstrap.GracefulShutdown
	rootCtx, rootCtxCancelFunc := context.WithCancel(context.Background())

	if !*useLocalServer {
		if *createServerWithPostgres {
			sqlConfig := &config2.SQL{
				ConnectAddr:     fmt.Sprintf("%v:%v", postgrestool.DefaultEndpoint, postgrestool.DefaultPort),
				User:            postgrestool.DefaultUserName,
				Password:        postgrestool.DefaultPassword,
				DBExtensionName: postgres.ExtensionName,
				DatabaseName:    testDBName,
			}
			err := extensions.CreateDatabase(*sqlConfig, testDBName)
			if err != nil {
				panic(err)
			}
			defer func() {
				err := extensions.DropDatabase(*sqlConfig, testDBName)
				if err != nil {
					fmt.Println("failed to drop database ", testDBName, err)
				} else {
					fmt.Println("testing database is deleted")
				}
			}()
			err = extensions.SetupSchema(sqlConfig, "../"+postgrestool.DefaultSchemaFilePath)
			if err != nil {
				panic(err)
			}
			err = extensions.SetupSchema(sqlConfig, "../"+postgrestool.SampleTablesSchemaFilePath)
			if err != nil {
				panic(err)
			}

			cfg := config2.Config{
				Log: config2.Logger{
					Level: "debug",
				},
				ApiService: &config2.ApiServiceConfig{
					HttpServer: config2.HttpServerConfig{
						Address:      ":" + xc.DefaultServerPort,
						ReadTimeout:  5 * time.Second,
						WriteTimeout: 60 * time.Second,
					},
					AsyncServiceAddress: "http://0.0.0.0:8701",
				},
				Database: &config2.DatabaseConfig{
					ProcessStoreConfig:    sqlConfig,
					VisibilityStoreConfig: sqlConfig,
				},
				AsyncService: &config2.AsyncServiceConfig{
					Mode: config2.AsyncServiceModeStandalone,
					InternalHttpServer: config2.HttpServerConfig{
						Address: "0.0.0.0:8701",
					},
				},
			}

			shutdownFunc = bootstrap.StartXCherryServer(rootCtx, &cfg, nil)
		}
	}

	// looks like this wait can fix some flaky failure
	// where API call is made before Gin server is ready
	time.Sleep(time.Millisecond * 100)

	resultCode = m.Run()
	fmt.Println("finished running integ test with status code", resultCode)
	rootCtxCancelFunc()
	if shutdownFunc != nil {
		_ = shutdownFunc(rootCtx)
	}
}
