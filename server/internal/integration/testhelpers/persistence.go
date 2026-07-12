// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package testhelpers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/persistence/factory"
	"github.com/superdurable/dex/server/internal/persistence/mongo"
	"github.com/superdurable/dex/server/internal/persistence/postgres"
	"github.com/stretchr/testify/require"
)

// PersistenceBackendEnvVar selects which backend the integration suite runs
// against. Defaults to postgres (the server default). CI runs the suite once
// per backend.
const PersistenceBackendEnvVar = "DEX_TEST_PERSISTENCE_BACKEND"

// PostgresURIEnvVar locates the test Postgres server (DSN without a dbname).
const PostgresURIEnvVar = "DEX_TEST_POSTGRES_URI"

// Backend returns the configured test backend ("postgres" or "mongo"),
// defaulting to postgres.
func Backend() string {
	if b := os.Getenv(PersistenceBackendEnvVar); b != "" {
		return b
	}
	return config.BackendPostgres
}

// PostgresURI returns the test Postgres URI from the environment, or "" unset.
func PostgresURI() string { return os.Getenv(PostgresURIEnvVar) }

// TestDBURI returns the URI for the active backend, or "" when unset (which
// causes individual tests to skip).
func TestDBURI() string {
	if Backend() == config.BackendMongo {
		return MongoURI()
	}
	return PostgresURI()
}

// PostgresConfigForPrefix builds a PostgresPersistenceConfig pointing every
// store at uri with a distinct per-store database under dbPrefix — the
// Postgres analogue of MongoConfigForPrefix.
func PostgresConfigForPrefix(uri, dbPrefix string) config.PostgresPersistenceConfig {
	cfg := config.DefaultPostgresPersistenceConfig()
	cfg.URI = uri
	// Cap pool size per store: the parallel integration suite runs many
	// servers (the 2-node cluster alone is 2x6 pools) against one Postgres,
	// so the default 10/store would exhaust max_connections and hang.
	cfg.MaxConns = 4
	cfg.Shards.Database = dbPrefix + "_shards"
	cfg.Runs.Database = dbPrefix + "_runs"
	cfg.Blobs.Database = dbPrefix + "_blobs"
	cfg.Tasklists.Database = dbPrefix + "_tasklists"
	cfg.Visibility.Database = dbPrefix + "_visibility"
	cfg.History.Database = dbPrefix + "_history"
	return cfg
}

// PersistenceConfigForPrefix builds a full PersistenceConfig for the active
// backend with per-prefix databases.
func PersistenceConfigForPrefix(uri, dbPrefix string) config.PersistenceConfig {
	pcfg := config.DefaultPersistenceConfig()
	pcfg.Backend = Backend()
	if Backend() == config.BackendMongo {
		pcfg.Mongo = MongoConfigForPrefix(uri, dbPrefix)
	} else {
		pcfg.Postgres = PostgresConfigForPrefix(uri, dbPrefix)
	}
	return pcfg
}

// ApplyPersistence overlays the per-prefix persistence config for the active
// backend onto cfg. Replaces ApplyMongo at backend-agnostic call sites.
func ApplyPersistence(t *testing.T, cfg *config.Config, uri, dbPrefix string) {
	t.Helper()
	cfg.Persistence = PersistenceConfigForPrefix(uri, dbPrefix)
}

// EnsureSchemaForPrefix provisions the per-store schema for dbPrefix on the
// active backend. No-op when the backend URI is unset.
func EnsureSchemaForPrefix(dbPrefix string) error {
	uri := TestDBURI()
	if uri == "" {
		return nil
	}
	if Backend() == config.BackendMongo {
		return mongo.EnsureSchemaForConfig(context.Background(), MongoConfigForPrefix(uri, dbPrefix))
	}
	return postgres.EnsureSchemaForConfig(context.Background(), PostgresConfigForPrefix(uri, dbPrefix))
}

// RunMain is the standard sub-package TestMain body: bootstrap schema for
// dbPrefix on the active backend, then run the package's tests.
func RunMain(m interface{ Run() int }, dbPrefix string) {
	if err := EnsureSchemaForPrefix(dbPrefix); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize %s schema for %q: %v\n", Backend(), dbPrefix, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// NewStoreSetForTest builds the full set of stores for the active backend under
// dbPrefix and registers cleanup. Used by integration sub-packages that
// construct stores directly (runengine, batchprocessing). Skips the test when
// the backend URI is unset.
func NewStoreSetForTest(t *testing.T, dbPrefix string) *factory.StoreSet {
	t.Helper()
	uri := TestDBURI()
	if uri == "" {
		t.Skip(PersistenceBackendEnvVar + "=" + Backend() + ": backend URI not set")
	}
	set, err := factory.BuildStoreSet(context.Background(), PersistenceConfigForPrefix(uri, dbPrefix))
	require.Nil(t, err)
	t.Cleanup(func() { set.Close() })
	return set
}
