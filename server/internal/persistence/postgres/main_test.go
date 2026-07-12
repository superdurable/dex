// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// testDBName is the single throw-away database holding every store's tables
// for this package's tests (mirrors the Mongo package's dex_test_store).
const testDBName = "dex_test_postgres_store"

func testURI() string { return os.Getenv("DEX_TEST_POSTGRES_URI") }

func TestMain(m *testing.M) {
	uri := testURI()
	if uri == "" {
		fmt.Fprintln(os.Stderr, "DEX_TEST_POSTGRES_URI must be set to run the Postgres store tests")
		os.Exit(1)
	}
	if err := EnsureSchemaAllInDatabase(context.Background(), uri, testDBName); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize Postgres schema: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func testPoolConfig(uri string) PoolConfig {
	return PoolConfig{URI: uri, Database: testDBName, MaxConns: 4, Timeouts: DefaultOperationTimeouts()}
}
