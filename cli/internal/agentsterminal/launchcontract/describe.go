// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package launchcontract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"unicode/utf8"
)

// DescribeDocument is the `dexcli agents-tui --describe` output.
type DescribeDocument struct {
	// DexcliVersion is the dexcli version.
	DexcliVersion string `json:"dexcli_version"`
	// Contracts maps each contract version, as a decimal string, to what
	// dexcli runs for it.
	Contracts map[string]DescribedContract `json:"contracts"`
}

// DescribedContract lists the modes and capabilities of one contract.
type DescribedContract struct {
	// Modes maps each mode dexcli runs to the methods it may call.
	Modes map[string]DescribedMode `json:"modes"`
	// Capabilities are the launch document capabilities dexcli reads.
	Capabilities []string `json:"capabilities"`
}

// DescribedMode lists the local request methods one mode may call.
type DescribedMode struct {
	// UsesMethods are the methods the mode may call.
	UsesMethods []string `json:"uses_methods"`
}

// modeMethods are the methods each mode of contract 1 may call.
var modeMethods = map[string][]string{
	ModeChat:       {MethodPutImage, MethodStartSession, MethodListConnectors, MethodOpenURL, MethodGetRequestResult},
	ModeSessions:   {},
	ModeConnectors: {MethodListConnectors, MethodOpenURL},
}

// NewDescribeDocument describes the modes dexcli runs.
func NewDescribeDocument(dexcliVersion string, supported SupportedModes) DescribeDocument {
	document := DescribeDocument{DexcliVersion: dexcliVersion, Contracts: map[string]DescribedContract{}}
	for contract, modes := range supported {
		described := DescribedContract{Modes: map[string]DescribedMode{}, Capabilities: []string{"images", "plan_mode", "turn_control", "reasoning_effort"}}
		for _, mode := range modes {
			described.Modes[mode] = DescribedMode{UsesMethods: slices.Clone(modeMethods[mode])}
		}
		document.Contracts[strconv.Itoa(contract)] = described
	}
	return document
}

var contractKeyPattern = regexp.MustCompile(`^[1-9][0-9]*\z`)

// CheckDescribeOutput checks --describe stdout against
// terminal-ui-describe.schema.json and decodes it.
//
// output is the whole stdout, which holds one JSON object and a newline.
func CheckDescribeOutput(output []byte) (DescribeDocument, error) {
	if len(output) > MaxLineBytes+1 || !utf8.Valid(output) {
		return DescribeDocument{}, fmt.Errorf("describe output is not UTF-8 of at most 1 MiB")
	}
	value, err := decodeJSONValue(output)
	if err != nil {
		return DescribeDocument{}, fmt.Errorf("describe output is not one JSON value: %w", err)
	}
	if violation := checkDescribeSchema(value); violation != nil {
		return DescribeDocument{}, violation
	}
	var document DescribeDocument
	if err := json.Unmarshal(output, &document); err != nil {
		return DescribeDocument{}, fmt.Errorf("decode describe output: %w", err)
	}
	return document, nil
}

func checkDescribeSchema(value any) *SchemaViolation {
	rules := &jsonRules{}
	document, isObject := rules.object("", value)
	if !isObject || !rules.required("", document, "dexcli_version", "contracts") {
		return rules.violation
	}
	rules.string("/dexcli_version", document["dexcli_version"], 1)
	contracts, isObject := rules.object("/contracts", document["contracts"])
	if !isObject {
		return rules.violation
	}
	for _, key := range sortedKeys(contracts) {
		path := "/contracts/" + key
		if !contractKeyPattern.MatchString(key) {
			rules.fail("/contracts", "propertyNames")
			return rules.violation
		}
		contract, isObject := rules.object(path, contracts[key])
		if !isObject || !rules.required(path, contract, "modes", "capabilities") {
			return rules.violation
		}
		modes, isObject := rules.object(path+"/modes", contract["modes"])
		if !isObject {
			return rules.violation
		}
		for _, mode := range sortedKeys(modes) {
			modePath := path + "/modes/" + mode
			if !lowercaseNamePattern.MatchString(mode) {
				rules.fail(path+"/modes", "propertyNames")
				return rules.violation
			}
			described, isObject := rules.object(modePath, modes[mode])
			if !isObject || !rules.required(modePath, described, "uses_methods") {
				return rules.violation
			}
			checkMethodNames(rules, modePath+"/uses_methods", described["uses_methods"])
		}
		checkMethodNames(rules, path+"/capabilities", contract["capabilities"])
	}
	return rules.violation
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ContractChoice is the contract a launcher hands off with, or the reason it
// uses the plain chat.
type ContractChoice struct {
	// Contract is the chosen version, or 0.
	Contract int
	// PlainReason is "no_common_contract" or "mode_missing" when Contract is 0.
	PlainReason string
}

// ChooseContract returns the highest contract both sides support that has
// mode, as a launcher chooses it.
func ChooseContract(launcherContracts []int, describe DescribeDocument, mode string) ContractChoice {
	var common []int
	for key := range describe.Contracts {
		version, err := strconv.Atoi(key)
		if err == nil && slices.Contains(launcherContracts, version) {
			common = append(common, version)
		}
	}
	if len(common) == 0 {
		return ContractChoice{PlainReason: "no_common_contract"}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(common)))
	for _, version := range common {
		if _, hasMode := describe.Contracts[strconv.Itoa(version)].Modes[mode]; hasMode {
			return ContractChoice{Contract: version}
		}
	}
	return ContractChoice{PlainReason: "mode_missing"}
}
