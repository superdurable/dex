// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionfeed

import "context"

// targetSessionKey is the context key of TargetSessionContext.
type targetSessionKey struct{}

// TargetSessionContext marks the actions submitted with ctx as accepted
// for sessionID. A source refuses them once it shows another session.
func TargetSessionContext(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, targetSessionKey{}, sessionID)
}

// TargetSessionOf returns the session TargetSessionContext set on ctx, and
// false when ctx has none.
func TargetSessionOf(ctx context.Context) (string, bool) {
	sessionID, isSet := ctx.Value(targetSessionKey{}).(string)
	return sessionID, isSet
}
