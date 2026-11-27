// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"cmp"
	"fmt"
	"slices"
)

// methodExternalCalls marks one checked Step method; script analyzers emit "methodExternalCalls".
// Analyzers may mark only methods sharing an RPC lock; unmarked methods never warn.
type methodExternalCalls struct {
	// NodeID is a Step node ID, such as step:SendReminder.
	NodeID string `json:"nodeId"`
	// Phase is execute or wait_for, as in resource_lock metadata.
	Phase string `json:"phase"`
	// Calls is empty when the method is clean.
	Calls []possibleExternalCall `json:"calls"`
}

// possibleExternalCall is a call outside the allowlist, or a method body dexcli cannot read.
type possibleExternalCall struct {
	// Callee is source text, such as client.Send.
	Callee string `json:"callee"`
	Span   *Span  `json:"span,omitempty"`
	// Certainty is externalCallUncertain for a call that may be pure, such as a third-party library; empty means definite.
	Certainty string `json:"certainty,omitempty"`
}

// externalCallUncertain marks a call dexcli cannot classify, such as a third-party function that may do no I/O.
const externalCallUncertain = "uncertain"

func (call possibleExternalCall) isUncertain() bool {
	return call.Certainty == externalCallUncertain
}

type methodLockHolder struct {
	resourceID string
	instance   string
}

type rpcLockHolder struct {
	rpcName  string
	instance string
}

// addLockedMethodExternalCallWarnings reports a marked Step method that shares an RPC lock.
// Definite external calls give a warning; a method with only uncertain calls gives an info diagnostic.
func addLockedMethodExternalCallWarnings(graph *Graph) {
	if len(graph.methodExternalCalls) == 0 {
		return
	}
	methodLocks, rpcLocks := graph.lockHolders(graph.Edges, graph.undrawnLockEdges)
	markers := slices.Clone(graph.methodExternalCalls)
	slices.SortFunc(markers, func(left, right methodExternalCalls) int {
		return cmp.Or(cmp.Compare(left.NodeID, right.NodeID), cmp.Compare(left.Phase, right.Phase))
	})
	for _, marker := range markers {
		if len(marker.Calls) == 0 {
			continue
		}
		lockName, rpcName, isShared := graph.firstLockSharedWithRPC(methodLocks[stepMethodKey(marker.NodeID, marker.Phase)], rpcLocks)
		if !isShared {
			continue
		}
		var definiteCalls, uncertainCalls []possibleExternalCall
		for _, call := range marker.Calls {
			if call.isUncertain() {
				uncertainCalls = append(uncertainCalls, call)
			} else {
				definiteCalls = append(definiteCalls, call)
			}
		}
		methodLabel, stepName := stepMethodLabel(marker.Phase), graph.nodeName(marker.NodeID)
		if len(definiteCalls) > 0 {
			call := definiteCalls[0]
			graph.AddDiagnostic("warning", "locked_method_external_call", fmt.Sprintf(
				"%s of %s holds lock %s, which RPC %s also takes, and calls %s%s; Dex fails that RPC while the lock is held. Make the call in the next, unlocked Step.",
				methodLabel, stepName, lockName, rpcName, call.Callee, definiteCallCountSuffix(len(definiteCalls)-1, len(uncertainCalls)),
			), call.Span)
			continue
		}
		call := uncertainCalls[0]
		graph.AddDiagnostic("info", "locked_method_uncertain_call", fmt.Sprintf(
			"%s of %s holds lock %s, which RPC %s also takes, and calls %s, which dexcli cannot tell whether it does I/O%s. If it calls an external system, Dex fails that RPC while the lock is held; make that call in the next, unlocked Step.",
			methodLabel, stepName, lockName, rpcName, call.Callee, uncertainCallCountSuffix(len(uncertainCalls)-1),
		), call.Span)
	}
}

// definiteCallCountSuffix returns the count text after the first definite call, such as " (and 1 more definite, 2 uncertain)".
func definiteCallCountSuffix(moreDefinite int, uncertain int) string {
	switch {
	case uncertain == 0 && moreDefinite == 0:
		return ""
	case uncertain == 0:
		return fmt.Sprintf(" (and %d more)", moreDefinite)
	case moreDefinite == 0:
		return fmt.Sprintf(" (and %d uncertain)", uncertain)
	default:
		return fmt.Sprintf(" (and %d more definite, %d uncertain)", moreDefinite, uncertain)
	}
}

// uncertainCallCountSuffix returns the count text after the first uncertain call, such as " (and 2 more uncertain calls)".
func uncertainCallCountSuffix(moreUncertain int) string {
	switch moreUncertain {
	case 0:
		return ""
	case 1:
		return " (and 1 more uncertain call)"
	default:
		return fmt.Sprintf(" (and %d more uncertain calls)", moreUncertain)
	}
}

// stepMethodsSharingRPCLocks returns the stepMethodKey of every Step method that shares a lock with an RPC.
func (graph *Graph) stepMethodsSharingRPCLocks(edgeSets ...[]Edge) map[string]bool {
	methodLocks, rpcLocks := graph.lockHolders(edgeSets...)
	shared := make(map[string]bool)
	for key, held := range methodLocks {
		if _, _, isShared := graph.firstLockSharedWithRPC(held, rpcLocks); isShared {
			shared[key] = true
		}
	}
	return shared
}

func stepMethodKey(nodeID string, phase string) string {
	return nodeID + "\x00" + phase
}

// lockHolders returns Step method locks by stepMethodKey and RPC locks by resource ID.
func (graph *Graph) lockHolders(edgeSets ...[]Edge) (map[string][]methodLockHolder, map[string][]rpcLockHolder) {
	methodLocks := make(map[string][]methodLockHolder)
	rpcLocks := make(map[string][]rpcLockHolder)
	for _, edges := range edgeSets {
		for _, edge := range edges {
			if edge.Kind != "resource_lock" {
				continue
			}
			phase, _ := edge.Metadata["phase"].(string)
			instance, _ := edge.Metadata["instance"].(string)
			switch phase {
			case "execute", "wait_for":
				key := stepMethodKey(edge.From, phase)
				methodLocks[key] = append(methodLocks[key], methodLockHolder{resourceID: edge.To, instance: instance})
			case "rpc":
				rpcName, _ := edge.Metadata["rpcName"].(string)
				if rpcName == "" {
					rpcName = graph.nodeName(edge.From)
				}
				rpcLocks[edge.To] = append(rpcLocks[edge.To], rpcLockHolder{rpcName: rpcName, instance: instance})
			}
		}
	}
	return methodLocks, rpcLocks
}

// firstLockSharedWithRPC returns the first shared lock and RPC names, and whether one exists.
// AttributeMap locks conflict unless both name different constant instances.
func (graph *Graph) firstLockSharedWithRPC(held []methodLockHolder, rpcLocks map[string][]rpcLockHolder) (string, string, bool) {
	bestLock, bestRPC := "", ""
	isShared := false
	for _, lock := range held {
		lockName := graph.nodeName(lock.resourceID)
		for _, rpc := range rpcLocks[lock.resourceID] {
			if lock.instance != "" && rpc.instance != "" && lock.instance != rpc.instance {
				continue
			}
			if !isShared || lockName < bestLock || (lockName == bestLock && rpc.rpcName < bestRPC) {
				bestLock, bestRPC, isShared = lockName, rpc.rpcName, true
			}
		}
	}
	return bestLock, bestRPC, isShared
}

func (graph *Graph) nodeName(nodeID string) string {
	for _, node := range graph.Nodes {
		if node.ID == nodeID {
			return node.Name
		}
	}
	return nodeID
}

func stepMethodLabel(phase string) string {
	if phase == "wait_for" {
		return "WaitFor"
	}
	return "Execute"
}
