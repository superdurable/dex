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
	"fmt"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type searchQuery struct{ name, query string }

func (benchmark *searchBenchmark) registerIndexes(ctx context.Context, shouldIncludeDeferred bool) error {
	definitions := searchIndexDefinitions(benchmark.config.Attributes)
	if benchmark.config.Backfill && !shouldIncludeDeferred {
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
	if benchmark.config.Backfill {
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
	result := SearchQueryResult{Name: name, Query: query, Phase: phase, Errors: map[string]int{}}
	latencies := make([]time.Duration, 0, benchmark.config.Samples)
	for index := -benchmark.config.Warmup; index < benchmark.config.Samples; index++ {
		if shouldCheckBackfill {
			state, err := benchmark.slotState(ctx)
			if err != nil {
				return result, err
			}
			if state != "backfilling" {
				break
			}
		}
		started := time.Now()
		page, err := benchmark.searchPage(ctx, query, token)
		elapsed := time.Since(started)
		if index < 0 {
			continue
		}
		if err != nil {
			result.Errors[err.Error()]++
			continue
		}
		if shouldCheckBackfill {
			state, err := benchmark.slotState(ctx)
			if err != nil {
				return result, err
			}
			if state != "backfilling" {
				break
			}
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
	fmt.Fprintf(os.Stderr, "%s %s: n=%d p50=%.3fms p99=%.3fms errors=%v\n", phase, name, result.Samples, result.Latency.P50, result.Latency.P99, result.Errors)
	return result, ctx.Err()
}

func (benchmark *searchBenchmark) searchPage(ctx context.Context, query, token string) (dex.SearchFlowsPage, error) {
	callContext, cancel := context.WithTimeout(ctx, benchmark.config.Timeout)
	defer cancel()
	return benchmark.connection.client.SearchFlows(callContext, query, 20, token)
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
	for _, query := range []searchQuery{{"keyword-equality", "SearchField00 = 'rare'"}, {"keyword-common-equality", "SearchField00 = 'common'"}} {
		result, err := benchmark.measureQuery(ctx, query.name, query.query, "", "backfilling", true)
		benchmark.result.Queries = append(benchmark.result.Queries, result)
		if err != nil {
			return err
		}
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

type searchBackfillCompletion struct {
	elapsed time.Duration
	err     error
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

func (benchmark *searchBenchmark) slotState(ctx context.Context) (string, error) {
	return benchmark.databaseQuery(ctx, "SELECT state FROM dexzd_visibility_attribute_slots WHERE index_key = 'SearchField00'")
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
