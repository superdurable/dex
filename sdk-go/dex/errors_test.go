// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestTranslateRPCErrorPreservesWorkerDetailsAndUnwraps(t *testing.T) {
	rpcStatus, err := status.New(codes.FailedPrecondition, "fallback").WithDetails(
		&dexpb.ErrorResponse{
			Detail:                    "worker failed",
			SubStatus:                 dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
			OriginalWorkerErrorStatus: int32(codes.InvalidArgument),
			OriginalWorkerErrorType:   "ValidationError",
			OriginalWorkerErrorDetail: "invalid input",
		},
	)
	require.NoError(t, err)

	translated := translateRPCError(
		rpcStatus.Err(),
		"InvokeRPC",
		"flow-1",
		flowTargetActive,
	)
	var workerError *WorkerInvocationError
	require.ErrorAs(t, translated, &workerError)
	require.Equal(t, codes.InvalidArgument, workerError.Worker.Code)
	require.Equal(t, "ValidationError", workerError.Worker.Type)
	require.Equal(t, "invalid input", workerError.Worker.Detail)

	var serviceError *ServiceError
	require.ErrorAs(t, translated, &serviceError)
	require.Equal(t, "InvokeRPC", serviceError.Op)
	require.Equal(t, "flow-1", serviceError.FlowID)
	require.Equal(t, codes.FailedPrecondition, serviceError.Code)
	require.Equal(t, ErrorSubStatusWorkerAPI, serviceError.SubStatus)
	require.Equal(t, "worker failed", serviceError.Detail)
	require.Equal(t, codes.FailedPrecondition, status.Code(translated))
}

func TestTranslateRPCErrorFallbackAndLocalError(t *testing.T) {
	translated := translateRPCError(
		status.Error(codes.Unavailable, "backend unavailable"),
		"HealthCheck",
		"",
		flowTargetNone,
	)
	var serviceError *ServiceError
	require.ErrorAs(t, translated, &serviceError)
	require.Equal(t, ErrorSubStatusUncategorized, serviceError.SubStatus)
	require.Equal(t, "backend unavailable", serviceError.Detail)

	local := errors.New("local")
	require.Same(t, local, translateRPCError(local, "", "", flowTargetNone))
	require.NoError(t, translateRPCError(nil, "", "", flowTargetNone))
}

func TestTranslateRPCErrorMalformedDetailsFallback(t *testing.T) {
	rpcError := status.ErrorProto(&statuspb.Status{
		Code:    int32(codes.Internal),
		Message: "broken details",
		Details: []*anypb.Any{{
			TypeUrl: "type.googleapis.com/dex.ErrorResponse",
			Value:   []byte{0xff},
		}},
	})
	translated := translateRPCError(rpcError, "WaitForFlow", "flow-1", flowTargetExisting)
	var serviceError *ServiceError
	require.ErrorAs(t, translated, &serviceError)
	require.ErrorContains(t, translated, "malformed error details")
	var missing *FlowNotFoundError
	require.NotErrorAs(t, translated, &missing)
}
