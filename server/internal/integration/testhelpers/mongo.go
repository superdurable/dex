// Package testhelpers contains shared helpers for the integration sub-packages
// under server/internal/integration/. Each integration sub-package owns its
// own mongo database prefix to allow `go test ./...` to run sub-packages in
// parallel without cross-package data contention. The helpers in this package
// build the per-prefix MongoPersistenceConfig and bootstrap schema once per
// sub-package via TestMain.
package testhelpers

import (
	"os"
	"strings"
	"testing"

	"github.com/superdurable/dex/server/config"
	"github.com/stretchr/testify/require"
)

// MongoURIEnvVar is the env var read by every integration sub-package to
// locate the test mongos cluster. When unset, integration tests skip.
const MongoURIEnvVar = "DEX_TEST_MONGO_URI"

// MongoURI returns the test mongo URI from the environment, or "" when unset.
func MongoURI() string { return os.Getenv(MongoURIEnvVar) }

// MongoConfigForPrefix builds a MongoPersistenceConfig that points every store
// at the given URI but uses a distinct per-store database under dbPrefix.
// Mirrors the production per-store database layout (every store has its own
// database) while keeping every store on one local test cluster. Each
// integration sub-package owns its own dbPrefix so sub-packages can run in
// parallel without trampling each other.
func MongoConfigForPrefix(uri, dbPrefix string) config.MongoPersistenceConfig {
	cfg := config.DefaultMongoPersistenceConfig()
	cfg.URI = capMongoPoolSize(uri)
	cfg.Shards.Database = dbPrefix + "_shards"
	cfg.Runs.Database = dbPrefix + "_runs"
	cfg.Blobs.Database = dbPrefix + "_blobs"
	cfg.Tasklists.Database = dbPrefix + "_tasklists"
	cfg.Visibility.Database = dbPrefix + "_visibility"
	cfg.History.Database = dbPrefix + "_history"
	return cfg
}

// capMongoPoolSize appends a small maxPoolSize to the test mongo URI. The
// parallel integration suite runs many servers (the 2-node cluster alone is
// 2x6 store clients) against one mongos; the driver default of 100 per client
// saturates mongos connections and the heaviest tests stall.
func capMongoPoolSize(uri string) string {
	if strings.Contains(uri, "maxPoolSize=") {
		return uri
	}
	if strings.Contains(uri, "?") {
		return uri + "&maxPoolSize=10"
	}
	// The query "?" must follow a "/". Detect whether a path is already
	// present after the host (mongodb://host[:port][/path]).
	rest := uri
	for _, scheme := range []string{"mongodb+srv://", "mongodb://"} {
		if strings.HasPrefix(rest, scheme) {
			rest = strings.TrimPrefix(rest, scheme)
			break
		}
	}
	if strings.Contains(rest, "/") {
		return uri + "?maxPoolSize=10"
	}
	return uri + "/?maxPoolSize=10"
}

// RunsDBName returns the name of the runs/blobs database for a given prefix.
// Used by legacy tests that build a RunStore + BlobStore directly (no
// cmd.NewServerApp) and store both run rows and blobs in one database.
func RunsDBName(dbPrefix string) string { return dbPrefix + "_runs" }

// ApplyMongo overlays the per-prefix mongo config onto cfg.Persistence.Mongo
// and selects the mongo backend. Use in test setup helpers that build a Config
// for cmd.NewServerApp.
func ApplyMongo(t *testing.T, cfg *config.Config, uri, dbPrefix string) {
	t.Helper()
	cfg.Persistence.Backend = config.BackendMongo
	cfg.Persistence.Mongo = MongoConfigForPrefix(uri, dbPrefix)
	require.NotEmpty(t, cfg.Persistence.Mongo.For(config.StoreRuns).Database)
}
