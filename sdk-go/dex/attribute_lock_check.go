// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

const (
	attributeLockCheckMethodWaitFor = "WaitFor"
	attributeLockCheckMethodExecute = "Execute"
	attributeLockCheckMethodRPC     = "RPC"
)

// attributeLockCheckHandler describes one handler invocation and the locks and loads its registration declares.
type attributeLockCheckHandler struct {
	flowType string
	stepType string
	method   string
	rpcName  string
	locks    []AttributeLock
	loads    stateLoads
}

// attributeLockCheckReportKey deduplicates warnings; a struct avoids collisions between joined names.
type attributeLockCheckReportKey struct {
	flowType      string
	method        string
	stepType      string
	rpcName       string
	attributeName string
}

// unlockedAttributeReadModifyWriteCheck reports Attributes that one invocation read and wrote without a lock.
type unlockedAttributeReadModifyWriteCheck struct {
	failsInvocation    bool
	logger             Logger
	reportedAttributes sync.Map
	detectionCount     atomic.Uint64
}

func newUnlockedAttributeReadModifyWriteCheck(
	failsInvocation bool,
	logger Logger,
) *unlockedAttributeReadModifyWriteCheck {
	return &unlockedAttributeReadModifyWriteCheck{
		failsInvocation: failsInvocation,
		logger:          resolveLogger(logger, nil),
	}
}

// checkInvocation runs after a handler returned successfully and before its result is sent.
// It returns UnlockedAttributeReadModifyWriteError only when the Worker fails invocations on a detection.
func (check *unlockedAttributeReadModifyWriteCheck) checkInvocation(
	handler attributeLockCheckHandler,
	invocation *invocationContext,
) error {
	candidates := invocation.attributeReadModifyWriteCandidates()
	if len(candidates) == 0 {
		return nil
	}
	lockKeys, lockErr := mapAttributeLocks(handler.locks)
	registeredMapLoads, _, _, loadErr := validateStateLoads(invocation.flow, handler.loads)
	if err := errors.Join(lockErr, loadErr); err != nil {
		// Registration validated these options, so this is unexpected; only strict mode fails on it.
		if check.failsInvocation {
			return fmt.Errorf("dex: map registered Attribute locks and loads: %w", err)
		}
		check.logSafely(func() {
			check.logger.Error("skip unlocked Attribute read-modify-write check",
				"flowType", handler.flowType, "error", err)
		})
		return nil
	}
	lockedKeys := stringSet(lockKeys)
	registeredMapLoadSet := stringSet(registeredMapLoads)
	var unlocked []UnlockedAttributeAccess
	for _, candidate := range candidates {
		if _, locked := lockedKeys[candidate.physical]; locked {
			continue
		}
		// A per-call load may come with a per-call lock that the Worker cannot see.
		if candidate.isMap && !isMapInstanceLoaded(registeredMapLoadSet, candidate.name, candidate.physical) {
			continue
		}
		unlocked = append(unlocked, UnlockedAttributeAccess{
			AttributeName: candidate.name,
			Instance:      candidate.instance,
		})
	}
	if len(unlocked) == 0 {
		return nil
	}
	check.detectionCount.Add(uint64(len(unlocked)))
	detection := &UnlockedAttributeReadModifyWriteError{
		FlowType:   handler.flowType,
		StepType:   handler.stepType,
		Method:     handler.method,
		RPCName:    handler.rpcName,
		Attributes: unlocked,
	}
	if check.failsInvocation {
		return detection
	}
	check.warnOncePerAttribute(detection)
	return nil
}

func (check *unlockedAttributeReadModifyWriteCheck) warnOncePerAttribute(
	detection *UnlockedAttributeReadModifyWriteError,
) {
	handlerName := detection.StepType + "." + detection.Method
	if detection.Method == attributeLockCheckMethodRPC {
		handlerName = "RPC." + detection.RPCName
	}
	for _, access := range detection.Attributes {
		reportKey := attributeLockCheckReportKey{
			flowType:      detection.FlowType,
			method:        detection.Method,
			stepType:      detection.StepType,
			rpcName:       detection.RPCName,
			attributeName: access.AttributeName,
		}
		if _, reported := check.reportedAttributes.LoadOrStore(reportKey, struct{}{}); reported {
			continue
		}
		check.logSafely(func() {
			check.logger.Warn(
				"unlocked Attribute read-modify-write: a concurrent invocation can overwrite this update; "+
					"lock the Attribute in the handler's registered options",
				"flowType", detection.FlowType,
				"handler", handlerName,
				"attribute", access.AttributeName,
				"instance", access.Instance,
			)
		})
	}
}

// logSafely keeps a failing application Logger from failing a successful invocation.
func (check *unlockedAttributeReadModifyWriteCheck) logSafely(log func()) {
	defer func() {
		_ = recover()
	}()
	log()
}

func (check *unlockedAttributeReadModifyWriteCheck) count() uint64 {
	return check.detectionCount.Load()
}

func stepAttributeLockCheckHandler(
	flow *registeredFlow,
	step *registeredStep,
	method string,
) attributeLockCheckHandler {
	handler := attributeLockCheckHandler{
		flowType: flow.flowType,
		stepType: step.stepType,
		method:   method,
	}
	if step.options == nil {
		return handler
	}
	if method == attributeLockCheckMethodWaitFor {
		handler.locks = step.options.WaitForLockAttributes
		handler.loads = waitForStepStateLoads(step.options)
	} else {
		handler.locks = step.options.ExecuteLockAttributes
		handler.loads = executeStepStateLoads(step.options)
	}
	return handler
}

func rpcAttributeLockCheckHandler(
	flow *registeredFlow,
	rpc *registeredRPC,
) attributeLockCheckHandler {
	handler := attributeLockCheckHandler{
		flowType: flow.flowType,
		method:   attributeLockCheckMethodRPC,
		rpcName:  rpc.durableName,
		loads:    rpcStateLoads(rpc.options),
	}
	if rpc.options != nil {
		handler.locks = rpc.options.LockAttributes
	}
	return handler
}
