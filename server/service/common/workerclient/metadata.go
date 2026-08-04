// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package workerclient

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateDefaultHeaders rejects metadata keys that gRPC cannot send.
func ValidateDefaultHeaders(headers map[string]string) error {
	for key := range headers {
		if err := validateMetadataKey(key); err != nil {
			return err
		}
	}
	return nil
}

func validateMetadataKey(key string) error {
	if key == "" {
		return fmt.Errorf("defaultHeaders: empty metadata key")
	}
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, "grpc-") {
		return fmt.Errorf("defaultHeaders: key %q must not start with grpc-", key)
	}
	for _, r := range key {
		if unicode.IsUpper(r) {
			return fmt.Errorf("defaultHeaders: key %q must be lowercase", key)
		}
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return fmt.Errorf("defaultHeaders: key %q has invalid character %q", key, r)
		}
	}
	return nil
}
