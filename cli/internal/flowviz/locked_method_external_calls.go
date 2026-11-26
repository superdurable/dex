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

// methodExternalCalls marks one Step method an analyzer checked; no marker means not checked.
// Script analyzers emit markers in the top-level "methodExternalCalls" output array.
type methodExternalCalls struct {
	// NodeID is the Step node ID, such as step:SendReminder.
	NodeID string `json:"nodeId"`
	// Phase is execute or wait_for, matching the resource_lock edge metadata.
	Phase string `json:"phase"`
	// Calls lists every possible external call; empty means analyzed and clean.
	Calls []possibleExternalCall `json:"calls"`
}

// possibleExternalCall is a call outside the allowlist: Dex SDK, built-ins, pure standard library, pure same-module helpers.
// Dynamic calls, injected dependencies, third-party packages, and helpers past the depth bound count.
type possibleExternalCall struct {
	// Callee is the source text of the call target, such as client.Send.
	Callee string `json:"callee"`
	// Span is the call site in the Step method.
	Span *Span `json:"span,omitempty"`
}

type methodLockHolder struct {
	resourceID string
	instance   string
}

type rpcLockHolder struct {
	rpcName  string
	instance string
}

// addLockedMethodExternalCallWarnings warns when a Step method holds a lock an RPC takes and may call outside Dex.
// It reads resource_lock edges, the analyzer's undrawn lock edges, and the methodExternalCalls markers.
func addLockedMethodExternalCallWarnings(graph *Graph) {
	if len(graph.methodExternalCalls) == 0 {
		return
	}
	methodLocks := make(map[string][]methodLockHolder)
	rpcLocks := make(map[string][]rpcLockHolder)
	for _, edges := range [][]Edge{graph.Edges, graph.undrawnLockEdges} {
		for _, edge := range edges {
			if edge.Kind != "resource_lock" {
				continue
			}
			phase, _ := edge.Metadata["phase"].(string)
			instance, _ := edge.Metadata["instance"].(string)
			switch phase {
			case "execute", "wait_for":
				key := edge.From + "\x00" + phase
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
	markers := slices.Clone(graph.methodExternalCalls)
	slices.SortFunc(markers, func(left, right methodExternalCalls) int {
		return cmp.Or(cmp.Compare(left.NodeID, right.NodeID), cmp.Compare(left.Phase, right.Phase))
	})
	for _, marker := range markers {
		if len(marker.Calls) == 0 {
			continue
		}
		lockName, rpcName, isShared := graph.firstLockSharedWithRPC(methodLocks[marker.NodeID+"\x00"+marker.Phase], rpcLocks)
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

// firstLockSharedWithRPC returns the alphabetically first lock and RPC name that the method shares, and whether one exists.
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
