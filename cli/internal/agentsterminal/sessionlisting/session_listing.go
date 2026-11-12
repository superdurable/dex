// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package sessionlisting reads pages of agent sessions with their
// summary Attributes from Dex.
package sessionlisting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/superdurable/dex/sdk-go/dex"
)

// The index keys of the session summary Attributes, as every agents core
// declares them: AgentSessionTitle, AgentSessionLastActivityAt, and
// AgentSessionTurnCount.
const (
	titleIndexKey        = "text1"
	lastActivityIndexKey = "datetime1"
	turnCountIndexKey    = "int1"
)

// PageSize is the number of sessions in one page.
const PageSize = 50

// unknownTitleFieldText is the server's text when a query names an index
// key the namespace has not registered.
const unknownTitleFieldText = `unknown visibility field "` + titleIndexKey + `"`

// ErrTitleSearchUnavailable means the namespace has no title index.
var ErrTitleSearchUnavailable = errors.New("the namespace has no session title index")

// SessionSummary is one session of a page.
type SessionSummary struct {
	// SessionID is the session's Flow ID.
	SessionID string
	// Status is the Flow status.
	Status dex.FlowStatus
	// StartedAt is when the session started.
	StartedAt time.Time
	// Title is the session title, or nil when the session has none.
	Title *string
	// LastActivityAt is when the latest turn started or ended, or nil.
	LastActivityAt *time.Time
	// TurnCount is the number of ended turns, or nil.
	TurnCount *int64
}

// HasSummary reports whether the session has any summary Attribute.
func (summary SessionSummary) HasSummary() bool {
	return summary.Title != nil || summary.LastActivityAt != nil || summary.TurnCount != nil
}

// SessionPage is one page of sessions, in Dex search order.
type SessionPage struct {
	// Sessions are the page's sessions: running first, then the most
	// recently closed, then the most recently started.
	Sessions []SessionSummary
	// NextPageToken continues the list, or is "" on the last page.
	NextPageToken string
}

// FlowSearcher is the part of the Dex client the listing calls.
type FlowSearcher interface {
	SearchFlows(ctx context.Context, query string, pageSize int32, nextPageToken string) (dex.SearchFlowsPage, error)
	DescribeFlow(ctx context.Context, flowID string) (dex.FlowInfo, error)
}

// SessionLister lists the sessions of one session Flow type.
type SessionLister struct {
	searcher FlowSearcher
	flowType string
}

// NewSessionLister lists the sessions of flowType through searcher.
func NewSessionLister(searcher FlowSearcher, flowType string) *SessionLister {
	if searcher == nil {
		panic("sessionlisting: a nil FlowSearcher")
	}
	return &SessionLister{searcher: searcher, flowType: flowType}
}

// ListSessions returns the page after nextPageToken ("" for the first).
// titleSearch keeps sessions whose title has its words; pass it with each
// page token. It returns ErrTitleSearchUnavailable when titleSearch is set
// and the namespace has no title index, and the Dex error otherwise.
func (lister *SessionLister) ListSessions(ctx context.Context, nextPageToken string, titleSearch string) (SessionPage, error) {
	query := "FlowType = " + quoteQueryText(lister.flowType)
	isTitleSearch := strings.TrimSpace(titleSearch) != ""
	if isTitleSearch {
		query += " AND " + titleIndexKey + " = " + quoteQueryText(titleSearch)
	}
	page, err := lister.searcher.SearchFlows(ctx, query, PageSize, nextPageToken)
	if err != nil {
		if isTitleSearch && isUnknownTitleField(err) {
			return SessionPage{}, ErrTitleSearchUnavailable
		}
		return SessionPage{}, fmt.Errorf("search the sessions: %w", err)
	}
	sessions := make([]SessionSummary, 0, len(page.Flows))
	for _, entry := range page.Flows {
		sessions = append(sessions, sessionSummaryOf(entry))
	}
	return SessionPage{Sessions: sessions, NextPageToken: page.NextPageToken}, nil
}

// ResumeCheck is why a session can or cannot be resumed.
type ResumeCheck int

const (
	// ResumeAllowed means the session is a running session of the Flow type.
	ResumeAllowed ResumeCheck = iota
	// ResumeNotRunning means the session ended.
	ResumeNotRunning
	// ResumeOtherFlowType means the Flow ID belongs to another Flow type.
	ResumeOtherFlowType
)

// CheckResume reports, with DescribeFlow, whether the session can resume,
// and its status.
func (lister *SessionLister) CheckResume(ctx context.Context, sessionID string) (ResumeCheck, dex.FlowStatus, error) {
	info, err := lister.searcher.DescribeFlow(ctx, sessionID)
	if err != nil {
		return ResumeNotRunning, 0, fmt.Errorf("describe the session: %w", err)
	}
	switch {
	case info.FlowType != lister.flowType:
		return ResumeOtherFlowType, info.Status, nil
	case info.Status != dex.FlowRunning:
		return ResumeNotRunning, info.Status, nil
	}
	return ResumeAllowed, info.Status, nil
}

func isUnknownTitleField(err error) bool {
	var serviceError *dex.ServiceError
	return errors.As(err, &serviceError) && serviceError.Code == codes.InvalidArgument &&
		strings.Contains(serviceError.Detail, unknownTitleFieldText)
}

func sessionSummaryOf(entry dex.SearchFlowEntry) SessionSummary {
	attributes := make(map[string]attributeValue, len(entry.IndexedAttributes))
	for key, value := range entry.IndexedAttributes {
		attributes[key] = value
	}
	return summaryOfAttributes(SessionSummary{SessionID: entry.FlowID, Status: entry.Status, StartedAt: entry.StartedAt}, attributes)
}

// attributeValue is the part of dex.Value the decoding reads.
type attributeValue interface {
	Decode(valuePtr any) error
}

// summaryOfAttributes fills the summary fields from the indexed Attributes.
// A value of another type counts as missing.
func summaryOfAttributes(summary SessionSummary, attributes map[string]attributeValue) SessionSummary {
	if value, isPresent := attributes[titleIndexKey]; isPresent {
		var title string
		if value.Decode(&title) == nil {
			summary.Title = &title
		}
	}
	if value, isPresent := attributes[lastActivityIndexKey]; isPresent {
		summary.LastActivityAt = decodeActivityTime(value)
	}
	if value, isPresent := attributes[turnCountIndexKey]; isPresent {
		var turnCount int64
		if value.Decode(&turnCount) == nil {
			summary.TurnCount = &turnCount
		}
	}
	return summary
}

// decodeActivityTime reads the RFC 3339 text the cores write, or a time
// value; anything else counts as missing.
func decodeActivityTime(value attributeValue) *time.Time {
	var text string
	if value.Decode(&text) == nil {
		moment, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil
		}
		return &moment
	}
	var moment time.Time
	if value.Decode(&moment) == nil && !moment.IsZero() {
		return &moment
	}
	return nil
}

// quoteQueryText writes text as a double-quoted query string, as the agents
// cores quote it.
func quoteQueryText(text string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`) + `"`
}
