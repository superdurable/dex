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

package grpctarget

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
)

// NormalizeWorkerTarget validates a plaintext gRPC worker_target and applies optional host rewrites.
func NormalizeWorkerTarget(target *dexpb.WorkerTarget) (*dexpb.WorkerTarget, error) {
	if target == nil {
		return nil, fmt.Errorf("worker_target is required")
	}
	address, err := NormalizeAddress(target.GetAddress())
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

// NormalizeAddress validates a plaintext gRPC address and applies optional host rewrites.
func NormalizeAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("worker_target is empty")
	}
	lower := strings.ToLower(address)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("HTTP(S) worker_target %q rejected; use a plaintext gRPC target (host:port)", address)
	}

	autofixHost := os.Getenv("AUTO_FIX_WORKER_URL")
	if autofixHost != "" {
		address = strings.Replace(address, "localhost", autofixHost, 1)
		address = strings.Replace(address, "127.0.0.1", autofixHost, 1)
	}
	autofixPortEnv := os.Getenv("AUTO_FIX_WORKER_PORT_FROM_ENV")
	if autofixPortEnv != "" {
		envVal := os.Getenv(autofixPortEnv)
		address = strings.Replace(address, "$"+autofixPortEnv+"$", envVal, 1)
	}
	return address, nil
}
