// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/superdurable/dex/web/api"
)

const (
	forwardedPrefixHeader           = "X-Forwarded-Prefix"
	forwardedEmbeddedHeader         = "X-Dex-Web-Embedded"
	forwardedCSRFTokenHeader        = "X-Dex-Web-CSRF-Token"
	browserCSRFHeader               = "X-CSRF-Token"
	forwardedEmbeddingVaryHeader    = "X-Forwarded-Prefix, X-Dex-Web-Embedded, X-Dex-Web-CSRF-Token"
	maximumForwardedPrefixLength    = 2048
	maximumForwardedCSRFTokenLength = 4096
	untrustedEmbeddingHeadersCode   = "FORWARDED_EMBEDDING_HEADERS_UNTRUSTED"
	invalidForwardedEmbeddingCode   = "FORWARDED_EMBEDDING_HEADERS_INVALID"
)

type webRequestConfig struct {
	basePath   string
	isEmbedded bool
	csrfToken  string
}

type webBootstrapConfig struct {
	WorkQueuePermissionMode string `json:"workQueuePermissionMode"`
	BasePath                string `json:"basePath"`
	Embedded                bool   `json:"embedded"`
	CSRFHeaderName          string `json:"csrfHeaderName,omitempty"`
	CSRFToken               string `json:"csrfToken,omitempty"`
}

type webRequestConfigContextKey struct{}

func forwardedEmbeddingHandler(cfg *Config, next http.Handler) http.Handler {
	if cfg == nil {
		panic("Web config must not be nil")
	}
	if next == nil {
		panic("Web HTTP handler must not be nil")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestConfig, err := webRequestConfigFromHeaders(request.Header, cfg.TrustForwardedEmbeddingHeaders)
		if err != nil {
			statusCode := http.StatusBadRequest
			code := invalidForwardedEmbeddingCode
			if !cfg.TrustForwardedEmbeddingHeaders {
				statusCode = http.StatusForbidden
				code = untrustedEmbeddingHeadersCode
			}
			api.WriteCodedError(response, statusCode, code, err.Error())
			return
		}
		ctx := context.WithValue(request.Context(), webRequestConfigContextKey{}, requestConfig)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func webRequestConfigFromHeaders(headers http.Header, trustForwardedHeaders bool) (webRequestConfig, error) {
	prefixValues, hasPrefix := headerValues(headers, forwardedPrefixHeader)
	embeddedValues, hasEmbedded := headerValues(headers, forwardedEmbeddedHeader)
	csrfValues, hasCSRF := headerValues(headers, forwardedCSRFTokenHeader)
	if !hasPrefix && !hasEmbedded && !hasCSRF {
		return webRequestConfig{basePath: "/"}, nil
	}
	if !trustForwardedHeaders {
		return webRequestConfig{}, fmt.Errorf("forwarded Dex Web embedding headers are not trusted")
	}
	if !hasPrefix || !hasEmbedded || len(prefixValues) != 1 || len(embeddedValues) != 1 || (hasCSRF && len(csrfValues) != 1) {
		return webRequestConfig{}, fmt.Errorf("forwarded Dex Web embedding headers must contain one prefix and embedded value")
	}
	basePath, err := validateForwardedPrefix(prefixValues[0])
	if err != nil {
		return webRequestConfig{}, err
	}
	isEmbedded, err := parseForwardedEmbedded(embeddedValues[0])
	if err != nil {
		return webRequestConfig{}, err
	}
	csrfToken := ""
	if hasCSRF {
		csrfToken, err = validateForwardedCSRFToken(csrfValues[0])
		if err != nil {
			return webRequestConfig{}, err
		}
	}
	return webRequestConfig{basePath: basePath, isEmbedded: isEmbedded, csrfToken: csrfToken}, nil
}

func webRequestConfigFromContext(ctx context.Context) webRequestConfig {
	requestConfig, ok := ctx.Value(webRequestConfigContextKey{}).(webRequestConfig)
	if !ok {
		return webRequestConfig{basePath: "/"}
	}
	return requestConfig
}

func validateForwardedPrefix(value string) (string, error) {
	if value == "" || len(value) > maximumForwardedPrefixLength || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", fmt.Errorf("forwarded Dex Web prefix must be an absolute path")
	}
	lowerValue := strings.ToLower(value)
	if strings.ContainsAny(value, "?#\\") || strings.Contains(lowerValue, "%2f") || strings.Contains(lowerValue, "%5c") {
		return "", fmt.Errorf("forwarded Dex Web prefix contains a forbidden separator, query, or fragment")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("forwarded Dex Web prefix is not a valid path")
	}
	if value != "/" && (strings.HasSuffix(value, "/") || path.Clean(value) != value) {
		return "", fmt.Errorf("forwarded Dex Web prefix must not contain empty or dot segments")
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", fmt.Errorf("forwarded Dex Web prefix contains invalid escaping")
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("forwarded Dex Web prefix must not contain dot segments")
		}
		for _, character := range segment {
			if unicode.IsControl(character) {
				return "", fmt.Errorf("forwarded Dex Web prefix contains control characters")
			}
		}
	}
	return value, nil
}

func parseForwardedEmbedded(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("forwarded Dex Web embedded value must be true or false")
	}
}

func validateForwardedCSRFToken(value string) (string, error) {
	if value == "" || len(value) > maximumForwardedCSRFTokenLength {
		return "", fmt.Errorf("forwarded Dex Web CSRF token must be non-empty and at most %d bytes", maximumForwardedCSRFTokenLength)
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return "", fmt.Errorf("forwarded Dex Web CSRF token must contain visible ASCII characters")
		}
	}
	return value, nil
}

func prefixAssetReferences(index []byte, basePath string) []byte {
	if basePath == "/" {
		return index
	}
	escapedPrefix := html.EscapeString(basePath)
	replacer := strings.NewReplacer(
		`href="/`, `href="`+escapedPrefix+`/`,
		`src="/`, `src="`+escapedPrefix+`/`,
	)
	return []byte(replacer.Replace(string(index)))
}

func headerValues(headers http.Header, name string) ([]string, bool) {
	values, ok := headers[http.CanonicalHeaderKey(name)]
	return values, ok
}
