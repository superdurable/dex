// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build linux

package agentsterminal

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// processStat is the part of /proc/PID/stat that job control needs.
type processStat struct {
	parentID       int
	processGroupID int
	sessionID      int
}

// processGroupHasJobControl applies the POSIX orphan rule to this process
// group: some member's parent is in another group of the same session.
func processGroupHasJobControl() bool {
	own, isRead := readProcessStat("self")
	if !isRead {
		return false
	}
	entries, err := filepath.Glob("/proc/[0-9]*/stat")
	if err != nil {
		debugLog("list processes", err)
		return false
	}
	stats := map[int]processStat{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(entry)))
		if err != nil {
			continue
		}
		if stat, isRead := readProcessStat(strconv.Itoa(pid)); isRead {
			stats[pid] = stat
		}
	}
	for _, member := range stats {
		if member.processGroupID != own.processGroupID {
			continue
		}
		parent, isKnown := stats[member.parentID]
		if isKnown && parent.processGroupID != own.processGroupID && parent.sessionID == own.sessionID {
			return true
		}
	}
	return false
}

// readProcessStat reads /proc/NAME/stat and reports whether it could.
func readProcessStat(name string) (processStat, bool) {
	data, err := os.ReadFile("/proc/" + name + "/stat")
	if err != nil {
		return processStat{}, false
	}
	// The command name may hold spaces and parentheses; fields follow the last ")".
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	if len(fields) < 4 {
		return processStat{}, false
	}
	parentID, parentErr := strconv.Atoi(fields[1])
	groupID, groupErr := strconv.Atoi(fields[2])
	sessionID, sessionErr := strconv.Atoi(fields[3])
	if parentErr != nil || groupErr != nil || sessionErr != nil {
		return processStat{}, false
	}
	return processStat{parentID: parentID, processGroupID: groupID, sessionID: sessionID}, true
}
