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
}

type methodLockHolder struct {
	resourceID string
	instance   string
}

type rpcLockHolder struct {
	rpcName  string
	instance string
}

// addLockedMethodExternalCallWarnings warns when a marked Step method shares an RPC lock.
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
		call := marker.Calls[0]
		callee := call.Callee
		if len(marker.Calls) > 1 {
			callee = fmt.Sprintf("%s (and %d more)", callee, len(marker.Calls)-1)
		}
		graph.AddDiagnostic("warning", "locked_method_external_call", fmt.Sprintf(
			"%s of %s holds lock %s, which RPC %s also takes, and calls %s; Dex fails that RPC while the lock is held. Make the call in the next, unlocked Step.",
			stepMethodLabel(marker.Phase), graph.nodeName(marker.NodeID), lockName, rpcName, callee,
		), call.Span)
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
