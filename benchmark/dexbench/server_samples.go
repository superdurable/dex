// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
)

// ServerSamples holds CPU and RSS samples taken on the server host by `dexbench sample`.
type ServerSamples struct {
	Cores     int              `json:"cores"`
	CPUMethod string           `json:"cpu_method"`
	Samples   []ResourceSample `json:"samples"`
}

// SampleServerProcess samples the process tree rooted at processID every second until ctx ends.
func SampleServerProcess(ctx context.Context, processID int) ServerSamples {
	output := make(chan []ResourceSample, 1)
	sampleResources(ctx, newResourceSampler([]int{processID}), output)
	return ServerSamples{Cores: runtime.NumCPU(), CPUMethod: resourceSamplingMethod(), Samples: <-output}
}

func WriteServerSamples(path string, samples ServerSamples) error {
	data, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		return fmt.Errorf("encode server samples: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func ReadServerSamples(path string) (ServerSamples, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ServerSamples{}, fmt.Errorf("read server samples %s: %w", path, err)
	}
	var samples ServerSamples
	if err := json.Unmarshal(data, &samples); err != nil {
		return ServerSamples{}, fmt.Errorf("decode server samples %s: %w", path, err)
	}
	return samples, nil
}

// clockSkewAllowance tolerates small clock differences between the load host and the server host.
const clockSkewAllowance = 500 * time.Millisecond

// MergeServerSamples keeps the server samples whose interval lies inside the measurement window.
func MergeServerSamples(result *Result, samples ServerSamples) error {
	if result.MeasurementStartedAt.IsZero() || result.MeasurementEndedAt.IsZero() {
		return errors.New("result has no measurement window")
	}
	windowStart := result.MeasurementStartedAt.Add(-clockSkewAllowance)
	windowEnd := result.MeasurementEndedAt.Add(clockSkewAllowance)
	var kept []ResourceSample
	for _, sample := range samples.Samples {
		intervalStart := sample.At.Add(-time.Duration(sample.IntervalSeconds * float64(time.Second)))
		if !intervalStart.Before(windowStart) && !sample.At.After(windowEnd) {
			kept = append(kept, sample)
		}
	}
	if len(kept) == 0 {
		return fmt.Errorf("none of %d server samples falls inside the measurement window %s to %s",
			len(samples.Samples), result.MeasurementStartedAt.Format(time.RFC3339), result.MeasurementEndedAt.Format(time.RFC3339))
	}
	result.Resources = kept
	result.ServerCores = samples.Cores
	result.CPUMethod = samples.CPUMethod
	return nil
}
