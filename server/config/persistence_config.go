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

package config

import "time"

const (
	DBTypePostgres = "postgres"
)

type PersistenceConfig struct {
	Type string `yaml:"type" env:"DEX_DB_TYPE"`

	Postgres PostgresPersistenceConfig `yaml:"postgres"`
}

type PostgresPersistenceConfig struct {
	// URI is the PostgreSQL connection string (pgx/libpq DSN)
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

	Shards PostgresStoreConfig `yaml:"shards"`
	Runs   PostgresStoreConfig `yaml:"runs"`
	Blobs  PostgresStoreConfig `yaml:"blobs"`
	TaskQueues PostgresStoreConfig `yaml:"taskQueues"`
}

// PostgresStoreConfig is the per-store config
// Database is the store's database name on the resolved server.
type PostgresStoreConfig struct {
	Database string `yaml:"database"`
}

type ResolvedPGStoreConfig struct {
	URI                   string
	Database              string
	MaxConns              int32
	ShortOperationTimeout time.Duration
	LongOperationTimeout  time.Duration
}

func (c *PostgresPersistenceConfig) ResolvedShardsStoreConfig() ResolvedPGStoreConfig {
	return ResolvedPGStoreConfig{
		URI:                   c.URI,
		Database:              c.Shards.Database,
		MaxConns:              c.MaxConns,
		ShortOperationTimeout: c.ShortOperationTimeout,
		LongOperationTimeout:  c.LongOperationTimeout,
	}
}

func (c *PostgresPersistenceConfig) ResolvedRunsStoreConfig() ResolvedPGStoreConfig {
	return ResolvedPGStoreConfig{
		URI:                   c.URI,
		Database:              c.Runs.Database,
		MaxConns:              c.MaxConns,
		ShortOperationTimeout: c.ShortOperationTimeout,
		LongOperationTimeout:  c.LongOperationTimeout,
	}
}

func (c *PostgresPersistenceConfig) ResolvedBlobsStoreConfig() ResolvedPGStoreConfig {
	return ResolvedPGStoreConfig{
		URI:                   c.URI,
		Database:              c.Blobs.Database,
		MaxConns:              c.MaxConns,
		ShortOperationTimeout: c.ShortOperationTimeout,
		LongOperationTimeout:  c.LongOperationTimeout,
	}
}

func (c *PostgresPersistenceConfig) ResolvedTaskQueuesStoreConfig() ResolvedPGStoreConfig {
	return ResolvedPGStoreConfig{
		URI:                   c.URI,
		Database:              c.TaskQueues.Database,
		MaxConns:              c.MaxConns,
		ShortOperationTimeout: c.ShortOperationTimeout,
		LongOperationTimeout:  c.LongOperationTimeout,
	}
}

// DefaultPostgresPersistenceConfig returns the default Postgres configuration:
// the local single-server URI shared by every store, a distinct per-store
// database for each logical store, and the default pool/timeouts.
func DefaultPostgresPersistenceConfig() PostgresPersistenceConfig {
	return PostgresPersistenceConfig{
		URI:                   "postgres://dex:dex@localhost:5432/?sslmode=disable",
		MaxConns:              100,
		ShortOperationTimeout: 5 * time.Second,
		LongOperationTimeout:  30 * time.Second,
		Shards:                PostgresStoreConfig{Database: "dex_shards"},
		Runs:                  PostgresStoreConfig{Database: "dex_runs"},
		Blobs:                 PostgresStoreConfig{Database: "dex_blobs"},
	}
}

func DefaultPersistenceConfig() PersistenceConfig {
	return PersistenceConfig{
		Type:     DBTypePostgres,
		Postgres: DefaultPostgresPersistenceConfig(),
	}
}
