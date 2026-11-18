// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
)

// partitionFlowIDGenerator mints Flow IDs that server-zd places in one chosen partition.
//
// It replicates server-zd's PartitionHasher (server-zd/crates/dexzd-keyspace/src/partition_hash.rs):
// keyed SipHash-2-4 over the tenant ID as 8 big-endian bytes followed by the UTF-8 Flow ID (lines 79-83),
// with key bytes 0-7 and 8-15 read as little-endian k0 and k1 (lines 60-63), and the partition taken
// from the high bits of the hash (lines 87-93). The key is random per cluster and stored only in the
// cluster object (server-zd/crates/dexzd-cluster-metadata/src/cluster.rs:61,174), so callers must supply it.
type partitionFlowIDGenerator struct {
	key0          uint64
	key1          uint64
	partitionBits int
	partition     uint64
}

// benchmarkTenantID is server-zd's TenantId::DEFAULT (server-zd/crates/dexzd-keyspace/src/identifiers.rs:35).
const benchmarkTenantID uint64 = 0

// newPartitionFlowIDGenerator parses a 32-hex-digit cluster key and validates the partition choice.
// It returns an error when the key is not 16 bytes, the count is not a power of two in 1..65536,
// or the partition is outside the count.
func newPartitionFlowIDGenerator(keyHex string, partitionCount, partition int) (*partitionFlowIDGenerator, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("partition hash key is not hex: %w", err)
	}
	if len(key) != 16 {
		return nil, fmt.Errorf("partition hash key must be 16 bytes, got %d", len(key))
	}
	if partitionCount < 1 || partitionCount > 65536 || partitionCount&(partitionCount-1) != 0 {
		return nil, fmt.Errorf("partition count %d must be a power of two between 1 and 65536", partitionCount)
	}
	if partition < 0 || partition >= partitionCount {
		return nil, fmt.Errorf("partition %d is outside 0..%d", partition, partitionCount-1)
	}
	return &partitionFlowIDGenerator{
		key0:          binary.LittleEndian.Uint64(key[:8]),
		key1:          binary.LittleEndian.Uint64(key[8:]),
		partitionBits: bits.TrailingZeros(uint(partitionCount)),
		partition:     uint64(partition),
	}, nil
}

// flowHash returns server-zd's FlowHash of (tenantID, flowID).
func (generator *partitionFlowIDGenerator) flowHash(tenantID uint64, flowID string) uint64 {
	message := make([]byte, 8, 8+len(flowID))
	binary.BigEndian.PutUint64(message, tenantID)
	message = append(message, flowID...)
	return sipHash24(generator.key0, generator.key1, message)
}

// partitionOf returns the partition server-zd selects for a FlowHash.
func (generator *partitionFlowIDGenerator) partitionOf(flowHash uint64) uint64 {
	if generator.partitionBits == 0 {
		return 0
	}
	return flowHash >> (64 - generator.partitionBits)
}

// nextFlowID returns the first ID "<prefix>-<n>", from *counter upward, that lands in the chosen partition.
// It advances *counter past the returned candidate, so consecutive calls never repeat an ID.
func (generator *partitionFlowIDGenerator) nextFlowID(prefix string, counter *int64) string {
	for {
		*counter++
		flowID := fmt.Sprintf("%s-%d", prefix, *counter)
		if generator.partitionOf(generator.flowHash(benchmarkTenantID, flowID)) == generator.partition {
			return flowID
		}
	}
}

// sipHash24 is SipHash-2-4 with a 64-bit output, as in the SipHash paper and Rust's siphasher crate.
func sipHash24(key0, key1 uint64, message []byte) uint64 {
	v0 := key0 ^ 0x736f6d6570736575
	v1 := key1 ^ 0x646f72616e646f6d
	v2 := key0 ^ 0x6c7967656e657261
	v3 := key1 ^ 0x7465646279746573
	round := func() {
		v0 += v1
		v1 = bits.RotateLeft64(v1, 13)
		v1 ^= v0
		v0 = bits.RotateLeft64(v0, 32)
		v2 += v3
		v3 = bits.RotateLeft64(v3, 16)
		v3 ^= v2
		v0 += v3
		v3 = bits.RotateLeft64(v3, 21)
		v3 ^= v0
		v2 += v1
		v1 = bits.RotateLeft64(v1, 17)
		v1 ^= v2
		v2 = bits.RotateLeft64(v2, 32)
	}
	length := len(message)
	for len(message) >= 8 {
		word := binary.LittleEndian.Uint64(message)
		v3 ^= word
		round()
		round()
		v0 ^= word
		message = message[8:]
	}
	last := uint64(length&0xff) << 56
	for index, value := range message {
		last |= uint64(value) << (8 * index)
	}
	v3 ^= last
	round()
	round()
	v0 ^= last
	v2 ^= 0xff
	round()
	round()
	round()
	round()
	return v0 ^ v1 ^ v2 ^ v3
}
