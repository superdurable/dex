// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package launchcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The environment variables that hold the secret of each dex.auth mode.
const (
	// CredentialVariable holds a bearer token.
	CredentialVariable = "DEX_CREDENTIAL"
	// CredentialFileVariable holds the path of a bearer token file.
	CredentialFileVariable = "DEX_CREDENTIAL_FILE"
	// APIKeyVariable holds an API key.
	APIKeyVariable = "DEX_API_KEY"
)

// TerminalVariables are the variables a launcher copies for the terminal UI,
// besides the auth variable.
var TerminalVariables = []string{
	"HOME", "TERM", "COLORTERM", "TERM_PROGRAM", "NO_COLOR", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "DEX_AGENTS_TUI_DEBUG",
}

// RequiredAuthVariable returns the variable that holds the secret of auth,
// or "" for AuthModeNone.
func RequiredAuthVariable(auth DexAuth) string {
	switch auth.Mode {
	case AuthModeBearer:
		if auth.Source == "file" {
			return CredentialFileVariable
		}
		return CredentialVariable
	case AuthModeAPIKey:
		return APIKeyVariable
	default:
		return ""
	}
}

// MissingAuthVariable returns the auth variable that auth needs but the
// environment lacks or has empty, or "".
func MissingAuthVariable(lookupEnv func(string) (string, bool), auth DexAuth) string {
	variable := RequiredAuthVariable(auth)
	if variable == "" {
		return ""
	}
	if value, isSet := lookupEnv(variable); isSet && value != "" {
		return ""
	}
	return variable
}

// The years a key's UUIDv7 time must lie in.
const (
	minimumKeyTimeMilliseconds = 946_684_800_000
	maximumKeyTimeMilliseconds = 253_402_300_799_999
)

// NewIdempotencyKey returns a new lowercase UUIDv7 key for one user action.
func NewIdempotencyKey() (string, error) {
	key, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("make an idempotency key: %w", err)
	}
	return key.String(), nil
}

// IdempotencyKeyTime returns the time a valid key carries.
//
// It returns an error when key is not a lowercase hyphenated UUIDv7 or its
// time lies outside the years 2000 to 9999.
func IdempotencyKeyTime(key string) (time.Time, error) {
	if !idempotencyKeyPattern.MatchString(key) {
		return time.Time{}, fmt.Errorf("idempotency key %q is not a lowercase UUIDv7", key)
	}
	milliseconds, err := strconv.ParseInt(key[0:8]+key[9:13], 16, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("idempotency key %q has no time: %w", key, err)
	}
	if milliseconds < minimumKeyTimeMilliseconds || milliseconds > maximumKeyTimeMilliseconds {
		return time.Time{}, fmt.Errorf("idempotency key %q carries a time outside the allowed years", key)
	}
	return time.UnixMilli(milliseconds).UTC(), nil
}

// CandidateSessionIDs returns the short and the long session ID that
// start_session derives from key for harness.
//
// It returns an error for an invalid key.
func CandidateSessionIDs(harness string, key string) (string, string, error) {
	keyTime, err := IdempotencyKeyTime(key)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(key))
	hexDigest := hex.EncodeToString(digest[:])
	prefix := harness + "-" + keyTime.Truncate(time.Second).Format("20060102-150405") + "-"
	return prefix + hexDigest[:6], prefix + hexDigest[:12], nil
}

// IsSessionIDOfKey reports whether sessionID is one of the IDs start_session
// may return for key.
func IsSessionIDOfKey(harness string, key string, sessionID string) bool {
	shortID, longID, err := CandidateSessionIDs(harness, key)
	return err == nil && (sessionID == shortID || sessionID == longID)
}

var (
	authorityPattern = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?)(?::([^:]*))?\z`)
	portPattern      = regexp.MustCompile(`^[1-9][0-9]{0,4}\z`)
	schemePattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*\z`)
)

// IsOpenURLAllowed reports whether open_url may open target for a launch
// document whose web_url is webURL ("" for null).
func IsOpenURLAllowed(webURL string, target string) bool {
	if webURL == "" || !hasOnlyPlainCharacters(target) {
		return false
	}
	baseOrigin, isBaseValid := urlOrigin(webURL)
	targetOrigin, isTargetValid := urlOrigin(target)
	if !isBaseValid || !isTargetValid || baseOrigin != targetOrigin {
		return false
	}
	targetPath := splitURL(target).path
	for _, segment := range strings.Split(targetPath, "/") {
		lowered := strings.ReplaceAll(strings.ToLower(segment), "%2e", ".")
		if lowered == "." || lowered == ".." {
			return false
		}
	}
	basePath := strings.TrimRight(splitURL(webURL).path, "/")
	return basePath == "" || targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}

type webOrigin struct {
	scheme string
	host   string
	port   int
}

func urlOrigin(rawURL string) (webOrigin, bool) {
	if !hasOnlyPlainCharacters(rawURL) {
		return webOrigin{}, false
	}
	parts := splitURL(rawURL)
	defaultPort := map[string]int{"http": 80, "https": 443}[parts.scheme]
	if defaultPort == 0 || strings.ContainsAny(parts.authority, "@%") {
		return webOrigin{}, false
	}
	match := authorityPattern.FindStringSubmatch(parts.authority)
	if match == nil {
		return webOrigin{}, false
	}
	origin := webOrigin{scheme: parts.scheme, host: strings.ToLower(match[1]), port: defaultPort}
	if !strings.Contains(strings.TrimPrefix(parts.authority, match[1]), ":") {
		return origin, true
	}
	if !portPattern.MatchString(match[2]) {
		return webOrigin{}, false
	}
	port, err := strconv.Atoi(match[2])
	if err != nil || port > 65535 {
		return webOrigin{}, false
	}
	origin.port = port
	return origin, true
}

type splitURLParts struct {
	scheme    string
	authority string
	path      string
}

// splitURL splits like Python's urllib.parse.urlsplit, which the contract's
// reference rules use: scheme, authority after "//", then path up to ? or #.
func splitURL(rawURL string) splitURLParts {
	var parts splitURLParts
	rest := rawURL
	if colon := strings.Index(rest, ":"); colon > 0 && schemePattern.MatchString(rest[:colon]) {
		parts.scheme = strings.ToLower(rest[:colon])
		rest = rest[colon+1:]
	}
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		parts.authority, rest = rest[:end], rest[end:]
	}
	if end := strings.IndexAny(rest, "?#"); end >= 0 {
		rest = rest[:end]
	}
	parts.path = rest
	return parts
}

func hasOnlyPlainCharacters(text string) bool {
	for _, character := range text {
		if character <= 0x20 || character >= 0x7f || character == '\\' {
			return false
		}
	}
	return true
}
