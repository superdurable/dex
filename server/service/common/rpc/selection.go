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
)

// RPCStateSelection identifies collection values loaded for one RPC.
type RPCStateSelection struct {
	AttributeMapNames []string
	ChannelNames      []string
	ChannelMapNames   []string
}

// NormalizeStateSelection validates and sorts RPC collection selectors.
func NormalizeStateSelection(
	attributeMapNames []string,
	channelNames []string,
	channelMapNames []string,
) (RPCStateSelection, error) {
	normalizedAttributeMapNames, err := normalizeDefinitionNames(
		"AttributeMap",
		attributeMapNames,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	normalizedChannelNames, err := normalizeDefinitionNames("Channel", channelNames)
	if err != nil {
		return RPCStateSelection{}, err
	}
	normalizedChannelMapNames, err := normalizeDefinitionNames(
		"ChannelMap",
		channelMapNames,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	return RPCStateSelection{
		AttributeMapNames: normalizedAttributeMapNames,
		ChannelNames:      normalizedChannelNames,
		ChannelMapNames:   normalizedChannelMapNames,
	}, nil
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
