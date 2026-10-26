// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func machineInfo() MachineInfo {
	info := MachineInfo{Cores: runtime.NumCPU(), OS: runtime.GOOS, Architecture: runtime.GOARCH}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.HasPrefix(line, "model name") {
				info.CPUModel = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				break
			}
		}
	}
	data, err = os.ReadFile("/proc/meminfo")
	if err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
				if parseErr == nil {
					info.RAMBytes = kilobytes * 1024
				}
				break
			}
		}
	}
	if info.CPUModel == "" {
		if output, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			info.CPUModel = strings.TrimSpace(string(output))
		}
	}
	if info.RAMBytes == 0 {
		if output, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			if bytes, parseErr := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64); parseErr == nil {
				info.RAMBytes = bytes
			}
		}
	}
	var disk syscall.Statfs_t
	diskPath := os.Getenv("DEXBENCH_DISK_PATH")
	if diskPath == "" {
		diskPath = "."
	}
	if err := syscall.Statfs(diskPath, &disk); err == nil {
		info.FreeDiskBytes = disk.Bavail * uint64(disk.Bsize)
	}
	return info
}

func MachineDetails() MachineInfo { return machineInfo() }

func serverPID(config Config) (int, error) {
	if config.ServerPID > 0 {
		return config.ServerPID, nil
	}
	if config.ServerProcess == "" {
		return 0, nil
	}
	output, err := exec.Command("pgrep", "-n", "-x", config.ServerProcess).Output()
	if err != nil {
		return 0, fmt.Errorf("find server process %s: %w", config.ServerProcess, err)
	}
	return strconv.Atoi(strings.TrimSpace(string(output)))
}

func sampleProcess(pid int) (ResourceSample, error) {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "%cpu=,rss=").Output()
	if err != nil {
		return ResourceSample{}, fmt.Errorf("sample process %d: %w", pid, err)
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 {
		return ResourceSample{}, fmt.Errorf("unexpected ps output for %d: %q", pid, output)
	}
	cpu, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return ResourceSample{}, err
	}
	rss, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return ResourceSample{}, err
	}
	return ResourceSample{At: time.Now().UTC(), CPUPercent: cpu, RSSBytes: rss * 1024}, nil
}

func scrapeMetrics(ctx context.Context, address string) (map[string]float64, error) {
	if address == "" {
		return nil, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics returned %s", response.Status)
	}
	return parseMetrics(response.Body)
}

func parseMetrics(reader io.Reader) (map[string]float64, error) {
	values := map[string]float64{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if !strings.HasPrefix(fields[0], "dexzd_object_store_") && !strings.HasPrefix(fields[0], "dexzd_commit_latency_seconds") && !strings.HasPrefix(fields[0], "dexzd_recovery_last_") {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("parse metric %s: %w", fields[0], err)
		}
		values[fields[0]] = value
	}
	return values, scanner.Err()
}

func metricDifference(before, after map[string]float64, duration time.Duration) *ObjectStoreMetrics {
	if after == nil {
		return nil
	}
	metrics := &ObjectStoreMetrics{RequestsPerMinute: map[string]float64{}, CommitLatencyBuckets: map[string]float64{}}
	for name, final := range after {
		delta := final - before[name]
		if delta < 0 {
			delta = final
		}
		switch {
		case strings.HasPrefix(name, "dexzd_object_store_requests"):
			metrics.RequestsPerMinute[name] = delta / duration.Minutes()
		case strings.HasPrefix(name, "dexzd_object_store_request_errors"):
			metrics.RequestErrors += int64(delta)
		case strings.HasPrefix(name, "dexzd_commit_latency_seconds_bucket"):
			metrics.CommitLatencyBuckets[name] = delta
		case strings.HasPrefix(name, "dexzd_commit_latency_seconds_count"):
			metrics.CommitLatencyCount += int64(delta)
		case strings.HasPrefix(name, "dexzd_commit_latency_seconds_sum"):
			metrics.CommitLatencySumSeconds += delta
		}
	}
	return metrics
}
