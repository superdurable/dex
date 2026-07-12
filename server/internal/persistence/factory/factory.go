// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

// Package factory is the single bootstrap choke point that constructs the
// persistence stores for the configured backend (postgres by default, or
// mongo). It lives in its own package — not in persistence — because it
// imports the backend packages (mongo, postgres) which themselves import
// persistence, and a self-import would cycle.
package factory

import (
	"context"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/config"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/persistence/mongo"
	"github.com/superdurable/dex/server/internal/persistence/postgres"
)

// StoreSet bundles the six logical stores built for one backend. DLQ is built
// separately (run-service only) via BuildDLQStore.
type StoreSet struct {
	Run        p.RunStore
	Blob       p.BlobStore
	Shard      p.ShardStore
	Tasklist   p.TasklistStore
	Visibility p.VisibilityStore
	History    p.HistoryStore
}

// Close closes every non-nil store. Used to roll back a partial build.
func (s *StoreSet) Close() {
	for _, c := range []interface{ Close() error }{s.Run, s.Blob, s.Shard, s.Tasklist, s.Visibility, s.History} {
		if c != nil {
			_ = c.Close()
		}
	}
}

func resolveBackend(cfg config.PersistenceConfig) string {
	if cfg.Backend == "" {
		return config.BackendPostgres
	}
	return cfg.Backend
}

// BuildStoreSet constructs all six stores for the configured backend.
func BuildStoreSet(ctx context.Context, cfg config.PersistenceConfig) (*StoreSet, errors.CategorizedError) {
	switch resolveBackend(cfg) {
	case config.BackendMongo:
		return buildMongoStoreSet(ctx, cfg.Mongo)
	case config.BackendPostgres:
		return buildPostgresStoreSet(ctx, cfg.Postgres)
	default:
		return nil, errors.NewInvalidInputError("unknown persistence backend: "+cfg.Backend, nil)
	}
}

// BuildDLQStore constructs the DLQ store (co-located with runs) for the backend.
func BuildDLQStore(ctx context.Context, cfg config.PersistenceConfig) (p.DLQStore, errors.CategorizedError) {
	switch resolveBackend(cfg) {
	case config.BackendMongo:
		r := cfg.Mongo.For(config.StoreDLQ)
		return mongo.NewDLQStoreWithDatabase(ctx, r.URI, r.Database, mongoTimeouts(r))
	case config.BackendPostgres:
		return postgres.NewDLQStore(ctx, pgPoolConfig(cfg.Postgres.For(config.StoreDLQ)))
	default:
		return nil, errors.NewInvalidInputError("unknown persistence backend: "+cfg.Backend, nil)
	}
}

// --- Mongo ---

func mongoTimeouts(r config.MongoConfig) mongo.OperationTimeouts {
	return mongo.OperationTimeouts{Short: r.ShortOperationTimeout, Long: r.LongOperationTimeout}
}

func buildMongoStoreSet(ctx context.Context, cfg config.MongoPersistenceConfig) (*StoreSet, errors.CategorizedError) {
	set := &StoreSet{}
	var err errors.CategorizedError

	r := cfg.For(config.StoreRuns)
	if set.Run, err = mongo.NewRunStoreWithDatabase(ctx, r.URI, r.Database, mongoTimeouts(r)); err != nil {
		return nil, err
	}
	r = cfg.For(config.StoreBlobs)
	if set.Blob, err = mongo.NewBlobStoreWithDatabase(ctx, r.URI, r.Database, mongoTimeouts(r)); err != nil {
		set.Close()
		return nil, err
	}
	r = cfg.For(config.StoreShards)
	if set.Shard, err = mongo.NewShardStoreWithDatabase(ctx, r.URI, r.Database, mongoTimeouts(r)); err != nil {
		set.Close()
		return nil, err
	}
	r = cfg.For(config.StoreTasklists)
	if set.Tasklist, err = mongo.NewTasklistStoreWithDatabase(ctx, r.URI, r.Database, mongoTimeouts(r)); err != nil {
		set.Close()
		return nil, err
	}
	r = cfg.For(config.StoreVisibility)
	if set.Visibility, err = mongo.NewVisibilityStoreWithDatabase(ctx, r.URI, r.Database, mongoTimeouts(r)); err != nil {
		set.Close()
		return nil, err
	}
	r = cfg.For(config.StoreHistory)
	if set.History, err = mongo.NewHistoryStoreWithDatabase(ctx, r.URI, r.Database, mongoTimeouts(r)); err != nil {
		set.Close()
		return nil, err
	}
	return set, nil
}

// --- Postgres ---

func pgPoolConfig(r config.PostgresConfig) postgres.PoolConfig {
	return postgres.PoolConfig{
		URI:      r.URI,
		Database: r.Database,
		MaxConns: r.MaxConns,
		Timeouts: postgres.OperationTimeouts{Short: r.ShortOperationTimeout, Long: r.LongOperationTimeout},
	}
}

func buildPostgresStoreSet(ctx context.Context, cfg config.PostgresPersistenceConfig) (*StoreSet, errors.CategorizedError) {
	set := &StoreSet{}
	var err errors.CategorizedError

	if set.Run, err = postgres.NewRunStore(ctx, pgPoolConfig(cfg.For(config.StoreRuns))); err != nil {
		return nil, err
	}
	if set.Blob, err = postgres.NewBlobStore(ctx, pgPoolConfig(cfg.For(config.StoreBlobs))); err != nil {
		set.Close()
		return nil, err
	}
	if set.Shard, err = postgres.NewShardStore(ctx, pgPoolConfig(cfg.For(config.StoreShards))); err != nil {
		set.Close()
		return nil, err
	}
	if set.Tasklist, err = postgres.NewTasklistStore(ctx, pgPoolConfig(cfg.For(config.StoreTasklists))); err != nil {
		set.Close()
		return nil, err
	}
	if set.Visibility, err = postgres.NewVisibilityStore(ctx, pgPoolConfig(cfg.For(config.StoreVisibility))); err != nil {
		set.Close()
		return nil, err
	}
	if set.History, err = postgres.NewHistoryStore(ctx, pgPoolConfig(cfg.For(config.StoreHistory))); err != nil {
		set.Close()
		return nil, err
	}
	return set, nil
}
