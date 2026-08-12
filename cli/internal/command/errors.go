// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Error struct {
	operation string
	kind      string
	cause     error
}

func newUsageError(operation string, cause error) *Error {
	return &Error{operation: operation, kind: "usage", cause: cause}
}

func newConfirmationError(operation string) *Error {
	return &Error{
		operation: operation,
		kind:      "confirmation_required",
		cause:     fmt.Errorf("operation requires --yes"),
	}
}

func newOperationError(operation string, cause error) *Error {
	var commandError *Error
	if errors.As(cause, &commandError) {
		return commandError
	}
	return &Error{operation: operation, kind: "operation", cause: cause}
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %v", e.operation, e.cause)
}

func (e *Error) Unwrap() error {
	return e.cause
}

func ExitCode(err error) int {
	var commandError *Error
	if errors.As(err, &commandError) {
		if commandError.kind == "usage" || commandError.kind == "confirmation_required" {
			return 2
		}
	}
	return 1
}

func WriteError(output io.Writer, err error) {
	if output == nil {
		panic("error output must not be nil")
	}
	commandError := newOperationError("dexcli", err)
	var typed *Error
	if errors.As(err, &typed) {
		commandError = typed
	}
	payload := map[string]any{
		"error": map[string]any{
			"kind":      commandError.kind,
			"operation": commandError.operation,
			"message":   commandError.cause.Error(),
		},
	}
	if rpcStatus, ok := status.FromError(commandError.cause); ok {
		errorPayload := payload["error"].(map[string]any)
		errorPayload["grpcCode"] = int32(rpcStatus.Code())
		errorPayload["grpcCodeName"] = rpcStatus.Code().String()
		details := make([]any, 0, len(rpcStatus.Details()))
		for _, detail := range rpcStatus.Details() {
			message, messageOK := detail.(proto.Message)
			if !messageOK {
				details = append(details, fmt.Sprint(detail))
				continue
			}
			data, marshalErr := protojson.Marshal(message)
			if marshalErr != nil {
				details = append(details, fmt.Sprint(detail))
				continue
			}
			var mapped any
			if unmarshalErr := json.Unmarshal(data, &mapped); unmarshalErr != nil {
				details = append(details, string(data))
				continue
			}
			details = append(details, mapped)
		}
		if len(details) > 0 {
			errorPayload["details"] = details
		}
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if encodeErr := encoder.Encode(payload); encodeErr != nil {
		fmt.Fprintf(output, "{\"error\":{\"kind\":\"output\",\"message\":%q}}\n", encodeErr.Error())
	}
}
