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
	"testing"

	"github.com/superdurable/dex/sdk-go/dex"
)

type searchSamplingFixture struct {
	searchCalls  int
	stateCalls   int
	states       []string
	searchErrors map[int]error
	stateErrors  map[int]error
}

func TestBackfillSamplingContinuesAfterBothQueriesFail(t *testing.T) {
	failure := errors.New("search unavailable")
	source := &searchSamplingFixture{searchErrors: map[int]error{0: failure, 1: failure}}
	benchmark := &searchBenchmark{config: SearchConfig{Samples: 3}}
	if err := benchmark.doMeasureBackfillQueries(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if source.searchCalls != 6 || source.stateCalls != 12 {
		t.Fatalf("calls: search=%d state=%d", source.searchCalls, source.stateCalls)
	}
	if len(benchmark.result.Queries) != 2 {
		t.Fatalf("query distributions: %d", len(benchmark.result.Queries))
	}
	for _, result := range benchmark.result.Queries {
		if result.Samples != 2 || result.Errors[failure.Error()] != 1 || len(result.TransitionErrors) != 0 {
			t.Fatalf("sampling stopped after errors or misclassified them: %+v", result)
		}
	}
}

func TestBackfillSamplingClassifiesTransitionErrors(t *testing.T) {
	for _, warmup := range []int{0, 1} {
		t.Run(map[int]string{0: "measured", 1: "warmup"}[warmup], func(t *testing.T) {
			failure := errors.New("search unavailable during transition")
			source := &searchSamplingFixture{states: []string{"backfilling", "ready"}, searchErrors: map[int]error{0: failure}}
			result, hasBackfillEnded, err := measureSearchQuerySamples(context.Background(), source, "keyword-equality", "SearchField00 = 'rare'", "", "backfilling", true, 3, warmup)
			if err != nil {
				t.Fatal(err)
			}
			if !hasBackfillEnded || source.searchCalls != 1 || source.stateCalls != 2 {
				t.Fatalf("transition not observed: ended=%v search=%d state=%d", hasBackfillEnded, source.searchCalls, source.stateCalls)
			}
			if len(result.Errors) != 0 || len(result.WarmupErrors) != 0 || result.TransitionErrors[failure.Error()] != 1 || result.Samples != 0 {
				t.Fatalf("error attributed to the wrong state: %+v", result)
			}
		})
	}
}

func TestBackfillSamplingStopsOnObservedReadyState(t *testing.T) {
	source := &searchSamplingFixture{states: []string{"backfilling", "backfilling", "ready"}}
	benchmark := &searchBenchmark{config: SearchConfig{Samples: 3}}
	if err := benchmark.doMeasureBackfillQueries(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if source.searchCalls != 1 || benchmark.result.Queries[0].Samples != 1 || benchmark.result.Queries[1].Samples != 0 {
		t.Fatalf("sampling continued past ready: calls=%d results=%+v", source.searchCalls, benchmark.result.Queries)
	}
}

func TestBackfillSamplingPreservesQueryAndStateProbeFailures(t *testing.T) {
	searchFailure := errors.New("search failed")
	stateFailure := errors.New("state probe failed")
	source := &searchSamplingFixture{searchErrors: map[int]error{0: searchFailure}, stateErrors: map[int]error{1: stateFailure}}
	_, _, err := measureSearchQuerySamples(context.Background(), source, "keyword-equality", "SearchField00 = 'rare'", "", "backfilling", true, 1, 0)
	if !errors.Is(err, searchFailure) || !errors.Is(err, stateFailure) {
		t.Fatalf("lost failure: %v", err)
	}
}

func (fixture *searchSamplingFixture) searchPage(context.Context, string, string) (dex.SearchFlowsPage, error) {
	call := fixture.searchCalls
	fixture.searchCalls++
	if err := fixture.searchErrors[call]; err != nil {
		return dex.SearchFlowsPage{}, err
	}
	return dex.SearchFlowsPage{Flows: make([]dex.SearchFlowEntry, 20)}, nil
}

func (fixture *searchSamplingFixture) slotState(context.Context) (string, error) {
	call := fixture.stateCalls
	fixture.stateCalls++
	if err := fixture.stateErrors[call]; err != nil {
		return "", err
	}
	if call < len(fixture.states) {
		return fixture.states[call], nil
	}
	return "backfilling", nil
}
