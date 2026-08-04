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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestConvertRPCErrorPreservesDexDetails(t *testing.T) {
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

	converted := convertRPCError(rpcStatus.Err())
	var dexError *Error
	require.ErrorAs(t, converted, &dexError)
	require.Equal(t, codes.FailedPrecondition, dexError.Code)
	require.Equal(t, ErrorWorkerAPI, dexError.SubStatus)
	require.Equal(t, "worker failed", dexError.Detail)
	require.Equal(t, codes.InvalidArgument, dexError.OriginalWorkerError.Code)
	require.Equal(t, "ValidationError", dexError.OriginalWorkerError.Type)
	require.Equal(t, "invalid input", dexError.OriginalWorkerError.Detail)
}

func TestConvertRPCErrorFallbackAndLocalError(t *testing.T) {
	converted := convertRPCError(status.Error(codes.Unavailable, "backend unavailable"))
	var dexError *Error
	require.ErrorAs(t, converted, &dexError)
	require.Equal(t, ErrorUncategorized, dexError.SubStatus)
	require.Equal(t, "backend unavailable", dexError.Detail)

	local := errors.New("local")
	require.Same(t, local, convertRPCError(local))
	require.NoError(t, convertRPCError(nil))
}
