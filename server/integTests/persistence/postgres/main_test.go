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

package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/server/config"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/persistence/postgres"
)

var (
	startErr   error
	dbSuffix   string
	shardStore p.ShardStore
)

func TestMain(m *testing.M) {
	startErr = setup()
	code := m.Run()
	teardown()
	os.Exit(code)
}

func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	dbSuffix = suffix

	if err := runSetupSh(ctx, suffix); err != nil {
		return fmt.Errorf("setup.sh: %w", err)
	}

	pg := config.DefaultPostgresPersistenceConfig()
	pg.URI = postgresURI()
	pg.Shards.Database = "dex_shards_" + suffix
	pg.Runs.Database = "dex_runs_" + suffix
	pg.Blobs.Database = "dex_blobs_" + suffix
	pg.TaskQueues = config.PostgresStoreConfig{Database: "dex_taskqueues_" + suffix}

	cfg := pg.ResolvedShardsStoreConfig()
	store, catErr := postgres.NewShardStore(ctx, &cfg)
	if catErr != nil {
		return fmt.Errorf("NewShardStore: %w", catErr)
	}
	shardStore = store
	return nil
}

func teardown() {
	if shardStore != nil {
		_ = shardStore.Close() // test teardown; close errors are not actionable
	}
	if dbSuffix == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := openPool(ctx, postgresURI(), "postgres")
	if err != nil {
		return
	}
	defer pool.Close()
	for _, db := range []string{
		"dex_shards_" + dbSuffix,
		"dex_runs_" + dbSuffix,
		"dex_blobs_" + dbSuffix,
		"dex_taskqueues_" + dbSuffix,
	} {
		// FORCE drops connections so package teardown cannot race a leftover pool.
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", db))
	}
}

func runSetupSh(ctx context.Context, suffix string) error {
	// --no-deps: schema only, does not start Postgres (needs make postgres-up).
	// Uses compose init image so the host needs no local psql.
	root := serverRoot()
	cmd := exec.CommandContext(ctx,
		"docker", "compose", "-f", "dependency-postgres.yaml",
		"run", "--rm", "--no-deps",
		"-e", "PGHOST=postgres",
		"-e", "PGPORT=5432",
		"-e", "PGUSER="+envOr("DEX_TEST_POSTGRES_USER", "dex"),
		"-e", "PGPASSWORD="+envOr("DEX_TEST_POSTGRES_PASSWORD", "dex"),
		"--entrypoint", "bash",
		"postgres-init",
		"/schema/setup.sh", suffix,
	)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func randomSuffix() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("random suffix: %w", err)
	}
	// Package-scoped prefix keeps names readable in psql \l.
	return "integ_pg_" + hex.EncodeToString(buf[:]), nil
}

func serverRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	// .../server/integTests/persistence/postgres/main_test.go → .../server
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

func postgresURI() string {
	host := envOr("DEX_TEST_POSTGRES_HOST", "127.0.0.1")
	port := envOr("DEX_TEST_POSTGRES_PORT", "5432")
	user := envOr("DEX_TEST_POSTGRES_USER", "dex")
	pass := envOr("DEX_TEST_POSTGRES_PASSWORD", "dex")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/?sslmode=disable", user, pass, host, port)
}

func openPool(ctx context.Context, uri, database string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Database = database
	return pgxpool.NewWithConfig(ctx, cfg)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
