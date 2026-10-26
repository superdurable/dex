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
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	cpuMethodLinux  = "linux: per-interval delta of /proc/<pid>/stat utime+stime over each root's process tree; cgroup v2 cpu.stat usage_usec delta for containers"
	cpuMethodDarwin = "darwin (best effort): per-interval delta of cumulative ps TIME over each root's process tree; 10 ms resolution, no cgroups"
)

// processTime is the cumulative CPU time of one process instance.
type processTime struct {
	parentPID  int
	startTicks uint64
	cpuSeconds float64
	rssBytes   uint64
}

type resourceSampler struct {
	rootPIDs        []int
	cgroups         []string
	clockTicks      float64
	pageSize        uint64
	previousAt      time.Time
	previousProcess map[int]processTime
	previousCgroup  map[string]float64
}

func newResourceSampler(rootPIDs []int, cgroups []string) *resourceSampler {
	sampler := &resourceSampler{rootPIDs: rootPIDs, cgroups: cgroups, clockTicks: 100, pageSize: uint64(os.Getpagesize())}
	if output, err := exec.Command("getconf", "CLK_TCK").Output(); err == nil {
		if ticks, parseErr := strconv.ParseFloat(strings.TrimSpace(string(output)), 64); parseErr == nil && ticks > 0 {
			sampler.clockTicks = ticks
		}
	}
	return sampler
}

func resourceSamplingMethod() string {
	if runtime.GOOS == "linux" {
		return cpuMethodLinux
	}
	return cpuMethodDarwin
}

// sampleResources emits one sample per second; each covers only the preceding interval.
func sampleResources(ctx context.Context, sampler *resourceSampler, output chan<- []ResourceSample) {
	var samples []ResourceSample
	if err := sampler.establishBaseline(); err != nil {
		slog.Warn("establish resource baseline", "error", err)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			output <- samples
			return
		case <-ticker.C:
			sample, err := sampler.sample()
			if err != nil {
				slog.Warn("sample server resources", "error", err)
				continue
			}
			samples = append(samples, sample)
		}
	}
}

func (sampler *resourceSampler) establishBaseline() error {
	_, err := sampler.sample()
	return err
}

// sample returns usage since the previous call. PIDs are re-resolved on every call.
func (sampler *resourceSampler) sample() (ResourceSample, error) {
	now := time.Now()
	processes, err := readProcessTable(sampler.clockTicks, sampler.pageSize)
	if err != nil {
		return ResourceSample{}, err
	}
	cgroupUsage := make(map[string]float64, len(sampler.cgroups))
	var cgroupRSS uint64
	for _, directory := range sampler.cgroups {
		usage, rss, cgroupErr := readCgroup(directory)
		if cgroupErr != nil {
			return ResourceSample{}, cgroupErr
		}
		cgroupUsage[directory] = usage
		cgroupRSS += rss
	}
	cpuSeconds, processRSS, processCount := processTreeUsage(sampler.previousProcess, processes, sampler.rootPIDs)
	sample := ResourceSample{At: now.UTC(), RSSBytes: cgroupRSS + processRSS, ProcessCount: processCount}
	for directory, usage := range cgroupUsage {
		cpuSeconds += usage - sampler.previousCgroup[directory]
	}
	isBaseline := sampler.previousAt.IsZero()
	interval := now.Sub(sampler.previousAt).Seconds()
	sampler.previousAt = now
	sampler.previousProcess = processes
	sampler.previousCgroup = cgroupUsage
	if isBaseline {
		return ResourceSample{}, nil
	}
	sample.IntervalSeconds = interval
	sample.CPUPercent = cpuSeconds / interval * 100
	return sample, nil
}

// processTreeUsage returns the trees' CPU seconds since previous, current RSS bytes, and process count.
// A process absent from previous, or with a new start time, contributes all its CPU time.
func processTreeUsage(previous, current map[int]processTime, roots []int) (float64, uint64, int) {
	var cpuSeconds float64
	var rssBytes uint64
	tree := processTree(current, roots)
	for _, processID := range tree {
		process := current[processID]
		rssBytes += process.rssBytes
		earlier, found := previous[processID]
		if found && earlier.startTicks == process.startTicks {
			cpuSeconds += process.cpuSeconds - earlier.cpuSeconds
		} else {
			cpuSeconds += process.cpuSeconds
		}
	}
	return cpuSeconds, rssBytes, len(tree)
}

func processTree(processes map[int]processTime, roots []int) []int {
	children := make(map[int][]int, len(processes))
	for processID, process := range processes {
		children[process.parentPID] = append(children[process.parentPID], processID)
	}
	visited := make(map[int]bool)
	pending := make([]int, 0, len(roots))
	for _, root := range roots {
		if _, found := processes[root]; found {
			pending = append(pending, root)
		}
	}
	tree := make([]int, 0, len(pending))
	for len(pending) > 0 {
		processID := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if visited[processID] {
			continue
		}
		visited[processID] = true
		tree = append(tree, processID)
		pending = append(pending, children[processID]...)
	}
	return tree
}

func readProcessTable(clockTicks float64, pageSize uint64) (map[int]processTime, error) {
	if runtime.GOOS == "linux" {
		return readLinuxProcessTable(clockTicks, pageSize)
	}
	return readPSProcessTable()
}

func readLinuxProcessTable(clockTicks float64, pageSize uint64) (map[int]processTime, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("list /proc: %w", err)
	}
	processes := make(map[int]processTime, len(entries))
	for _, entry := range entries {
		processID, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if readErr != nil {
			// The process exited between ReadDir and ReadFile.
			continue
		}
		process, statErr := parseProcStat(data, clockTicks, pageSize)
		if statErr != nil {
			return nil, fmt.Errorf("parse /proc/%d/stat: %w", processID, statErr)
		}
		processes[processID] = process
	}
	return processes, nil
}

// parseProcStat reads ppid, utime, stime, starttime, and rss from /proc/<pid>/stat.
func parseProcStat(data []byte, clockTicks float64, pageSize uint64) (processTime, error) {
	// comm may contain spaces and parentheses, so fields start after the last ')'.
	closing := bytes.LastIndexByte(data, ')')
	if closing < 0 {
		return processTime{}, fmt.Errorf("missing command terminator")
	}
	fields := strings.Fields(string(data[closing+1:]))
	if len(fields) < 22 {
		return processTime{}, fmt.Errorf("expected at least 22 fields after command, got %d", len(fields))
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return processTime{}, fmt.Errorf("ppid: %w", err)
	}
	userTicks, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return processTime{}, fmt.Errorf("utime: %w", err)
	}
	systemTicks, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return processTime{}, fmt.Errorf("stime: %w", err)
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return processTime{}, fmt.Errorf("starttime: %w", err)
	}
	residentPages, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil {
		return processTime{}, fmt.Errorf("rss: %w", err)
	}
	if residentPages < 0 {
		residentPages = 0
	}
	return processTime{
		parentPID:  parentPID,
		startTicks: startTicks,
		cpuSeconds: float64(userTicks+systemTicks) / clockTicks,
		rssBytes:   uint64(residentPages) * pageSize,
	}, nil
}

func readPSProcessTable() (map[int]processTime, error) {
	output, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,time=,rss=").Output()
	if err != nil {
		return nil, fmt.Errorf("run ps: %w", err)
	}
	processes := map[int]processTime{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}
		processID, pidErr := strconv.Atoi(fields[0])
		parentPID, parentErr := strconv.Atoi(fields[1])
		cpuSeconds, timeErr := parsePSTime(fields[2])
		residentKilobytes, rssErr := strconv.ParseUint(fields[3], 10, 64)
		if pidErr != nil || parentErr != nil || timeErr != nil || rssErr != nil {
			return nil, fmt.Errorf("parse ps line %q", scanner.Text())
		}
		processes[processID] = processTime{parentPID: parentPID, cpuSeconds: cpuSeconds, rssBytes: residentKilobytes * 1024}
	}
	return processes, scanner.Err()
}

// parsePSTime parses cumulative CPU time such as "1:02.50", "1:02:03.50", or "2-01:02:03".
func parsePSTime(value string) (float64, error) {
	var days float64
	if dayText, rest, hasDays := strings.Cut(value, "-"); hasDays {
		parsedDays, err := strconv.ParseFloat(dayText, 64)
		if err != nil {
			return 0, fmt.Errorf("days in %q: %w", value, err)
		}
		days = parsedDays
		value = rest
	}
	var seconds float64
	for _, part := range strings.Split(value, ":") {
		parsed, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, fmt.Errorf("time %q: %w", value, err)
		}
		seconds = seconds*60 + parsed
	}
	return days*86400 + seconds, nil
}

// readCgroup returns cumulative CPU seconds and anon+file_mapped bytes for one cgroup v2 directory.
func readCgroup(directory string) (float64, uint64, error) {
	cpuStat, err := readKeyedCounters(filepath.Join(directory, "cpu.stat"))
	if err != nil {
		return 0, 0, err
	}
	usage, found := cpuStat["usage_usec"]
	if !found {
		return 0, 0, fmt.Errorf("%s/cpu.stat has no usage_usec", directory)
	}
	memoryStat, err := readKeyedCounters(filepath.Join(directory, "memory.stat"))
	if err != nil {
		return 0, 0, err
	}
	return float64(usage) / 1e6, memoryStat["anon"] + memoryStat["file_mapped"], nil
}

func readKeyedCounters(path string) (map[string]uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	counters := map[string]uint64{}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s in %s: %w", fields[0], path, parseErr)
		}
		counters[fields[0]] = value
	}
	return counters, nil
}
