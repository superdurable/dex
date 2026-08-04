// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dextest

import "github.com/superdurable/dex/sdk-go/dex"

func NewTestObject(obj interface{}) dex.Object {
	obj2, err := dex.GetDefaultObjectEncoder().Encode(obj)
	if err != nil {
		panic(err)
	}
	return dex.NewObject(obj2, dex.GetDefaultObjectEncoder())
}

func NewTestObjectWithEncoder(obj interface{}, encoder dex.ObjectEncoder) dex.Object {
	obj2, err := encoder.Encode(obj)
	if err != nil {
		panic(err)
	}
	return dex.NewObject(obj2, encoder)
}
