// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"flag"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/benchmark/dexbench"
)

// slotStreamFlags holds the slot stream scenario flags until they are parsed into a SlotStreamConfig.
type slotStreamFlags struct {
	config          benchmark.SlotStreamConfig
	handlerDuration time.Duration
	probeDuration   time.Duration
	baseline        time.Duration
	idleGap         time.Duration
	loadPeriod      time.Duration
	pingInterval    time.Duration
	openTimeout     time.Duration
	drainTimeout    time.Duration
	burstLevels     string
	loadPercents    string
}

// registerSlotStreamFlags adds the slot stream scenario flags; zero means the scenario default where noted.
func registerSlotStreamFlags(flags *flag.FlagSet) *slotStreamFlags {
	slot := &slotStreamFlags{}
	options := &slot.config
	flags.IntVar(&options.SlotsPerWorker, "slots-per-worker", 0, "slot scenarios: MaxConcurrentTasks per pull Worker; 0 uses 256 for idle-slots and 99 otherwise")
	flags.IntVar(&options.Workers, "slot-workers", 0, "slot scenarios: pull Worker count for burst, fairness, and periodic; 0 derives it (fairness: 8)")
	flags.IntVar(&options.FlowTypes, "flow-types", 0, "slot scenarios: registered Flow types when the variant does not fix them; 0 means 1")
	flags.DurationVar(&slot.handlerDuration, "handler-duration", 0, "slot scenarios: time each probe Step holds its slot; 0 uses the scenario default")
	flags.StringVar(&options.StepDurability, "step-durability", "async", "slot scenarios: async or sync, when the variant does not fix it")
	flags.DurationVar(&slot.baseline, "baseline", 5*time.Second, "idle-slots: observation before any Worker opens")
	flags.Float64Var(&options.ProbeRatePerSecond, "probe-rate", 50, "idle-slots: probe Steps per second while the streams are open")
	flags.DurationVar(&slot.probeDuration, "probe-duration", 30*time.Second, "idle-slots: length of the probe load")
	flags.StringVar(&options.RestartCommand, "restart-command", "", "idle-slots: bash command that restarts the server after the probe; empty skips the restart")
	flags.StringVar(&options.ServerPIDFile, "server-pid-file", "", "idle-slots: file holding the server PID that a restart changes; empty uses the server's process_start_time_seconds")
	flags.DurationVar(&slot.pingInterval, "ping-interval", 20*time.Second, "idle-slots: the server's matching.pingInterval, for the derived Ping rate")
	flags.DurationVar(&slot.idleGap, "idle-gap", 5*time.Second, "burst: zero-load gap before each level")
	flags.StringVar(&slot.burstLevels, "burst-levels", "8,32,96", "burst: comma-separated in-flight Steps per Worker of each level")
	flags.StringVar(&slot.loadPercents, "load-percents", "25,50,90,110", "fairness: comma-separated load levels as a percent of total slot capacity")
	flags.StringVar(&options.LoadWave, "load-wave", "square", "periodic: square or sine")
	flags.DurationVar(&slot.loadPeriod, "load-period", 10*time.Second, "periodic: wave period")
	flags.Float64Var(&options.LoadLowPercent, "load-low-percent", 10, "periodic: trough as a percent of total slot capacity")
	flags.Float64Var(&options.LoadHighPercent, "load-high-percent", 90, "periodic: peak as a percent of total slot capacity")
	flags.StringVar(&options.PartitionHashKey, "partition-hash-key", "", "backlog: the cluster's 32-hex-digit partition hash key; pins Flow IDs to -pin-partition")
	flags.IntVar(&options.PartitionCount, "partition-count", 1024, "backlog: the server's cluster.partitionCount, used with -partition-hash-key")
	flags.IntVar(&options.PinnedPartition, "pin-partition", 0, "backlog: partition the Flow IDs hash into, used with -partition-hash-key")
	flags.IntVar(&options.StartParallelism, "start-parallelism", 256, "slot scenarios: concurrent StartFlow calls")
	flags.IntVar(&options.ClientConnections, "client-connections", 4, "slot scenarios: Clients that start probe Flows")
	flags.DurationVar(&slot.openTimeout, "open-timeout", 10*time.Minute, "slot scenarios: limit for all streams to register")
	flags.DurationVar(&slot.drainTimeout, "drain-timeout", 5*time.Minute, "slot scenarios: limit for started Steps to finish")
	flags.BoolVar(&options.IsAdaptiveSlots, "adaptive-slots", false, "slot scenarios: use adaptive slot streams; fails until the Go SDK supports them")
	flags.IntVar(&options.MinOpenSlots, "min-open-slots", 0, "slot scenarios: adaptive floor of open slot streams; fails until the Go SDK supports it")
	return slot
}

// slotStreamConfig returns the parsed configuration; call it after the flag set is parsed.
func (slot *slotStreamFlags) slotStreamConfig() (*benchmark.SlotStreamConfig, error) {
	options := slot.config
	options.HandlerMilliseconds = float64(slot.handlerDuration) / float64(time.Millisecond)
	options.BaselineSeconds = slot.baseline.Seconds()
	options.ProbeSeconds = slot.probeDuration.Seconds()
	options.IdleGapSeconds = slot.idleGap.Seconds()
	options.LoadPeriodSeconds = slot.loadPeriod.Seconds()
	options.PingIntervalSeconds = slot.pingInterval.Seconds()
	options.OpenTimeoutSeconds = slot.openTimeout.Seconds()
	options.DrainTimeoutSeconds = slot.drainTimeout.Seconds()
	if options.PingIntervalSeconds <= 0 {
		return nil, fmt.Errorf("ping-interval must be positive")
	}
	for name, value := range map[string]float64{"probe-rate": options.ProbeRatePerSecond,
		"load-low-percent": options.LoadLowPercent, "load-high-percent": options.LoadHighPercent} {
		if !isFiniteNonnegative(value) {
			return nil, fmt.Errorf("%s %v must be finite and nonnegative", name, value)
		}
	}
	for _, value := range strings.Split(slot.burstLevels, ",") {
		level, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || level < 1 {
			return nil, fmt.Errorf("invalid burst level %q", value)
		}
		options.BurstLevels = append(options.BurstLevels, level)
	}
	for _, value := range strings.Split(slot.loadPercents, ",") {
		percent, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || !isFiniteNonnegative(percent) || percent == 0 {
			return nil, fmt.Errorf("invalid load percent %q", value)
		}
		options.LoadPercents = append(options.LoadPercents, percent)
	}
	return &options, nil
}

// isFiniteNonnegative rejects NaN and infinity, which strconv and the flag package both accept.
func isFiniteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
