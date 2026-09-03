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
	AttributeMapSelectors []string
	ChannelNames          []string
	ChannelMapSelectors   []string
}

// ValidateAndSortSelections validates and sorts RPC collection selectors.
func ValidateAndSortSelections(
	attributeMapSelectors []string,
	channelNames []string,
	channelMapSelectors []string,
) (RPCStateSelection, error) {
	sortedAttributeMapSelectors, err := validateAndSortMapSelectors(
		"AttributeMap",
		attributeMapSelectors,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	sortedChannelNames, err := validateAndSortDefinitionNames("Channel", channelNames)
	if err != nil {
		return RPCStateSelection{}, err
	}
	sortedChannelMapSelectors, err := validateAndSortMapSelectors(
		"ChannelMap",
		channelMapSelectors,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	return RPCStateSelection{
		AttributeMapSelectors: sortedAttributeMapSelectors,
		ChannelNames:          sortedChannelNames,
		ChannelMapSelectors:   sortedChannelMapSelectors,
	}, nil
}

func validateAndSortMapSelectors(kind string, selectors []string) ([]string, error) {
	sortedSelectors := make([]string, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		if strings.TrimSpace(selector) == "" {
			return nil, fmt.Errorf("RPC load %s selector is empty", kind)
		}
		separatorIndex := strings.IndexByte(selector, '/')
		if separatorIndex <= 0 || separatorIndex != strings.LastIndexByte(selector, '/') {
			return nil, fmt.Errorf(
				"RPC load %s selector %q must contain one '/' after the definition name",
				kind,
				selector,
			)
		}
		if _, exists := seen[selector]; exists {
			return nil, fmt.Errorf("RPC load %s selector %q is duplicated", kind, selector)
		}
		seen[selector] = struct{}{}
		sortedSelectors = append(sortedSelectors, selector)
	}
	sort.Strings(sortedSelectors)
	return sortedSelectors, nil
}

func validateAndSortDefinitionNames(kind string, names []string) ([]string, error) {
	sortedNames := make([]string, 0, len(names))
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
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)
	return sortedNames, nil
}
