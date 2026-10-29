// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

func validateAndRewriteWorkerTarget(target *dexpb.WorkerTarget) (*dexpb.WorkerTarget, error) {
	if target == nil {
		return nil, fmt.Errorf("worker_target is required")
	}
	if target.GetIsPullMode() {
		if target.GetAddress() != "" {
			return nil, fmt.Errorf("pull worker_target cannot specify address")
		}
		if target.GetIsHeadlessAddress() {
			return nil, fmt.Errorf("pull worker_target cannot use headless address")
		}
		return &dexpb.WorkerTarget{IsPullMode: true}, nil
	}
	address, err := validateAndRewriteWorkerAddress(target.GetAddress())
	if err != nil {
		return nil, err
	}
	if target.GetIsHeadlessAddress() {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return nil, fmt.Errorf("headless worker_target %q must use host:port: %w", address, err)
		}
	}
	return &dexpb.WorkerTarget{
		Address:           address,
		IsHeadlessAddress: target.GetIsHeadlessAddress(),
	}, nil
}

func validateAndRewriteWorkerAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("worker_target is empty")
	}
	lowerAddress := strings.ToLower(address)
	if strings.HasPrefix(lowerAddress, "http://") || strings.HasPrefix(lowerAddress, "https://") {
		return "", fmt.Errorf("HTTP(S) worker_target %q rejected; use a plaintext gRPC target (host:port)", address)
	}

	workerHostReplacement := os.Getenv("AUTO_FIX_WORKER_URL")
	if workerHostReplacement != "" {
		address = strings.Replace(address, "localhost", workerHostReplacement, 1)
		address = strings.Replace(address, "127.0.0.1", workerHostReplacement, 1)
	}
	workerPortEnvironmentVariable := os.Getenv("AUTO_FIX_WORKER_PORT_FROM_ENV")
	if workerPortEnvironmentVariable != "" {
		workerPortReplacement := os.Getenv(workerPortEnvironmentVariable)
		address = strings.Replace(address, "$"+workerPortEnvironmentVariable+"$", workerPortReplacement, 1)
	}
	return address, nil
}

func validateFlowID(flowID string) error {
	for _, reservedCharacter := range []string{"/", "$", ":"} {
		if strings.Contains(flowID, reservedCharacter) {
			return fmt.Errorf("flow ID contains reserved character %q", reservedCharacter)
		}
	}
	return nil
}
