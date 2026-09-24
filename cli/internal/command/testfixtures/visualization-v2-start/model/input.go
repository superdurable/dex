// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package model

import "time"

type Priority int64

const (
	PriorityNormal Priority = 1
	PriorityUrgent Priority = 9223372036854775807
)

type NestedPayload struct {
	Enabled bool      `json:"enabled"`
	DueAt   time.Time `json:"dueAt"`
}

type EmbeddedPayload struct {
	CorrelationID string `json:"correlationId"`
}

type StartPayload struct {
	EmbeddedPayload
	Name       string            `json:"name"`
	Priority   Priority          `json:"priority"`
	Nested     NestedPayload     `json:"nested"`
	Optional   *string           `json:"optional"`
	Omitted    string            `json:"omitted,omitempty"`
	Scores     []int16           `json:"scores"`
	Decisions  [2]bool           `json:"decisions"`
	Counters   map[string]uint64 `json:"counters"`
	Ratio      float32           `json:"ratio"`
	NotEncoded chan string       `json:"-"`
}
