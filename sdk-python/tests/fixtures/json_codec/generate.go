// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"encoding/json"
	"os"
	"time"
)

type result[ValueT any] struct {
	Branch  string  `json:"branch"`
	Value   ValueT  `json:"value"`
	Receipt receipt `json:"receipt"`
	Failure *string `json:"failure"`
}

type item struct {
	Count int `json:"count"`
}

type receipt struct {
	CallID     string    `json:"callId"`
	ObservedAt time.Time `json:"observedAt"`
}

func main() {
	value := result[item]{
		Branch: "success",
		Value:  item{Count: 42},
		Receipt: receipt{
			CallID:     "call-42",
			ObservedAt: time.Date(2026, 10, 30, 7, 0, 0, 120000000, time.UTC),
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		panic(err)
	}
}
