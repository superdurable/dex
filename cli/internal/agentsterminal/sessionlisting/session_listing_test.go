// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionlisting

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/superdurable/dex/sdk-go/dex"
)

type recordingSearcher struct {
	queries    []string
	pageTokens []string
	page       dex.SearchFlowsPage
	searchErr  error
	info       dex.FlowInfo
	infoErr    error
}

func (searcher *recordingSearcher) SearchFlows(_ context.Context, query string, pageSize int32, nextPageToken string) (dex.SearchFlowsPage, error) {
	if pageSize != PageSize {
		return dex.SearchFlowsPage{}, errors.New("unexpected page size")
	}
	searcher.queries = append(searcher.queries, query)
	searcher.pageTokens = append(searcher.pageTokens, nextPageToken)
	return searcher.page, searcher.searchErr
}

func (searcher *recordingSearcher) DescribeFlow(context.Context, string) (dex.FlowInfo, error) {
	return searcher.info, searcher.infoErr
}

// Queries name the Flow type; a title search quotes the user text.
func TestListSessionsQueries(t *testing.T) {
	searcher := &recordingSearcher{page: dex.SearchFlowsPage{NextPageToken: "next"}}
	lister := NewSessionLister(searcher, `Flow"Type`)
	page, err := lister.ListSessions(context.Background(), "", "")
	require.NoError(t, err)
	require.Equal(t, "next", page.NextPageToken)
	_, err = lister.ListSessions(context.Background(), "token", `fix "flaky" \ test`)
	require.NoError(t, err)
	_, err = lister.ListSessions(context.Background(), "", "   ")
	require.NoError(t, err)
	require.Equal(t, []string{
		`FlowType = "Flow\"Type"`,
		`FlowType = "Flow\"Type" AND text1 = "fix \"flaky\" \\ test"`,
		`FlowType = "Flow\"Type"`,
	}, searcher.queries)
	require.Equal(t, []string{"", "token", ""}, searcher.pageTokens)
}

// A namespace without the title index turns a title search into
// ErrTitleSearchUnavailable; other errors stay errors.
func TestListSessionsTitleSearchUnavailable(t *testing.T) {
	unknownField := &dex.ServiceError{Op: "SearchFlows", Code: codes.InvalidArgument, Detail: `invalid query: unknown visibility field "text1"`}
	searcher := &recordingSearcher{searchErr: unknownField}
	lister := NewSessionLister(searcher, "AgentSession")
	_, err := lister.ListSessions(context.Background(), "", "flaky")
	require.ErrorIs(t, err, ErrTitleSearchUnavailable)
	_, err = lister.ListSessions(context.Background(), "", "")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrTitleSearchUnavailable, "only a title search maps the error")
	searcher.searchErr = &dex.ServiceError{Op: "SearchFlows", Code: codes.Unavailable, Detail: "connection refused"}
	_, err = lister.ListSessions(context.Background(), "", "flaky")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrTitleSearchUnavailable)
}

// fakeValue decodes like a dex.Value holding one JSON payload.
type fakeValue struct {
	payload any
}

func (value fakeValue) Decode(valuePtr any) error {
	encoded, err := json.Marshal(value.payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, valuePtr)
}

// Missing or mistyped summary values are nil.
func TestSummaryOfAttributes(t *testing.T) {
	base := SessionSummary{SessionID: "openai-sdk-20261111-142233-a1b2c3", Status: dex.FlowRunning}
	full := summaryOfAttributes(base, map[string]attributeValue{
		"text1":     fakeValue{"Fix the flaky retry test"},
		"datetime1": fakeValue{"2026-11-02T09:30:00.000Z"},
		"int1":      fakeValue{4},
	})
	require.True(t, full.HasSummary())
	require.Equal(t, "Fix the flaky retry test", *full.Title)
	require.Equal(t, time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC), full.LastActivityAt.UTC())
	require.Equal(t, int64(4), *full.TurnCount)

	empty := summaryOfAttributes(base, map[string]attributeValue{})
	require.False(t, empty.HasSummary())

	mistyped := summaryOfAttributes(base, map[string]attributeValue{
		"text1":     fakeValue{12},
		"datetime1": fakeValue{"yesterday"},
		"int1":      fakeValue{"four"},
	})
	require.False(t, mistyped.HasSummary())
}

// CheckResume allows only a running session of the lister's Flow type.
func TestCheckResume(t *testing.T) {
	searcher := &recordingSearcher{info: dex.FlowInfo{FlowType: "AgentSession", Status: dex.FlowRunning}}
	lister := NewSessionLister(searcher, "AgentSession")
	check, _, err := lister.CheckResume(context.Background(), "session")
	require.NoError(t, err)
	require.Equal(t, ResumeAllowed, check)

	searcher.info.Status = dex.FlowCompleted
	check, status, err := lister.CheckResume(context.Background(), "session")
	require.NoError(t, err)
	require.Equal(t, ResumeNotRunning, check)
	require.Equal(t, dex.FlowCompleted, status)

	searcher.info = dex.FlowInfo{FlowType: "OtherFlow", Status: dex.FlowRunning}
	check, _, err = lister.CheckResume(context.Background(), "session")
	require.NoError(t, err)
	require.Equal(t, ResumeOtherFlowType, check)

	searcher.infoErr = errors.New("not found")
	_, _, err = lister.CheckResume(context.Background(), "session")
	require.Error(t, err)
}
