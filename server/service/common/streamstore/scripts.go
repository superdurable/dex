// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package streamstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const backgroundTrimBatchSize = 256

type writeScriptStatus int64

const (
	writeScriptStatusSucceeded writeScriptStatus = iota
	writeScriptStatusCapacityExceeded
)

type writeScriptInput struct {
	keys                   redisKeys
	internalIdentity       string
	publicIdempotencyKey   string
	payload                []byte
	chargedBytes           int64
	capacityBytes          int64
	trimTriggerBytes       int64
	baseTrimTargetBytes    int64
	messageTrimTargetBytes int64
}

type writeScriptOutput struct {
	redisID         string
	isDuplicate     bool
	totalBytes      int64
	needsTrim       bool
	trimTargetBytes int64
	status          writeScriptStatus
}

type trimScriptInput struct {
	keys        redisKeys
	targetBytes int64
	leaseOwner  string
}

type trimScriptOutput struct {
	remainingBytes  int64
	trimmedMessages int64
}

type renewLeaseScriptInput struct {
	leaseKey   string
	leaseOwner string
	leaseTTL   time.Duration
}

type renewLeaseScriptOutput struct {
	isRenewed bool
}

type releaseLeaseScriptInput struct {
	leaseKey   string
	leaseOwner string
}

func runWriteScript(
	ctx context.Context,
	client *redis.Client,
	input writeScriptInput,
) (writeScriptOutput, error) {
	result, err := writeRedisScript.Run(ctx, client, []string{
		input.keys.fifo,
		input.keys.chargedBytes,
		input.keys.idempotency,
		input.keys.instance,
	},
		input.internalIdentity,
		input.publicIdempotencyKey,
		input.payload,
		input.chargedBytes,
		input.capacityBytes,
		input.trimTriggerBytes,
		input.baseTrimTargetBytes,
		input.messageTrimTargetBytes,
	).Result()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeScriptOutput{}, err
		}
		return writeScriptOutput{}, fmt.Errorf("%w: write message: %v", ErrUnavailable, err)
	}
	return decodeWriteScriptOutput(result)
}

func decodeWriteScriptOutput(result any) (writeScriptOutput, error) {
	values, err := scriptValues(result, 6)
	if err != nil {
		return writeScriptOutput{}, fmt.Errorf("decode Stream write result: %w", err)
	}
	redisID, err := scriptString(values[0])
	if err != nil {
		return writeScriptOutput{}, fmt.Errorf("decode Stream message ID: %w", err)
	}
	isDuplicate, err := scriptBool(values[1])
	if err != nil {
		return writeScriptOutput{}, fmt.Errorf("decode Stream duplicate result: %w", err)
	}
	totalBytes, err := scriptInt64(values[2])
	if err != nil {
		return writeScriptOutput{}, fmt.Errorf("decode Stream total bytes: %w", err)
	}
	needsTrim, err := scriptBool(values[3])
	if err != nil {
		return writeScriptOutput{}, fmt.Errorf("decode Stream trim result: %w", err)
	}
	trimTargetBytes, err := scriptInt64(values[4])
	if err != nil {
		return writeScriptOutput{}, fmt.Errorf("decode Stream trim target: %w", err)
	}
	statusCode, err := scriptInt64(values[5])
	if err != nil {
		return writeScriptOutput{}, fmt.Errorf("decode Stream write status: %w", err)
	}
	status := writeScriptStatus(statusCode)
	switch status {
	case writeScriptStatusSucceeded,
		writeScriptStatusCapacityExceeded:
	default:
		return writeScriptOutput{}, fmt.Errorf("unexpected Stream write status %d", statusCode)
	}
	return writeScriptOutput{
		redisID:         redisID,
		isDuplicate:     isDuplicate,
		totalBytes:      totalBytes,
		needsTrim:       needsTrim,
		trimTargetBytes: trimTargetBytes,
		status:          status,
	}, nil
}

func runTrimScript(
	ctx context.Context,
	client *redis.Client,
	input trimScriptInput,
) (trimScriptOutput, error) {
	result, err := trimRedisScript.Run(ctx, client, []string{
		input.keys.fifo,
		input.keys.chargedBytes,
		input.keys.idempotency,
		input.keys.lease,
	}, input.targetBytes, backgroundTrimBatchSize, input.leaseOwner).Result()
	if err != nil {
		return trimScriptOutput{}, fmt.Errorf("trim Redis Stream: %w", err)
	}
	values, err := scriptValues(result, 2)
	if err != nil {
		return trimScriptOutput{}, fmt.Errorf("decode Redis trim result: %w", err)
	}
	remainingBytes, err := scriptInt64(values[0])
	if err != nil {
		return trimScriptOutput{}, fmt.Errorf("decode Redis trim bytes: %w", err)
	}
	trimmedMessages, err := scriptInt64(values[1])
	if err != nil {
		return trimScriptOutput{}, fmt.Errorf("decode Redis trimmed messages: %w", err)
	}
	return trimScriptOutput{
		remainingBytes:  remainingBytes,
		trimmedMessages: trimmedMessages,
	}, nil
}

func runRenewLeaseScript(
	ctx context.Context,
	client *redis.Client,
	input renewLeaseScriptInput,
) (renewLeaseScriptOutput, error) {
	result, err := renewRedisLeaseScript.Run(
		ctx,
		client,
		[]string{input.leaseKey},
		input.leaseOwner,
		input.leaseTTL.Milliseconds(),
	).Result()
	if err != nil {
		return renewLeaseScriptOutput{}, fmt.Errorf("renew Redis trim lease: %w", err)
	}
	isRenewed, err := scriptBool(result)
	if err != nil {
		return renewLeaseScriptOutput{}, fmt.Errorf("decode Redis trim lease renewal: %w", err)
	}
	return renewLeaseScriptOutput{isRenewed: isRenewed}, nil
}

func runReleaseLeaseScript(
	ctx context.Context,
	client *redis.Client,
	input releaseLeaseScriptInput,
) error {
	result, err := releaseRedisLeaseScript.Run(
		ctx,
		client,
		[]string{input.leaseKey},
		input.leaseOwner,
	).Result()
	if err != nil {
		return err
	}
	if _, err := scriptBool(result); err != nil {
		return fmt.Errorf("decode Redis trim lease release: %w", err)
	}
	return nil
}

func scriptValues(result any, expected int) ([]any, error) {
	values, ok := result.([]any)
	if !ok || len(values) != expected {
		return nil, fmt.Errorf("expected %d values", expected)
	}
	return values, nil
}

func scriptString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", fmt.Errorf("unexpected string type %T", value)
	}
}

func scriptInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected integer type %T", value)
	}
}

func scriptBool(value any) (bool, error) {
	integer, err := scriptInt64(value)
	if err != nil {
		return false, err
	}
	switch integer {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("unexpected boolean value %d", integer)
	}
}

var writeRedisScript = redis.NewScript(`
local fifoKey = KEYS[1]
local chargedKey = KEYS[2]
local idemKey = KEYS[3]
local instanceKey = KEYS[4]
local identity = ARGV[1]
local publicKey = ARGV[2]
local payload = ARGV[3]
local charge = tonumber(ARGV[4])
local capacity = tonumber(ARGV[5])
local trigger = tonumber(ARGV[6])
local baseTarget = tonumber(ARGV[7])
local messageTarget = tonumber(ARGV[8])

local existingID = redis.call('HGET', idemKey, identity)
if existingID then
  return {existingID, 1, 0, 0, 0, 0}
end

local currentTotal = tonumber(redis.call('GET', chargedKey) or '0')
if currentTotal + charge > capacity then
  return {'', 0, currentTotal, 1, baseTarget, 1}
end

local entryID = redis.call('XADD', fifoKey, '*', 'i', instanceKey, 'd', identity, 'c', charge)
redis.call('XADD', instanceKey, entryID, 'v', payload, 'k', publicKey)
redis.call('HSET', idemKey, identity, entryID)
local total = redis.call('INCRBY', chargedKey, charge)
local needsTrim = 0
if total >= trigger then needsTrim = 1 end
return {entryID, 0, total, needsTrim, messageTarget, 0}
`)

var trimRedisScript = redis.NewScript(`
local fifoKey = KEYS[1]
local chargedKey = KEYS[2]
local idemKey = KEYS[3]
local leaseKey = KEYS[4]
local target = tonumber(ARGV[1])
local trimLimit = tonumber(ARGV[2])
local owner = ARGV[3]

if redis.call('GET', leaseKey) ~= owner then return {-1, 0} end
local total = tonumber(redis.call('GET', chargedKey) or '0')
local trimmed = 0
while total > target and trimmed < trimLimit do
  local entries = redis.call('XRANGE', fifoKey, '-', '+', 'COUNT', 1)
  if #entries == 0 then
    break
  end
  local oldID = entries[1][1]
  local fields = entries[1][2]
  local oldInstance = nil
  local oldIdentity = nil
  local oldCharge = 0
  for index = 1, #fields, 2 do
    if fields[index] == 'i' then oldInstance = fields[index + 1] end
    if fields[index] == 'd' then oldIdentity = fields[index + 1] end
    if fields[index] == 'c' then oldCharge = tonumber(fields[index + 1]) end
  end
  if oldInstance then
    redis.call('XDEL', oldInstance, oldID)
    if redis.call('XLEN', oldInstance) == 0 then redis.call('DEL', oldInstance) end
  end
  if oldIdentity and redis.call('HGET', idemKey, oldIdentity) == oldID then
    redis.call('HDEL', idemKey, oldIdentity)
  end
  redis.call('XDEL', fifoKey, oldID)
  total = redis.call('DECRBY', chargedKey, oldCharge)
  if total < 0 then
    total = 0
    redis.call('DEL', fifoKey, chargedKey, idemKey)
  end
  trimmed = trimmed + 1
end
return {total, trimmed}
`)

var renewRedisLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

var releaseRedisLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
