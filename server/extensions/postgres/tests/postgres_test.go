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
	"testing"

	"github.com/stretchr/testify/assert"
	sqltest2 "github.com/superdurable/dex/server/persistence/process/sqltest"
)

func TestBasic(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLBasicTest(t, assert.New(t), store)
}

func TestGracefulComplete(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLGracefulCompleteTest(t, assert.New(t), store)
}

func TestForceFail(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLForceFailTest(t, assert.New(t), store)
}

func TestProcessIdReusePolicyDisallowReuse(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLProcessIdReusePolicyDisallowReuseTest(t, assert.New(t), store)
}

func TestProcessIdReusePolicyAllowIfNoRunning(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLProcessIdReusePolicyAllowIfNoRunning(t, assert.New(t), store)
}

func TestProcessIdReusePolicyTerminateIfRunning(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLProcessIdReusePolicyTerminateIfRunning(t, assert.New(t), store)
}

func TestProcessIdReusePolicyAllowIfPreviousExitAbnormally(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLProcessIdReusePolicyAllowIfPreviousExitAbnormally(t, assert.New(t), store)
}

func TestProcessIdReusePolicyDefault(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLProcessIdReusePolicyDefault(t, assert.New(t), store)
}

func TestBackoffTimer(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLBackoffTest(t, assert.New(t), store)
}

func TestStateFailureRecovery(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLStateFailureRecoveryTest(t, assert.New(t), store)
}

func TestAppDatabase(t *testing.T) {
	sqltest2.CleanupEnv(assert.New(t), store)
	sqltest2.SQLAppDatabaseTest(t, assert.New(t), store)
}
