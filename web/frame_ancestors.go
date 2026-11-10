// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

const selfFrameAncestor = "'self'"

// frameAncestorHostSourcePattern accepts an explicit CSP host-source; "*" may only start a subdomain wildcard.
var frameAncestorHostSourcePattern = regexp.MustCompile(
	`^(?:[A-Za-z][A-Za-z0-9+.-]*://)?(?:\*\.)?[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*(?::(?:[0-9]{1,5}|\*))?(?:/[A-Za-z0-9._~!$&()*+=:@%/-]*)?$`,
)

// ValidateFrameAncestors checks the CSP frame-ancestors sources that may embed Dex Web in a frame.
//
// Each entry is 'self' or an explicit host source, such as https://host.example, https://*.host.example, or
// https://host.example:8443. Bare "*", scheme-only sources such as "https:", 'none', and entries with
// whitespace, commas, semicolons, or quotes are rejected. An empty list keeps the default, which forbids framing.
//
// It returns nil when every entry is valid, or an error that names the first invalid entry.
func ValidateFrameAncestors(sources []string) error {
	for _, source := range sources {
		if source == selfFrameAncestor {
			continue
		}
		if !frameAncestorHostSourcePattern.MatchString(source) {
			return fmt.Errorf("frame ancestor %q must be 'self' or an explicit host source such as https://host.example", source)
		}
	}
	return nil
}

// SplitFrameAncestors turns a --frame-ancestors value into Config.FrameAncestors entries.
// Commas and whitespace both separate entries, and empty entries are dropped.
func SplitFrameAncestors(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool {
		return character == ',' || character == ' ' || character == '\t' || character == '\n' || character == '\r'
	})
}

// framePolicy holds the anti-framing headers for Dex Web pages.
type framePolicy struct {
	contentSecurityPolicy string
	// frameOptions is empty when X-Frame-Options cannot express the allowlist, so the header is left out.
	frameOptions string
}

func newFramePolicy(sources []string) (framePolicy, error) {
	if err := ValidateFrameAncestors(sources); err != nil {
		return framePolicy{}, err
	}
	if len(sources) == 0 {
		return framePolicy{contentSecurityPolicy: "frame-ancestors 'none'", frameOptions: "DENY"}, nil
	}
	policy := framePolicy{contentSecurityPolicy: "frame-ancestors " + strings.Join(sources, " ")}
	if len(sources) == 1 && sources[0] == selfFrameAncestor {
		policy.frameOptions = "SAMEORIGIN"
	}
	return policy, nil
}

func (policy framePolicy) apply(header http.Header) {
	header.Set("Content-Security-Policy", policy.contentSecurityPolicy)
	if policy.frameOptions != "" {
		header.Set("X-Frame-Options", policy.frameOptions)
	}
}
