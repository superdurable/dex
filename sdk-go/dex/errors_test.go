// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
