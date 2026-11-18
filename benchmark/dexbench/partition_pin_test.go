// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"fmt"
	"strings"
	"testing"
)

// The expected values are the server-zd test vectors in server-zd/crates/dexzd-keyspace/src/partition_hash.rs.
func TestPartitionFlowIDGeneratorMatchesServerVectors(t *testing.T) {
	zeroKey, err := newPartitionFlowIDGenerator(strings.Repeat("00", 16), 1024, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hash := fmt.Sprintf("%016x", zeroKey.flowHash(0, "flow")); hash != "3a72068066124e22" {
		t.Fatalf("zero-key hash = %s", hash)
	}
	distinctKey, err := newPartitionFlowIDGenerator("000102030405060708090a0b0c0d0e0f", 1024, 985)
	if err != nil {
		t.Fatal(err)
	}
	hash := distinctKey.flowHash(0x0102030405060708, "order-42")
	if got := fmt.Sprintf("%016x", hash); got != "f6487cae05e30bcc" {
		t.Fatalf("distinct-key hash = %s", got)
	}
	if partition := distinctKey.partitionOf(hash); partition != 985 {
		t.Fatalf("partition = %d, want 985", partition)
	}
}

func TestPartitionFlowIDGeneratorPinsEveryID(t *testing.T) {
	generator, err := newPartitionFlowIDGenerator("000102030405060708090a0b0c0d0e0f", 16, 7)
	if err != nil {
		t.Fatal(err)
	}
	var counter int64
	seen := map[string]bool{}
	for range 50 {
		flowID := generator.nextFlowID("pin", &counter)
		if seen[flowID] {
			t.Fatalf("repeated Flow ID %s", flowID)
		}
		seen[flowID] = true
		if partition := generator.partitionOf(generator.flowHash(benchmarkTenantID, flowID)); partition != 7 {
			t.Fatalf("%s is in partition %d", flowID, partition)
		}
	}
}

func TestPartitionFlowIDGeneratorRejectsBadInput(t *testing.T) {
	for _, test := range []struct {
		key       string
		count     int
		partition int
	}{
		{strings.Repeat("00", 15), 1024, 0},
		{"zz" + strings.Repeat("00", 15), 1024, 0},
		{strings.Repeat("00", 16), 1000, 0},
		{strings.Repeat("00", 16), 1024, 1024},
	} {
		if _, err := newPartitionFlowIDGenerator(test.key, test.count, test.partition); err == nil {
			t.Fatalf("accepted key %q count %d partition %d", test.key, test.count, test.partition)
		}
	}
}
