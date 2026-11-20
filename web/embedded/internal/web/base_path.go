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
	"path"
	"strings"
)

// maxBasePathLength bounds a base path so it stays a small part of every URL Dex Web builds.
const maxBasePathLength = 1024

// ValidateBasePath checks a Dex Web mount prefix and returns its canonical form.
//
// An empty value and "/" both mean the root. A trailing slash is removed, so "/dex/" becomes "/dex". Every
// segment must be non-empty, must not be "." or "..", and may contain only ASCII letters, digits, '.', '_',
// '~', and '-'. Percent-encoding is therefore rejected, which keeps encoded separators out of the prefix.
//
// It returns the canonical base path, or an error that names the rule the value breaks.
func ValidateBasePath(value string) (string, error) {
	if value == "" || value == "/" {
		return "/", nil
	}
	if len(value) > maxBasePathLength {
		return "", fmt.Errorf("base path must be at most %d bytes", maxBasePathLength)
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("base path %q must start with /", value)
	}
	canonical := strings.TrimSuffix(value, "/")
	for _, segment := range strings.Split(canonical[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("base path %q must not contain empty, '.', or '..' segments", value)
		}
		for _, character := range segment {
			if !isBasePathCharacter(character) {
				return "", fmt.Errorf("base path %q may contain only letters, digits, '.', '_', '~', '-', and '/'", value)
			}
		}
	}
	return canonical, nil
}

func isBasePathCharacter(character rune) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9') || character == '.' || character == '_' || character == '~' || character == '-'
}

// joinBasePath prefixes a rooted Dex Web path, such as "/v1/connectors", with a canonical base path.
func joinBasePath(basePath string, rootedPath string) string {
	if basePath == "/" {
		return rootedPath
	}
	return basePath + rootedPath
}

// serveUnderBasePath strips a canonical base path from the request and serves the rest with next.
// A request outside the base path, or with dot or empty segments under it, gets HTTP 404.
func serveUnderBasePath(response http.ResponseWriter, request *http.Request, basePath string, next http.Handler) {
	if basePath == "/" {
		next.ServeHTTP(response, request)
		return
	}
	requestPath := request.URL.Path
	if requestPath == basePath && request.URL.EscapedPath() == basePath {
		redirectToBasePathRoot(response, request, basePath)
		return
	}
	innerPath, isUnderBasePath := strings.CutPrefix(requestPath, basePath+"/")
	_, isEscapedUnderBasePath := strings.CutPrefix(request.URL.EscapedPath(), basePath+"/")
	if !isUnderBasePath || !isEscapedUnderBasePath || !isCleanRequestPath("/"+innerPath) {
		http.NotFound(response, request)
		return
	}
	http.StripPrefix(basePath, next).ServeHTTP(response, request)
}

// redirectToBasePathRoot sends a page request for "/dex" to "/dex/" so relative browser state stays under it.
func redirectToBasePathRoot(response http.ResponseWriter, request *http.Request, basePath string) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.NotFound(response, request)
		return
	}
	location := basePath + "/"
	if request.URL.RawQuery != "" {
		location += "?" + request.URL.RawQuery
	}
	http.Redirect(response, request, location, http.StatusMovedPermanently)
}

// isCleanRequestPath rejects dot and empty segments, which ServeMux would redirect outside the base path.
func isCleanRequestPath(requestPath string) bool {
	cleaned := path.Clean(requestPath)
	return cleaned == requestPath || cleaned+"/" == requestPath
}
