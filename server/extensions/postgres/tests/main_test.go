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

package tests

import (
	"fmt"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/extensions"
	"github.com/superdurable/dex/server/extensions/postgres"
	"github.com/superdurable/dex/server/extensions/postgres/postgrestool"
	"github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/persistence/process"

	"os"
	"testing"
	"time"
)

var store persistence.ProcessStore

func TestMain(m *testing.M) {
	testDBName := fmt.Sprintf("test%v", time.Now().UnixNano())
	fmt.Println("using database name ", testDBName)

	sqlConfig := &config.SQL{
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

	err = extensions.SetupSchema(sqlConfig, "../../../"+postgrestool.DefaultSchemaFilePath)
	if err != nil {
		panic(err)
	}
	err = extensions.SetupSchema(sqlConfig, "../../../"+postgrestool.SampleTablesSchemaFilePath)
	if err != nil {
		panic(err)
	}

	store, err = process.NewSQLProcessStore(*sqlConfig, log.NewDevelopmentLogger())
	if err != nil {
		panic(err)
	}

	resultCode := m.Run()
	fmt.Println("finished running persistence test with status code", resultCode)

	_ = extensions.DropDatabase(*sqlConfig, testDBName)
	fmt.Println("testing database deleted")
	os.Exit(resultCode)
}
