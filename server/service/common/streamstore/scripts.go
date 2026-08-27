// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package streamstore

import "github.com/redis/go-redis/v9"

const (
	syncTrimBatchSize       = 64
	backgroundTrimBatchSize = 256
)

var writeScript = redis.NewScript(`
local fifoKey = KEYS[1]
local chargedKey = KEYS[2]
local idemKey = KEYS[3]
local instanceKey = KEYS[4]
local identity = ARGV[1]
local publicKey = ARGV[2]
local payload = ARGV[3]
local charge = tonumber(ARGV[4])
local capacity = tonumber(ARGV[5])
local baseTarget = tonumber(ARGV[6])
local messageTarget = tonumber(ARGV[7])
local ttlMillis = tonumber(ARGV[8])
local trimLimit = tonumber(ARGV[9])

local function refreshTTL()
  if ttlMillis > 0 then
    redis.call('PEXPIRE', fifoKey, ttlMillis)
    redis.call('PEXPIRE', chargedKey, ttlMillis)
    redis.call('PEXPIRE', idemKey, ttlMillis)
    redis.call('PEXPIRE', instanceKey, ttlMillis)
  end
end

local existingID = redis.call('HGET', idemKey, identity)
if existingID then
  local retained = redis.call('XRANGE', instanceKey, existingID, existingID, 'COUNT', 1)
  if #retained > 0 then
    refreshTTL()
    local existingTotal = tonumber(redis.call('GET', chargedKey) or '0')
    local existingNeedsTrim = 0
    if existingTotal > capacity then existingNeedsTrim = 1 end
    return {existingID, 1, existingTotal, existingNeedsTrim, baseTarget, 0}
  end
  redis.call('HDEL', idemKey, identity)
end

if charge > capacity then return {'', 0, tonumber(redis.call('GET', chargedKey) or '0'), 0, baseTarget, 1} end

local entryID = redis.call('XADD', fifoKey, '*', 'i', instanceKey, 'd', identity, 'c', charge)
redis.call('XADD', instanceKey, entryID, 'v', payload, 'k', publicKey)
redis.call('HSET', idemKey, identity, entryID)
local total = redis.call('INCRBY', chargedKey, charge)

local trimmed = 0
local mustTrim = total > capacity
while mustTrim and total > messageTarget and trimmed < trimLimit do
  local entries = redis.call('XRANGE', fifoKey, '-', '+', 'COUNT', 1)
  if #entries == 0 then
    total = 0
    redis.call('SET', chargedKey, 0)
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
    redis.call('SET', chargedKey, 0)
  end
  trimmed = trimmed + 1
end

refreshTTL()
local needsTrim = 0
if mustTrim and total > messageTarget then needsTrim = 1 end
return {entryID, 0, total, needsTrim, messageTarget, 0}
`)

var trimScript = redis.NewScript(`
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
    total = 0
    redis.call('DEL', fifoKey, chargedKey, idemKey)
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

var renewLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

var releaseLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
