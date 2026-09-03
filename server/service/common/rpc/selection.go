// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package rpc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
)

// StateSelection identifies collection values loaded for one RPC.
type StateSelection struct {
	AttributeMapNames []string
	ChannelNames      []string
	ChannelMapNames   []string
}

// NormalizeStateSelection validates and sorts RPC collection selectors.
func NormalizeStateSelection(
	attributeMapNames []string,
	channelNames []string,
	channelMapNames []string,
) (StateSelection, error) {
	normalizedAttributeMapNames, err := normalizeDefinitionNames(
		"AttributeMap",
		attributeMapNames,
	)
	if err != nil {
		return StateSelection{}, err
	}
	normalizedChannelNames, err := normalizeDefinitionNames("Channel", channelNames)
	if err != nil {
		return StateSelection{}, err
	}
	normalizedChannelMapNames, err := normalizeDefinitionNames(
		"ChannelMap",
		channelMapNames,
	)
	if err != nil {
		return StateSelection{}, err
	}
	return StateSelection{
		AttributeMapNames: normalizedAttributeMapNames,
		ChannelNames:      normalizedChannelNames,
		ChannelMapNames:   normalizedChannelMapNames,
	}, nil
}

// NormalizeInvokeRequestSelection validates and normalizes one public request.
func NormalizeInvokeRequestSelection(request *dexpb.InvokeRPCRequest) (StateSelection, error) {
	selection, err := NormalizeStateSelection(
		request.GetLoadAttributeMapNames(),
		request.GetLoadChannelNames(),
		request.GetLoadChannelMapNames(),
	)
	if err != nil {
		return StateSelection{}, err
	}
	request.LoadAttributeMapNames = selection.AttributeMapNames
	request.LoadChannelNames = selection.ChannelNames
	request.LoadChannelMapNames = selection.ChannelMapNames
	return selection, nil
}

// NormalizePrepareRequestSelection validates one internal query request.
func NormalizePrepareRequestSelection(
	request *dexpb.PrepareRpcQueryRequest,
) (StateSelection, error) {
	return NormalizeStateSelection(
		request.GetLoadAttributeMapNames(),
		request.GetLoadChannelNames(),
		request.GetLoadChannelMapNames(),
	)
}

func normalizeDefinitionNames(kind string, names []string) ([]string, error) {
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("RPC load %s name is empty", kind)
		}
		if strings.Contains(name, "/") {
			return nil, fmt.Errorf("RPC load %s name %q contains '/'", kind, name)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("RPC load %s name %q is duplicated", kind, name)
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	sort.Strings(normalized)
	return normalized, nil
}
