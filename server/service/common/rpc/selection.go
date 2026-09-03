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
	AttributeMapInstances []string
	ChannelNames          []string
	ChannelMapInstances   []string
}

// ValidateAndSortSelections validates and sorts RPC collection names and instances.
func ValidateAndSortSelections(
	attributeMapInstances []string,
	channelNames []string,
	channelMapInstances []string,
) (RPCStateSelection, error) {
	sortedAttributeMapInstances, err := validateAndSortMapInstances(
		"AttributeMap",
		attributeMapInstances,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	sortedChannelNames, err := validateAndSortDefinitionNames("Channel", channelNames)
	if err != nil {
		return RPCStateSelection{}, err
	}
	sortedChannelMapInstances, err := validateAndSortMapInstances(
		"ChannelMap",
		channelMapInstances,
	)
	if err != nil {
		return RPCStateSelection{}, err
	}
	return RPCStateSelection{
		AttributeMapInstances: sortedAttributeMapInstances,
		ChannelNames:          sortedChannelNames,
		ChannelMapInstances:   sortedChannelMapInstances,
	}, nil
}

func validateAndSortMapInstances(kind string, instances []string) ([]string, error) {
	sortedInstances := make([]string, 0, len(instances))
	seen := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		if strings.TrimSpace(instance) == "" {
			return nil, fmt.Errorf("RPC load %s instance is empty", kind)
		}
		separatorIndex := strings.IndexByte(instance, '/')
		if separatorIndex <= 0 || separatorIndex != strings.LastIndexByte(instance, '/') {
			return nil, fmt.Errorf(
				"RPC load %s instance %q must contain one '/' after the definition name",
				kind,
				instance,
			)
		}
		if _, exists := seen[instance]; exists {
			return nil, fmt.Errorf("RPC load %s instance %q is duplicated", kind, instance)
		}
		seen[instance] = struct{}{}
		sortedInstances = append(sortedInstances, instance)
	}
	sort.Strings(sortedInstances)
	return sortedInstances, nil
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
