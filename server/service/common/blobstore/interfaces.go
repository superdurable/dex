// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobstore

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex/config"
)

const (
	StepEventInputMethodWaitFor = "wait_for"
	StepEventInputMethodExecute = "execute"
)

func MustExtractWorkflowId(workflowPath string) string {
	workflowId, err := ExtractWorkflowId(workflowPath)
	if err != nil {
		panic(err)
	}
	return workflowId
}

func ExtractWorkflowId(workflowPath string) (string, error) {
	parts := strings.Split(workflowPath, "$")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid workflow path: %s", workflowPath)
	}
	flowID, err := decodePathPart(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode flow ID: %w", err)
	}
	return flowID, nil
}

func ExtractYyyymmddToUnixSeconds(workflowPath string) (int64, bool) {
	// yyyymmdd$encodedFlowId
	yyyymmdd, err := ExtractYyyymmdd(workflowPath)
	if err != nil {
		return 0, false
	}
	parsedTime, err := time.Parse("20060102", yyyymmdd)
	if err != nil {
		panic(err)
	}
	return parsedTime.Unix(), true
}

func ExtractYyyymmdd(workflowPath string) (string, error) {
	parts := strings.Split(workflowPath, "$")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid workflow path: %s", workflowPath)
	}
	return parts[0], nil
}

type WorkflowPath struct {
	StartedDate string
	FlowID      string
	RunID       string
}

func ParseWorkflowPath(workflowPath string) (WorkflowPath, error) {
	parts := strings.Split(workflowPath, "$")
	if len(parts) != 2 && len(parts) != 3 {
		return WorkflowPath{}, fmt.Errorf("invalid workflow path: %s", workflowPath)
	}
	if _, err := time.Parse("20060102", parts[0]); err != nil {
		return WorkflowPath{}, fmt.Errorf("invalid workflow path date: %w", err)
	}
	flowID, err := decodePathPart(parts[1])
	if err != nil {
		return WorkflowPath{}, fmt.Errorf("decode flow ID: %w", err)
	}
	if len(parts) == 2 {
		return WorkflowPath{StartedDate: parts[0], FlowID: flowID}, nil
	}
	runID, err := decodePathPart(parts[2])
	if err != nil {
		return WorkflowPath{}, fmt.Errorf("decode run ID: %w", err)
	}
	return WorkflowPath{StartedDate: parts[0], FlowID: flowID, RunID: runID}, nil
}

func StepEventInputPath(
	runStarted time.Time,
	flowID string,
	runID string,
	stepExecutionID string,
	method string,
) string {
	workflowPath := strings.Join([]string{
		runStarted.UTC().Format("20060102"),
		encodePathPart(flowID),
		encodePathPart(runID),
	}, "$")
	return strings.Join([]string{workflowPath, encodePathPart(stepExecutionID), method + ".pb"}, "/")
}

func ValueObjectPath(flowID string, locator string) (string, error) {
	if flowID == "" {
		return "", fmt.Errorf("Blob locator requires a Flow ID")
	}
	parts := strings.Split(locator, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid Blob locator %q", locator)
	}
	if _, err := time.Parse("20060102", parts[0]); err != nil {
		return "", fmt.Errorf("invalid Blob locator date: %w", err)
	}
	objectID := parts[1]
	if len(objectID) < config.MinimumBlobStoreObjectIDLength || len(objectID) > config.MaximumBlobStoreObjectIDLength {
		return "", fmt.Errorf("invalid Blob object ID length %d", len(objectID))
	}
	for _, character := range objectID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'z') {
			return "", fmt.Errorf("invalid Blob object ID %q", objectID)
		}
	}
	return parts[0] + "$" + encodePathPart(flowID) + "/" + objectID, nil
}

func encodePathPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodePathPart(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

type BlobStore interface {
	Close() error
	// WriteObject stores data under the Flow-owned path and returns its compact locator.
	WriteObject(ctx context.Context, flowID, invocationID string, data []byte) (storeID, locator string, err error)
	// ReadObject resolves a compact locator under its owning Flow.
	ReadObject(ctx context.Context, storeID, flowID, locator string) ([]byte, error)
	WriteStepEventInput(
		ctx context.Context,
		runStarted time.Time,
		flowID string,
		runID string,
		stepExecutionID string,
		method string,
		data []byte,
	) error
	ReadStepEventInput(
		ctx context.Context,
		runStarted time.Time,
		flowID string,
		runID string,
		stepExecutionID string,
		method string,
	) ([]byte, bool, error)
	// DeleteWorkflowObjects will delete all the objects of the workflowId
	// workflowPath is yyyymmdd$encodedFlowId, where yymmdd is needed to compose the path
	DeleteWorkflowObjects(ctx context.Context, storeId, workflowPath string) error
	// ListWorkflowPaths will list the workflowPaths ( yyyymmdd$encodedFlowId ) as CommonPrefixes from S3
	// It uses of delimiter "/" before the object ID to get all the CommonPrefixes
	// StartAfterYyyymmdd is the yyyymmdd to exclude the date when listing
	ListWorkflowPaths(ctx context.Context, input ListObjectPathsInput) (*ListObjectPathsOutput, error)
	// CountWorkflowObjectsForTesting is for testing ONLY.
	// count the number of S3 objects for this workflowId
	// Limitation:
	//  1. It doesn't count across two days(so expect test to fail if you happen to run the test across day boundary :)
	//  2. Only count less than 1000 objects(because it only make one API call to S3 which return at most 1000 objects)
	CountWorkflowObjectsForTesting(ctx context.Context, flowID string) (int64, error)
}

type ListObjectPathsInput struct {
	StoreId           string
	ContinuationToken *string
}

type ListObjectPathsOutput struct {
	ContinuationToken *string
	WorkflowPaths     []string
}
