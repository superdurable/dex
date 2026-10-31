// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// SearchConfig selects the isolated S7 search dataset and measurement settings.
type SearchConfig struct {
	// ProjectionOnly omits queries. The command default is false.
	ProjectionOnly bool `json:"projection_only"`
	// Address serves FlowService. The command default is 127.0.0.1:8801.
	Address string `json:"address"`
	// Backend selects builtin, postgres, or paradedb. The command default is builtin.
	Backend string `json:"backend"`
	// Runs sets the fresh tenant's run count. The command default is 100000.
	Runs int `json:"runs"`
	// Attributes selects 0, 3, or 10 indexed values per run. The command default is 10.
	Attributes int `json:"attributes"`
	// Concurrency sets simultaneous seed clients. The command default is 128.
	Concurrency int `json:"concurrency"`
	// Samples caps measured requests per query. The command default is 200.
	Samples int `json:"samples"`
	// Warmup sets untimed requests per ready query. The command default is 10.
	Warmup int `json:"warmup"`
	// DeepPage selects the token-reached page to measure. The command default is 500.
	DeepPage int `json:"deep_page"`
	// Timeout bounds each seed run and query. The command default is 30 seconds.
	Timeout time.Duration `json:"timeout_ns"`
	// DatabaseContainer names the owned database container. Default: empty; required for external backends.
	DatabaseContainer string `json:"database_container,omitempty"`
	// Backfill defers Keyword registration until after seeding. Default: false; requires postgres and 10 Attributes.
	Backfill bool `json:"backfill"`
	// GitCommit records the source revision. Default: empty.
	GitCommit string `json:"git_commit"`
}

// SearchResult records S7 seed, projection, query, and optional backfill measurements.
type SearchResult struct {
	Config          SearchConfig        `json:"config"`
	StartedAt       time.Time           `json:"started_at"`
	Machine         MachineInfo         `json:"machine"`
	SeedSeconds     float64             `json:"seed_seconds"`
	CatchupSeconds  float64             `json:"catchup_seconds"`
	RowsPerSecond   float64             `json:"rows_per_second"`
	WALStart        string              `json:"wal_start,omitempty"`
	WALEnd          string              `json:"wal_end,omitempty"`
	WALBytesPerRow  float64             `json:"wal_bytes_per_row,omitempty"`
	BackfillSeconds float64             `json:"backfill_seconds,omitempty"`
	Queries         []SearchQueryResult `json:"queries"`
	Error           string              `json:"error,omitempty"`
}

// SearchQueryResult includes successful request percentiles and every failed attempt.
type SearchQueryResult struct {
	WarmupErrors    map[string]int `json:"warmup_errors,omitempty"`
	Name            string         `json:"name"`
	Query           string         `json:"query"`
	Phase           string         `json:"phase"`
	Samples         int            `json:"samples"`
	Latency         Percentiles    `json:"latency"`
	Errors          map[string]int `json:"errors,omitempty"`
	FirstPageRows   int            `json:"first_page_rows"`
	LastPageRows    int            `json:"last_page_rows"`
	RawMilliseconds []float64      `json:"raw_ms"`
}

type searchBenchmark struct {
	config         SearchConfig
	connection     *benchmarkConnection
	cache          *blobcache.Cache
	cacheDirectory string
	prefix         string
	result         SearchResult
}

// RunSearch seeds a fresh server and measures search, projection, and backfill; partial results accompany errors.
func RunSearch(ctx context.Context, config SearchConfig) (SearchResult, error) {
	benchmark := &searchBenchmark{config: config, prefix: fmt.Sprintf("search-%d", time.Now().UnixNano())}
	benchmark.result = SearchResult{Config: config, StartedAt: time.Now().UTC(), Machine: machineInfo()}
	if err := benchmark.validate(); err != nil {
		return benchmark.result, err
	}
	if err := benchmark.open(); err != nil {
		return benchmark.result, errors.Join(err, benchmark.close())
	}
	err := benchmark.run(ctx)
	err = errors.Join(err, benchmark.close())
	if err != nil {
		benchmark.result.Error = err.Error()
	}
	return benchmark.result, err
}

func (benchmark *searchBenchmark) validate() error {
	config := benchmark.config
	if config.Runs < 1 || config.Concurrency < 1 || config.Samples < 1 || config.Warmup < 0 || config.DeepPage < 1 || config.Timeout <= 0 {
		return fmt.Errorf("runs, concurrency, samples, deep page, and timeout must be positive; warmup cannot be negative")
	}
	if config.Attributes != 0 && config.Attributes != 3 && config.Attributes != 10 {
		return fmt.Errorf("attributes must be 0, 3, or 10")
	}
	if config.Backend != "builtin" && config.Backend != "postgres" && config.Backend != "paradedb" {
		return fmt.Errorf("unknown search backend %q", config.Backend)
	}
	if config.Backend != "builtin" && config.DatabaseContainer == "" {
		return fmt.Errorf("external search requires database-container for convergence and WAL measurements")
	}
	if config.Backfill && (config.Backend != "postgres" || config.Attributes != 10) {
		return fmt.Errorf("backfill requires postgres with 10 Attributes")
	}
	return nil
}

func (benchmark *searchBenchmark) open() error {
	registry, err := dex.NewRegistry([]dex.Flow{searchCommonFlow{}, searchSecondaryFlow{}, searchRareFlow{}})
	if err != nil {
		return err
	}
	benchmark.cacheDirectory, err = os.MkdirTemp("", "dexbench-search-cache-")
	if err != nil {
		return err
	}
	benchmark.cache, err = blobcache.New(&blobcache.Config{Dir: benchmark.cacheDirectory, MaxBytes: 64 << 20})
	if err != nil {
		return err
	}
	runner := &Runner{config: Config{Address: benchmark.config.Address}, cache: benchmark.cache}
	benchmark.connection, err = runner.openConnection(registry, min(benchmark.config.Concurrency, 256))
	return err
}

func (benchmark *searchBenchmark) run(ctx context.Context) error {
	if err := benchmark.registerIndexes(ctx, false); err != nil {
		return err
	}
	if benchmark.config.DatabaseContainer != "" {
		if err := benchmark.waitForInitialSlots(ctx); err != nil {
			return err
		}
		start, err := benchmark.databaseQuery(ctx, "SELECT pg_current_wal_lsn()")
		if err != nil {
			return err
		}
		benchmark.result.WALStart = start
	}
	started := time.Now()
	if err := benchmark.seed(ctx); err != nil {
		return err
	}
	benchmark.result.SeedSeconds = time.Since(started).Seconds()
	if err := benchmark.waitForProjection(ctx); err != nil {
		return err
	}
	benchmark.result.CatchupSeconds = time.Since(started).Seconds() - benchmark.result.SeedSeconds
	benchmark.result.RowsPerSecond = float64(benchmark.config.Runs) / time.Since(started).Seconds()
	if benchmark.config.DatabaseContainer != "" {
		end, err := benchmark.databaseQuery(ctx, "SELECT pg_current_wal_lsn()")
		if err != nil {
			return err
		}
		benchmark.result.WALEnd = end
		walBytes, err := benchmark.databaseNumber(ctx, fmt.Sprintf("SELECT pg_wal_lsn_diff('%s', '%s')", end, benchmark.result.WALStart))
		if err != nil {
			return err
		}
		benchmark.result.WALBytesPerRow = walBytes / float64(benchmark.config.Runs)
	}
	fmt.Fprintf(os.Stderr, "seed ready: %d rows, %.1f rows/s, %.1f WAL bytes/row\n", benchmark.config.Runs, benchmark.result.RowsPerSecond, benchmark.result.WALBytesPerRow)
	if benchmark.config.ProjectionOnly {
		return nil
	}
	if benchmark.config.Backfill {
		if err := benchmark.measureBackfill(ctx); err != nil {
			return err
		}
	}
	return benchmark.measureQueries(ctx, "ready")
}

func (benchmark *searchBenchmark) seed(ctx context.Context) error {
	seedContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var waitGroup sync.WaitGroup
	var nextIndex atomic.Int64
	failures := make(chan error, benchmark.config.Concurrency)
	for index := 0; index < benchmark.config.Concurrency; index++ {
		waitGroup.Add(1)
		go benchmark.seedClient(seedContext, cancel, &waitGroup, &nextIndex, failures)
	}
	waitGroup.Wait()
	close(failures)
	var combined error
	for err := range failures {
		combined = errors.Join(combined, err)
	}
	return errors.Join(combined, ctx.Err())
}

func (benchmark *searchBenchmark) seedClient(ctx context.Context, cancel context.CancelFunc, waitGroup *sync.WaitGroup, nextIndex *atomic.Int64, failures chan<- error) {
	defer waitGroup.Done()
	for ctx.Err() == nil {
		index := int(nextIndex.Add(1)) - 1
		if index >= benchmark.config.Runs {
			return
		}
		callContext, stop := context.WithTimeout(ctx, benchmark.config.Timeout)
		err := benchmark.seedRun(callContext, index)
		stop()
		if err != nil {
			failures <- fmt.Errorf("seed run %d: %w", index, err)
			cancel()
			return
		}
		if index > 0 && index%10000 == 0 {
			fmt.Fprintf(os.Stderr, "seeded %d/%d\n", index, benchmark.config.Runs)
		}
	}
}

func (benchmark *searchBenchmark) seedRun(ctx context.Context, index int) error {
	flowType := "searchCommonFlow"
	if index%10 >= 8 {
		flowType = "searchSecondaryFlow"
	}
	if index%2000 == 1999 {
		flowType = "searchRareFlow"
	}
	flowID := fmt.Sprintf("%s-%09d", benchmark.prefix, index)
	shouldRemainRunning := index%20 == 0
	// Raw writes allow registration to follow existing indexed values during the backfill experiment.
	_, err := benchmark.connection.service.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId: flowID, FlowType: flowType, StartStepType: "searchCompletionStep",
		StepOptions: &dexpb.StepOptions{SkipWaitFor: true}, RequestId: flowID, StepInput: &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: shouldRemainRunning}},
		FlowStartOptions: &dexpb.FlowStartOptions{Attributes: searchAttributeWrites(index, benchmark.config.Attributes), FlowConfigOverride: &dexpb.FlowConfig{WorkerTarget: &dexpb.WorkerTarget{IsPullMode: true}}},
	})
	if err != nil {
		return err
	}
	if shouldRemainRunning {
		return nil
	}
	result, err := benchmark.connection.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
	if err != nil {
		return err
	}
	if result.Status != dex.FlowCompleted {
		return fmt.Errorf("Flow %s ended with %v", flowID, result.Status)
	}
	return nil
}

func (benchmark *searchBenchmark) close() error {
	var combined error
	if benchmark.connection != nil {
		combined = errors.Join(combined, benchmark.connection.close())
	}
	if benchmark.cache != nil {
		combined = errors.Join(combined, benchmark.cache.Close())
	}
	if benchmark.cacheDirectory != "" {
		combined = errors.Join(combined, os.RemoveAll(benchmark.cacheDirectory))
	}
	return combined
}

func searchIndexDefinitions(count int) map[string]dexpb.IndexType {
	definitions := make(map[string]dexpb.IndexType)
	for index := 0; index < count; index++ {
		indexType := dexpb.IndexType_INDEX_TYPE_KEYWORD
		if index == 1 {
			indexType = dexpb.IndexType_INDEX_TYPE_INT
		}
		if index == 2 {
			indexType = dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY
		}
		if index == 3 {
			indexType = dexpb.IndexType_INDEX_TYPE_TEXT
		}
		definitions[fmt.Sprintf("SearchField%02d", index)] = indexType
	}
	return definitions
}

func searchAttributeWrites(index, count int) []*dexpb.AttributeWrite {
	definitions := searchIndexDefinitions(count)
	writes := make([]*dexpb.AttributeWrite, 0, count)
	for fieldIndex := 0; fieldIndex < count; fieldIndex++ {
		key := fmt.Sprintf("SearchField%02d", fieldIndex)
		keyword := "common"
		if index%10 >= 8 {
			keyword = "secondary"
		}
		if index%1000 == 999 {
			keyword = "rare"
		}
		value := &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: keyword}}
		switch fieldIndex {
		case 1:
			value = &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: int64(index % 100000)}}
		case 2:
			value = &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(fmt.Sprintf(`["shared","tag%02d"]`, index%100))}}}
		case 3:
			value = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: fmt.Sprintf("search benchmark document topic%02d", index%100)}}
		}
		writes = append(writes, &dexpb.AttributeWrite{Key: key, Value: value, IndexConfig: &dexpb.IndexConfig{Enable: true, Type: definitions[key], IndexKey: key}})
	}
	return writes
}
