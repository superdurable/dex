// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	sqltest2 "github.com/xcherryio/xcherry/server/persistence/process/sqltest"
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
