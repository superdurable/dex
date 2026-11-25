// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowdefinitiondiff

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/superdurable/dex/cli/internal/flowviz"
)

var (
	integerLiteralPattern  = regexp.MustCompile(`^[+-]?[0-9]+$`)
	keywordArgumentPattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=([^=].*)$`)
)

// channelWaitMethod is the wait method of a Channel condition, decoded from the analyzer label.
type channelWaitMethod struct {
	name string
	// parameterNames lists the count parameters in call order, after any ChannelMap instance.
	parameterNames []string
	// labelValues holds the count texts the label shows, used when the call passed no argument.
	labelValues []string
}

// describeChannelWait returns a comparison key for one Channel wait condition.
// The key holds the registered Channel name, the wait method, the ChannelMap instance, and each count.
// Positional and keyword arguments bind to the same parameter. A literal keeps its value.
// Any other argument becomes "?", so renaming a variable does not change the key.
// A label that names no known wait method falls back to the label text.
func describeChannelWait(condition flowviz.WaitCondition, resource flowviz.Node) string {
	method, isKnown := decodeChannelWaitMethod(condition.Label)
	if !isKnown {
		return fmt.Sprintf("channel %s %s", resource.Name, condition.Label)
	}
	positional, keywords := splitCallArguments(condition.Expression)
	instance := ""
	if resource.Resource != nil && resource.Resource.Map && len(positional) > 0 {
		instance, positional = describeLiteral(positional[0]), positional[1:]
	}
	if value, exists := keywords["instance"]; exists {
		instance = describeLiteral(value)
		delete(keywords, "instance")
	}
	parts := []string{"channel", resource.Name}
	if instance != "" {
		parts = append(parts, "instance="+instance)
	}
	parts = append(parts, method.name)
	for index, parameterName := range method.parameterNames {
		value := ""
		switch keywordValue, exists := keywords[parameterName]; {
		case index < len(positional):
			value = describeLiteral(positional[index])
		case exists:
			value = describeLiteral(keywordValue)
		case index < len(method.labelValues) && method.labelValues[index] != "N":
			value = describeLiteral(method.labelValues[index])
		}
		delete(keywords, parameterName)
		if value != "" {
			parts = append(parts, parameterName+"="+value)
		}
	}
	for _, name := range sortedKeys(keywords) {
		parts = append(parts, name+"="+describeLiteral(keywords[name]))
	}
	return strings.Join(parts, " ")
}

// decodeChannelWaitMethod reads the method from the text after the last ".for ", ".at least ", or ".at most ".
func decodeChannelWaitMethod(label string) (channelWaitMethod, bool) {
	start := -1
	for _, marker := range []string{".for ", ".at least ", ".at most "} {
		start = max(start, strings.LastIndex(label, marker))
	}
	if start < 0 {
		return channelWaitMethod{}, false
	}
	suffix := label[start+1:]
	count := []string{"count"}
	switch {
	case suffix == "for all branches", suffix == "for first branch":
		return channelWaitMethod{name: suffix}, true
	case strings.HasPrefix(suffix, "for remaining branches after "):
		return channelWaitMethod{name: "for remaining branches after", parameterNames: count,
			labelValues: []string{strings.TrimPrefix(suffix, "for remaining branches after ")}}, true
	case strings.HasSuffix(suffix, " branches"):
		return channelWaitMethod{name: "for branches", parameterNames: count,
			labelValues: []string{strings.TrimSuffix(strings.TrimPrefix(suffix, "for "), " branches")}}, true
	case strings.HasPrefix(suffix, "for ") && strings.Contains(suffix, "…"):
		bounds := strings.SplitN(strings.TrimPrefix(suffix, "for "), "…", 2)
		return channelWaitMethod{name: "for range", parameterNames: []string{"at_least", "at_most"}, labelValues: bounds}, true
	case strings.HasPrefix(suffix, "at least "):
		return channelWaitMethod{name: "at least", parameterNames: count, labelValues: []string{strings.TrimPrefix(suffix, "at least ")}}, true
	case strings.HasPrefix(suffix, "at most "):
		return channelWaitMethod{name: "at most", parameterNames: count, labelValues: []string{strings.TrimPrefix(suffix, "at most ")}}, true
	default:
		return channelWaitMethod{name: "for n", parameterNames: count, labelValues: []string{strings.TrimPrefix(suffix, "for ")}}, true
	}
}

// splitCallArguments splits the analyzer's argument text at top-level commas into positional and keyword arguments.
func splitCallArguments(expression string) ([]string, map[string]string) {
	var arguments []string
	depth, quote, start := 0, rune(0), 0
	for index, character := range expression {
		switch {
		case quote != 0:
			if character == quote && (index == 0 || expression[index-1] != '\\') {
				quote = 0
			}
		case character == '"' || character == '\'' || character == '`':
			quote = character
		case strings.ContainsRune("([{", character):
			depth++
		case strings.ContainsRune(")]}", character):
			depth--
		case character == ',' && depth == 0:
			arguments = append(arguments, expression[start:index])
			start = index + 1
		}
	}
	arguments = append(arguments, expression[start:])
	positional := make([]string, 0, len(arguments))
	keywords := make(map[string]string)
	for _, argument := range arguments {
		argument = strings.TrimSpace(argument)
		if argument == "" {
			continue
		}
		if match := keywordArgumentPattern.FindStringSubmatch(argument); match != nil {
			keywords[match[1]] = strings.TrimSpace(match[2])
			continue
		}
		positional = append(positional, argument)
	}
	return positional, keywords
}

// describeLiteral returns an integer or quoted string literal in one canonical form, or "?" for any other expression.
func describeLiteral(text string) string {
	text = strings.TrimSpace(text)
	if integerLiteralPattern.MatchString(text) {
		value, err := strconv.ParseInt(text, 10, 64)
		if err == nil {
			return strconv.FormatInt(value, 10)
		}
		return text
	}
	if text == "∞" {
		return text
	}
	if len(text) >= 2 {
		first, last := text[0], text[len(text)-1]
		if first == last && (first == '"' || first == '\'' || first == '`') {
			return strconv.Quote(text[1 : len(text)-1])
		}
	}
	return "?"
}

func sortedStrings(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
