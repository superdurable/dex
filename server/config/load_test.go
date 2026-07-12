package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadFile_OverlaysYAML(t *testing.T) {
	cfg := DefaultConfig()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configPath, []byte(`
grpcListenAddress: ":9001"
persistence:
  mongo:
    uri: mongodb+srv://example.mongodb.net
shard:
  maxShards: 32
  leaseDuration: 45s
  defaultShardsForNewNamespaces: 32
  cluster:
    bindAddress: "0.0.0.0:7946"
    advertiseAddress: "10.0.0.1:7946"
    advertiseGrpcAddress: "10.0.0.1:7233"
    staticAddresses:
      - "10.0.0.2:7946"
    numberOfVNodes: 256
    minMembersBeforeReady: 3
    claimRetryInterval: 2s
    claimRetryIntervalJitter: 250ms
metrics:
  provider: prometheus
  metricPrefix: custom_
  maxEmittingTier: 3
`), 0o600)
	require.NoError(t, err)

	require.NoError(t, LoadFile(configPath, &cfg))
	require.Equal(t, ":9001", cfg.GRPCListenAddress)
	require.Equal(t, "mongodb+srv://example.mongodb.net", cfg.Persistence.Mongo.URI)
	// Per-store databases remain at their defaults; the parent URI applies
	// to every store via For(...).
	require.Equal(t, "mongodb+srv://example.mongodb.net", cfg.Persistence.Mongo.For(StoreRuns).URI)
	require.Equal(t, "dex_runs", cfg.Persistence.Mongo.For(StoreRuns).Database)
	require.Equal(t, 32, cfg.Shard.MaxShards)
	require.Equal(t, 45*time.Second, cfg.Shard.LeaseDuration)
	require.Equal(t, "10.0.0.1:7946", cfg.Shard.Cluster.AdvertiseAddress)
	require.Equal(t, "10.0.0.1:7233", cfg.Shard.Cluster.AdvertiseGRPCAddress)
	require.Equal(t, []string{"10.0.0.2:7946"}, cfg.Shard.Cluster.StaticAddresses)
	require.Equal(t, MetricsProviderPrometheus, cfg.Metrics.Provider)
	require.Equal(t, "custom_", cfg.Metrics.MetricPrefix)
	require.Equal(t, 3, cfg.Metrics.MaxEmittingTier)
}

func TestLoad_AppliesEnvOverridesAfterYAML(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configPath, []byte(`
persistence:
  mongo:
    uri: mongodb://yaml-host:27017
metrics:
  provider: prometheus
  metricPrefix: yaml_
`), 0o600)
	require.NoError(t, err)

	t.Setenv("DEX_MONGO_URI", "mongodb://env-host:27017")

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.Equal(t, "mongodb://env-host:27017", cfg.Persistence.Mongo.URI)
	require.Equal(t, MetricsProviderPrometheus, cfg.Metrics.Provider)
	require.Equal(t, "yaml_", cfg.Metrics.MetricPrefix)
}

func TestPersistence_PerStoreOverridesAndInheritance(t *testing.T) {
	cfg := DefaultConfig()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configPath, []byte(`
persistence:
  mongo:
    uri: mongodb://default-cluster
    visibility:
      uri: mongodb://visibility-cluster
      database: viz_db
    history:
      database: hist_db
`), 0o600)
	require.NoError(t, err)

	require.NoError(t, LoadFile(configPath, &cfg))

	// Visibility overrides BOTH URI and Database.
	resolved := cfg.Persistence.Mongo.For(StoreVisibility)
	require.Equal(t, "mongodb://visibility-cluster", resolved.URI)
	require.Equal(t, "viz_db", resolved.Database)

	// History overrides ONLY Database; URI inherits from the default block.
	resolved = cfg.Persistence.Mongo.For(StoreHistory)
	require.Equal(t, "mongodb://default-cluster", resolved.URI)
	require.Equal(t, "hist_db", resolved.Database)

	// Runs has no override in YAML, but DefaultMongoPersistenceConfig() ships
	// a per-store database default.
	resolved = cfg.Persistence.Mongo.For(StoreRuns)
	require.Equal(t, "mongodb://default-cluster", resolved.URI)
	require.Equal(t, "dex_runs", resolved.Database)

	// Blobs and DLQ alias to the runs cluster.
	// DLQ aliases to the runs cluster (DLQ rows reference task rows).
	require.Equal(t, cfg.Persistence.Mongo.For(StoreRuns), cfg.Persistence.Mongo.For(StoreDLQ))
	// Blobs has its OWN cluster — must not equal runs.
	require.NotEqual(t, cfg.Persistence.Mongo.For(StoreRuns), cfg.Persistence.Mongo.For(StoreBlobs),
		"blobs has its own per-store config, should not alias to runs")
}

func TestPersistence_DefaultConfigUsesDistinctDatabases(t *testing.T) {
	cfg := DefaultConfig()
	expected := map[string]string{
		StoreShards:     "dex_shards",
		StoreRuns:       "dex_runs",
		StoreBlobs:      "dex_blobs",
		StoreTasklists:  "dex_tasklists",
		StoreVisibility: "dex_visibility",
		StoreHistory:    "dex_history",
	}
	for store, want := range expected {
		require.Equal(t, want, cfg.Persistence.Mongo.For(store).Database, "store %s", store)
		require.Equal(t, "mongodb://localhost:27018", cfg.Persistence.Mongo.For(store).URI, "store %s URI inherits default", store)
	}
}

func TestLoadFile_ExpandsEnvironmentTemplates(t *testing.T) {
	t.Setenv("POD_IP", "10.1.2.3")

	cfg := DefaultConfig()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configPath, []byte(`
shard:
  cluster:
    advertiseAddress: "${POD_IP}:7946"
`), 0o600)
	require.NoError(t, err)

	require.NoError(t, LoadFile(configPath, &cfg))
	require.Equal(t, "10.1.2.3:7946", cfg.Shard.Cluster.AdvertiseAddress)
}

func TestLoadFile_PartialClusterPreservesDefaults(t *testing.T) {
	cfg := DefaultConfig()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(configPath, []byte(`
shard:
  cluster:
    minMembersBeforeReady: 3
tasklist:
  cluster:
    minMembersBeforeReady: 3
`), 0o600)
	require.NoError(t, err)

	require.NoError(t, LoadFile(configPath, &cfg))

	defaults := DefaultClusterConfig()
	require.Equal(t, 3, cfg.Shard.Cluster.MinMembersBeforeReady)
	require.Equal(t, defaults.NumberOfVNodes, cfg.Shard.Cluster.NumberOfVNodes,
		"numberOfVNodes should retain default when not set in YAML")
	require.Equal(t, defaults.ClaimRetryInterval, cfg.Shard.Cluster.ClaimRetryInterval,
		"claimRetryInterval should retain default when not set in YAML")
	require.Equal(t, defaults.OwnershipOpsMaxAttempts, cfg.Shard.Cluster.OwnershipOpsMaxAttempts,
		"ownershipOpsMaxAttempts should retain default when not set in YAML")
	require.Equal(t, defaults.BindAddress, cfg.Shard.Cluster.BindAddress,
		"bindAddress should retain default when not set in YAML")

	require.Equal(t, 3, cfg.Tasklist.Cluster.MinMembersBeforeReady)
	require.Equal(t, defaults.NumberOfVNodes, cfg.Tasklist.Cluster.NumberOfVNodes)
	require.Equal(t, defaults.OwnershipOpsMaxAttempts, cfg.Tasklist.Cluster.OwnershipOpsMaxAttempts)
}
