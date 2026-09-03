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

// ValidateAndSortSelections validates and sorts RPC collection selectors.
func ValidateAndSortSelections(
	attributeMapNames []string,
	channelNames []string,
	channelMapNames []string,
) (RPCStateSelection, error) {
	sortedAttributeMapNames, err := validateAndSortDefinitionNames(
		"AttributeMap",
		attributeMapNames,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	sortedChannelNames, err := validateAndSortDefinitionNames("Channel", channelNames)
	if err != nil {
		return RPCStateSelection{}, err
	}
	sortedChannelMapNames, err := validateAndSortDefinitionNames(
		"ChannelMap",
		channelMapNames,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	return RPCStateSelection{
		AttributeMapNames: sortedAttributeMapNames,
		ChannelNames:      sortedChannelNames,
		ChannelMapNames:   sortedChannelMapNames,
	}, nil
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
