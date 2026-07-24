// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package uclient

import (
	"context"
	"time"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
)

type UnifiedClient interface {
	Close()
	errorHandler
	StartInterpreterWorkflow(
		ctx context.Context, options StartWorkflowOptions, args ...interface{},
	) (runId string, err error)
	StartBlobStoreCleanupWorkflow(
		ctx context.Context, taskQueue, workflowID, cronSchedule, storeId string,
	) error
	SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error
	CancelWorkflow(ctx context.Context, workflowID string, runID string) error
	TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string) error
	ListWorkflow(ctx context.Context, request *ListWorkflowExecutionsRequest) (*ListWorkflowExecutionsResponse, error)
	QueryWorkflow(
		ctx context.Context, valuePtr interface{}, workflowID string, runID string, queryType string,
		args ...interface{},
	) error
	DescribeWorkflowExecution(
		ctx context.Context, workflowID, runID string, indexedAttrTypes map[string]iwfpb.IndexType,
	) (*DescribeWorkflowExecutionResponse, error)
	GetWorkflowResult(
		ctx context.Context, valuePtr interface{}, workflowID string, runID string,
	) (resolvedRunID string, status iwfpb.FlowStatus, err error)
	SynchronousUpdateWorkflow(
		ctx context.Context, valuePtr interface{}, workflowID, runID, updateType string, input interface{},
	) error
	ResetWorkflow(ctx context.Context, request *iwfpb.ResetFlowRequest) (runId string, err error)
	GetBackendType() (backendType service.BackendType)
	GetApiService() interface{}
}

type errorHandler interface {
	GetApplicationErrorTypeIfIsApplicationError(err error) string
	GetApplicationErrorDetails(err error, detailsPtr interface{}) error
	GetApplicationErrorTypeAndDetails(err error) (string, string)
	IsWorkflowAlreadyStartedError(error) bool
	GetRunIdFromWorkflowAlreadyStartedError(error) (string, bool)
	IsNotFoundError(error) bool
	IsRequestTimeoutError(error) bool
	IsWorkflowTimeoutError(error) bool
}

type StartWorkflowOptions struct {
	ID                       string
	TaskQueue                string
	WorkflowExecutionTimeout time.Duration
	IdReusePolicy            *iwfpb.IdReusePolicy
	CronSchedule             *string
	RetryPolicy              *iwfpb.FlowRetryPolicy
	// SearchAttributes are Temporal/Cadence indexed fields (already encoded as backend values).
	SearchAttributes   map[string]interface{}
	Memo               map[string]interface{}
	WorkflowStartDelay *time.Duration
}

type ListWorkflowExecutionsRequest struct {
	PageSize      int32
	Query         string
	NextPageToken []byte
}

type ListWorkflowExecutionsResponse struct {
	Executions    []*iwfpb.SearchFlowsResponseEntry
	NextPageToken []byte
}

type DescribeWorkflowExecutionResponse struct {
	Status               iwfpb.FlowStatus
	RunId                string
	FirstRunId           string
	IndexedAttributes    map[string]*iwfpb.Value
	Memos                map[string]*iwfpb.Value
	FlowStartedTimestamp int64
}
