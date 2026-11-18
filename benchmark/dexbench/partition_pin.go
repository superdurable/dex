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

// partitionFlowIDGenerator mints Flow IDs in one partition, replicating server-zd's PartitionHasher.
// benchmark/README.md cites the server source it follows.
type partitionFlowIDGenerator struct {
	key0          uint64
	key1          uint64
	partitionBits int
	partition     uint64
}

// benchmarkTenantID is server-zd's TenantId::DEFAULT.
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

// flowHash returns the SipHash-2-4 of the big-endian tenant ID followed by the Flow ID.
func (generator *partitionFlowIDGenerator) flowHash(tenantID uint64, flowID string) uint64 {
	message := make([]byte, 8, 8+len(flowID))
	binary.BigEndian.PutUint64(message, tenantID)
	message = append(message, flowID...)
	return sipHash24(generator.key0, generator.key1, message)
}

// partitionOf returns the partition server-zd selects for a FlowHash: its high partitionBits bits.
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

// sipHash24 returns the 64-bit SipHash-2-4 of message, as Rust's siphasher crate computes it.
func sipHash24(key0, key1 uint64, message []byte) uint64 {
	state := sipHashState{
		v0: key0 ^ 0x736f6d6570736575,
		v1: key1 ^ 0x646f72616e646f6d,
		v2: key0 ^ 0x6c7967656e657261,
		v3: key1 ^ 0x7465646279746573,
	}
	length := len(message)
	for len(message) >= 8 {
		state.compress(binary.LittleEndian.Uint64(message))
		message = message[8:]
	}
	last := uint64(length&0xff) << 56
	for index, value := range message {
		last |= uint64(value) << (8 * index)
	}
	state.compress(last)
	state.v2 ^= 0xff
	for range 4 {
		state.round()
	}
	return state.v0 ^ state.v1 ^ state.v2 ^ state.v3
}

// sipHashState is the four-word SipHash internal state.
type sipHashState struct {
	v0, v1, v2, v3 uint64
}

// compress mixes one message word in with two rounds, the "2" of SipHash-2-4.
func (state *sipHashState) compress(word uint64) {
	state.v3 ^= word
	state.round()
	state.round()
	state.v0 ^= word
}

func (state *sipHashState) round() {
	state.v0 += state.v1
	state.v1 = bits.RotateLeft64(state.v1, 13)
	state.v1 ^= state.v0
	state.v0 = bits.RotateLeft64(state.v0, 32)
	state.v2 += state.v3
	state.v3 = bits.RotateLeft64(state.v3, 16)
	state.v3 ^= state.v2
	state.v0 += state.v3
	state.v3 = bits.RotateLeft64(state.v3, 21)
	state.v3 ^= state.v0
	state.v2 += state.v1
	state.v1 = bits.RotateLeft64(state.v1, 17)
	state.v1 ^= state.v2
	state.v2 = bits.RotateLeft64(state.v2, 32)
}
