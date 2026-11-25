// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"net/url"
	"strings"
)

const (
	requiredLockMethodWaitFor = "WaitFor"
	requiredLockMethodExecute = "Execute"
	requiredLockMethodRPC     = "RPC"
)

type lockRequirementMode uint8

const (
	// lockRequirementNone declares no requirement.
	lockRequirementNone lockRequirementMode = iota
	// lockRequirementOwnKey requires the lock of the written Attribute or AttributeMap instance.
	lockRequirementOwnKey
	// lockRequirementAttribute requires the lock of the singleton Attribute lockAttributeName.
	lockRequirementAttribute
)

// lockRequirement is a declared write or publish lock; lockAttributeName applies only to lockRequirementAttribute.
type lockRequirement struct {
	mode              lockRequirementMode
	lockAttributeName string
}

// requiredLockHandler describes one handler invocation and the lock keys its registration declares.
type requiredLockHandler struct {
	flowType string
	stepType string
	method   string
	rpcName  string
	lockKeys []string
}

func stepRequiredLockHandler(flow *registeredFlow, step *registeredStep, method string) requiredLockHandler {
	handler := requiredLockHandler{flowType: flow.flowType, stepType: step.stepType, method: method}
	if method == requiredLockMethodWaitFor {
		handler.lockKeys = step.waitForLockKeys
	} else {
		handler.lockKeys = step.executeLockKeys
	}
	return handler
}

func rpcRequiredLockHandler(flow *registeredFlow, rpc *registeredRPC) requiredLockHandler {
	return requiredLockHandler{
		flowType: flow.flowType,
		method:   requiredLockMethodRPC,
		rpcName:  rpc.durableName,
		lockKeys: rpc.lockKeys,
	}
}

// requiredLockAccess is one buffered write or publish whose definition declares a required lock.
type requiredLockAccess struct {
	access  MissingAttributeLockAccess
	lockKey string
}

// checkRequiredAttributeLocks runs after a handler returned successfully and before its result is sent.
// Only registration-time locks count; per-call locks never reach the Worker.
func checkRequiredAttributeLocks(handler requiredLockHandler, invocation *invocationContext) error {
	accesses := invocation.requiredLockAccesses()
	if len(accesses) == 0 {
		return nil
	}
	heldLocks := stringSet(handler.lockKeys)
	var missing []MissingAttributeLockAccess
	for _, access := range accesses {
		if _, held := heldLocks[access.lockKey]; !held {
			missing = append(missing, access.access)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingAttributeLockError{
		FlowType: handler.flowType,
		StepType: handler.stepType,
		Method:   handler.method,
		RPCName:  handler.rpcName,
		Accesses: missing,
	}
}

// requiredLockAccesses lists writes and publishes of declared definitions, in write then publish order.
func (invocation *invocationContext) requiredLockAccesses() []requiredLockAccess {
	var accesses []requiredLockAccess
	for _, physical := range invocation.attributeWriteOrder {
		name, instance := splitPhysicalName(physical)
		attribute, found := invocation.flow.attributes[name]
		if !found || attribute.writeLock.mode == lockRequirementNone {
			continue
		}
		access := MissingAttributeLockAccess{Kind: "Attribute", Name: name, Instance: instance}
		if attribute.isMap {
			access.Kind = "AttributeMap"
		}
		lockKey := attribute.writeLock.lockAttributeName
		access.LockAttributeName = attribute.writeLock.lockAttributeName
		if attribute.writeLock.mode == lockRequirementOwnKey {
			lockKey = physical
			access.LockAttributeName = name
			access.LockInstance = instance
		}
		accesses = append(accesses, requiredLockAccess{access: access, lockKey: lockKey})
	}
	published := make(map[string]struct{})
	for _, publication := range invocation.publications {
		physical := publication.GetChannelName()
		if _, seen := published[physical]; seen {
			continue
		}
		published[physical] = struct{}{}
		name, instance := splitPhysicalName(physical)
		channel, found := invocation.flow.channels[name]
		if !found || channel.publishLock.mode == lockRequirementNone {
			continue
		}
		kind := "Channel"
		if channel.isMap {
			kind = "ChannelMap"
		}
		accesses = append(accesses, requiredLockAccess{
			access: MissingAttributeLockAccess{
				Kind:              kind,
				Name:              name,
				Instance:          instance,
				LockAttributeName: channel.publishLock.lockAttributeName,
			},
			lockKey: channel.publishLock.lockAttributeName,
		})
	}
	return accesses
}

// splitPhysicalName returns the definition name and the decoded map instance of a stored key.
func splitPhysicalName(physical string) (string, string) {
	name, escapedInstance, isMap := strings.Cut(physical, "/")
	if !isMap {
		return name, ""
	}
	instance, err := url.PathUnescape(escapedInstance)
	if err != nil {
		return name, escapedInstance
	}
	return name, instance
}
