package config

import "time"

// Persistence backend identifiers accepted by PersistenceConfig.Backend.
const (
	BackendPostgres = "postgres"
	BackendMongo    = "mongo"
)

// PersistenceConfig groups external persistence dependencies. Backend selects
// which implementation the bootstrap factory wires; the matching sub-config
// (Postgres or Mongo) supplies connection settings. Postgres is the default.
type PersistenceConfig struct {
	// Backend selects the persistence implementation: "postgres" (default)
	// or "mongo". The bootstrap factory (persistence.BuildStoreSet) switches
	// on this value.
	Backend string `yaml:"backend" env:"DEX_DB_BACKEND"`

	Postgres PostgresPersistenceConfig `yaml:"postgres"`
	Mongo    MongoPersistenceConfig    `yaml:"mongo"`
}

// MongoPersistenceConfig configures connection settings for every store-backed
// MongoDB cluster. The top-level URI + timeouts act as defaults that any
// per-store override inherits when its own value is empty / zero. Database
// is intentionally per-store only — every store ships with a distinct default
// database name (e.g. "dex_runs", "dex_visibility") so the
// production schema in v0.js and integration tests get isolated databases
// without extra configuration.
//
// The single-cluster path stays simple: set just the parent URI (or
// DEX_MONGO_URI) and every store reuses that connection while still
// landing in its own database.
//
// Multi-cluster deployments override URI per-store (typically Visibility +
// History to spread read traffic across dedicated clusters).
type MongoPersistenceConfig struct {
	// URI is the default MongoDB connection string used for any store whose
	// per-store URI override is empty. Atlas should normally use a
	// mongodb+srv URI injected from a Kubernetes Secret.
	// Default: "mongodb://localhost:27018"
	URI string `yaml:"uri" env:"DEX_MONGO_URI"`

	// ShortOperationTimeout caps contexts for single-document operations
	// (FindOne, UpdateOne, InsertOne, DeleteOne, transactions with bounded
	// docs). If the caller already has a tighter deadline, that wins.
	// Applied to every store unless its per-store override is non-zero.
	// Default: 5s
	ShortOperationTimeout time.Duration `yaml:"shortOperationTimeout" env:"DEX_MONGO_SHORT_TIMEOUT"`

	// LongOperationTimeout caps contexts for multi-document operations
	// (Find+cursor iteration, DeleteMany, BulkWrite, InsertMany). Applied to
	// every store unless its per-store override is non-zero.
	// Default: 30s
	LongOperationTimeout time.Duration `yaml:"longOperationTimeout" env:"DEX_MONGO_LONG_TIMEOUT"`

	// Per-store override blocks. The Database field is REQUIRED on each
	// store (defaulted by DefaultMongoPersistenceConfig); the URI + timeout
	// fields are optional and inherit from the parent above when empty.
	//
	// Note: the DLQ collection is co-located with the runs cluster (DLQ
	// rows are created when a task in `runs` exhausts retries, and they
	// reference the run row), so it does NOT have its own override block —
	// MongoPersistenceConfig.For(StoreDLQ) returns the resolved Runs
	// config. Blobs, by contrast, get their own block: blob payloads are
	// often the largest documents in the system and operators may want to
	// host them on a separate, cheaper-storage cluster.
	//
	// No env tags on these blocks: the parent-level env vars set the
	// default only — the recursive env walker would otherwise fan a single
	// DEX_MONGO_URI value into every per-store URI.
	Shards     MongoStoreConfig `yaml:"shards"`
	Runs       MongoStoreConfig `yaml:"runs"`
	Blobs      MongoStoreConfig `yaml:"blobs"`
	Tasklists  MongoStoreConfig `yaml:"tasklists"`
	Visibility MongoStoreConfig `yaml:"visibility"`
	History    MongoStoreConfig `yaml:"history"`
}

// MongoStoreConfig is the per-store override block. URI / timeouts inherit
// from the parent MongoPersistenceConfig when empty / zero. Database is the
// store's database name on the resolved cluster — DefaultMongoPersistenceConfig
// ships a distinct value per store so every store has a database without
// operator configuration.
//
// No env tags: see comment on MongoPersistenceConfig.
type MongoStoreConfig struct {
	URI                   string        `yaml:"uri"`
	Database              string        `yaml:"database"`
	ShortOperationTimeout time.Duration `yaml:"shortOperationTimeout"`
	LongOperationTimeout  time.Duration `yaml:"longOperationTimeout"`
}

// MongoConfig is a fully-resolved per-store Mongo connection config returned
// by MongoPersistenceConfig.For. Pass it to mongo.New<Store>WithDatabase /
// mongo.OperationTimeouts at the wiring layer.
type MongoConfig struct {
	URI                   string
	Database              string
	ShortOperationTimeout time.Duration
	LongOperationTimeout  time.Duration
}

// Logical store names accepted by MongoPersistenceConfig.For. DLQ rows live
// in the same cluster as the run state (they reference run/task rows in
// the runs collection), so the DLQ name aliases to "runs". Blobs have
// their own configurable cluster — blob payloads can be very large and
// operators may want to spread them across cheaper storage independently
// of the run state.
const (
	StoreShards     = "shards"
	StoreRuns       = "runs"
	StoreBlobs      = "blobs"
	StoreDLQ        = "dlq"
	StoreTasklists  = "tasklists"
	StoreVisibility = "visibility"
	StoreHistory    = "history"
)

// AllStoreNames returns the set of distinct logical clusters that the server
// connects to. The DLQ alias ("dlq" → "runs") is not included so callers
// iterating to ensure schema or open clients do not double-connect.
func AllStoreNames() []string {
	return []string{StoreShards, StoreRuns, StoreBlobs, StoreTasklists, StoreVisibility, StoreHistory}
}

// For returns the resolved MongoConfig for a store. The per-store Database
// is taken as-is (always non-empty in DefaultMongoPersistenceConfig); URI
// and timeouts fall back to the parent defaults when the per-store override
// is empty / zero.
func (m MongoPersistenceConfig) For(store string) MongoConfig {
	override := m.overrideFor(store)
	cfg := MongoConfig{
		URI:                   m.URI,
		Database:              override.Database,
		ShortOperationTimeout: m.ShortOperationTimeout,
		LongOperationTimeout:  m.LongOperationTimeout,
	}
	if override.URI != "" {
		cfg.URI = override.URI
	}
	if override.ShortOperationTimeout != 0 {
		cfg.ShortOperationTimeout = override.ShortOperationTimeout
	}
	if override.LongOperationTimeout != 0 {
		cfg.LongOperationTimeout = override.LongOperationTimeout
	}
	return cfg
}

func (m MongoPersistenceConfig) overrideFor(store string) MongoStoreConfig {
	switch store {
	case StoreShards:
		return m.Shards
	case StoreRuns, StoreDLQ: // DLQ co-located with runs cluster
		return m.Runs
	case StoreBlobs:
		return m.Blobs
	case StoreTasklists:
		return m.Tasklists
	case StoreVisibility:
		return m.Visibility
	case StoreHistory:
		return m.History
	default:
		return MongoStoreConfig{}
	}
}

// ============================================================================
// PostgreSQL
// ============================================================================

// PostgresPersistenceConfig configures connection settings for every
// store-backed PostgreSQL database. It mirrors MongoPersistenceConfig: the
// top-level URI + timeouts + pool settings act as defaults that any per-store
// override inherits when its own value is empty / zero. Database is per-store
// only — every store ships with a distinct default database name (e.g.
// "dex_runs") so the schema and tests get isolated databases.
//
// The URI is a libpq/pgx DSN WITHOUT a database path; the resolved per-store
// Database is applied to the connection at pool-creation time. This keeps the
// single-server path simple (one URI, six databases) while allowing per-store
// URI overrides for multi-server deployments.
type PostgresPersistenceConfig struct {
	// URI is the default PostgreSQL connection string (pgx/libpq DSN) used
	// for any store whose per-store URI override is empty. The database is
	// supplied separately per store, so this URI should not pin a dbname.
	// Default: "postgres://dex:dex@localhost:5432/?sslmode=disable"
	URI string `yaml:"uri" env:"DEX_POSTGRES_URI"`

	// MaxConns caps the pgxpool size per store. Default: 10.
	MaxConns int32 `yaml:"maxConns" env:"DEX_POSTGRES_MAX_CONNS"`

	// ShortOperationTimeout caps contexts for single-row operations.
	// Default: 5s
	ShortOperationTimeout time.Duration `yaml:"shortOperationTimeout" env:"DEX_POSTGRES_SHORT_TIMEOUT"`

	// LongOperationTimeout caps contexts for multi-row / transaction
	// operations. Default: 30s
	LongOperationTimeout time.Duration `yaml:"longOperationTimeout" env:"DEX_POSTGRES_LONG_TIMEOUT"`

	// Per-store override blocks. Database is REQUIRED (defaulted by
	// DefaultPostgresPersistenceConfig); URI / pool / timeout fields inherit
	// from the parent when empty / zero. DLQ co-locates with Runs (see
	// overrideFor) so it has no block. No env tags here — the parent env
	// vars set the default only.
	Shards     PostgresStoreConfig `yaml:"shards"`
	Runs       PostgresStoreConfig `yaml:"runs"`
	Blobs      PostgresStoreConfig `yaml:"blobs"`
	Tasklists  PostgresStoreConfig `yaml:"tasklists"`
	Visibility PostgresStoreConfig `yaml:"visibility"`
	History    PostgresStoreConfig `yaml:"history"`
}

// PostgresStoreConfig is the per-store override block. URI / pool / timeouts
// inherit from the parent PostgresPersistenceConfig when empty / zero.
// Database is the store's database name on the resolved server.
type PostgresStoreConfig struct {
	URI                   string        `yaml:"uri"`
	Database              string        `yaml:"database"`
	MaxConns              int32         `yaml:"maxConns"`
	ShortOperationTimeout time.Duration `yaml:"shortOperationTimeout"`
	LongOperationTimeout  time.Duration `yaml:"longOperationTimeout"`
}

// PostgresConfig is a fully-resolved per-store Postgres connection config
// returned by PostgresPersistenceConfig.For. Pass it to postgres.New<Store>.
type PostgresConfig struct {
	URI                   string
	Database              string
	MaxConns              int32
	ShortOperationTimeout time.Duration
	LongOperationTimeout  time.Duration
}

// For returns the resolved PostgresConfig for a store. The per-store Database
// is taken as-is (always non-empty in DefaultPostgresPersistenceConfig); URI,
// pool size, and timeouts fall back to the parent defaults when the per-store
// override is empty / zero.
func (pcfg PostgresPersistenceConfig) For(store string) PostgresConfig {
	override := pcfg.overrideFor(store)
	cfg := PostgresConfig{
		URI:                   pcfg.URI,
		Database:              override.Database,
		MaxConns:              pcfg.MaxConns,
		ShortOperationTimeout: pcfg.ShortOperationTimeout,
		LongOperationTimeout:  pcfg.LongOperationTimeout,
	}
	if override.URI != "" {
		cfg.URI = override.URI
	}
	if override.MaxConns != 0 {
		cfg.MaxConns = override.MaxConns
	}
	if override.ShortOperationTimeout != 0 {
		cfg.ShortOperationTimeout = override.ShortOperationTimeout
	}
	if override.LongOperationTimeout != 0 {
		cfg.LongOperationTimeout = override.LongOperationTimeout
	}
	return cfg
}

func (pcfg PostgresPersistenceConfig) overrideFor(store string) PostgresStoreConfig {
	switch store {
	case StoreShards:
		return pcfg.Shards
	case StoreRuns, StoreDLQ: // DLQ co-located with runs database
		return pcfg.Runs
	case StoreBlobs:
		return pcfg.Blobs
	case StoreTasklists:
		return pcfg.Tasklists
	case StoreVisibility:
		return pcfg.Visibility
	case StoreHistory:
		return pcfg.History
	default:
		return PostgresStoreConfig{}
	}
}

// DefaultPostgresPersistenceConfig returns the default Postgres configuration:
// the local single-server URI shared by every store, a distinct per-store
// database for each of the six logical stores, and the default pool/timeouts.
func DefaultPostgresPersistenceConfig() PostgresPersistenceConfig {
	return PostgresPersistenceConfig{
		URI:                   "postgres://dex:dex@localhost:5432/?sslmode=disable",
		MaxConns:              10,
		ShortOperationTimeout: 5 * time.Second,
		LongOperationTimeout:  30 * time.Second,
		Shards:                PostgresStoreConfig{Database: "dex_shards"},
		Runs:                  PostgresStoreConfig{Database: "dex_runs"},
		Blobs:                 PostgresStoreConfig{Database: "dex_blobs"},
		Tasklists:             PostgresStoreConfig{Database: "dex_tasklists"},
		Visibility:            PostgresStoreConfig{Database: "dex_visibility"},
		History:               PostgresStoreConfig{Database: "dex_history"},
	}
}

// DefaultPersistenceConfig returns a PersistenceConfig with sensible defaults.
// Postgres is the default backend; Mongo config is still populated so it can
// be selected via Backend: "mongo".
func DefaultPersistenceConfig() PersistenceConfig {
	return PersistenceConfig{
		Backend:  BackendPostgres,
		Postgres: DefaultPostgresPersistenceConfig(),
		Mongo:    DefaultMongoPersistenceConfig(),
	}
}

// DefaultMongoPersistenceConfig returns the default Mongo configuration: the
// local single-node URI shared by every store, a distinct per-store database
// for each of the six logical clusters, and the default operation timeouts.
// Production deployments override per-store URIs to spread the stores across
// dedicated Mongo clusters.
func DefaultMongoPersistenceConfig() MongoPersistenceConfig {
	return MongoPersistenceConfig{
		URI:                   "mongodb://localhost:27018",
		ShortOperationTimeout: 5 * time.Second,
		LongOperationTimeout:  30 * time.Second,
		Shards:                MongoStoreConfig{Database: "dex_shards"},
		Runs:                  MongoStoreConfig{Database: "dex_runs"},
		Blobs:                 MongoStoreConfig{Database: "dex_blobs"},
		Tasklists:             MongoStoreConfig{Database: "dex_tasklists"},
		Visibility:            MongoStoreConfig{Database: "dex_visibility"},
		History:               MongoStoreConfig{Database: "dex_history"},
	}
}
