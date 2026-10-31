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
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// SearchConfig selects the isolated S7 search dataset and measurement settings.
type SearchConfig struct {
	// ShouldMeasureProjectionOnly omits queries. The command default is false.
	ShouldMeasureProjectionOnly bool `json:"projection_only"`
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
	// ShouldMeasureBackfill defers Keyword registration until after seeding. Default: false; requires postgres and 10 Attributes.
	ShouldMeasureBackfill bool `json:"backfill"`
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
	WarmupErrors map[string]int `json:"warmup_errors,omitempty"`
	// TransitionErrors records failures whose state probes straddle backfill completion.
	TransitionErrors map[string]int `json:"transition_errors,omitempty"`
	Name             string         `json:"name"`
	Query            string         `json:"query"`
	Phase            string         `json:"phase"`
	Samples          int            `json:"samples"`
	Latency          Percentiles    `json:"latency"`
	Errors           map[string]int `json:"errors,omitempty"`
	FirstPageRows    int            `json:"first_page_rows"`
	LastPageRows     int            `json:"last_page_rows"`
	RawMilliseconds  []float64      `json:"raw_ms"`
}

type searchBenchmark struct {
	config         SearchConfig
	connection     *benchmarkConnection
	cache          *blobcache.Cache
	cacheDirectory string
	prefix         string
	result         SearchResult
}

type searchQuery struct{ name, query string }

type searchQuerySource interface {
	searchPage(context.Context, string, string) (dex.SearchFlowsPage, error)
	slotState(context.Context) (string, error)
}

type searchBackfillCompletion struct {
	elapsed time.Duration
	err     error
}

// RunSearch seeds an empty server, measures search, and returns partial results on failure. Database containers must be exclusive.
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
	if config.ShouldMeasureBackfill && (config.Backend != "postgres" || config.Attributes != 10) {
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
	if err := writeSearchProgress("seed ready: %d rows, %.1f rows/s, %.1f WAL bytes/row\n", benchmark.config.Runs, benchmark.result.RowsPerSecond, benchmark.result.WALBytesPerRow); err != nil {
		return err
	}
	if benchmark.config.ShouldMeasureProjectionOnly {
		return nil
	}
	if benchmark.config.ShouldMeasureBackfill {
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
			if err := writeSearchProgress("seeded %d/%d\n", index, benchmark.config.Runs); err != nil {
				failures <- err
				cancel()
				return
			}
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

func (benchmark *searchBenchmark) measureBackfill(ctx context.Context) error {
	started := time.Now()
	if err := benchmark.registerIndexes(ctx, true); err != nil {
		return err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := benchmark.slotState(ctx)
		if err != nil {
			return err
		}
		if state == "backfilling" {
			break
		}
		if state == "ready" {
			return fmt.Errorf("backfill finished before a backfilling state could be observed")
		}
		if time.Since(started) > time.Minute {
			return fmt.Errorf("Attribute backfill did not start in one minute")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	readyResult := make(chan searchBackfillCompletion, 1)
	monitorContext, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	go benchmark.monitorBackfill(monitorContext, started, readyResult)
	if err := benchmark.measureBackfillQueries(ctx); err != nil {
		return err
	}
	completion := <-readyResult
	if completion.err != nil {
		return completion.err
	}
	benchmark.result.BackfillSeconds = completion.elapsed.Seconds()
	result, err := benchmark.measureQuery(ctx, "keyword-common-equality", "SearchField00 = 'common'", "", "ready", false)
	benchmark.result.Queries = append(benchmark.result.Queries, result)
	return err
}

func (benchmark *searchBenchmark) monitorBackfill(ctx context.Context, started time.Time, completion chan<- searchBackfillCompletion) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := benchmark.slotState(ctx)
		if err != nil {
			completion <- searchBackfillCompletion{err: err}
			return
		}
		if state == "ready" {
			completion <- searchBackfillCompletion{elapsed: time.Since(started)}
			return
		}
		if time.Since(started) > 30*time.Minute {
			completion <- searchBackfillCompletion{err: fmt.Errorf("backfill did not become ready in 30 minutes")}
			return
		}
		select {
		case <-ctx.Done():
			completion <- searchBackfillCompletion{err: ctx.Err()}
			return
		case <-ticker.C:
		}
	}
}

func (benchmark *searchBenchmark) measureBackfillQueries(ctx context.Context) error {
	return benchmark.doMeasureBackfillQueries(ctx, benchmark)
}

func (benchmark *searchBenchmark) doMeasureBackfillQueries(ctx context.Context, source searchQuerySource) error {
	queries := []searchQuery{{"keyword-equality", "SearchField00 = 'rare'"}, {"keyword-common-equality", "SearchField00 = 'common'"}}
	results := make([]SearchQueryResult, len(queries))
	latencies := make([][]time.Duration, len(queries))
	for index, query := range queries {
		results[index] = SearchQueryResult{Name: query.name, Query: query.query, Phase: "backfilling", Errors: map[string]int{}, TransitionErrors: map[string]int{}}
	}
	for sample := 0; sample < benchmark.config.Samples; sample++ {
		hasBackfillEnded := false
		for index, query := range queries {
			measured, hasQueryBackfillEnded, err := measureSearchQuerySamples(ctx, source, query.name, query.query, "", "backfilling", true, 1, 0)
			if err != nil {
				return err
			}
			result := &results[index]
			for failure, count := range measured.Errors {
				result.Errors[failure] += count
			}
			for failure, count := range measured.TransitionErrors {
				result.TransitionErrors[failure] += count
			}
			if hasQueryBackfillEnded {
				hasBackfillEnded = true
				break
			}
			if measured.Samples == 0 {
				continue
			}
			if result.Samples == 0 {
				result.FirstPageRows = measured.FirstPageRows
			}
			result.LastPageRows = measured.LastPageRows
			result.Samples += measured.Samples
			result.RawMilliseconds = append(result.RawMilliseconds, measured.RawMilliseconds...)
			latencies[index] = append(latencies[index], time.Duration(measured.RawMilliseconds[0]*float64(time.Millisecond)))
		}
		if hasBackfillEnded {
			break
		}
	}
	for index := range results {
		results[index].Latency = calculatePercentiles(latencies[index])
		benchmark.result.Queries = append(benchmark.result.Queries, results[index])
		if err := writeSearchProgress("backfilling %s: n=%d p50=%.3fms p99=%.3fms\n", results[index].Name, results[index].Samples, results[index].Latency.P50, results[index].Latency.P99); err != nil {
			return err
		}
	}
	return nil
}

func (benchmark *searchBenchmark) measureQueries(ctx context.Context, phase string) error {
	queries := []searchQuery{{"default-page-1", ""}, {"common-flow-type", "FlowType = 'searchCommonFlow'"}, {"rare-flow-type", "FlowType = 'searchRareFlow'"}, {"start-time-desc", "ORDER BY StartTime DESC"}}
	if benchmark.config.Attributes >= 3 {
		queries = append(queries, searchQuery{"keyword-equality", "SearchField00 = 'rare'"}, searchQuery{"int-range", "SearchField01 BETWEEN 50000 AND 50099"}, searchQuery{"keyword-array-contains", "SearchField02 = 'tag42'"})
	}
	if benchmark.config.Attributes == 10 {
		queries = append(queries, searchQuery{"text-contains", "SearchField03 = 'topic42'"})
	}
	for _, query := range queries {
		result, err := benchmark.measureQuery(ctx, query.name, query.query, "", phase, false)
		benchmark.result.Queries = append(benchmark.result.Queries, result)
		if err != nil {
			return err
		}
	}
	token := ""
	for page := 1; page < benchmark.config.DeepPage; page++ {
		response, err := benchmark.searchPage(ctx, "", token)
		if err != nil {
			return fmt.Errorf("reach deep page %d: %w", page+1, err)
		}
		if response.NextPageToken == "" {
			return fmt.Errorf("only %d pages exist; cannot reach page %d", page, benchmark.config.DeepPage)
		}
		token = response.NextPageToken
	}
	result, err := benchmark.measureQuery(ctx, fmt.Sprintf("default-page-%d", benchmark.config.DeepPage), "", token, phase, false)
	benchmark.result.Queries = append(benchmark.result.Queries, result)
	return err
}

func (benchmark *searchBenchmark) measureQuery(ctx context.Context, name, query, token, phase string, shouldCheckBackfill bool) (SearchQueryResult, error) {
	return benchmark.measureQuerySamples(ctx, name, query, token, phase, shouldCheckBackfill, benchmark.config.Samples, benchmark.config.Warmup)
}

func (benchmark *searchBenchmark) measureQuerySamples(ctx context.Context, name, query, token, phase string, shouldCheckBackfill bool, samples, warmup int) (SearchQueryResult, error) {
	result, _, err := measureSearchQuerySamples(ctx, benchmark, name, query, token, phase, shouldCheckBackfill, samples, warmup)
	return result, err
}

func measureSearchQuerySamples(ctx context.Context, source searchQuerySource, name, query, token, phase string, shouldCheckBackfill bool, samples, warmup int) (SearchQueryResult, bool, error) {
	result := SearchQueryResult{Name: name, Query: query, Phase: phase, Errors: map[string]int{}, WarmupErrors: map[string]int{}, TransitionErrors: map[string]int{}}
	hasBackfillEnded := false
	latencies := make([]time.Duration, 0, samples)
	for index := -warmup; index < samples; index++ {
		if shouldCheckBackfill {
			state, err := source.slotState(ctx)
			if err != nil {
				return result, hasBackfillEnded, err
			}
			if state != "backfilling" {
				hasBackfillEnded = true
				break
			}
		}
		started := time.Now()
		page, err := source.searchPage(ctx, query, token)
		elapsed := time.Since(started)
		if shouldCheckBackfill {
			state, stateErr := source.slotState(ctx)
			if stateErr != nil {
				return result, hasBackfillEnded, errors.Join(err, stateErr)
			}
			if state != "backfilling" {
				if err != nil {
					result.TransitionErrors[err.Error()]++
				}
				hasBackfillEnded = true
				break
			}
		}
		if index < 0 {
			if err != nil {
				result.WarmupErrors[err.Error()]++
			}
			continue
		}
		if err != nil {
			result.Errors[err.Error()]++
			continue
		}

		if result.Samples == 0 {
			result.FirstPageRows = len(page.Flows)
		}
		result.LastPageRows = len(page.Flows)
		result.Samples++
		latencies = append(latencies, elapsed)
		result.RawMilliseconds = append(result.RawMilliseconds, float64(elapsed)/float64(time.Millisecond))
	}
	result.Latency = calculatePercentiles(latencies)
	if samples > 1 {
		if err := writeSearchProgress("%s %s: n=%d p50=%.3fms p99=%.3fms errors=%v\n", phase, name, result.Samples, result.Latency.P50, result.Latency.P99, result.Errors); err != nil {
			return result, hasBackfillEnded, err
		}
	}
	return result, hasBackfillEnded, ctx.Err()
}

func (benchmark *searchBenchmark) searchPage(ctx context.Context, query, token string) (dex.SearchFlowsPage, error) {
	callContext, cancel := context.WithTimeout(ctx, benchmark.config.Timeout)
	defer cancel()
	return benchmark.connection.client.SearchFlows(callContext, query, 20, token)
}

func (benchmark *searchBenchmark) slotState(ctx context.Context) (string, error) {
	return benchmark.databaseQuery(ctx, "SELECT state FROM dexzd_visibility_attribute_slots WHERE index_key = 'SearchField00'")
}

func (benchmark *searchBenchmark) registerIndexes(ctx context.Context, shouldIncludeDeferred bool) error {
	definitions := searchIndexDefinitions(benchmark.config.Attributes)
	if benchmark.config.ShouldMeasureBackfill && !shouldIncludeDeferred {
		delete(definitions, "SearchField00")
	}
	_, err := benchmark.connection.service.SyncAttributeIndexes(ctx, &dexpb.SyncAttributeIndexRequest{AttributeIndexes: definitions})
	return err
}

func (benchmark *searchBenchmark) waitForInitialSlots(ctx context.Context) error {
	if benchmark.config.Backend != "postgres" {
		return nil
	}
	expected := benchmark.config.Attributes
	if benchmark.config.ShouldMeasureBackfill {
		expected--
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(time.Minute)
	for {
		count, err := benchmark.databaseNumber(ctx, "SELECT count(*) FROM dexzd_visibility_attribute_slots WHERE state = 'ready' AND index_key LIKE 'SearchField%'")
		if err != nil {
			return err
		}
		if int(count) == expected {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("initial slots not ready: %v/%d", count, expected)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (benchmark *searchBenchmark) waitForProjection(ctx context.Context) error {
	if benchmark.config.Backend == "builtin" {
		// Builtin search reads the committed local view; seed waits for every closing Step.
		_, err := benchmark.connection.client.SearchFlows(ctx, "", 20, "")
		return err
	}
	deadline := time.Now().Add(15 * time.Minute)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	runningCount := (benchmark.config.Runs + 19) / 20
	for {
		counts, err := benchmark.databaseQuery(ctx, "SELECT count(*), count(*) FILTER (WHERE execution_status = 'Completed'), count(*) FILTER (WHERE execution_status = 'Running') FROM dexzd_visibility_runs")
		if err != nil {
			return err
		}
		expected := fmt.Sprintf("%d|%d|%d", benchmark.config.Runs, benchmark.config.Runs-runningCount, runningCount)
		if counts == expected {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("projection did not converge: got %s, expected %s", counts, expected)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (benchmark *searchBenchmark) databaseNumber(ctx context.Context, query string) (float64, error) {
	value, err := benchmark.databaseQuery(ctx, query)
	if err != nil {
		return 0, err
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse database number %q: %w", value, err)
	}
	return number, nil
}

func (benchmark *searchBenchmark) databaseQuery(ctx context.Context, query string) (string, error) {
	callContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(callContext, "docker", "exec", benchmark.config.DatabaseContainer, "psql", "-U", "postgres", "-d", "dexzd", "-XAt", "-v", "ON_ERROR_STOP=1", "-c", query)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("database probe: %w: %s", err, output)
	}
	return strings.TrimSpace(string(output)), nil
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

func writeSearchProgress(format string, arguments ...any) error {
	_, err := fmt.Fprintf(os.Stderr, format, arguments...)
	if err != nil {
		return fmt.Errorf("write search benchmark progress: %w", err)
	}
	return nil
}
